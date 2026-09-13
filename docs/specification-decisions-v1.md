# v1 specification decision register

Status: complete, September 11, 2026. Requirements: [technical specification](technical-specification-v1.md).

This register separates user-confirmed requirements, adopted engineering defaults, captured evidence, and future implementation checks. Phase 01 closes specification decisions; it does not claim an implemented or tested service. See the [implementation contract](implementation-contract-v1.md), [configuration defaults](examples/config-v1.yaml), and [evidence inventory](evidence/phase-01/README.md).

## Decision status and implementation gates

| ID | Item | Status and disposition | Evidence or required input | Dependent phase |
| --- | --- | --- | --- | --- |
| D01 | Deployment topology and outgoing IP | One instance confirmed. Working interpretation of the user's “No” is that no other clients call these exchanges from the same outgoing IP; this does not prove ownership of a dedicated IP. Use a local limiter for v1. | User clarification on September 13, 2026: the host may be this computer, an internet server, or another host; always one running instance on one outgoing IP. The user confirms exchange access from deployment hosts. Current-computer compatibility checks pass in [phase 12](release-verification-v1.md). No dedicated-IP ownership or unrelated-client traffic audit is claimed; the local limiter still assumes no unaccounted exchange traffic. | 02 config, 05 admission, 12 deployment |
| D02 | Enabled markets | Confirmed: enable spot and linear on both Binance and Bybit by default. | User response on September 11, 2026. Four enabled exchange/market pairs; Binance linear maps to USDⓈ-M. | 02 config |
| D03 | Workload and memory | Confirmed profile: 3–4 clients, up to 50 symbols, three history windows (1h for 20 days, 5m for 2 days, 1m for 8 hours), and a 1 GB service RAM limit. Clients need closed candles and may request after each close or repeat reads at other times. Complete confirmed ranges are served from cache without upstream requests. For conservative sizing, assume 50 symbols per exchange/market pair. Phase 12 measurements meet the 800 MB peak process-memory target for this conservative workload; all-interval capacity exceeds 1 GB. See the [release audit](release-verification-v1.md). | User responses on September 11, 2026. No fixed client polling schedule is required. A configurable 30-second request deadline is confirmed in D18. Exact incoming request rate is unspecified; cache hits still consume local resources. See the workload calculation below. | 02 bounds, 10 API, 12 load verification |
| D04 | Operation budgets without borrowing | Confirmed: tickers 60%, klines 30%, instruments 5%, independent market_stats 5%, with no borrowing. Configurable integer defaults under upstream.operation_share_percent; shares apply to common service allowances after the safety margin. Adopted engineering choice under the user's request for judgment: combine Bybit's shared ticker/statistics allocation into a fixed 65% pool, counted once as tickers; this is not runtime borrowing. | User accepted separate budgets and specified the four shares on September 11, 2026. Section 32 defines scope, integer rounding, and inapplicable paths. D05 records absolute allowances and resource bounds. | 02 config, 05 admission |
| D05 | Limit and configuration schema | Adopted engineering defaults: captured common ceilings with 20% margin, finite lanes, queues, deadlines, attempts, pacing, and checked operation shares. | Complete schema and feasibility calculations in the implementation contract; validate configuration in phase 02 and behavior in phase 05. | 02 config, 05 admission, 10 fills |
| D06 | Cooldown and restart behavior | User confirmed memory-only state for v1. Restart discards counters, discovered limits, and cooldowns; configured bootstrap limits apply immediately. No persistent journal or automatic restart wait. Exchange-side limits can survive restart. | Per-signal fallbacks are specified in the contract. Persistence is deferred beyond v1. Verify restart resets only local state and document the limitation. | 02 config, 05 admission, 12 operations |
| D07 | Binance limit discovery and updates | Adopted: bootstrap every request through configured scoped budgets; runtime catalogs and trustworthy headers can tighten limits without resetting usage. Unknown applicable limits block the scope. | Captured exchangeInfo ceilings and update rules in the implementation contract. New windows without enough local history require a full-window wait. | 02 config, 05 admission |
| D08 | Binance funding interval | Adopted: use explicit valid fundingInfo intervals; missing symbols return null, with no inferred eight-hour fallback. A required-source failure preserves the previous whole snapshot. | Live metadata excerpt and synthetic failure cases distinguish 1/4/8-hour values, absence, malformed values, and request failure. | 06 instruments |
| D09 | Binance delisting source | Deferred with an explicit v1 behavior: keep delisting_time null. The source remains unconfirmed. | E02 below. A non-null implementation needs authoritative perpetual-contract semantics and a fixture distinguishing a real event from deliveryDate placeholders. | 06 only for non-null Binance delisting |
| D10 | Weekly and multi-day alignment | Anchors fixed from live examples: Monday 00:00 UTC for 1w; Binance 3d uses 1970-01-02 00:00 UTC plus multiples of 72h. | Twelve raw calendar captures cover BTCUSDT/ETHUSDT, all affected markets, and a year boundary, with expected normalized rows. Replay offline during implementation; samples do not prove every historical candle. | 03 calendar, 08–09 adapters |
| D11 | Binance Spot FULL 24hr bulk access | Confirmed by documentation and a successful public bulk request without symbol filters, type=FULL. | Captured 3,701-row response summary, required-field checks, and excerpt. Local SDK integration passes. The September 13, 2026 current-host live check again returns 3701 rows with required fields; the user confirms access from their deployment hosts. See [phase 12 evidence](evidence/phase-12/live-compatibility.json). | 07 adapter verification, 12 deployment |
| D12 | HTTP contracts | Adopted: common envelopes, strict filters, independent readiness for all snapshots, RFC 3339 aligned half-open kline ranges, completeness and stable errors. | Implementation contract and synthetic HTTP examples cover success, empty/unready, invalid, overload, and incomplete data. Existing explicit open-slot capability remains available; the normal workload uses closed slots. | 06–07 snapshot APIs, 10 kline API |
| D13 | History depth, retention, and older reads | Confirmed replacement: every supported interval uses the most recent 1,000 closed calendar slots. Configure N with klines.max_history_candles, default 1000; the same value bounds request slots and cache history. Older requests return 400 range_out_of_retention before cache/fill access. Counts above N return 400 request_too_large. | User replaced the per-interval duration policy on September 11, 2026. Existing configurability preference remains applicable. Sections 38 and 41 define calendar cutoff, scope, pruning, and concurrent reads. D10 calendar evidence and D12 completeness rules define affected behavior. Memory must be measured across all requested series. | 02 config, 03 calendar, 04 repository, 08 planning, 10 reads, 11 cleanup, 12 memory |
| D14 | Initial instrument load | Adopted: bind HTTP after local initialization, schedule first loads immediately and independently, and never await exchange success for global readiness. | Contract defines per-scope data_not_ready, full-snapshot publication, failure backoff, and ticker independence from instrument loading. | 02 lifecycle, 06 workers |
| D15 | Candle finalization wording | Confirmed existing requirement. Time passing alone does not finalize cached values. | Specification sections 8 and 31 require a successful fetch whose request started after close. Section 26 now uses that qualification. | 04 cache metadata, 08–10 |
| D16 | Decimal JSON | Confirmed existing requirement. Decimal market values are JSON strings. | Specification sections 5–8 already require strings; section 39 now uses mandatory wording. | 03 models, 06–10 transport |
| D17 | Toolchain and earlier SDK checks | Official release catalog confirms Go 1.27.1 availability; keep the pin. Local Go is 1.26.0; the pinned toolchain has not been executed here. | Captured release metadata and archive checksums. Phase 02 installs/verifies the pin; phases 06–09 recreate SDK contracts at pinned revisions. Historical checks are not current project tests. | 02 build, 06–09 adapters, 12 release |
| D18 | Kline request deadline | Confirmed: default 30 seconds, configurable as klines.request_timeout; environment override MDS_KLINES_REQUEST_TIMEOUT. Apply one total deadline to each caller, without restarting it for pages or retries. | User accepted 30 seconds and explicitly requested configuration on September 11, 2026. Scope, validation, and timeout behavior are defined below and in specification sections 33 and 42–44. | 02 config, 10 API |

