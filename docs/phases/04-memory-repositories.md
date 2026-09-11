# 04 — In-memory repositories

Status: not started. Dependencies: [03](03-domain-and-contracts.md), retention boundary from [01](01-specification-decisions.md). Next: [05](05-upstream-admission-and-retries.md).

## Outcome

Thread-safe current snapshots and candle storage behind application contracts. Source: [specification](../technical-specification-v1.md), sections 8, 17–20, 23–24, 31, 41, and 58.

## Work

- Implement independent Instrument, Ticker, and MarketStats snapshot repositories. Validate a replacement before atomically publishing it; remove absent symbols only within the replaced scope.
- Key snapshots by exchange/market and statistics additionally by window. Distinguish a never-loaded instrument, ticker, or MarketStats scope from a successfully published empty snapshot.
- Reject duplicate keys and mismatched records as required by each contract. Return safe copies, including optional pointer values and collection contents.
- Implement candle GetRange, UpsertMany, and exchange/market/interval-scoped DeleteBefore with keys including exchange/market/symbol/timeframe/OpenTime. Return ordered ranges using the agreed boundaries. Use the shared N-slot history cutoff. Delete OpenTime strictly before it, prune expired rows during fill merges, and prevent late writes from reinserting rows below an applied cutoff.
- Preserve request-start/finalization metadata internally. Prevent older responses from replacing newer open values; define ordering consistently with the agreed finalization rules.
- Make validation/write errors preserve the previous relevant state. Different repositories remain independent; do not introduce a transaction spanning ticker and statistics.

## Tests and checks

- Replacement removes absent symbols and preserves other exchange/market/window scopes.
- Uninitialized versus successful empty snapshots in all three repositories; duplicate and wrong-scope rejection; failed replacement leaves values and timestamps intact.
- Mutating inputs or returned slices/pointers does not change repository state.
- Ordered ranges, empty results, exact boundaries, overlapping upserts, stale updates, and independent candle series.
- Retention cutoff behavior from phase 01, including retained neighbors and concurrent reads/writes.
- At least 50 concurrent ticker/statistics readers during replacement see complete snapshots. Run race tests across all repositories. Additional window scopes in storage tests do not enable them in the v1 API.

## Exit criteria

Repositories pass behavior and race tests. They expose no internal mutable state, preserve failed-refresh data, and support post-close confirmation without a public schema change.
