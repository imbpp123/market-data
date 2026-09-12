# Development guide

Run all commands from the repository root. The current build includes configuration loading, the process scaffold, exact domain models, UTC candle calendars, funding read models, application contracts, in-memory repositories, and bounded upstream admission/retries. Instrument normalization, scheduled refresh workers, the instruments read API, and publication counters are implemented. Ticker/statistics collectors, candle loading, other market-data endpoints, and observability exporters remain future work.

## Run

Use Go **1.27.1**. The module, Makefile, and CI use this exact version.

```sh
make build
./bin/market-data-service -config docs/examples/config-v1.yaml -check-config
make run
```

Without `-config`, the process uses built-in defaults and environment overrides. `-check-config` validates settings and exits without opening HTTP or starting workers. Logs are JSON on stderr. SIGINT and SIGTERM cancel root work, close the HTTP listener, and wait for owned workers within `server.shutdown_timeout`.

The service serves `GET /health`, `GET /ready`, and `GET /api/v1/instruments`. Health means the process is alive. Readiness means local bootstrap initialization has finished; no exchange request is required. Bootstrap creates the four memory repositories before binding HTTP. Instrument workers start immediately after HTTP bind, independently per enabled exchange/market. Local readiness does not imply that instrument snapshots are available. Unknown routes return 404; unsupported health methods return 405.

Storage lives in `internal/infrastructure/storage/memory` behind application interfaces. Instrument, ticker, and statistics snapshots publish independently and keep scope readiness separate from empty data. Candle merges use `klines.max_history_candles`, an injected clock, and the domain history calendar; cleanup cutoffs cannot move backwards. Post-close confirmation uses internal successful-attempt start metadata. Periodic cleanup scheduling is phase 11 work; merge-time pruning is already active.

## Instruments

```sh
curl 'http://localhost:8080/api/v1/instruments?exchange=bybit&market=linear&status=trading'
```

Exchange, market, symbol, and status are optional canonical filters. Omitted scopes select all enabled pairs. Every selected scope must have a successful snapshot; otherwise the API returns 503 data_not_ready. A ready empty result returns `{"data":[]}`. Rows are sorted by exchange, market, and symbol. Invalid or disabled scope filters return 400 invalid_filter, and unknown statuses return 400 invalid_status. Query shape, request size, concurrent snapshot requests, and deadlines follow the implementation contract.

The application uses one instrument.Provider interface with Scope() and GetInstruments(ctx). Each exchange has separate spot and linear implementations in instruments_spot.go and instruments_linear.go; market-specific filters, statuses, pagination, and funding logic live there. Bootstrap selects a provider for each enabled pair. Binance uses separate SDK client implementations behind its Client interface. Bybit shares its unified SDK client and admission across its two market providers.

Each worker refreshes one scope without overlap, then waits the exchange's configured instruments.refresh_interval. All pages and required metadata requests share one bounded instruments operation, including retries. A failed refresh leaves the prior snapshot and UpdatedAt intact. Reads never trigger an upstream request. Decimal fields are strings, optional fields are explicit nulls, and funding_interval is in seconds. Binance delisting_time remains null. Bybit linear collects the default catalog plus PreLaunch, including all cursor pages; this is not a historical catalog.

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

Admission keeps usage, discovered limits, and cooldown state in memory. Exchange-side usage and bans may survive restarts. Restarting cannot guarantee continuity of local accounting. Bootstrap constructs a shared controller and pinned SDK clients before HTTP bind, without making exchange calls. See [phase 05](phases/05-upstream-admission-and-retries.md) for the implementation and checks.

Feature adapters use the shared `binance.Client.Fetch` (implemented separately for spot and linear) or `bybit.Client.Fetch` with one `Controller.Begin` context per full cycle or fill. Pages and retries reuse that context. The returned raw body retains exact source numbers and successful-attempt start/receipt timestamps; feature normalization must not rebuild numeric values from the Bybit SDK result. A background worker owns one `CycleGate` and calls `Run` with the full cycle, including publication, so a new cycle cannot reset failure backoff. Instrument workers and normalization are implemented; ticker/statistics and candle adapters remain assigned to their feature phases.

The transport resolves actual endpoint costs, performs atomic sliding-window admission, and bounds retries, response bodies after decompression, queues, HTTP concurrency, and deadlines. It exposes per-attempt request/error/duration events; bootstrap logs attempt failures. Instrument publication success/error counts, last-success times, and snapshot sizes are stored in memory by exchange/market. Refresh outcomes are logged. Other statistics aggregation and exporters remain assigned to their feature and operations phases. Integration tests use loopback HTTP servers, so sandboxed test runs need permission to bind local ports.

Sentry, Prometheus, and built-in statistics settings are validated. Exporters, periodic statistics logging, and debug endpoints are deferred to phase 11. The process logs that limitation when those options are enabled; metrics and debug routes are absent in this phase.

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
