# Development guide

Run all commands from the repository root. The current build includes configuration loading, the process scaffold, exact domain models, UTC candle calendars, funding read models, application contracts, in-memory repositories, and bounded upstream admission/retries. Instrument normalization, scheduled refresh workers, the instruments read API, and publication counters are implemented. Ticker/statistics collectors and their cache-only APIs are implemented. Bounded candle cache fills and the kline endpoint are implemented. Retention scheduling and optional observability exporters are implemented.

See the [transport implementation note](grpc-transport.md) for request bounds, resource ownership, and telemetry limits.

## Run

Use Go **1.27.1**. The module, Makefile, and CI use this exact version.

```sh
make build
./bin/market-data-service -config config/config-v1.yaml -check-config
make run
```

`-healthcheck` checks `/health` on the configured operational HTTP listener with a two-second timeout and exits without starting the service. It cannot be combined with `-check-config`. Without `-config`, the process uses built-in defaults and environment overrides. `-check-config` validates settings and exits without opening listeners or starting workers. Logs are JSON on stderr. SIGINT and SIGTERM cancel root work, stop both listeners, and wait for owned workers within `server.shutdown_timeout`.

The required listeners are gRPC `0.0.0.0:9090` for the four generated market-data methods and HTTP `0.0.0.0:8080` for `/health`, `/ready`, and enabled exporters. Both must bind before workers start or readiness becomes true. A bind failure closes any listener already opened; a serve failure stops the whole process. Health means the process is alive. Readiness means local initialization and both server owners are ready, independent of exchange snapshots. All `/api/v1/*` HTTP routes return 404; non-GET operational methods return 405.

Storage lives in `internal/infrastructure/storage/memory` behind application interfaces. Instrument, ticker, and statistics snapshots publish independently and keep scope readiness separate from empty data. Candle merges use `klines.max_history_candles`, an injected clock, and the domain history calendar; cleanup cutoffs cannot move backwards. Post-close confirmation uses internal successful-attempt start metadata. An owned worker runs cleanup immediately and then waits storage.cleanup_interval after each pass. Merge-time pruning remains active.

Candle planning lives in `internal/application/kline`. Construct a `Planner` for one enabled scope with the provider's supported intervals, the configured history size, and the final per-request candle limit. Call `Validate` before cache lookup, then `Plan` with the query, cached rows, and a captured clock value. Each plan rechecks the rolling history window. It merges missing and non-final slots into the fewest half-open requests, including cached slots between gaps. The planner performs no I/O.

Candle adapters implement `kline.Provider` in each exchange package. Construct them with `binance.NewKlineProvider(spot, linear)` or `bybit.NewKlineProvider(client)`. `SupportedTimeframes` returns a fresh slice from the same mapping table used to build requests. Each `GetKlines` call handles exactly one planned page inside an existing `Controller.Begin` kline fill context; it does not start a new operation or paginate. The fill service reuses that context across all pages and retries.

Adapters send `limit=Request.Limit`, convert the aligned half-open end to inclusive milliseconds (`To - 1ms`), and retain the successful attempt's start and response receipt times. The shared transport enforces configured page limits and charges each retry using the sent limit. Normalization reads the original response body: Binance integer timestamps/counts and Bybit string timestamps remain exact, including counts above 2^53. Bybit candle requests use a direct HTTP path through the same admitted transport because the SDK generic decoder rejects valid JSON numbers outside the float64 range, such as `1e309`. The candle path applies the normalizer's decimal bounds without passing through that SDK decoder. Missing fields, invalid OHLC/volumes, wrong boundaries, duplicate or out-of-range rows, and Bybit category/symbol mismatches reject the whole page. Short and empty pages remain incomplete data for the fill service. Successful pages stay cached if a later page fails.

## Instruments

With an installed `grpcurl`, use the checked-in descriptor (reflection is disabled):

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 30 \
  -d '{"exchange":"bybit","market":"linear","status":"trading"}' localhost:9090 marketdata.v1.MarketDataService/ListInstruments
