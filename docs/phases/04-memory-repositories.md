# 04 — In-memory repositories

Status: complete, September 12, 2026. Dependencies: [03](03-domain-and-contracts.md), retention boundary from [01](01-specification-decisions.md). Next: [05](05-upstream-admission-and-retries.md).

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

## Implementation

- `internal/infrastructure/storage/memory` implements the four application repository contracts. Each repository owns its maps and RWMutex; no shared ticker/statistics transaction exists. Complete input validation and copying precede publication. Reads return owned slices and optional values, preserve timestamps, and sort deterministically.
- Snapshot scopes track readiness by map presence, including successfully empty maps. Lists check all selected scopes before symbol/status filtering. Repeated selected scopes do not duplicate rows. Statistics storage accepts positive window durations independently; this does not expand the v1 API window allowlist.
- Candle batches validate series keys, calendar boundaries, duplicate time instants, and attempt/receipt metadata before an atomic merge. Storage rejects pre-epoch or future slots, missing attempt/receipt times, and attempts after receipt. An attempt may start before a returned slot opens if the response crosses that boundary; this is still intermediate data. Numeric field normalization remains an adapter responsibility. Range reads use inclusive start and exclusive end; an equal-boundary range is empty, and gaps stay absent.
- Intermediate candles order by successful attempt start and then receipt time. Exact ties preserve existing values. An attempt started at or after close confirms the row; confirmed rows are immutable. Clock passage or a late receipt alone never confirms data.
- `NewKlineRepository` takes the shared history count and a concurrency-safe clock. Each nonempty merge captures one clock value and uses `Calendar.HistoryCutoff`. It prunes touched series against the later of the current and applied cutoffs, skips expired incoming rows, and preserves the cutoff at exchange/market/interval scope. `DeleteBefore` applies to every symbol in that scope and preserves equality. Empty series maps are released; only the finite calendar-scope cutoffs remain. Calendar overflow fails before changing rows or cutoffs.
- `internal/bootstrap/state.go` creates and owns storage before HTTP bind. It passes the configured history size and real clock; tests use fixed clocks. Application/provider interfaces and the public candle schema are unchanged. Periodic cleanup workers, API retention validation, and fill coordination remain assigned to phases 11, 10, and 10 respectively.

Behavior tests cover all three snapshot contracts, independent scopes/windows and repositories, failed initial loads, successful empty loads, failed refresh preservation, every optional pointer, nil values, cancellation/deadlines, and 50 concurrent readers during publication. Candle tests cover ordered half-open ranges, gaps, time-zone-equivalent keys, stale writes, post-close confirmation, atomic invalid batches, calendar cutoffs, monotonic cleanup, pending fills during cleanup, concurrent reads/writes/cleanup, and release of empty series metadata.

Executed checks: `make check` passed on Go 1.27.1, including formatting, build, example configuration validation, standard lint with govet (zero issues), unit tests, and race tests. Checks used writable temporary Go/linter caches because the sandbox blocked the default Go cache. Focused coverage was 95.2% for memory storage. Additional tests verify an HTTP response crossing a slot opening, the 1,000-closed-slot plus current-slot capacity, and overlapping writes preserving neighbors. Local documentation links, whitespace in all changed/new files, and `git diff --check` passed. No upstream access or credentials were used.