## Evidence checked on September 11, 2026

### E01 — Funding interval

The [Binance fundingInfo API](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#get-funding-rate-info) describes adjusted funding settings. The [Binance funding explanation](https://www.binance.com/en/support/faq/detail/360033525031) identifies an eight-hour default and possible interval adjustments.

Adopted mapping: after a successful complete response, use an explicit valid interval where present and null for absent symbols. The generic default is not enough to establish a current value for every symbol. Spot and expiry futures retain null. Request or parsing failure retains the previous whole instrument snapshot. See the [captured funding response](../testdata/exchange/binance-funding-info.json) and [synthetic failure cases](evidence/phase-01/funding-failure-cases.json).

### E02 — Delisting

The [Delist-Schedule URL](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/market-data/rest-api/Delist-Schedule) returned the general developer landing page, not an endpoint contract. The checked USDⓈ-M market-data reference did not resolve the perpetual deliveryDate interpretation. This is evidence of an unresolved source, not proof that no delisting endpoint exists. Keep the existing null behavior.

### E03 — Spot bulk statistics

The [official Binance REST API source](https://github.com/binance/binance-spot-api-docs/blob/master/rest-api.md#24hr-ticker-price-change-statistics) explicitly allows both symbol parameters to be absent, returns all symbols as an array, supports FULL, and assigns weight 80 to the bulk request. This resolves the documentation conflict recorded in section 14. A subsequent live public request on September 11, 2026 returned 3,701 rows. The [capture summary](evidence/phase-01/binance-spot-full-statistics.json) contains provenance, body checksum, and a row excerpt; it does not contain the complete body.

## Phase 02 handoff

The specification decisions are complete. Implement the complete configuration profile and lifecycle next. Future gates remain explicit: run the pinned toolchain, replay calendar/metadata fixtures through real adapters, test admission and API behavior, measure the 1 GB memory envelope, and verify deployment egress/access. These are implementation and release checks, not unanswered product questions. Limiter persistence is explicitly deferred by the user.

## Confirmed operation allocation

The user's allocation is recorded as exact default shares: tickers 60%, klines 30%, instruments 5%, and independent market_stats 5%, with no borrowing. Each common allowance uses the unit and scope of its limit. Percentage values are configurable under upstream.operation_share_percent. Missing entries inherit defaults; the final four positive integer shares must total 100. Section 32 defines floor rounding, provider capability handling, and endpoint-group limits.

The user then asked for engineering judgment. Adopted engineering choice: keep 60/30/5/5 as the base profile, and combine ticker/statistics shares for a provider with MarketStatsWithTicker=true. Bybit therefore has a fixed 65% joint path, 30% klines, and 5% instruments. Combine percentages before integer rounding and charge the shared response once as tickers. Binance keeps the independent four-way allocation. This supersedes the initial idea of leaving Bybit statistics capacity idle; no runtime borrowing is introduced.

### Minimum feasible allowances

These are arithmetic constraints from the request costs already recorded in specification section 14, minimum feasibility constraints; the actual common budget defaults are recorded in the implementation contract. For cost c and percentage p, the integer common allowance B must satisfy floor(B * p / 100) >= c. The same check applies to every allocation window involved in a request.

| Common allocation scope and operation | Most expensive single-request cost relevant here | Share | Minimum common allowance to admit it |
| --- | --- | --- | --- |
| Binance Spot market_stats FULL bulk | 80 weight | 5% | 1,600 weight |
| Binance Spot instruments exchangeInfo | 20 weight | 5% | 400 weight |
| Binance Spot tickers, either bulk price/book request | 4 weight | 60% | 7 weight |
| Binance Spot klines | 2 weight | 30% | 7 weight |
| Binance USDⓈ-M market_stats bulk | 40 weight | 5% | 800 weight |
| Binance USDⓈ-M instruments exchangeInfo | 1 weight | 5% | 20 weight |
| Binance USDⓈ-M tickers premiumIndex bulk | 10 weight | 60% | 17 weight |
| Binance USDⓈ-M klines, sent limit up to 1,000 | 5 weight | 30% | 17 weight |
| Binance USDⓈ-M klines, sent limit 1,001–1,500 when allowed by config | 10 weight | 30% | 34 weight |
| Bybit instruments, one page | 1 request | 5% | 20 requests |

Binance fundingInfo has zero common weight but still consumes its separate endpoint-family request limit and any applicable common request-count allowance. Zero weight does not permit unrestricted calls.

If a minute allocation must support two ordinary Binance statistics refreshes without retries, its minimum common allowance is 3,200 Spot weight or 1,600 USDⓈ-M weight. The existing worker waits 30 seconds after completion, so these are capacity checks, not an exact twice-per-minute execution promise. A single-request feasibility check alone does not establish the desired refresh cadence. Retries compete within the same 5% share; stale-snapshot and deadline rules still apply.

The implementation contract records absolute budgets, exchange windows, a 20% safety margin, and enabled paths. Tiny derived budgets, especially 5% shares in short request-count windows, must fail configuration validation instead of starving instrument loading. Test derived shares, floor rounding, invalid sums, threshold minus one versus exact threshold, shared versus independent provider paths, disabled operations, and expensive requests in phase 02; phase 05 tests observable no-borrowing behavior with controlled time.

## Confirmed kline request timeout

```yaml
klines:
  request_timeout: 30s
```

This is a service-wide default for each kline API caller requesting one exchange/market/symbol/interval. It starts when the HTTP handler begins processing and includes validation, cache reads, waiting for admission or a shared fill, upstream work awaited by this caller, retries, and the final completeness check. Return earlier when data is available; a cache hit does not wait for the timeout. Pages, retries, and repeated coordination do not reset the clock. An earlier caller deadline or cancellation takes precedence.

Omission uses 30s. YAML and then MDS_KLINES_REQUEST_TIMEOUT may override it with a positive, finite duration representable by time.Duration. Reject explicit empty, zero, negative, malformed, and overflowing values at startup. This is distinct from http_client.timeout, which limits an individual upstream HTTP attempt. A shared fill retains its own service-owned bounded lifetime from section 31; one caller timing out must not cancel work still awaited by others. D05 sets a separate configurable 30s fill lifetime.

If the caller's service deadline expires before a complete response is ready, return HTTP 504 with code request_timeout while the connection remains writable. Do not return a successful partial range. Successfully saved pages can remain cached. Client disconnection stops that caller's work without attempting to guarantee a response.

Phase 02 must test defaults, YAML/environment precedence, and invalid values. Phase 10 must test timely cache hits, expiry during waiting/fetching/retry, no deadline reset across pages, an earlier caller deadline, and independent caller cancellation during a shared fill. Use controlled time and synchronization; no real 30-second sleeps.

## Workload calculation

Confirmed user input on September 11, 2026: 3–4 clients, up to 50 symbols, and the following three history windows. Counts below use aligned, half-open ranges and exclude an additional current candle.

The user does not need the current open candle for this workload. Clients may request after a candle closes and may repeat requests at other times; no fixed polling schedule is required. A range containing only cached, confirmed closed candles returns from the repository with zero upstream requests. When a newly closed candle is missing, fetch the missing data through the existing planner and fill coordination. Cache-hit requests do not consume upstream budgets, but still use local CPU, memory, and HTTP capacity; this is not an unlimited incoming-throughput guarantee.

Size normal upstream demand around closed-candle updates without repeated intra-candle refreshes. For all 200 assumed exchange/market/symbol combinations, one new candle per interval boundary means 73 new candles per hour per combination (60 minute candles, 12 five-minute candles, and one hourly candle), or 14,600 new candles per hour overall. This is data growth under continuous demand, not an HTTP request-rate guarantee: pagination, batching within a series, gaps, retries, and concurrent request reuse affect actual attempts. Boundaries create bursts, so hourly averages alone cannot size admission.

This workload decision does not by itself remove the existing open-candle API behavior from the specification; D12 records explicit endpoint range behavior. A previously cached intermediate candle still requires the post-close confirmation defined in section 8 before reuse as final data.

| Interval | Requested history | Candles per series | Candles for 50 symbols in one exchange/market pair | Candles for 50 symbols in each of four pairs |
| --- | --- | --- | --- | --- |
| 1h | 20 days | 480 | 24,000 | 96,000 |
| 5m | 2 days | 576 | 28,800 | 115,200 |
| 1m | 8 hours | 480 | 24,000 | 96,000 |
| Total | Three intervals | 1,536 per symbol | 76,800 | 307,200 |

The user's symbol count does not specify whether it is global or per pair. The four-pair calculation is a conservative sizing assumption, not a new product requirement or an instrument-collection filter. Snapshot collectors still fetch the full configured markets. Shared series are stored once regardless of client count; distinct ranges and requests for open candles can still create more work.

Each listed cold range fits in one upstream page at the existing section 30 defaults. Loading all three intervals for 50 symbols in each pair therefore needs 600 successful kline HTTP requests before retries or separate boundary refreshes. This is a count estimate, not a completion-time guarantee. The existing request-cost table implies 300 Binance Spot weight units, 450 Binance USDⓈ-M weight units, and 300 Bybit requests for this load. These totals are usage, not proposed per-window budgets. Actual costs use the sent page limit.

Confirmed bound: klines.max_history_candles defaults to 1000 and limits both lookback from the latest close and the requested slot count for one exchange/market/symbol/interval. It covers the largest stated range of 576 candles. It is separate from the upstream page limit and does not authorize arbitrary older 1,000-slot ranges. All originally supported intervals remain in scope; D12 defines exact range inclusion and missing-data rules.

### Confirmed common history limit

The latest user decision replaces the previously accepted 21d/3d/12h durations with one configurable slot count for every supported timeframe:

```yaml
klines:
  request_timeout: 30s
  max_history_candles: 1000
```

Omitting max_history_candles uses 1000. Override it in YAML or with MDS_KLINES_MAX_HISTORY_CANDLES. No per-interval retention map or duration fallback remains. Section 44 defines integer validation. The configuration loader is implemented in phase 02, not in this documentation phase.

| Interval | Span of 1,000 closed slots |
| --- | --- |
| 1m | 16 hours 40 minutes |
| 5m | 3 days 11 hours 20 minutes |
| 1h | 41 days 16 hours |
| 1w | 1,000 calendar-aligned weeks |
| 1M | 1,000 calendar months |

At each check, find C, the latest exchange/market slot boundary at or before the injected UTC clock value, then step N slots backwards to cutoff. The valid closed-slot window is [cutoff, C). Gaps, absent pre-listing candles, cache contents, and client-supplied to do not shift that window backwards. All existing timestamp validation still applies; the window is not a guarantee that an exchange has N historical candles. D10 records weekly/multi-day evidence; D12 defines missing/pre-listing results.

Count requested slots before candle storage or upstream access. More than N returns 400 request_too_large; a smaller request with from before cutoff returns 400 range_out_of_retention. Equality is allowed by the depth check. Neither case may return a trimmed successful result. The open candle does not change the history anchor; its explicit inclusion is allowed by D12 and it counts toward request size if requested.

Cleanup deletes OpenTime strictly before the same cutoff, keeping equality, with exchange/market/interval scope to respect calendars. Prune during fill merges and clean idle series periodically. Missing recent slots do not justify retaining older ones to reach N rows. Late writes must not reinsert data below an applied cutoff. A waiting caller whose range expires fails instead of repeatedly refetching data removed by cleanup. See specification sections 38 and 41.

### Memory verification

The user assigned 1 GB to the service. For conservative sizing, interpret this as 1,000,000,000 bytes for the whole process, not just candle records or the Go heap.

The initial three-window load remains 307,200 candles under the conservative assumption of 50 symbols per exchange/market pair. Continuous demand for those three intervals can retain up to 600,000 closed-slot records (50 symbols × 4 pairs × 3 intervals × 1,000), plus current open records if that behavior is used. This replaces the earlier 417,600-slot duration-profile calculation.

The specification supports 16 Binance Spot intervals, 15 Binance linear intervals, and 13 per Bybit market. If every supported interval is requested for 50 symbols in every pair, the slot capacity is 2,850,000 closed records. This is a conservative capacity scenario, not a confirmed workload or a preloading requirement; some series have less history. Slots alone do not estimate bytes. A per-series bound does not bound the number of symbols/series clients can request, and the 50-symbol assumption is not an enforced API allowlist.

Before phase 12, measure retained heap and peak process memory using implemented decimal values, cache indexes, finalization metadata, full instrument/ticker/statistics snapshots, concurrent fills, and response serialization. Include four clients, the main three-interval workload at retained capacity, broader interval demand, idle-series cleanup, and late writes. Record data counts, decimal lengths, configuration, toolchain, and measurement method. Use deterministic synthetic data without exchange access. Do not claim that 1 GB is sufficient before measurement.

Engineering verification target: peak process memory at or below 800,000,000 bytes for the agreed workload, leaving 200,000,000 bytes of operating headroom. This is a test target, not a runtime memory guarantee or a Go runtime setting. If measurements fail, revisit the count, concurrency, series scope, or memory envelope before release.

### Required implementation checks

- Phase 02: omitted count uses 1000; YAML and environment precedence; reject null, empty, nonpositive, fractional, boolean, malformed, and overflowing values; reject obsolete duration-map settings.
- Phases 03 and 08: 1,000 versus 1,001 slots; exact and just-before cutoff; boundary advancement; leap-year/calendar-month subtraction; weekly/multi-day anchors; overflow; gaps must not widen lookback. Calculate expected values independently.
- Phases 04, 10, and 11: old requests against empty/populated caches, partly expired ranges, expiration during waiting, scoped cleanup, pruning on fill merge, and late writes. Rejected requests make no upstream attempts or trimmed successful responses.

These are future implementation checks. No application tests were executed in phase 01.
