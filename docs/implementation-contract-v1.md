# v1 implementation decisions

Current business and configuration contract. The [gRPC contract](grpc-migration-specification.md) and [generated schema](../api/proto/marketdata/v1/market_data.proto) define the active data transport. All old HTTP data paths return 404. Initial decisions were adopted on September 11, 2026; Binance admission rules were updated on September 13, 2026. This document fills in the engineering details of the [specification](technical-specification-v1.md). The [decision register](specification-decisions-v1.md) identifies user requirements and engineering defaults.

The [complete configuration example](examples/config-v1.yaml) is the normative field/default inventory for phase 02. This contract takes precedence over superseded illustrative configuration fragments. All durations are elapsed Go-style duration strings (ns, us, ms, s, m, h); days are no longer needed by the history policy. Markets and intervals use canonical strings. Market data remains in memory.

## Common allowances and reserved operation shares

For Binance, the stop line is floor(E × P / 100), or floor(min(E, C) × P / 100) with an explicit user cap C. P is `upstream.binance.stop_threshold_percent`, default 90. E is the last valid exchange limit, or the reviewed starting limit before discovery. Built-in defaults are not permanent user caps. Bybit keeps its configured 20% safety margin and strict common allowance. Percentages use checked integer arithmetic; no fractional allowance is rounded up.

| Scope/window | Captured or documented ceiling | Common service allowance | Tickers | Klines | Instruments | Independent statistics |
| --- | --- | --- | --- | --- | --- | --- |
| Binance Spot weight / 1m | 6,000 | 5,400 | 3,240 | 1,620 | 270 | 270 |
| Binance Spot raw requests / 5m | 300,000 | 270,000 | 162,000 | 81,000 | 13,500 | 13,500 |
| Binance USDⓈ-M weight / 1m | 2,400 | 2,160 | 1,296 | 648 | 108 | 108 |
| Binance funding metadata requests / 5m | 500 | 450 | Not applicable | Not applicable | All 450 | Not applicable |
| Bybit HTTP requests / 5s, both markets | 600 | 480 | 312, including statistics | 144 | 24 | Shared with tickers |

