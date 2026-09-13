# market-data

A Go REST service for Binance and Bybit spot and linear market data. It provides instruments, current tickers, 24-hour market statistics, and cached candles through one API. Binance linear means USDⓈ-M.

**Status:** Phases 01–12 are complete for the agreed v1 workload. Release packaging, all 41 acceptance items, capacity measurements, and current-host compatibility checks are recorded in the [release audit](docs/release-verification-v1.md). Deployment remains an explicit operator action.

## Run locally

Use Go **1.27.1**, the exact version in the module, Makefile, Dockerfile, and CI.

```sh
make build
./bin/market-data-service -config docs/examples/config-v1.yaml -check-config
make run
```

The default listener is `0.0.0.0:8080`. Both markets on both exchanges are enabled. Startup initializes memory storage, binds HTTP, and starts independent collectors. No credentials are required for public exchange data. JSON logs go to stderr.

## Configuration

Precedence: **built-in defaults → YAML → explicit MDS_ environment values → validation**. `-config` is optional; omitting it uses defaults and environment values. Unknown keys, invalid types, duplicate keys, and invalid bounds fail startup. YAML does not interpolate environment variables.

Environment names are uppercase configuration paths with underscores. Lists are JSON arrays and replace the whole list:

```sh
MDS_SERVER_PORT=8081 \
MDS_EXCHANGES_BYBIT_MARKETS='["linear"]' \
./bin/market-data-service -config docs/examples/config-v1.yaml
```

See the [complete configuration example](docs/examples/config-v1.yaml) and [configuration contract](docs/implementation-contract-v1.md). Use environment values for Sentry DSNs; do not commit them. Sentry, Prometheus, and the debug statistics endpoint are optional and disabled by default.

## API

```sh
curl 'http://localhost:8080/health'
curl 'http://localhost:8080/ready'
curl 'http://localhost:8080/api/v1/instruments?exchange=bybit&market=linear&status=trading'
curl 'http://localhost:8080/api/v1/tickers?exchange=binance&market=spot&symbol=BTCUSDT'
curl 'http://localhost:8080/api/v1/market-stats?exchange=bybit&market=linear&window=24h'
```

All decimal market values are JSON strings. Missing optional fields are explicit `null`. Snapshot APIs read only their repositories. An unavailable selected snapshot returns `503 data_not_ready`; a successfully loaded empty snapshot returns `{"data":[]}`. Snapshot filters select enabled scopes only. Only the `24h` statistics window is supported.

Candles require exchange, market, symbol, interval, and aligned RFC 3339 half-open boundaries. For example, replace these timestamps with a recent closed range inside the configured history window:

```sh
curl 'http://localhost:8080/api/v1/klines?exchange=binance&market=spot&symbol=BTCUSDT&interval=1m&from=2026-09-12T11:50:00Z&to=2026-09-12T12:00:00Z'
```

The default history bound is 1000 slots per series, measured backwards from the latest close. Complete final cached ranges need no upstream work. Missing or unconfirmed candles share bounded cache fills. Success always contains the complete requested range. See the [API guide](docs/development.md) and [HTTP examples](docs/examples/http-contract-v1.json) for validation, errors, and open-candle behavior.

## Docker and Compose

Install Docker Engine/Desktop with a recent Compose v2 or later. Run from this directory:

```sh
make docker-build
make docker-up
docker compose logs --tail=100 -f
make docker-down
```

The image uses a digest-pinned Go builder and a `scratch` runtime with CA certificates, a static executable, and UID/GID 65532. Compose publishes only `127.0.0.1:8080`, mounts the example configuration read-only, drops capabilities, and makes the root filesystem read-only. Keep the mounted file readable by UID 65532. There is one service and no state volume.

Compose enforces 1,000,000,000 bytes of memory, disables swap, and sets `GOMEMLIMIT=700MiB`, a soft Go runtime memory target, not a process RSS limit. Capacity and its limits are documented in the [release audit](docs/release-verification-v1.md). Rebuild with `make docker-build` after source changes, then run `make docker-up` to recreate the service.

The image runs `/market-data-service -config /etc/market-data/config.yaml`. Its healthcheck uses the same file and environment with `-healthcheck`; it makes one local `/health` request with a two-second timeout and never starts collectors. When running the image directly, mount configuration at that path. Override configuration, ports, or environment with a local Compose override; host `MDS_` variables are not forwarded automatically by Compose. Change both the port mapping and service settings if changing the container's listener port.

