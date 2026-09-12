# v1 implementation decisions

Status: engineering decisions adopted during phase 01, September 11, 2026. These complete the open implementation details in the [specification](technical-specification-v1.md). User-confirmed requirements are identified in the [decision register](specification-decisions-v1.md); the additional defaults below are engineering choices within that scope. This document does not claim that the service is implemented.

The [complete configuration example](examples/config-v1.yaml) is the normative field/default inventory for phase 02. This contract takes precedence over superseded illustrative configuration fragments. All durations are elapsed Go-style duration strings (ns, us, ms, s, m, h); days are no longer needed by the history policy. Markets and intervals use canonical strings. Market data remains in memory.

## Common allowances and reserved operation shares

Use a 20% safety margin. For configured ceiling L and a compatible current exchange limit E, use floor(min(L, E) × 80 / 100) as the common service allowance. Before catalog discovery, E is the captured bootstrap ceiling. Percentages use checked integer arithmetic; no fractional allowance is rounded up. This is a conservative service policy, not a guarantee against every upstream limit change.

| Scope/window | Captured or documented ceiling | Common service allowance | Tickers | Klines | Instruments | Independent statistics |
| --- | --- | --- | --- | --- | --- | --- |
| Binance Spot weight / 1m | 6,000 | 4,800 | 2,880 | 1,440 | 240 | 240 |
| Binance Spot raw requests / 5m | 300,000 | 240,000 | 144,000 | 72,000 | 12,000 | 12,000 |
| Binance USDⓈ-M weight / 1m | 2,400 | 1,920 | 1,152 | 576 | 96 | 96 |
| Binance funding metadata requests / 5m | 500 | 400 | Not applicable | Not applicable | All 400 | Not applicable |
| Bybit HTTP requests / 5s, both markets | 600 | 480 | 312, including statistics | 144 | 24 | Shared with tickers |

