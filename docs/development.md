# Development guide

Run all commands from the repository root. The current build includes configuration loading, the process scaffold, exact domain models, UTC candle calendars, funding read models, application contracts, in-memory repositories, and bounded upstream admission/retries. Instrument normalization, scheduled refresh workers, the instruments read API, and publication counters are implemented. Ticker/statistics collectors and their cache-only APIs are implemented. Bounded candle cache fills and the kline endpoint are implemented. Retention scheduling and optional observability exporters are implemented.

## Run

Use Go **1.27.1**. The module, Makefile, and CI use this exact version.

```sh
make build
./bin/market-data-service -config docs/examples/config-v1.yaml -check-config
make run
```

Without `-config`, the process uses built-in defaults and environment overrides. `-check-config` validates settings and exits without opening HTTP or starting workers. Logs are JSON on stderr. SIGINT and SIGTERM cancel root work, close the HTTP listener, and wait for owned workers within `server.shutdown_timeout`.

The service serves `GET /health`, `GET /ready`, `GET /api/v1/instruments`, `GET /api/v1/tickers`, `GET /api/v1/market-stats`, and `GET /api/v1/klines`. Health means the process is alive. Readiness means local bootstrap initialization has finished; no exchange request is required. Bootstrap creates the four memory repositories before binding HTTP. Instrument, ticker, and independent statistics workers start immediately after HTTP bind, independently per enabled exchange/market. Local readiness does not imply that any exchange snapshot is available. Unknown routes return 404; unsupported health methods return 405.

Storage lives in `internal/infrastructure/storage/memory` behind application interfaces. Instrument, ticker, and statistics snapshots publish independently and keep scope readiness separate from empty data. Candle merges use `klines.max_history_candles`, an injected clock, and the domain history calendar; cleanup cutoffs cannot move backwards. Post-close confirmation uses internal successful-attempt start metadata. An owned worker runs cleanup immediately and then waits storage.cleanup_interval after each pass. Merge-time pruning remains active.

Candle planning lives in `internal/application/kline`. Construct a `Planner` for one enabled scope with the provider's supported intervals, the configured history size, and the final per-request candle limit. Call `Validate` before cache lookup, then `Plan` with the query, cached rows, and a captured clock value. Each plan rechecks the rolling history window. It merges missing and non-final slots into the fewest half-open requests, including cached slots between gaps. The planner performs no I/O.

Candle adapters implement `kline.Provider` in each exchange package. Construct them with `binance.NewKlineProvider(spot, linear)` or `bybit.NewKlineProvider(client)`. `SupportedTimeframes` returns a fresh slice from the same mapping table used to build requests. Each `GetKlines` call handles exactly one planned page inside an existing `Controller.Begin` kline fill context; it does not start a new operation or paginate. The fill service reuses that context across all pages and retries.

Adapters send `limit=Request.Limit`, convert the aligned half-open end to inclusive milliseconds (`To - 1ms`), and retain the successful attempt's start and response receipt times. The shared transport enforces configured page limits and charges each retry using the sent limit. Normalization reads the original response body: Binance integer timestamps/counts and Bybit string timestamps remain exact, including counts above 2^53. Bybit candle requests use a direct HTTP path through the same admitted transport because the SDK generic decoder rejects valid JSON numbers outside the float64 range, such as `1e309`. The candle path applies the normalizer's decimal bounds without passing through that SDK decoder. Missing fields, invalid OHLC/volumes, wrong boundaries, duplicate or out-of-range rows, and Bybit category/symbol mismatches reject the whole page. Short and empty pages remain incomplete data for the fill service. Successful pages stay cached if a later page fails.

## Instruments

```sh
curl 'http://localhost:8080/api/v1/instruments?exchange=bybit&market=linear&status=trading'
```

Exchange, market, symbol, and status are optional canonical filters. Omitted scopes select all enabled pairs. Every selected scope must have a successful snapshot; otherwise the API returns 503 data_not_ready. A ready empty result returns `{"data":[]}`. Rows are sorted by exchange, market, and symbol. Invalid or disabled scope filters return 400 invalid_filter, and unknown statuses return 400 invalid_status. Query shape, request size, concurrent snapshot requests, and deadlines follow the implementation contract.