SIGINT/SIGTERM cancel HTTP admission and owned work. The default internal shutdown bound is 35 seconds; Compose allows 40 seconds before forced termination. Increase `stop_grace_period` if increasing `server.shutdown_timeout`. The service does not restart automatically.

## Publish a release image

The [release workflow](.github/workflows/release.yml) builds the tagged source with the existing Dockerfile and pushes a Linux/amd64 image to `ghcr.io/<owner>/<repository>`. It runs when a version tag is pushed or a GitHub Release is published, including pre-releases. Use semantic versions with an optional `v` prefix: `v1.2.3` and `1.2.3` both publish `ghcr.io/imbpp123/market-data:1.2.3`; `v1.2.3-rc.1` publishes `:1.2.3-rc.1`. Invalid version tags fail before registry login. No `latest` tag is published.

Before building or publishing the image, the workflow runs `make check` on the tagged commit with Go 1.27.1, just like the Checks workflow. Formatting, build, example configuration, lint, tests, and race checks must all pass. Any failure stops publication.

After the workflow is merged, tag the commit to release and push the tag:

```sh
git tag v1.2.3
git push origin v1.2.3
```

Alternatively, publish a GitHub Release for the version tag. Publishing a release after pushing its tag runs the workflow again and replaces the same image tag. Use one trigger per version when a second build is not needed. The workflow uses the repository's `GITHUB_TOKEN` with `contents: read` and `packages: write`; no separate registry secret is needed. If the GHCR package already exists, it must grant this repository write access. See [GitHub's registry publishing guide](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images).

## Diagnose and operate

- `/health` means the process is alive; `/ready` means local initialization is complete. Neither proves exchange data is available or fresh.
- On `data_not_ready`, inspect collector errors and enabled scopes. A failed first refresh leaves its scope unready. Other scopes continue.
- Stale `updated_at` or `fetched_at` means a collector has not published new data. Failed refreshes preserve previous snapshots; v1 has no snapshot expiry. Check upstream errors, admission waits, cooldowns, and system time before restarting.
- `service_overloaded` means a finite caller/fill/queue limit was reached. `request_too_large` and `range_out_of_retention` require a valid smaller/recent request. No error returns a successful partial candle range.
- Optional `/metrics` and `/debug/stats` expose counters, last successes, durations, retained candle counts, and failures. Keep these operational routes on a trusted network. All counters reset on restart.

All data, usage counters, discovered exchange limits, and cooldowns are memory-only. A restart loses them immediately, begins from bootstrap budgets, and adds no automatic quiet period. Exchange-side usage and bans may remain. Restarting is not a rate-limit reset.

v1 assumes one instance and no other exchange clients sharing its outgoing IP. Verify this on the deployment network, including Binance Spot bulk `type=FULL` access. Common weighted windows, reserved operation shares, a 20% safety margin, bounded queues, and scoped cooldowns constrain every page and retry. They cannot account for traffic from other processes. See the [operating contract](docs/implementation-contract-v1.md).

Retention prunes old candles on merges and every hour by default. It keeps at most the configured rolling history per series, including every supported interval; it does not cap the number of requested series. Keep the host clock synchronized to UTC using NTP. A candle is final only after a request started at or after its close. v1 assumes exchanges do not later revise such confirmed candles; it does not reconcile later corrections.

## Checks

```sh
make check          # formatting, build, example config, pinned lint, tests, race
make vet
make docker-build
make docker-verify  # isolated Compose test; no exchange network access
make release-load   # opt-in synthetic capacity measurement; no network
```

`make docker-verify` requires Python 3 and a local Linux Docker engine with the host's CPU architecture. It mounts a compiled probe in a temporary Compose project with an internal network and checks health/readiness, unready data, certificates, permissions, and SIGTERM. CI runs the same build and lifecycle checks. Dependency downloads need network access; unit/integration test execution needs no exchange access or credentials.

## v1 scope

There is no trading execution, order/account/position/balance API, strategy calculation, MCP server, order book, raw trades, WebSocket ingestion, Redis/PostgreSQL implementation, ticker/statistics history, non-24h statistics, or statistics aggregation from candles.

Further documentation: [development guide](docs/development.md), [technical specification](docs/technical-specification-v1.md), [decision register](docs/specification-decisions-v1.md), [phase plan](docs/phases/README.md), and [development rules](AGENTS.md).

Proposed change under review: [request budget accounting rework](docs/request-budget-rework-specification.md). It records the revised limit-discovery and usage-accounting design; it does not change the implemented v1 behavior.
