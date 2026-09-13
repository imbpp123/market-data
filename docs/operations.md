# Operations guide

This guide covers container setup, diagnostics, and operating limits for the v1 service. For the first local request, use the [quick start](quickstart.md). For settings and older configuration changes, see the [configuration guide](development.md#configuration).

## Docker and Compose

Install Docker Engine/Desktop with a recent Compose v2 or later. Run from the repository root:

```sh
make docker-build
make docker-up
docker compose logs --tail=100 -f
make docker-down
```

The image uses a digest-pinned Go builder and a `scratch` runtime with CA certificates, a static executable, and UID/GID 65532. Compose publishes only `127.0.0.1:9090` and `127.0.0.1:8080`, mounts the example configuration read-only, drops capabilities, and makes the root filesystem read-only. Keep the mounted file readable by UID 65532. There is one service and no state volume.

Compose enforces 1,000,000,000 bytes of memory, disables swap, and sets `GOMEMLIMIT=700MiB`, a soft Go runtime memory target, not a process RSS limit. Validate memory use for the deployment workload. Rebuild with `make docker-build` after source changes, then run `make docker-up` to recreate the service.

The image runs `/market-data-service -config /etc/market-data/config.yaml`. Its healthcheck uses the same file and environment with `-healthcheck`; it makes one local `/health` request with a two-second timeout and never starts collectors. When running the image directly, mount configuration at that path. Override configuration, ports, or environment with a local Compose override; host `MDS_` variables are not forwarded automatically by Compose. Change the matching port mapping and `server.grpc.port` or `server.http.port` together. Removed flat `server.host`, `server.port`, HTTP settings and their old environment names fail validation.

SIGINT/SIGTERM close RPC admission and owned work. The default internal shutdown bound is 35 seconds; Compose allows 40 seconds before forced termination. Increase `stop_grace_period` if increasing `server.shutdown_timeout`. The service does not restart automatically.

The service has no native TLS or authentication. Keep both listeners on a trusted network. A remote deployment needs a separately configured protected connection. Direct local runs bind to `0.0.0.0` by default; the [local quick start](quickstart.md#local) overrides both hosts to `127.0.0.1`.

## Diagnose and operate

Logs are JSON on stderr. Use the operational HTTP listener for process checks:

```sh
curl 'http://localhost:8080/health'
curl 'http://localhost:8080/ready'
```

After local initialization, both return HTTP 200 with `{"status":"ok"}`. These checks do not wait for the first exchange snapshot.

- `/health` means the process is alive; `/ready` means local initialization is complete. Neither proves exchange data is available or fresh.
- On `data_not_ready`, inspect collector errors and enabled scopes. A failed first refresh leaves its scope unready. Other scopes continue.
- Stale `updated_at` or `fetched_at` means a collector has not published new data. Failed refreshes preserve previous snapshots; v1 has no snapshot expiry. Check upstream errors, admission waits, cooldowns, and system time before restarting.
- `service_overloaded` means a finite caller/fill/queue limit was reached or needed Binance exchange work failed a budget check. `request_too_large` and `range_out_of_retention` require a valid smaller/recent request. No error returns a successful partial candle range.
- Optional `/metrics` and `/debug/stats` expose counters, last successes, durations, retained candle counts, and failures. `/debug/stats?view=admission` shows current Binance limits, usage, per-window operation reasons, and recovery times. See the [diagnostic guide](development.md#binance-admission-diagnostics). Keep these operational routes on a trusted network. All counters reset on restart.

All data, usage counters, discovered exchange limits, and cooldowns are memory-only. A restart loses them immediately, begins from bootstrap budgets, and adds no automatic quiet period. Exchange-side usage and bans may remain. Restarting is not a rate-limit reset.

v1 assumes one instance and no other exchange clients sharing its outgoing IP. Verify this on the deployment network, including Binance Spot bulk `type=FULL` access. Binance uses a configurable stop line (default 90%) and strict 60/30/5/5 operation shares. No new positive-cost request is admitted when current accounted usage is already above a stop line; crossing from equality or below is allowed. Bybit keeps its 20% safety margin. These local checks cannot guarantee actual IP usage: external traffic, restarts, delayed observations, and unseen limits remain unknown. Conservative counter overlap can approach twice actual usage. See the [operating contract](implementation-contract-v1.md).

Retention prunes old candles on merges and every hour by default. It keeps at most the configured rolling history per series, including every supported interval; it does not cap the number of requested series. Keep the host clock synchronized to UTC using NTP. A candle is final only after a request started at or after its close. v1 assumes exchanges do not later revise such confirmed candles; it does not reconcile later corrections.

## Container and capacity checks

Run from the repository root:

```sh
make docker-build
make docker-verify  # isolated Compose test; no exchange network access
make release-load   # opt-in synthetic capacity measurement; no exchange access
```

`make docker-verify` requires Python 3 and a local Linux Docker engine with the host's CPU architecture. It mounts a compiled probe in a temporary Compose project with an internal network and checks health/readiness, unready data, certificates, permissions, and SIGTERM. CI runs the same build and lifecycle checks. Build and dependency downloads need network access; test execution needs no exchange access or credentials.

Use the capacity profile and acceptance criteria in the [gRPC specification](grpc-migration-specification.md#testing--validation). Keep new run output outside tracked documentation, for example under ignored `bin/`. Historical reports and measurements remain in Git history.

See the [development checks](development.md#checks) for service tests and the [release guide](releasing.md) for image publication.