The application uses one instrument.Provider interface with Scope() and GetInstruments(ctx). Each exchange has separate spot and linear implementations in instruments_spot.go and instruments_linear.go; market-specific filters, statuses, pagination, and funding logic live there. Bootstrap selects a provider for each enabled pair. Binance uses separate SDK client implementations behind its Client interface. Bybit shares its unified SDK client and admission across its two market providers.

Each worker refreshes one scope without overlap, then waits the exchange's configured instruments.refresh_interval. All pages and required metadata requests share one bounded instruments operation, including retries. A failed refresh leaves the prior snapshot and UpdatedAt intact. Reads never trigger an upstream request. Decimal fields are strings, optional fields are explicit nulls, and funding_interval is in seconds. Binance delisting_time remains null. Bybit linear collects the default catalog plus PreLaunch, including all cursor pages; this is not a historical catalog.

## Tickers and market statistics

```sh
curl 'http://localhost:8080/api/v1/tickers?exchange=bybit&market=linear&symbol=BTCUSDT'
curl 'http://localhost:8080/api/v1/market-stats?exchange=binance&market=spot&window=24h'
```

Both endpoints read only their own memory snapshots. Exchange, market, and exact symbol filters follow the instruments rules. Every selected scope must be ready; successful empty snapshots return `{"data":[]}`. Missing `window` means `24h`; any other, empty, or repeated value returns `unsupported_window` before data access. All three snapshot endpoints share `server.max_snapshot_requests`.

Ticker workers run continuously through admission and persistent cycle backoff. Binance joins bulk price/book responses, plus premiumIndex for linear, within one bounded cycle. Its separate statistics workers request FULL spot or linear 24hr data immediately and wait `market_stats.refresh_interval` after each completion. Bybit uses one ticker request for both snapshots, with independent normalization and publication. It has no independent statistics worker or fallback request. Every retry stays within its operation budget.

Decimal values remain exact JSON strings; optional values are explicit nulls. Funding countdown is computed from one clock value per HTTP response and never changes the stored timestamp. It becomes null when the known event time arrives. Internal instrument contract metadata suppresses funding placeholders for known expiry contracts; missing instrument data does not block an explicit upstream schedule. This metadata is not exposed in the instruments JSON.

Failed sources or publications preserve the previous snapshot and its source receipt time. A Bybit branch failure does not prevent the other branch from publishing; Binance ticker and statistics workers have separate deadlines and budgets. Snapshot freshness has no additional expiry policy in v1.

## Candles

```sh
curl 'http://localhost:8080/api/v1/klines?exchange=binance&market=spot&symbol=BTCUSDT&interval=1m&from=2026-09-12T11:50:00Z&to=2026-09-12T12:00:00Z'
```

Use a range inside the current configured history window; the timestamps above are illustrative. All six parameters are required. Timestamps use RFC 3339 with an explicit timezone and aligned half-open boundaries. Validation checks scope/interval, timestamp/range shape, slot count, history depth, then instrument readiness and symbol existence before candle access. A valid empty range still requires a known symbol in a ready catalog.

Final cached candles return without upstream work. Missing or intermediate slots use a shared fill keyed by exchange, market, symbol, and interval. Each caller rechecks its own range after a fill; overlapping ranges reuse saved slots. The service never extends an active fill for later arrivals or returns a successful partial range. Empty/no-progress results fail with `incomplete_data`; successful earlier pages stay cached after an error.

The service enforces `klines.max_callers`, global/per-exchange active fills, caller/fill deadlines, and total attempts across pages/retries. Full capacity returns `service_overloaded` immediately. HTTP caller slots cover DTO construction, serialization, and response writing, and are released when the handler returns. Request range validation runs before admission. A late joiner conservatively counts already dispatched attempts in the shared fill. Caller cancellation releases its wait without canceling the service-owned fill. Shutdown cancels fills and waits for their completion.

An explicitly requested current candle refreshes once for each HTTP request, including shared work. A new request evaluates it again. If the candle closes during a load, only an attempt started after close establishes finality. Refresh evidence inherited from an earlier fill is not passed off as a new refresh to another caller.