Binance ceiling evidence is the captured [exchangeInfo limit excerpt](evidence/phase-01/exchange-limits.json). Bybit and the funding endpoint-family limit follow the [official Bybit rules](https://bybit-exchange.github.io/docs/v5/rate-limit) and [Binance fundingInfo reference](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#get-funding-rate-info). ORDERS limits do not apply to this service. The funding-family window applies only to fundingInfo in v1, with no separate fundingRate fetch. All requests still pass every other applicable common window.

The configured window IDs and their units/scopes are fixed by the adapter contract. Overrides may lower or raise numeric ceilings, but do not relabel units, scopes, or endpoint families. Discovered limits always cap configuration. Unknown cost mappings fail before HTTP dispatch. No borrowing is allowed. The Bybit 65% joint share is derived before rounding, based on provider capability.

Minimum dispatch spacing is 20ms per Binance API scope and 10ms for the shared Bybit IP scope. It is a pacing constraint, not another pool divided into 5% shares. Pacing does not accumulate tokens during idle time. Pick among ready operation lanes in round-robin order, with FIFO within each lane; a ticker queue must not monopolize pacing. The full sliding-window check remains mandatory. A request reserves no HTTP slot while waiting for pacing, budget, or cooldown.

### Capacity checks

- Spot FULL statistics cost 80: the 240-unit share permits three attempts per minute, or two routine refreshes plus one retry. USDⓈ-M statistics cost 40: the 96-unit share permits two attempts, with 16 units left. Retries may delay a later refresh; the 30s worker interval is not a freshness guarantee.
- Spot exchangeInfo costs 20 and fits the 240-unit instruments share. USDⓈ-M exchangeInfo costs 1 and fits 96; fundingInfo additionally consumes the 400-request family allowance. Bybit instrument pages cost one request and fit 24 per 5s.
- All configured kline page costs fit: Spot 2, Bybit 1, USDⓈ-M up to 10 when a permitted page exceeds 1,000 candles. The default history bound only requires pages up to 1,000; validation still checks adapter calls allowed by configuration.
- Bulk ticker requests fit individually and as ordinary cycles: Spot 8 weight per full price/book cycle, USDⓈ-M 17, Bybit one shared request per market. Continuous operation eventually waits for its own share.
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
- Upstream response bodies are capped at 16 MiB before unbounded buffering/SDK decode. HTTP request headers are capped at 32 KiB and query strings at 8 KiB. Excess queries return 414 request_too_large; body-bearing GET requests return 400 invalid_parameter. Snapshot reads have a 5s timeout.

Instrument decimal fields accept at most 1,024 bytes of source numeric text and at most 1,024 characters of fixed-point expansion before trimming trailing fractional zeros, including the leading zero and decimal point where needed. Check source length before parsing and the coefficient/exponent bounds before decimal comparison or publication. The same bounds apply to explicit zero. This prevents small scientific-notation inputs from causing large allocations during normalization or HTTP serialization. Oversized values fail the whole refresh with invalid_upstream_data and preserve the prior snapshot; never round or truncate them. Scientific notation within these bounds remains exact.

HTTP handlers do not stream a partial successful JSON response before completeness validation. The server write deadline is derived as max(kline request timeout, snapshot timeout) + 5s for bounded serialization/writing, rather than a second independent 30s timer. Graceful shutdown defaults to 35s, cancels root work, stops accepting requests, and waits within that bound.

## Bootstrap, discovered limits, and cooldown

The configured window ceilings seed admission before the first exchangeInfo; that request uses the instruments lane and normal cost. A valid catalog is applied even if later instrument normalization fails, because limits are transport safety metadata. No market snapshot is published from an invalid refresh.

A catalog update is accepted only from a successful response to a request at least as new as the last accepted catalog request start. Keep configured windows and the funding-family limit even if omitted from a later catalog. Apply recognized REQUEST_WEIGHT/RAW_REQUESTS limits in their proper scope; ignore ORDERS. Never copy Spot limits onto USDⓈ-M. Limit increases cannot exceed configured ceilings or reset local usage. Reductions take effect immediately for new admissions; wait for recorded usage to age out when necessary.

Add newly observed applicable windows conservatively. If available local history does not cover a new/longer window, pause the affected scope for that full window while starting its ledger. Do not pretend prior usage was zero. An unrecognized applicable limit type or malformed catalog blocks new requests in that scope as upstream_unavailable and emits an actionable diagnostic. A derived allowance too small for an allowed request has the same fail-closed behavior; do not enter an endless queue. Previously ready snapshots remain readable.

Trustworthy usage headers may only tighten admission. A reported used amount above the tracked local amount with uncertain window positioning causes a scope wait for that window, not a counter reset. Ignore the documented inaccurate USDⓈ-M price-v2/book headers. Parse Retry-After seconds or an HTTP date; reject negative/invalid values. Compute Bybit reset waits from a valid timestamp on the rate-limit signal, not merely header presence on success. Late responses never shorten an existing wait.

Fallback cooldowns: Binance 429 60s; Binance 418 72h; Bybit 429 or retCode 10006 60s; Bybit access-too-frequent 403 at least 10m. Use the later of an existing cooldown and a newly indicated valid wait. With a valid explicit wait, the fallback does not impose a longer unrelated period, except Bybit's documented 10m minimum for the specific 403. Missing/invalid/past wait metadata activates the fallback. An unrelated 403 is a non-retryable upstream error. Binance ban duration can reach three days according to its [official IP-limit rules](https://github.com/binance/binance-spot-api-docs/blob/master/rest-api.md#ip-limits).

### Restart behavior

User decision: v1 keeps all admission state in memory. Do not persist request ledgers, discovered limits, cooldowns, or shutdown status. No state file, initialization command, persistent mount, or clean/unclean restart distinction is required.

Each process starts with empty local ledgers and configured bootstrap ceilings. Workers may attempt requests immediately through normal admission; there is no automatic restart quiet period. Exchange-side usage and bans can survive a restart. Therefore the service cannot guarantee continuity of budget accounting or cooldown enforcement across restarts. A new rate-limit response applies the normal in-memory cooldown again. Log this limitation at startup and document it in the operating guide. Do not use restart as a way to bypass an exchange limit. Persistence is deferred beyond v1.

This local policy does not account for another process sharing the outgoing IP. Verify the deployment topology before release.

## HTTP contract

### Common envelope and validation

All four endpoints are GET-only and return application/json. Other methods return 405 method_not_allowed. Successful data responses are always `{"data":[...]}`, including a symbol-filtered result or an empty list. Objects use the specification's snake_case fields, decimal strings, explicit optional nulls, and UTC RFC 3339 timestamps preserving available precision. No SDK objects appear in responses.

Reject unknown query keys, malformed percent encoding, empty present values, or repeated scalar parameters with 400 invalid_parameter, except window follows unsupported_window, status follows invalid_status, and interval follows invalid_interval. Do not silently accept the last repeated value. Exchange/market/interval/status are canonical and case-sensitive. Symbols are exact UTF-8 identifiers, 1–128 bytes, without whitespace or control characters; no uppercasing or ASCII-only assumption. A syntactically invalid symbol is invalid_filter.

Validation order is: HTTP/query shape; canonical enum/filter values and enabled scopes; timestamp syntax and range shape; bounded slot count; history depth; data readiness and symbol lookup; execution. When several parameters are invalid, use that order and then parameter order exchange, market, symbol, status/window, interval, from, to. Apply the documented special window error before data access. All invalid inputs fail without upstream calls.

### Instruments, tickers, and statistics

Filters are optional. Instruments accepts exchange/market/symbol/status; tickers accepts exchange/market/symbol; statistics additionally accepts window, default 24h. Omitted exchange/market selects all enabled supported pairs. An explicit unknown/disabled exchange or market, or a filter combination selecting no enabled pair, returns 400 invalid_filter. Return rows sorted by exchange, market, symbol ascending.

All selected scopes must have published their first successful snapshot before returning a combined list. Otherwise return 503 data_not_ready, including instruments and tickers. Keep readiness independent per repository and scope. A successful empty snapshot or absent symbol in ready selected scopes returns 200 with data: []. Status/symbol filtering cannot hide an unready scope. After refresh failure, return the previous successful data with its unchanged timestamps; a stale-data flag or maximum staleness policy is not added in v1. Snapshot reads never fetch upstream.

### Kline range contract

Required single parameters: exchange, market, symbol, interval, from, to. `from` and `to` are RFC 3339 timestamps with an explicit timezone; accept Z or a numeric offset and normalize to UTC. Reject integer Unix timestamps, date-only values, missing offsets, leap seconds, more than nine fractional digits, invalid calendar dates, and times before Unix epoch. Bound arithmetic before allocation. Range boundaries must align exactly to the selected calendar; reject unaligned values with 400 invalid_range rather than rounding.

The range is half-open by candle OpenTime. Include starts satisfying from <= OpenTime < to. `from > to` is invalid_range. A valid `from == to` returns data: [] after scope/catalog/symbol validation, without a kline fetch. Read the current instrument snapshot to validate symbol existence; unready instruments give 503 data_not_ready and a missing symbol gives 404 symbol_not_found. No historical symbol discovery call is added.

The default workload uses only closed candles, with `to=C`, where C is the latest boundary at or before now. Preserve the existing open-candle capability: a client may explicitly request the current slot by setting to to the next boundary after C. A range ending later, or beginning after C, is invalid_range; equality for an empty range at the next boundary is allowed. Open inclusion consumes a slot of the configured N-slot request limit. Do not add an include_open parameter, change the canonical interval set, or synthesize candles.

Apply the history window and size rules from specification section 38. Missing slots do not move the oldest allowed boundary. If a nonempty valid range includes pre-listing slots, a trading halt with no rows, or other unavailable history, a successful empty/short upstream response is not proof of a complete range. Return 502 incomplete_data after the bounded fetch shows no progress; keep valid fetched rows. Do not create zero candles, silently trim to listing, negatively cache absent slots as complete, or retry an unchanged logical gap forever. An empty requested range is different from an empty response to a nonempty range.

See the [synthetic request/response examples](examples/http-contract-v1.json) for successful, empty, unready, invalid, overloaded, and incomplete outcomes.

An API request for the maximum rolling window can become too old if a new slot closes while it waits. v1 returns range_out_of_retention in that case, as already specified, rather than extending history or returning a partial result. This is especially relevant to the optional 1s interval; clients can leave a small lookback margin. Record this limitation in the release guide and tests.

### Stable errors

| HTTP | Code | Condition |
| --- | --- | --- |
| 400 | invalid_parameter | Missing/empty/repeated/unknown query input or malformed query encoding |
| 400 | invalid_filter | Invalid, disabled, or incompatible exchange/market/symbol filter |
| 400 | invalid_status / unsupported_window / invalid_interval | Invalid canonical value for the corresponding field |
| 400 | invalid_range | Timestamp syntax, reversed/unaligned range, or disallowed future slots |
| 400 | request_too_large | More than configured N candle slots |
| 400 | range_out_of_retention | Valid size but an older range start |
| 404 | symbol_not_found | Symbol absent from the ready current instrument catalog for klines |
| 404 | not_found | Unknown HTTP route |
| 405 | method_not_allowed | Non-GET method |
| 414 | request_too_large | Query string exceeds the byte bound |
| 503 | data_not_ready | A selected required snapshot has never been published |
| 503 | service_overloaded | Full caller/fill/admission capacity or admission wait timeout |
| 503 | upstream_unavailable | Known cooldown or unusable runtime limits prevent execution within the operation lifetime |
| 504 | request_timeout | Caller or owned fill deadline expires before a complete result is ready |
| 502 | upstream_error | Non-retryable upstream failure or exhausted transport/HTTP retries |
| 502 | invalid_upstream_data | Malformed envelope/row, body limit, invalid decimals, or inconsistent response scope |
| 502 | upstream_attempt_limit | Total page/retry attempt allowance exhausted, including an impossible bounded plan detected before sending |
| 502 | incomplete_data | Remaining requested slots are absent after successful bounded fetching/no progress |
| 500 | internal_error | Unexpected internal or repository failure |

Errors use `{"error":{"code":"...","message":"..."}}`, with stable English messages and no raw exchange payload. A disconnected client receives no guaranteed response; cancellation releases that caller's resources. Never return a successful partial body. If saved data already satisfies that caller's complete range after a shared fill error, the existing section 31 cache-success rule still applies.

## Startup and metadata

After config validation and local initialization, bind HTTP and start instrument, ticker, independent statistics, and retention workers without awaiting successful instrument fetches. Instrument/ticker/statistics first cycles are scheduled immediately but pass admission and cooldown gates. Each enabled exchange/market owns at most one cycle per operation. A failed scope does not stop other scopes. Readiness means local HTTP/storage are initialized, not exchange availability. Before first data, the affected endpoint returns data_not_ready.

For Bybit linear instrument collection, fetch the default current catalog (documented Trading/PendingOpen) plus the explicit status=PreLaunch catalog, including all cursor pages; spot uses its non-paginated default catalog. Omit baseCoin so other categories cannot enter the result. Identical duplicate symbols across the two collections may be deduplicated; conflicting duplicates fail that refresh. These choices follow the [Bybit instrument reference](https://bybit-exchange.github.io/docs/v5/market/instrument). Do not claim a historical/delisted catalog. Binance uses complete exchangeInfo. Reject repeated cursors or no pagination progress; total pages and retries use the instruments attempt/deadline bounds. A partial catalog never replaces the current snapshot. Rows absent from a complete selected catalog are removed without inventing closed records.

Binance funding interval decision: use a valid explicit fundingIntervalHours from the successful full fundingInfo response; absent perpetual symbols return null. The generic eight-hour default in the FAQ is not a per-symbol current-value guarantee, especially for inactive instruments. Do not infer an interval from history or timestamps. Unknown contract types and expiry futures get no fallback. A required fundingInfo request/parse failure fails the refresh and preserves the previous instrument snapshot; it does not publish nulls or defaults. The fixture distinguishes explicit 1/4/8-hour values from absence. Binance delisting_time stays null until an authoritative source establishes the perpetual placeholder semantics; this is a deliberate v1 limitation, not a blocker for instruments.

## Configuration loading and verification gates

Load built-in defaults, recursively overlay supplied YAML leaves, then explicit MDS_ environment leaves, then validate. Missing values use defaults; present null/invalid values fail. Lists replace as a whole; windows have the fixed IDs in the example, with supported fields checked against scope/units. Unknown YAML keys, duplicate keys, and unknown MDS_ variables fail startup. Non-MDS_ environment variables are ignored. Scalar environment names use the uppercased underscore path, including the operation names already recorded in section 43; list values are JSON arrays. No shell interpolation or implicit ${VAR} expansion is performed in YAML. MDS_SENTRY_DSN remains an explicit alias for observability.sentry.dsn; specifying it together with MDS_OBSERVABILITY_SENTRY_DSN is an error.

Validate positive durations/counts, finite caps, 0–99 integer safety margin, exact four positive share percentages totaling 100, at least one enabled exchange/market, unique market lists, exactly [24h], provider capabilities, URL-free fixed scope names, and integer multiplication/calendar overflow. HTTP port is 1–65535. Retry counts and response byte limits are positive integers. Binance 418 fallback must be at least 72h; the Bybit access-too-frequent fallback must be at least 10m. Other configured rate-signal fallbacks must be at least 60s. Validate these minima without shortening a longer explicit exchange cooldown. Disabled providers do not create workers; configured values must still be syntactically valid.

Global HTTP/waiter caps must cover the sum of configured per-exchange lanes for enabled providers. Fills must not exceed their global/per-exchange limits or the available kline lane HTTP capacity. Global fill/caller caps must fit positive finite integers. Shutdown timeout must cover the maximum active caller/fill lifetime plus 5s. Verify the largest valid kline range fits at least its minimum page count within the total attempt bound; lowering the upstream page size may require increasing that bound. A worker may still fail on retries or unavailable exchange data. Validate budgets against each permitted single request and two ordinary statistics cycles per minute for the default 30s interval; changed schedules require recalculating capacity, not inventing higher exchange limits.

Phase 02 implements the loader, baseline lifecycle, and checks the pinned Go 1.27.1 toolchain. The official release catalog confirms availability; the current local binary is 1.26.0. Do not silently change the pin. Phase 05 implements in-memory admission and restart behavior and controlled-time tests. Phases 06–10 recreate earlier SDK contracts against local fake servers at the pinned revisions; historical reported tests are not accepted as executed project tests. Phase 12 measures memory and deployment access. None of these future gates prevents the specification decisions themselves from being recorded.
