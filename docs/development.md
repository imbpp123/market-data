# Development guide

Run all commands from the repository root. The current build includes configuration loading and the process scaffold. Exchange adapters, repositories, market-data endpoints, and observability integrations are not implemented yet.

## Run

Use Go **1.27.1**. The module, Makefile, and CI use this exact version.

```sh
make build
./bin/market-data-service -config docs/examples/config-v1.yaml -check-config
make run
```

Without `-config`, the process uses built-in defaults and environment overrides. `-check-config` validates settings and exits without opening HTTP or starting workers. Logs are JSON on stderr. SIGINT and SIGTERM cancel root work, close the HTTP listener, and wait for owned workers within `server.shutdown_timeout`.

The scaffold serves `GET /health` and `GET /ready`. Health means the process is alive. Readiness means local bootstrap initialization has finished; no exchange request is required. Phase 04 must initialize the actual memory repositories before starting the server, and feature phases must wire data routes and workers. Readiness in this scaffold is not evidence that market data is available. Unknown routes return 404; unsupported health methods return 405.

## Configuration

The [complete example](examples/config-v1.yaml) is executable and checked against built-in defaults in tests. The [implementation contract](implementation-contract-v1.md) defines its rules.

Loading order is **defaults → YAML → explicit MDS_ environment values → validation**. Koanf merges sources, its environment provider loads overrides, and mapstructure decodes typed settings. `go.yaml.in/yaml/v3` parses YAML. A strict input check rejects unknown or duplicate keys, explicit nulls, wrong scalar types, multiple YAML documents, and YAML anchors/aliases before a merge can hide them. Unknown or duplicate `MDS_` variables also fail. No YAML string interpolation takes place. Errors identify fields without printing their values.

Environment names are uppercase paths with dots replaced by underscores. Lists use JSON arrays and replace the entire list:

```sh
MDS_SERVER_PORT=8081 \
MDS_KLINES_MAX_HISTORY_CANDLES=500 \
MDS_EXCHANGES_BYBIT_MARKETS='["linear"]' \
./bin/market-data-service -config docs/examples/config-v1.yaml
```

`MDS_SENTRY_DSN` is an alias for `MDS_OBSERVABILITY_SENTRY_DSN`; using both is an error. Do not commit a real DSN or other credentials.

Validation covers the full agreed schema, including disabled providers' input syntax, endpoint page limits, history size, refresh schedules, reserved budget shares, fixed allocation windows, cooldown minima, concurrency, queues, attempts, observability options, and overflow. Kline caller timeout and HTTP attempt timeout are separate. The HTTP write timeout is `max(kline request timeout, snapshot timeout) + 5s`. Shutdown must cover the longest caller/fill lifetime plus 5s.

Reducing a kline page size may require raising the kline attempt bound. Increasing history also affects this bound. Counts use checked integer arithmetic; the history count additionally cannot exceed `MaxInt64 / (31 * 24 * 60 * 60)` so a worst-case monthly span fits signed seconds. Calendar-specific checks belong to the timeframe implementation.

Admission will keep usage and cooldown state in memory. Exchange-side usage and bans may survive restarts. Restarting cannot guarantee continuity of local accounting. Actual admission is phase 05 work; this scaffold makes no exchange calls.

Sentry, Prometheus, and built-in statistics settings are validated but their integrations are deferred to feature and operations phases. The process logs that limitation when those options are enabled; metrics and debug routes are absent in this phase.

## Checks

```sh
make check
```

`check` stops on the first failure and runs sequentially, including with `make -j`:

1. `fmt-check`: check `gofmt` output without changing files.
2. `build`: compile the executable.
3. `check-config`: validate the complete example with that executable.
4. `lint`: verify `.golangci.yml` and run golangci-lint with its default `standard` set.
5. `test`: run unit tests.
6. `test-race`: run tests with the race detector last.

This puts inexpensive checks first, subject to dependencies: the configuration check needs a built executable. `govet` is included in the standard linter set, so `check` does not run a second standalone vet pass. `make vet` remains available for a focused check.

The Makefile pins golangci-lint to **v2.13.2**. `make lint` and `make check` install the official binary on first use into the ignored `bin/golangci-lint/v2.13.2/` directory. The installer is fetched from the same release tag and verifies the archive checksum. Installation requires network access, `curl`, `tar`, and a SHA-256 utility; later runs reuse the installed binary. `make install-lint` installs it explicitly. Linter dependencies are kept out of the service's Go module.

The [linter configuration](../.golangci.yml) selects the upstream defaults without additional linters or custom exclusions. CI runs the same `make check`, including linter installation and example validation, without a second lint or configuration pass.

Other targets include `make run`. Docker targets will be added with packaging in phase 12.

Tests use `testify/require` for prerequisites and `testify/assert` for independent checks. They use no credentials or exchange access. Lifecycle tests exercise the real HTTP server over in-memory connections with `testing/synctest` for deterministic cancellation and deadline checks.

See the [phase plan](phases/README.md), [technical specification](technical-specification-v1.md), and [development rules](../AGENTS.md).
