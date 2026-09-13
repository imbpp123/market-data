# Operations

Use this guide to deploy, monitor, update, and stop the service. For a first run, follow the [quick start](quickstart.md). For settings, see [configuration](configuration.md). For failures, see [troubleshooting](troubleshooting.md).

## Docker and Compose

Install Docker with Compose v2 or later. From the repository root:

```sh
make docker-build
make docker-up
docker compose logs --tail=100 -f
```

`make docker-up` waits for the container healthcheck. To stop and remove the container:

```sh
make docker-down
```

[compose.yaml](../compose.yaml) builds the local source. It publishes gRPC on `127.0.0.1:9090` and operational HTTP on `127.0.0.1:8080`. It mounts [config/config-v1.yaml](../config/config-v1.yaml) read-only at `/etc/market-data/config.yaml`. There is one service and no state volume.

The image runs as UID/GID 65532 with a static executable and CA certificates. Compose makes the root filesystem read-only, drops capabilities, and prevents privilege escalation. Keep the mounted config readable by that user.

Use a local Compose override for environment or deployment changes. Host `MDS_` variables are not forwarded automatically. For example, this `compose.override.yaml` enables metrics:

```yaml
services:
  market-data-service:
    environment:
      MDS_OBSERVABILITY_PROMETHEUS_ENABLED: "true"
```