History uses calendar slots, including calendar months. A range can expire at the next boundary while waiting; it then returns `range_out_of_retention`. Old rows awaiting cleanup cannot extend API access. The memory repository prunes during merges and periodic cleanup. Internal counters track hits/misses, shared fills, actual attempts, and downloaded candles.

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

Feature adapters use the shared `binance.Client.Fetch` (implemented separately for spot and linear) or `bybit.Client.Fetch` with one `Controller.Begin` context per full cycle or fill. Pages and retries reuse that context. The returned raw body retains exact source numbers and successful-attempt start/receipt timestamps; feature normalization must not rebuild numeric values from the Bybit SDK result. A background worker owns one `CycleGate` and calls `Run` with the full cycle, including publication, so a new cycle cannot reset failure backoff. Instrument and current-data workers, normalization, and single-page candle adapters are implemented. Candle fills use the same operation boundary across pages and retries.

The transport resolves actual endpoint costs, performs atomic sliding-window admission, and bounds retries, response bodies after decompression, queues, HTTP concurrency, and deadlines. It exposes per-attempt request/error/duration events; bootstrap logs attempt failures. Instrument, ticker, and statistics publication success/error counts, last-success times, and snapshot sizes are stored in memory by exchange/market, with a separate window key for statistics. Instrument refresh outcomes and current-data failures are logged. Periodic aggregation and optional exporters read the same counters. Integration tests use loopback HTTP servers, so sandboxed test runs need permission to bind local ports.

## Operations

The retention worker takes one clock value per pass and uses the provider interval tables and domain calendars for each enabled exchange/market/interval. It removes only OpenTime strictly below the shared history cutoff. Storage cutoffs stay monotonic, late fills cannot restore expired rows, and empty series are released. Calendar or storage failures are reported and retried on the next scheduled pass; other scopes still run. Snapshot data is not subject to retention.

Built-in counters are independent of Prometheus and Sentry. `observability.stats.enabled` enables standalone collection and aggregate JSON logging at `stats.log_interval`. Each exporter can independently request collection when standalone logging is disabled. With stats, debug, and Prometheus all disabled, publication and attempt observers do not update operational counters. Sentry remains independent.

`observability.prometheus.enabled` exposes GET at the configured path (default `/metrics`) in [Prometheus text 0.0.4 format](https://prometheus.io/docs/instrumenting/exposition_formats/). `observability.stats.endpoint_enabled` exposes GET `/debug/stats`. Both routes are absent by default. The debug endpoint and aggregate logs contain a sorted array of `{name, labels, value}` samples. Last-success values use Unix seconds, with zero before the first publication. Durations expose count, sum, and max in seconds for exchange attempts and candle fills. Candle counts reflect actual retained rows, including idle rows awaiting cleanup. Aggregate reads are race-safe; different metric groups can reflect slightly different instants. Counters reset on restart. Labels use exchange/market, plus operation for exchange attempts and window for MarketStats; symbols are never metric labels.

Sentry uses the pinned [official Go SDK](https://pkg.go.dev/github.com/getsentry/sentry-go) behind infrastructure and bootstrap boundaries. Enable it with a DSN from the environment. Each process owns a private client with a 64-event queue and a two-second send timeout. Errors use stable application codes and operation context. HTTP error events preserve the response code and include the route and status. Expected data readiness and request cancellation do not create HTTP error events; other unclassified 5xx responses use http_error (500 uses internal_error). Raw error/panic text, candle arrays, request bodies, headers, and raw queries are excluded. Traces cover HTTP requests, exchange attempts, candle fills, and cleanup. Sampling zero disables traces while retaining error reports. Panics in HTTP handlers produce an internal error before any response is written; a panic after headers aborts the response. A worker panic stops the service; a fill panic fails that fill and releases its admission slot. An instrument refresh panic records a failure and preserves the previous publication counters, timestamp, and snapshot size.

SIGINT and SIGTERM cancel root work and close HTTP admission. Shutdown waits for active HTTP requests, shared fills, refresh workers, cleanup, and logging within `server.shutdown_timeout`. Sentry flush uses only the remaining deadline, then the client closes. A flush timeout is logged and does not extend shutdown or turn a normal stop into a service error. Tests use fake event sinks, virtual time, and local/in-memory HTTP; they do not send production telemetry.

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