```

Exchange, market, symbol, and status are optional canonical filters. Omitted scopes select all enabled pairs. Every selected scope must have a successful snapshot; otherwise the API returns UNAVAILABLE / data_not_ready. A ready empty result returns an empty repeated field. Rows are sorted by exchange, market, and symbol. Invalid or disabled scope filters return INVALID_ARGUMENT / invalid_filter, and unknown statuses return INVALID_ARGUMENT / invalid_status. Protobuf presence, request size, concurrent snapshot requests, and deadlines follow the implementation contract.

The application uses one instrument.Provider interface with Scope() and GetInstruments(ctx). Each exchange has separate spot and linear implementations in instruments_spot.go and instruments_linear.go; market-specific filters, statuses, pagination, and funding logic live there. Bootstrap selects a provider for each enabled pair. Binance uses separate SDK client implementations behind its Client interface. Bybit shares its unified SDK client and admission across its two market providers.

Each worker refreshes one scope without overlap, then waits the exchange's configured instruments.refresh_interval. All pages and required metadata requests share one bounded instruments operation, including retries. A failed refresh leaves the prior snapshot and UpdatedAt intact. Reads never trigger an upstream request. Decimal fields are strings, missing optional fields remain absent, and funding_interval_seconds is in whole seconds. Binance delisting_time remains absent. Bybit linear collects the default catalog plus PreLaunch, including all cursor pages; this is not a historical catalog.

## Tickers and market statistics

With an installed `grpcurl`, use the checked-in descriptor (reflection is disabled):

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 30 \
  -d '{"exchange":"binance","market":"spot","window":"24h"}' localhost:9090 marketdata.v1.MarketDataService/ListMarketStats
```

Both endpoints read only their own memory snapshots. Exchange, market, and exact symbol filters follow the instruments rules. Every selected scope must be ready; successful empty snapshots return an empty repeated field. Missing `window` means `24h`; any other, empty value returns `unsupported_window` before data access. All three snapshot endpoints share `server.max_snapshot_requests`.

Ticker workers run continuously through admission and persistent cycle backoff. Binance joins bulk price/book responses, plus premiumIndex for linear, within one bounded cycle. Its separate statistics workers request FULL spot or linear 24hr data immediately and wait `market_stats.refresh_interval` after each completion. Bybit uses one ticker request for both snapshots, with independent normalization and publication. It has no independent statistics worker or fallback request. Every retry stays within its operation budget.

Decimal values remain exact Protobuf strings; missing optional values remain absent. Funding countdown is computed from one clock value per RPC response and never changes the stored timestamp. It becomes absent when the known event time arrives. Internal instrument contract metadata suppresses funding placeholders for known expiry contracts; missing instrument data does not block an explicit upstream schedule. This metadata is not exposed in the instruments message.

Failed sources or publications preserve the previous snapshot and its source receipt time. A Bybit branch failure does not prevent the other branch from publishing; Binance ticker and statistics workers have separate deadlines and budgets. Snapshot freshness has no additional expiry policy in v1.

## Candles

With an installed `grpcurl`, use the checked-in descriptor (reflection is disabled):

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 30 \
  -d '{"exchange":"binance","market":"spot","symbol":"BTCUSDT","interval":"1m","from":"2026-09-13T11:50:00Z","to":"2026-09-13T12:00:00Z"}' localhost:9090 marketdata.v1.MarketDataService/GetKlines
