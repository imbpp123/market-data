# 06 — Instruments end to end

Status: complete, September 12, 2026. Dependencies: [04](04-memory-repositories.md), [05](05-upstream-admission-and-retries.md), metadata/API decisions from [01](01-specification-decisions.md). Next: [07](07-tickers-and-market-statistics.md).

## Outcome

The first complete data path: Binance/Bybit instrument response → normalization → atomic repository → HTTP API. Source: [specification](../technical-specification-v1.md), sections 5, 14–16, 19–20, 35, 39–40, and 54.

## Work

- Implement instrument adapters for the documented spot/linear pairs. Pin request categories, selected status filters, and cursor pagination so “full snapshot” has an explicit meaning.
- Map identifiers, all status rows, limit-order filters, optional limits, funding intervals, and delisting semantics exactly. Select Binance filters by type; preserve Bybit spot basePrecision as a step and MinQty as null.
- Join Binance funding metadata only under the phase 01 decision. Required-source or page failures retain the previous whole snapshot; never infer defaults from failed requests.
- Start an immediate refresh and then use each exchange's configured interval, with no overlapping refresh for a scope. Keep exchange failures independent and waits cancelable. Use the instruments budget for every metadata page and retry.
- Assign one UpdatedAt after full successful fetch and normalization. Atomically replace the scope only after all required sources succeed.
- Implement the application read use case and GET /api/v1/instruments with exchange/market/symbol/status filters and the agreed response contract. Add shared HTTP error mapping and DTO conventions as needed by this real endpoint.
- Wire the executable, real memory repository, health/readiness, and instrument worker. Add refresh success/error metrics at the publication boundary.

## Tests and checks

- Every specified status mapping plus missing/unknown values; unknown values remain unknown and invalid API status filters return 400 invalid_status.
- Decimal precision, missing/invalid/negative limits, zero or negative required steps, conflicting min/max, and filter ordering.
- Limit versus market/post-only values; Binance spot minimum-notional selection; no deprecated Bybit spot fields or precision metadata substitutions.
- Funding minutes/hours → domain duration → integer JSON seconds; perpetual versus expiry/spot, zero delivery time, and explicit Binance intervals, missing-symbol nulls, and failed-source preservation.
- Failed pagination, required-source fetch, normalization, or repository write preserves the previous snapshot and UpdatedAt. Successful replacement removes absent instruments.
- Controlled-clock immediate refresh and configured wait; cancellation, retry backoff, and one exchange failing while the other publishes.
- httptest.Server → real adapter → repository → HTTP verifies string decimals, explicit null, UTC timestamps, filters, and zero upstream requests caused by reads.

## Exit criteria

Both adapters and the read API work with local fixtures. Instrument refresh is owned, bounded, observable, and independently cancelable. Production access is not required for the automated acceptance tests.

## Delivery record

- Added Binance Spot/USDⓈ-M and Bybit spot/linear instrument providers using the pinned SDK clients and the shared admission transport. Binance uses complete exchangeInfo and required linear fundingInfo. Bybit linear requests the default catalog and PreLaunch with a page limit of 1,000; spot sends only category=spot.
- Added exact decimal and metadata parsing, explicit status mapping and unknown-status logs, duplicate/cursor/progress checks, and complete-result publication. Missing optional limits remain null. Zero optional limits stay explicit: the selected LOT_SIZE/notional filters do not document zero as disabling a bound. Required steps still reject zero. See [Binance Spot filters](https://developers.binance.com/en/docs/products/spot/filters#lot_size).
- Added the application reader/refresher, one bounded scheduled worker per enabled pair, and executable wiring. All records receive one UTC UpdatedAt after successful normalization. Failed pages, metadata, normalization, or publication preserve prior data and timestamps.
- Added GET /api/v1/instruments with strict filters, scope readiness, stable errors, decimal strings, explicit nulls, integer funding seconds, UTC timestamps, snapshot caller limits, and deadlines. Reads only use the repository.
- Added in-memory publication success/error counters, last-success time, and snapshot size per exchange/market, plus structured refresh logs. Exporters and periodic operational statistics reporting remain phase 11 work.
- Local HTTP integration tests cover all four exchange/market pairs through SDK transport → provider → refresher → memory repository → HTTP handler, failed-refresh preservation, successful empty replacement, and no upstream calls from reads. Unit tests cover status rows, precision and limit selection, malformed values, funding/delivery semantics, filter validation, publication failures, scheduling, cancellation, and deadlines. The captured 782-row Binance funding response is replayed for explicit 1/4/8-hour intervals.

Provider separation: each exchange/market pair has its own implementation behind instrument.Provider. The contract exposes Scope() and GetInstruments(ctx), so callers cannot select a different market on the same provider. Binance SDK clients are also separate implementations; common parsing and admission stay shared. Bootstrap tests verify publication into the selected scope for all four implementations.

Review fixes: bounded source and expanded instrument decimal sizes before comparison/publication, and moved pure application filter validation before HTTP snapshot admission. Regression tests cover extreme exponents, exact size boundaries, old-snapshot preservation across all four exchange/market pairs, and filter errors while request slots are full.

Verification: `make check` passed on September 12, 2026: formatting, executable build, example configuration, golangci-lint (including govet), all tests, and the race detector. Documentation links and diff whitespace were checked. Tests use local fixtures without production exchange requests.

Production exchange access, deployment topology, and full observability remain later verification gates. Binance delisting_time stays null under the phase 01 decision.
