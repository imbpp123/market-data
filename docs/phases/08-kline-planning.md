# 08 — Candle planning

Status: implemented. Dependencies: [03](03-domain-and-contracts.md), [04](04-memory-repositories.md), range/alignment decisions from [01](01-specification-decisions.md). Next: [09](09-kline-adapters.md).

## Outcome

A pure planner that covers missing and refresh-required slots with the fewest requests allowed by the configured per-request candle limit. Source: [specification](../technical-specification-v1.md), sections 8–9 and 25–30.

## Work

- Validate the canonical interval and selected market's support before cache lookup. Enforce the configured N-slot lookback from the latest close and the N-slot request bound before allocating all slots or building a plan. Calendar slots, not available row count, define depth.
- Detect missing slots, open candles needing an update, and cached intermediate candles requiring a post-close confirmation. Use internal request-start evidence, not FetchedAt alone.
- Implement the greedy range merge rule: start at the earliest required slot and cover as far right as allowed, including already cached slots when that reduces request count. Continue until all required slots are covered.
- Accept now, cached data/finalization metadata, and the final configured exchange/market limit as inputs. Keep I/O, retries, rate limiting, and configuration loading outside the planner.
- Use common Timeframe operations for slot counting and calendar months. Return half-open ranges that adapters can convert into upstream parameters.
- Bound computation and reject invalid boundaries, overflow, or lack of progress. Keep the specified objective of request count; do not replace it with minimum candles or minimum Binance request weight.

## Tests and checks

- Empty and complete cache; gaps at the beginning, middle, and end; several gaps merged and split; exact limit and one slot over the limit.
- Explicit expected plans for the specification example: missing 10:30, 10:35, 11:00, 11:05 at 5m fits [10:30, 11:10) with limit 8.
- Unordered input, duplicate handling under the storage contract, no required gaps omitted, and valid range size for every planned request.
- Open candles, already final candles, and a request started before close whose response arrives after close. Passing local time alone does not finalize a candle.
- Monthly ranges, leap years, confirmed weekly/multi-day alignment, empty/reversed/unaligned ranges according to phase 01, overflow, and oversized requests rejected before large allocation.
- Different configured limits change the plan; a warm final cache produces an empty plan.

## Exit criteria

Planner tests use explicit expected ranges and cover every required slot. The planner is independently testable with no repository, SDK, or network dependency.

## Implementation notes

- `internal/application/kline/fetch_planner.go` implements `Planner`. Construction takes one enabled scope, its provider-owned support list, the configured history size, and the final page limit. It copies the support list and contains no exchange support table or configuration loader.
- `Validate` is the pre-cache entry point. It checks scope/symbol syntax, canonical and supported interval, aligned nonnegative boundaries, future-slot rules, bounded slot count, and rolling history depth in contract order. Catalog readiness and symbol existence belong to the later application service.
- `Plan` repeats validation before inspecting cached data. Cached rows may be unordered, but duplicate keys, wrong series, rows outside the query, invalid boundaries, or invalid request metadata violate the repository contract and return `internal_error` without a partial plan.
- Missing slots, current open slots, and cached rows without a request started at or after close require fetching. Receipt time and elapsed local time cannot finalize intermediate data.
- Greedy pages begin at the earliest required slot and include every required slot that fits the configured limit, including cached slots between them. The page ends just after its last required slot; trailing final rows cannot reduce the request count. `Request.Limit` is the actual slot count, and all output boundaries are UTC and half-open.
- Calendar counting rejects oversized ranges before cache indexing or slot traversal. Planning uses at most one pass over the bounded slots, with memory proportional to cached rows and output pages. All calendar movement uses domain operations with boundary/progress checks.
- Unit tests cover the specification's explicit example, configured limit changes, cache ordering/duplicates, finalization, empty/open/future/expired ranges, overflow, monthly leap-year behavior, and the confirmed weekly/Binance 3d anchors. Exhaustive six-slot cases check coverage, page bounds, and request count against an independent search of all page placements.

The planner is independently usable and tested. Exchange adapters, no-progress handling after upstream fetches, concurrent fills, and HTTP integration remain phases 09–10.
