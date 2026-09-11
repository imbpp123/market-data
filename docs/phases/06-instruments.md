# 06 — Instruments end to end

Status: not started. Dependencies: [04](04-memory-repositories.md), [05](05-upstream-admission-and-retries.md), metadata/API decisions from [01](01-specification-decisions.md). Next: [07](07-tickers-and-market-statistics.md).

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
- Funding minutes/hours → domain duration → integer JSON seconds; perpetual versus expiry/spot, zero delivery time, and the approved Binance fallback cases.
- Failed pagination, required-source fetch, normalization, or repository write preserves the previous snapshot and UpdatedAt. Successful replacement removes absent instruments.
- Controlled-clock immediate refresh and configured wait; cancellation, retry backoff, and one exchange failing while the other publishes.
- httptest.Server → real adapter → repository → HTTP verifies string decimals, explicit null, UTC timestamps, filters, and zero upstream requests caused by reads.

## Exit criteria

Both adapters and the read API work with local fixtures. Instrument refresh is owned, bounded, observable, and independently cancelable. Production access is not required for the automated acceptance tests.