Binance ceilings in the table are bootstrap settings, not permanent exchange limits; discovery updates them. Bybit and the funding endpoint-family limit follow the [official Bybit rules](https://bybit-exchange.github.io/docs/v5/rate-limit) and [Binance fundingInfo reference](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#get-funding-rate-info). ORDERS limits do not apply to this service. The funding-family window applies only to fundingInfo in v1, with no separate fundingRate fetch. All requests still pass every other applicable common window.

The table shows starting values, not current exchange ceilings. Binance allows one request to cross a common stop line when current accounted usage is at or below it. A positive-cost request is rejected immediately when current accounted usage is already above any applicable stop line. Operation shares remain strict: current local operation usage plus the request cost must fit. Zero-cost constraints do not block a request. A request whose cost cannot fit its entire allowance fails as `upstream_unavailable`; it does not wait forever. The checkable invariant is local admission, not an absolute hard 90% IP ceiling. At high percentages, a crossing request can even exceed the selected exchange limit; a desired margin must cover its cost. The funding-family 450 line is also a stop line, so a request at current 450 may cross to 451.

The configured window IDs and their units/scopes are fixed by the adapter contract. Overrides may lower or raise numeric ceilings, but do not relabel units, scopes, or endpoint families. Discovered limits always cap configuration. Unknown cost mappings fail before HTTP dispatch. No borrowing is allowed. The Bybit 65% joint share is derived before rounding, based on provider capability.

Minimum dispatch spacing is 20ms per Binance API scope and 10ms for the shared Bybit IP scope. It is a pacing constraint, not another pool divided into 5% shares. Pacing does not accumulate tokens during idle time. Pick among ready operation lanes in round-robin order, with FIFO within each lane; a ticker queue must not monopolize pacing. The full sliding-window check remains mandatory. A request reserves no HTTP slot while waiting for pacing or cooldown. Binance budget rejection uses no queue or HTTP slot; background workers wait on usage expiry or state changes outside the failed cycle. Bybit still waits for its strict budget.

### Capacity checks

- Spot FULL statistics cost 80: the 270-unit share permits three attempts per minute, or two routine refreshes plus one retry. USDⓈ-M statistics cost 40: the 108-unit share permits two attempts, with 28 units left. Retries may delay a later refresh; the 30s worker interval is not a freshness guarantee.
- Spot exchangeInfo costs 20 and fits the 270-unit instruments share. USDⓈ-M exchangeInfo costs 1 and fits 108; fundingInfo additionally consumes the 450-request family allowance. Bybit instrument pages cost one request and fit 24 per 5s.
- All configured kline page costs fit: Spot 2, Bybit 1, USDⓈ-M up to 10 when a permitted page exceeds 1,000 candles. The default history bound only requires pages up to 1,000; validation still checks adapter calls allowed by configuration.
- Bulk ticker requests fit individually and as ordinary cycles: Spot 8 weight per full price/book cycle, USDⓈ-M 17, Bybit one shared request per market. Continuous operation eventually exhausts its own share. Binance workers then defer without an exchange attempt or error.
- A 50-symbol × three-interval cold load on Binance linear may need more than one minute allowance when all ranges have 1,000 slots. The 30s caller timeout and bounded queues can reject or time out requests during such bursts. Do not promise that all clients can load every series simultaneously in 30 seconds.

These checks prove admission feasibility, not production throughput. Build, load, and latency checks are phase 12 gates.

## Resource ownership and finite bounds

Defaults are listed in the configuration example. Resource scopes are explicit:

- At most 64 active kline callers in the process, including callers waiting for a shared fill. A warm read counts while it runs. At most 64 other snapshot API requests run concurrently. Reject excess requests immediately with service_overloaded; health/readiness use a separate short path.
- At most 12 owned fills globally and six per exchange. Callers waiting for a fill slot remain within the 64-caller bound. Fills for the same series reuse coordination; each fill executes pages sequentially and has its own 30s lifetime, independent of a caller's deadline.
- Upstream lanes are per exchange, shared by that exchange's markets: ticker 2 HTTP slots/4 admission waiters; instruments 2/4; klines 6/16; independent statistics 2/4. Global caps are 24 HTTP requests and 56 admission waiters. Unused slots are not borrowed between lanes. Bybit creates no statistics lane. Queue entries are bounded before starting work.
- An admission wait is at most 10s, also bounded by its owning operation deadline. Queue overflow or admission wait expiry yields service_overloaded for a caller. Known cooldown beyond the available operation lifetime yields upstream_unavailable immediately. An elapsed caller deadline yields request_timeout.
- A ticker cycle lasts at most 15s/9 total HTTP attempts; instruments 60s/100 attempts; independent statistics 15s/3 attempts; a kline fill 30s/12 attempts. Counts include all pages and retries. Each caller separately tracks attempts made by fills it awaits; count a shared attempt once for the exchange and once against each participating caller's logical bound. A late joiner need not count already completed attempts. No caller may escape its bound by joining a new fill.
- The per-request retry limit is three attempts including the first. Retry only transport/network failures that are temporary, per-attempt timeouts, 429, and 500/502/503/504. Other failures do not enter a generic retry loop. 418 and Bybit rate signals set cooldown; another attempt is possible only when both cooldown and the operation bounds permit it.
- Backoff after failed background cycles persists across cycles. Use exponential backoff capped at 2s with injected full jitter; interval-based workers wait at least their normal refresh interval after the failed cycle, and all workers respect shared cooldown. Reset failure backoff only after a successful full cycle. Do not build a job backlog.
- Upstream response bodies are capped at 16 MiB before unbounded buffering/SDK decode. Data RPC requests are capped at 8 KiB and responses at 16 MiB; headers are capped at 32 KiB. The operational listener separately bounds query strings at 8 KiB and rejects body-bearing GET requests. Snapshot reads have a 5s timeout.

Instrument, ticker, market-statistics, and candle source decimal fields accept at most 1,024 bytes of source numeric text and at most 1,024 characters of fixed-point expansion before trimming trailing fractional zeros, including the leading zero and decimal point where needed. Check source length before parsing and the coefficient/exponent bounds before decimal comparison or publication. The same bounds apply to explicit zero. This prevents small scientific-notation inputs from causing large allocations during normalization or RPC serialization. Oversized values fail the whole refresh or candle page with invalid_upstream_data and preserve the prior snapshot; never round or truncate them. Scientific notation within these bounds remains exact.

RPCs do not send partial successful data before completeness validation. Their transport bound is the request lifetime plus 5s from admission, or an earlier client deadline. Operational HTTP writes use server.http.write_timeout. Graceful shutdown defaults to 35s, cancels root work, stops accepting requests, and waits within that bound.

## Bootstrap, discovered limits, and cooldown

The configured window ceilings seed admission before the first exchangeInfo; that request uses the instruments lane and normal cost. A valid catalog is applied even if later instrument normalization fails, because limits are transport safety metadata. No market snapshot is published from an invalid refresh.

A catalog update is accepted only from a successful response to a request at least as new as the last accepted catalog request start. Keep configured windows and the funding-family limit even if omitted from a later catalog. Apply recognized REQUEST_WEIGHT/RAW_REQUESTS limits in their proper scope; ignore ORDERS. Never copy Spot limits onto USDⓈ-M. Explicit legacy Binance window `limit` values are user caps; omitted limits follow exchange increases. Catalog updates never reset usage. Valid reductions apply immediately, even when some request costs become impossible. Normal discovery can recover when exchangeInfo still fits its own allowance.

New or longer applicable windows use available history and report its limits. Missing history alone adds no pause. A malformed catalog, unknown applicable type, duplicate rule, invalid amount, or transport failure keeps the whole last valid catalog, or starting values before first success. Report the refresh failure without making it budget exhaustion. Previously ready snapshots remain readable. One instrument worker reuses exchangeInfo from instrument refreshes and sends a normal catalog request when `catalog_refresh_interval` (default 1h) is due. Failed refreshes remain bounded; no duplicate refresh or special recovery probe is sent.

Accounted usage includes local costs, in-flight reservations, and valid weight observations with unmatched local costs. Only the response's own attempt is proven included in its counter. Keep a valid observation for a full window after receipt, using monotonic elapsed time. Reversed or smaller counters cannot erase live costs. Missing headers do not return spent units; weight does not replace request or funding-family counts. Conservative overlap can approach twice actual usage and can defer work longer than needed. No discrepancy-only cooldown is added. Ignore the documented inaccurate USDⓈ-M price-v2/book headers. Parse Retry-After seconds or an HTTP date; reject negative/invalid values. Compute Bybit reset waits from a valid timestamp on the rate-limit signal, not merely header presence on success. Late responses never shorten an existing wait.

Fallback cooldowns: Binance 429 60s; Binance 418 72h; Bybit 429 or retCode 10006 60s; Bybit access-too-frequent 403 at least 10m. Use the later of an existing cooldown and a newly indicated valid wait. With a valid explicit wait, the fallback does not impose a longer unrelated period, except Bybit's documented 10m minimum for the specific 403. Missing/invalid/past wait metadata activates the fallback. An unrelated 403 is a non-retryable upstream error. Binance ban duration can reach three days according to its [official IP-limit rules](https://github.com/binance/binance-spot-api-docs/blob/master/rest-api.md#ip-limits).

### Restart behavior

User decision: v1 keeps all admission state in memory. Do not persist request ledgers, discovered limits, cooldowns, or shutdown status. No state file, initialization command, persistent mount, or clean/unclean restart distinction is required.

Each process starts with empty local ledgers and configured bootstrap ceilings. Workers may attempt requests immediately through normal admission; there is no automatic restart quiet period. Exchange-side usage and bans can survive a restart. Therefore the service cannot guarantee continuity of budget accounting or cooldown enforcement across restarts. A new rate-limit response applies the normal in-memory cooldown again. Log this limitation at startup and document it in the operating guide. Do not use restart as a way to bypass an exchange limit. Persistence is deferred beyond v1.

This local policy does not account for another process sharing the outgoing IP. Verify the deployment topology before release.

## gRPC contract

The active data API is `marketdata.v1.MarketDataService`, using the [checked-in schema](../api/proto/marketdata/v1/market_data.proto). It has four unary methods: `ListInstruments`, `ListTickers`, `ListMarketStats`, and `GetKlines`. Data is not served through HTTP. Generated Go and Python clients live in this repository; Python supports sync and async calls and exact-revision Git/subdirectory installation.

### HTTP contract

The old `/api/v1/*` routes return 404 without reading application data. The [JSON examples](examples/http-contract-v1.json) describe the historical contract, not current client instructions. Operational HTTP keeps GET `/health`, `/ready`, and enabled `/metrics` and `/debug/stats`; disabled exporters return 404 and other methods return 405.

### Common envelope and validation

There is no JSON data envelope. Snapshot responses contain repeated typed rows. Candle responses contain their series identity once and a repeated candle field. Decimal values are exact strings; optional fields distinguish absence from explicit zero. Counts are signed 64-bit integers, including values above 2^53. Times are Protobuf Timestamp values with nanosecond precision. Reject malformed Timestamp values and pre-epoch request times. Unknown Protobuf fields are tolerated.

String filters have explicit presence. Omission selects defaults; an explicitly empty value fails. Exchange, market, interval, and status are canonical case-sensitive strings. Symbols are exact UTF-8 identifiers of 1–128 bytes without whitespace or control characters. Never uppercase or trim them. Unknown or disabled scopes return `INVALID_ARGUMENT / invalid_filter`.

Global transport admission and byte bounds apply before decoding. After decoding, validate canonical scope/filter values, timestamp/range shape, bounded slot count, history depth, readiness and symbol existence, then execute. Invalid inputs make no upstream calls. The [migration contract](grpc-migration-specification.md#errors) defines field-specific reasons.

### Instruments, tickers, and statistics

Snapshot reads are cache-only and sorted by exchange, market, symbol. Instruments accepts exchange/market/symbol/status; tickers accepts exchange/market/symbol; statistics also accepts window. Omitted window means `24h`; an empty or different value is `INVALID_ARGUMENT / unsupported_window`.

Every selected scope must have a first successful snapshot, even if a symbol/status filter would hide its rows. Otherwise return `UNAVAILABLE / data_not_ready`. A successful empty snapshot or absent symbol in ready scopes returns an empty repeated field. Failed refreshes keep prior values and timestamps. There is no new stale-data flag or expiry policy.

### Kline range contract

Exchange, market, symbol, interval, from and to are required. From/to use valid nonnegative Protobuf Timestamps aligned exactly to the selected calendar. The range is half-open: `from <= OpenTime < to`. Reversed, unaligned, or disallowed future ranges fail with `INVALID_ARGUMENT / invalid_range`.

A valid empty range still validates the scope, catalog and symbol; it returns an empty candle field without a fetch. Unready instruments return `UNAVAILABLE / data_not_ready`; an absent candle symbol returns `NOT_FOUND / symbol_not_found`. There is no historical symbol discovery.

Use the [calendar history rules](technical-specification-v1.md#38-klines-api): 1,000 slots by default, independent of missing rows. The normal workload ends at the current slot boundary C and uses only closed candles. A caller can explicitly include the open slot by ending at the next boundary. This consumes one request slot; no `include_open` parameter or synthetic candle is added. An empty range at that next boundary is valid.

No-progress gaps, pre-listing gaps, and short successful upstream pages are not complete success. Return `FAILED_PRECONDITION / incomplete_data` after bounded fetching makes no progress, while retaining valid fetched pages. Never silently trim a range or negatively cache missing slots as complete.

Recheck history after waiting. A range that becomes too old returns `range_out_of_retention`; it must not trigger a fetch/cleanup loop. A complete read captured while valid can finish serialization after the boundary advances. If saved rows already satisfy the full range after a shared fill error, return those complete rows under the existing cache-success rule.

### Stable errors

Application failures use a gRPC status and generated `ErrorDetail.reason`, with a simple sanitized message. The complete [status/reason mapping](grpc-migration-specification.md#errors) is authoritative. New transport reasons are `response_too_large` and `request_canceled`; existing business reasons retain their meaning.

Native unknown methods, connection failures, invalid wire messages, message caps, local deadlines, and stream resets may have no detail. Clients must handle absent and unknown details. A disconnected caller has no guaranteed response. Never expose raw exchange payloads or a successful partial range.

### Listener and message bounds

Defaults: gRPC `0.0.0.0:9090`, operational HTTP `0.0.0.0:8080`; request message 8,192 bytes, response message 16,777,216 bytes, and headers 32,768 bytes. Message limits refer to uncompressed Protobuf bytes. Response conversion and sending are bounded; an oversized result returns `RESOURCE_EXHAUSTED / response_too_large` without truncation. Clients reuse channels and explicitly set the 16 MiB receive cap.

There is no native TLS, authentication, compression, gateway or reflection. Local Compose publishes both ports only on 127.0.0.1. An untrusted remote path requires separately verified authenticated encryption.

## Startup and metadata

After config validation and local initialization, bind both gRPC and operational HTTP and start instrument, ticker, independent statistics, and retention workers without awaiting successful instrument fetches. Instrument/ticker/statistics first cycles are scheduled immediately but pass admission and cooldown gates. Each enabled exchange/market owns at most one cycle per operation. A failed scope does not stop other scopes. Readiness means local storage and both server owners are initialized, not exchange availability. Before first data, the affected endpoint returns data_not_ready.

For Bybit linear instrument collection, fetch the default current catalog (documented Trading/PendingOpen) plus the explicit status=PreLaunch catalog, including all cursor pages; spot uses its non-paginated default catalog. Omit baseCoin so other categories cannot enter the result. Identical duplicate symbols across the two collections may be deduplicated; conflicting duplicates fail that refresh. These choices follow the [Bybit instrument reference](https://bybit-exchange.github.io/docs/v5/market/instrument). Do not claim a historical/delisted catalog. Binance uses complete exchangeInfo. Reject repeated cursors or no pagination progress; total pages and retries use the instruments attempt/deadline bounds. A partial catalog never replaces the current snapshot. Rows absent from a complete selected catalog are removed without inventing closed records.

The internal instrument model retains a contract classification (`perpetual`, `expiry`, or unknown) from explicit exchange metadata. Ticker normalization reads it from the local instrument snapshot to suppress funding for known expiry contracts. This field is not exposed by the instruments Protobuf message. An unready catalog is treated as unknown; an explicit positive next funding timestamp still confirms a schedule without an instrument fetch.

Binance funding interval decision: use a valid explicit fundingIntervalHours from the successful full fundingInfo response; absent perpetual symbols return null. The generic eight-hour default in the FAQ is not a per-symbol current-value guarantee, especially for inactive instruments. Do not infer an interval from history or timestamps. Unknown contract types and expiry futures get no fallback. A required fundingInfo request/parse failure fails the refresh and preserves the previous instrument snapshot; it does not publish nulls or defaults. The fixture distinguishes explicit 1/4/8-hour values from absence. Binance delisting_time stays null until an authoritative source establishes the perpetual placeholder semantics; this is a deliberate v1 limitation, not a blocker for instruments.

## Configuration loading and verification gates

Load built-in defaults, recursively overlay supplied YAML leaves, then explicit MDS_ environment leaves, then validate. Missing values use defaults; present null/invalid values fail. Lists replace as a whole; windows have the fixed IDs in the example, with supported fields checked against scope/units. Unknown YAML keys, duplicate keys, and unknown MDS_ variables fail startup. Non-MDS_ environment variables are ignored. Scalar environment names use the uppercased underscore path, including the operation names already recorded in section 43; list values are JSON arrays. No shell interpolation or implicit ${VAR} expansion is performed in YAML. MDS_SENTRY_DSN remains an explicit alias for observability.sentry.dsn; specifying it together with MDS_OBSERVABILITY_SENTRY_DSN is an error.

Validate Binance integer stop percentage 1–99 and positive finite catalog refresh interval. Explicit legacy window limits remain caps, including values equal to defaults. `safety_margin_percent` applies to Bybit only; set the Binance percentage to 80 explicitly to retain an older 80% policy.

Validate positive durations/counts, finite caps, 0–99 integer Bybit safety margin, exact four positive share percentages totaling 100, at least one enabled exchange/market, unique market lists, exactly [24h], provider capabilities, URL-free fixed scope names, and integer multiplication/calendar overflow. Both listener ports are 1–65535. Configure server.grpc and server.http independently; removed flat listener keys and old environment names fail. Retry counts and response byte limits are positive integers. Binance 418 fallback must be at least 72h; the Bybit access-too-frequent fallback must be at least 10m. Other configured rate-signal fallbacks must be at least 60s. Validate these minima without shortening a longer explicit exchange cooldown. Disabled providers do not create workers; configured values must still be syntactically valid.

Global HTTP/waiter caps must cover the sum of configured per-exchange lanes for enabled providers. Fills must not exceed their global/per-exchange limits or the available kline lane HTTP capacity. Global fill/caller caps must fit positive finite integers. Shutdown timeout must cover the maximum active caller/fill lifetime plus 5s. Verify the largest valid kline range fits at least its minimum page count within the total attempt bound; lowering the upstream page size may require increasing that bound. A worker may still fail on retries or unavailable exchange data. Validate starting budgets against each permitted single request and two ordinary statistics cycles per minute for the default 30s interval; changed schedules require recalculating capacity, not inventing higher exchange limits.

The loader, lifecycle, in-memory admission, SDK adapters, and generated clients use Go 1.27.1. Run the [development checks](development.md#checks) and [container and capacity checks](operations.md#container-and-capacity-checks) for release validation. Publishing and deployment remain operator actions.