Recreate the container after changes. If you change a container listener port, change the matching published port mapping too. The [Docker quick start](quickstart.md#docker) shows how to use a published image without building the repository.

## Network access

Public exchange data requires outbound network access and no exchange credentials. Verify that the deployment IP can use the selected exchange endpoints, including Binance spot bulk FULL statistics.

The service has no native TLS or authentication. Direct executable runs bind to `0.0.0.0` by default; the quick start overrides both hosts to loopback. Keep both listeners on a trusted network. For an untrusted remote path, configure authenticated encryption separately. A proxy must support gRPC over HTTP/2, including trailers.

Request-limit accounting assumes one service instance and no other exchange clients sharing its outgoing IP. Another process or instance can spend budget the service cannot fully observe.

## Health and readiness

Use the operational HTTP listener:

```sh
curl -i 'http://localhost:8080/health'
curl -i 'http://localhost:8080/ready'
```

`/health` checks process liveness. `/ready` returns 200 with `{"status":"ok"}` after local storage, services, both listeners, and worker ownership are initialized. It returns 503 during initialization or shutdown. Neither endpoint waits for exchange data or checks freshness.

The executable's `-healthcheck` mode makes one local `/health` request with a two-second timeout, then exits without starting collectors. It uses the same configuration and environment as the service. The container healthcheck uses this mode. It cannot be combined with `-check-config`.

Operational routes are separate from data admission, so saturated data requests do not consume their request slots. They still share the process's CPU and memory.

## Monitoring

Logs are JSON on stderr. They report collector failures, upstream attempts, limit changes, cooldowns, and lifecycle events. Repeated unchanged Binance budget deferrals are not logged as repeated failures.

In-memory statistics are collected by default. Enable HTTP exporters through [monitoring settings](configuration.md#logs-and-optional-monitoring):

```sh
curl 'http://localhost:8080/metrics'
curl 'http://localhost:8080/debug/stats'
```

Prometheus uses text format 0.0.4 at its configured path. The default debug response is a sorted array of `{name, labels, value}` samples. Both routes return 404 when disabled.

Counters cover snapshot publication, last success, retained candle rows, cache hits/misses, fills, exchange attempts, and RPC outcomes. Last-success times are Unix seconds; zero means no successful publication yet. Duration samples include count, sum, and maximum. Different metric groups can reflect slightly different moments. All counters reset on restart.

Labels are bounded: exchange, market, operation, statistics window, and RPC method/status/reason as applicable. Symbols do not become metric labels. RPC completion means local processing and stream closure, not confirmed receipt by the remote application.

Optional Sentry reporting uses sanitized errors and sampled traces. Expected invalid requests, missing symbols, unready data, and caller cancellation do not create RPC error reports. Raw exchange bodies, request bodies, headers, queries, and raw panic text are excluded. Trace sampling zero keeps error reports enabled. Keep operational endpoints on a trusted network.

## Binance admission diagnostics

When the debug endpoint is enabled, inspect the current controller state:

```sh
curl 'http://localhost:8080/debug/stats?view=admission'
```

This view shows Binance spot and linear separately. Reading it does not make exchange requests, reserve budget, or record a rejection.

Each window shows its limit source (`bootstrap` or `exchange_info`), age, selected limit, optional user cap, percentage, and stop line. A zero user cap means none was configured.

`local` already includes in-flight `reserved` cost. `accounted` combines local and observed usage. Do not add these fields together. `remaining` is headroom, not permission to send: an operation share can still block a request.

`history_since` and `incomplete_history` show accounting gaps after restart or a longer discovered window. `uncertain` marks estimates with unproven counter overlap. A false value does not prove that no other process uses the IP.

| Reason | Meaning |
| --- | --- |
| `common_threshold` | Accounted usage is above a common stop line. |
| `operation_share` | Operation usage plus the shown request cost exceeds its share. |
| `request_cost_exceeds_allowance` | This cost cannot fit even with zero usage. Waiting for expiry cannot fix it. |
| `exchange_cooldown` | A real exchange signal still blocks this scope. |
| `catalog_refresh_failed` | A dispatched catalog refresh failed; previous usable limits remain. |
| `oversized_body` | A decoded exchange response exceeded its size bound. |

Operation entries use the largest configured request cost for the window. A cheaper actual request can have a different result. Per-window `next_eligible` predicts recovery from known usage expiry, pacing, and cooldown. A zero time means unknown or impossible. Check every applicable window; these estimates exclude HTTP slots, queue capacity, caller deadlines, and request-specific retry backoff.

Prometheus exposes `admission_*` gauges with fixed window labels: `request_weight_1m`, `raw_requests_5m`, and `funding_requests_5m`. Other discovered windows are counted by unit, not turned into arbitrary labels. Exact windows remain in the detailed JSON. Prometheus operation recovery uses the latest relevant time, or zero if any is unknown. Times use Unix seconds.

For the admission algorithm and its limits, see [architecture](architecture.md#exchange-request-limits).

## Memory and history

Compose limits the whole container to 1,000,000,000 bytes and disables swap. `GOMEMLIMIT=700MiB` is a soft Go runtime target; it is not a limit on process RSS. Measure the deployment workload before increasing capacity settings.

The default history is 1,000 closed slots per series, with a current candle possible when explicitly requested. This bounds time depth, not the number of series. Many symbols and intervals can exceed the intended memory budget. No symbol count or request-rate allowlist is enforced.

The sizing workload uses 3–4 clients and up to 50 symbols, with 1h candles for 20 days, 5m candles for two days, and 1m candles for eight hours. For conservative sizing, assume 50 symbols in each of the four exchange/market pairs. That initial history contains 307,200 candles. Continuing to fill all three intervals to the retention bound can retain 600,000 closed candles across 600 series. These are sizing assumptions, not preloading behavior or an API restriction.

Keep the host clock synchronized. Retention, candle finality, and funding countdowns depend on it. The cache assumes that confirmed closed candles will not receive later exchange corrections.

## Restarts and updates

All snapshots, candles, usage ledgers, discovered limits, and cooldowns live in memory. Restarting clears them and starts with configured limits. There is no automatic quiet period after restart. Exchange-side usage and bans can remain, so a restart is not a rate-limit reset.

SIGINT and SIGTERM stop new RPC admission and cancel owned work. The internal shutdown limit defaults to 35 seconds. Compose allows 40 seconds before forced termination. If you raise `server.shutdown_timeout`, increase `stop_grace_period` too. Compose does not restart the service automatically.

For a source update, select the intended revision, run the [development checks](development.md#checks), rebuild with `make docker-build`, and recreate with `make docker-up`. For a published image, use its matching configuration and client contract. Watch readiness, collector success, and data timestamps after startup.

To roll back, run a known earlier version with its matching configuration and clients. There is no persistent schema to migrate; cache data is rebuilt. Release publication is described in [development](development.md#releases).

## Container and capacity checks

Run from the repository root:

```sh
make docker-build
make docker-verify
make release-load
```

`make docker-verify` needs Python 3 and a local Linux Docker engine matching the host CPU architecture. It uses an isolated Compose project with an internal network. It checks health/readiness, unready data, certificates, permissions, and SIGTERM without exchange access. Build and dependency downloads still need network access.

`make release-load` is an opt-in synthetic measurement. Its main profile uses 600 series, 600,000 candles after fills, 20,000-row snapshots per type, and four local TCP clients. It preloads 999 candles per series, then checks partial fills and warm reads. It uses synthetic providers, so it does not measure live exchange pacing or availability. The Go clients run in the measured process.

Use 800,000,000 bytes of peak process memory as the engineering target for this workload, leaving headroom within the 1 GB container. The Make target sets `GOMEMLIMIT`; it does not itself enforce a container limit. Record runtime, configuration, decimal lengths, process boundaries, CPU, memory, and latency when measuring. Do not reduce the profile silently to obtain a passing result.

For wire and client comparisons, use the tools described in the [API package guide](../api/README.md). Compare the same data over the same transport boundary, and keep encoded message bytes separate from network bytes. Store new measurement output outside tracked documentation, for example under ignored `bin/`.

[Documentation index](../README.md#documentation)