```

Use a range inside the current configured history window; the timestamps above are illustrative. All six fields are required. The wire uses Protobuf Timestamp; grpcurl accepts its standard JSON timestamp representation. Boundaries are aligned and half-open. Validation checks scope/interval, timestamp/range shape, slot count, history depth, then instrument readiness and symbol existence before candle access. A valid empty range still requires a known symbol in a ready catalog.

Final cached candles return without upstream work. Missing or intermediate slots use a shared fill keyed by exchange, market, symbol, and interval. Each caller rechecks its own range after a fill; overlapping ranges reuse saved slots. The service never extends an active fill for later arrivals or returns a successful partial range. Empty/no-progress results fail with `incomplete_data`; successful earlier pages stay cached after an error.

The service enforces `klines.max_callers`, global/per-exchange active fills, caller/fill deadlines, and total attempts across pages/retries. Full capacity returns `service_overloaded` immediately. RPC capacity covers validation, construction, serialization, and transport completion. Admission rejects excess calls immediately before decoding. Capacity stays owned until processing and the stream finish, including cancellation cleanup. A late joiner conservatively counts already dispatched attempts in the shared fill. Caller cancellation releases its wait without canceling the service-owned fill. Shutdown cancels fills and waits for their completion.

An explicitly requested current candle refreshes once for each RPC, including shared work. A new request evaluates it again. If the candle closes during a load, only an attempt started after close establishes finality. Refresh evidence inherited from an earlier fill is not passed off as a new refresh to another caller.

History uses calendar slots, including calendar months. A range can expire at the next boundary while waiting; it then returns `range_out_of_retention`. Old rows awaiting cleanup cannot extend API access. The memory repository prunes during merges and periodic cleanup. Internal counters track hits/misses, shared fills, actual attempts, and downloaded candles.

## Configuration

The [complete example](../config/config-v1.yaml) is executable and checked against built-in defaults in tests. The [implementation contract](implementation-contract-v1.md) and [gRPC listener contract](grpc-migration-specification.md#configuration-and-size-bounds) define its rules. Use `server.grpc` and `server.http`; removed flat host, port and HTTP fields, including old environment names, fail startup.

Loading order is **defaults → YAML → explicit MDS_ environment values → validation**. Koanf merges sources, its environment provider loads overrides, and mapstructure decodes typed settings. `go.yaml.in/yaml/v3` parses YAML. A strict input check rejects unknown or duplicate keys, explicit nulls, wrong scalar types, multiple YAML documents, and YAML anchors/aliases before a merge can hide them. Unknown or duplicate `MDS_` variables also fail. No YAML string interpolation takes place. Errors identify fields without printing their values.

Environment names are uppercase paths with dots replaced by underscores. Lists use JSON arrays and replace the entire list:

```sh
MDS_SERVER_HTTP_PORT=8081 \
MDS_KLINES_MAX_HISTORY_CANDLES=500 \
MDS_EXCHANGES_BYBIT_MARKETS='["linear"]' \
./bin/market-data-service -config config/config-v1.yaml
```

`MDS_SENTRY_DSN` is an alias for `MDS_OBSERVABILITY_SENTRY_DSN`; using both is an error. Do not commit a real DSN or other credentials.

Binance settings are `upstream.binance.stop_threshold_percent` (integer 1–99, default 90) and `catalog_refresh_interval` (positive finite duration, default 1h). Environment overrides are `MDS_UPSTREAM_BINANCE_STOP_THRESHOLD_PERCENT` and `MDS_UPSTREAM_BINANCE_CATALOG_REFRESH_INTERVAL`. Explicit legacy Binance window limits are user caps, including overrides equal to built-in defaults; omitted limits track exchange changes. `safety_margin_percent` affects Bybit only. To keep an older 80% Binance policy, explicitly set the new percentage to 80. Invalid settings fail validation.

Validation covers the full agreed schema, including disabled providers' input syntax, endpoint page limits, history size, refresh schedules, reserved budget shares, fixed allocation windows, cooldown minima, concurrency, queues, attempts, observability options, and overflow. Kline caller timeout and HTTP attempt timeout are separate. Each RPC transport deadline is its request lifetime plus 5s from admission; an earlier client deadline wins. Operational writes use `server.http.write_timeout` (5s). Shutdown must cover the longest caller/fill lifetime plus 5s.

Reducing a kline page size may require raising the kline attempt bound. Increasing history also affects this bound. Counts use checked integer arithmetic; the history count additionally cannot exceed `MaxInt64 / (31 * 24 * 60 * 60)` so a worst-case monthly span fits signed seconds. Calendar-specific checks belong to the timeframe implementation.

Admission keeps usage, discovered limits, and cooldown state in memory. Exchange-side usage and bans may survive restarts. Restarting cannot guarantee continuity of local accounting. Bootstrap constructs a shared controller and pinned SDK clients before both listeners bind, without making exchange calls. See the [implementation contract](implementation-contract-v1.md) for admission rules.

Feature adapters use the shared `binance.Client.Fetch` (implemented separately for spot and linear) or `bybit.Client.Fetch` with one `Controller.Begin` context per full cycle or fill. Pages and retries reuse that context. The returned raw body retains exact source numbers and successful-attempt start/receipt timestamps; feature normalization must not rebuild numeric values from the Bybit SDK result. A background worker owns one `CycleGate` and calls `Run` with the full cycle, including publication, so a new cycle cannot reset failure backoff. Instrument and current-data workers, normalization, and single-page candle adapters are implemented. Candle fills use the same operation boundary across pages and retries.

The transport resolves actual endpoint costs, performs atomic sliding-window admission, and bounds retries, response bodies after decompression, queues, HTTP concurrency, and deadlines. It exposes per-attempt request/error/duration events; bootstrap logs attempt failures. Instrument, ticker, and statistics publication success/error counts, last-success times, and snapshot sizes are stored in memory by exchange/market, with a separate window key for statistics. Instrument refresh outcomes and current-data failures are logged. Optional exporters read the same counters. Integration tests use loopback HTTP servers, so sandboxed test runs need permission to bind local ports.

## Binance admission diagnostics

Enable the existing statistics endpoint and request `/debug/stats?view=admission` for one controller snapshot. The default `/debug/stats` sample array is unchanged. Binance Spot and USD-M are separate scopes; Bybit admission behavior is unchanged and is not duplicated into these Binance diagnostics.

Each window shows source (`bootstrap` or `exchange_info`), age in seconds, update time, exchange limit, optional user cap (zero means absent), percentage, and stop line. `local` includes in-flight `reserved` cost; `accounted` already combines local and observed usage. Do not add these fields together. `remaining` is nonnegative headroom, not permission to send: equality can still permit one crossing request, while strict shares may reject it.

`history_since` and `incomplete_history` identify restart or longer-window history gaps. `uncertain` marks an estimate with unproven usage or overlap; false does not prove no external IP traffic. Only each response's own attempt is proven included. Conservative estimates can approach twice actual usage, and observations expire one full window after receipt. No hard 90% actual-IP guarantee is made.

Operation entries use the largest configured request cost for that window, shown as `request_cost`. This is a conservative diagnostic check, not a promise that every request in the operation has the same outcome: USD-M ticker costs differ, and funding windows apply only to funding requests. Reasons are current and may coexist:

| Reason | Meaning |
| --- | --- |
| `common_threshold` | Current accounted usage is above the common stop line. |
| `operation_share` | Local operation usage plus the shown request cost exceeds its strict share. |
| `request_cost_exceeds_allowance` | The shown request cannot fit even with no usage; expiry cannot repair the limit. |
| `exchange_cooldown` | A real exchange signal still blocks the scope. Budget expiry does not clear it. |
| `catalog_refresh_failed` | A dispatched catalog attempt failed; keep usable installed limits. This is separate scope metadata, not an admission rejection. |
| `oversized_body` | A decoded response exceeded the finite body bound. A catalog failure retains this distinct reason. |

Per-window `next_eligible` is the earliest time predicted from known expiries, pacing, and cooldown for the shown cost. Zero time means unknown (in-flight completion) or impossible cost. Evaluate every applicable window; Prometheus gives the latest of these times per operation, or zero if any is unknown. These estimates exclude HTTP slots, queue capacity, caller deadlines, and request-specific retry backoff. No diagnostic read sends a request, reserves budget, or records a rejection.

Prometheus and the default debug array expose `admission_*` gauges. Fixed window labels are `request_weight_1m`, `raw_requests_5m`, and `funding_requests_5m`. They include limits/source/age, observed/local/accounted/reserved usage, headroom, uncertainty/history, and operation cost/share/usage. Arbitrary discovered window names never become metric labels. `admission_discovered_windows` counts them by unit; `admission_blocking_windows` counts all blocking windows by operation and fixed reason. Counts do not add unlike usage units. Exact discovered limits and times remain in the detailed JSON. Times are numeric Unix seconds in metrics, with zero for unset/unknown values.

Catalog error metadata follows request order: late failures cannot replace newer accepted success; an older accepted catalog cannot clear a newer failure. A subsequent accepted response clears the error without resetting usage. Logs report changed limits with their source and stop line, catalog recovery/failures, extended cooldowns, and budget rejection/next-admission transitions with the actual request weight and funding flag. Admission of a cheaper request does not promise that a more expensive request now fits. Unchanged catalogs and repeated background deferrals do not produce repeated state logs. Deferrals are not exchange attempts, errors, or refresh failures. `exchange_oversized_bodies_total` counts only dispatched responses that exceed the body bound.

The checkable invariant is: no new positive-cost request is admitted when current accounted usage is already above a stop line. Restart, external traffic, delayed charging, and unseen limits prevent an absolute IP-usage guarantee. Cache and snapshot reads remain available during admission rejection.

## Operations

The retention worker takes one clock value per pass and uses the provider interval tables and domain calendars for each enabled exchange/market/interval. It removes only OpenTime strictly below the shared history cutoff. Storage cutoffs stay monotonic, late fills cannot restore expired rows, and empty series are released. Calendar or storage failures are reported and retried on the next scheduled pass; other scopes still run. Snapshot data is not subject to retention.

Built-in counters are independent of Prometheus and Sentry. `observability.stats.enabled` enables standalone collection without periodic logging. Each exporter can independently request collection when standalone collection is disabled. Remove the obsolete `observability.stats.log_interval` YAML key and `MDS_OBSERVABILITY_STATS_LOG_INTERVAL` environment variable from existing deployments; both are rejected as unknown settings. With stats, debug, and Prometheus all disabled, publication and attempt observers do not update operational counters. Sentry remains independent.

`observability.prometheus.enabled` exposes GET at the configured path (default `/metrics`) in [Prometheus text 0.0.4 format](https://prometheus.io/docs/instrumenting/exposition_formats/). `observability.stats.endpoint_enabled` exposes GET `/debug/stats`. Both routes are absent by default. The debug endpoint contains a sorted array of `{name, labels, value}` samples. Last-success values use Unix seconds, with zero before the first publication. Durations expose count, sum, and max in seconds for exchange attempts and candle fills. Candle counts reflect actual retained rows, including idle rows awaiting cleanup. Aggregate reads are race-safe; different metric groups can reflect slightly different instants. Counters reset on restart. Labels use exchange/market, plus operation for exchange attempts and window for MarketStats; symbols are never metric labels.

Sentry uses the pinned [official Go SDK](https://pkg.go.dev/github.com/getsentry/sentry-go) behind infrastructure and bootstrap boundaries. Enable it with a DSN from the environment. Each process owns a private client with a 64-event queue and a two-second send timeout. Errors use stable application codes and operation context. HTTP error events preserve the response code and include the route and status. Expected data readiness and request cancellation do not create HTTP error events; other unclassified 5xx responses use http_error (500 uses internal_error). Raw error/panic text, candle arrays, request bodies, headers, and raw queries are excluded. Traces cover data RPCs, operational HTTP requests, exchange attempts, candle fills, and cleanup. RPC metrics use bounded method/status/reason labels and include request/response message bytes through transport completion. Sampling zero disables traces while retaining error reports. Panics in HTTP handlers produce an internal error before any response is written; a panic after headers aborts the response. A worker panic stops the service; a fill panic fails that fill and releases its admission slot. An instrument refresh panic records a failure and preserves the previous publication counters, timestamp, and snapshot size.

SIGINT and SIGTERM close RPC admission and cancel root work. Shutdown drains both servers concurrently and waits for shared fills, refresh workers, and cleanup within `server.shutdown_timeout`. Sentry flush uses only the remaining deadline, then the client closes. A flush timeout does not extend shutdown or turn a normal stop into a service error. Tests use fake event sinks, virtual time, and local/in-memory HTTP; they do not send production telemetry.

## Checks

Run from the repository root:

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

Also run `make check-api` for generated files, compatibility, and Go/Python client checks. It requires Python **3.13 and 3.14**. See the [API prerequisites](../api/README.md#reproduce-and-check) for tools and installation checks. Tool and dependency downloads need network access; test execution uses controlled fixtures without exchange credentials or live exchange access.

This puts inexpensive checks first, subject to dependencies: the configuration check needs a built executable. `govet` is included in the standard linter set, so `check` does not run a second standalone vet pass. `make vet` remains available for a focused check.

The Makefile pins golangci-lint to **v2.13.2**. `make lint` and `make check` install the official binary on first use into the ignored `bin/golangci-lint/v2.13.2/` directory. The installer is fetched from the same release tag and verifies the archive checksum. Installation requires network access, `curl`, `tar`, and a SHA-256 utility; later runs reuse the installed binary. `make install-lint` installs it explicitly. Linter dependencies are kept out of the service's Go module.

The [linter configuration](../.golangci.yml) selects the upstream defaults without additional linters or custom exclusions. CI runs the same `make check`, including linter installation and example validation, without a second lint or configuration pass.

Other targets include `make run`, `make docker-build`, `make docker-up`, `make docker-down`, `make docker-verify`, and `make release-load`. See the [operations guide](operations.md) for container and capacity checks.

Tests use `testify/require` for prerequisites and `testify/assert` for independent checks. They use no credentials or exchange access. Lifecycle tests exercise both servers and generated RPC clients over in-memory connections with `testing/synctest` for deterministic cancellation and deadline checks.

See the [technical specification](technical-specification-v1.md) and [development rules](../AGENTS.md).
