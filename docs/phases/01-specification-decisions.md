# 01 — Close specification decisions

Status: complete, September 11, 2026. Next: [02](02-bootstrap-and-configuration.md).

Decisions and future checks: [decision register](../specification-decisions-v1.md), [implementation contract](../implementation-contract-v1.md), [configuration defaults](../examples/config-v1.yaml), and [evidence inventory](../evidence/phase-01/README.md). User requirements are recorded, including memory-only limiter state and loss of local counters/cooldowns on restart. Numeric defaults and API details are adopted engineering choices. Twelve live calendar captures, metadata evidence, and synthetic HTTP examples make the decisions reviewable. Application tests, pinned-toolchain execution, and memory measurements belong to later phases.

## Outcome

Make the draft implementable without treating examples and open questions as approved requirements. Source: [specification](../technical-specification-v1.md), sections 5, 9, 14, 26, 32–44, and 54.

## Work

1. Record each decision, its evidence, status, and dependent phase in the specification or a linked decision document. Separate confirmed requirements from proposed defaults.
2. Confirm the deployment instance count, outgoing IP ownership, enabled markets, and expected request workload. The plan covers the documented spot/linear mappings for both exchanges; the user confirmed both markets enabled by default.
3. Define the complete limit/config schema: scopes, units, sliding windows, common limits and margins, operation shares without borrowing as confirmed, burst smoothing, HTTP concurrency, waiting callers, active fills, deadlines, candle count, and total upstream attempts. Include cooldown fallbacks, Binance bootstrap limits, later limit updates, and memory-only restart behavior.
4. Resolve Binance funding interval fallback and document the delisting source outcome. An unresolved source retains the specified null behavior; failed requests cannot activate defaults.
5. Obtain evidence for weekly/multi-day candle anchors and Binance Spot FULL 24hr bulk access or bounded complete batches. Recover earlier SDK checks if available; otherwise recreate the relevant contracts during adapter work. Store fixtures with source, capture context, and expected normalized values. Live discovery, if needed, stays separate from automated tests.
6. Complete HTTP contracts for instruments, tickers, and klines: response envelopes/order, required and repeated parameters, unknown/disabled filters, missing symbols, and initial data behavior. Define kline from/to format and half-open semantics, unaligned/empty/reversed/future ranges, pre-listing or missing slots, request limits, and stable error/status mappings for deadlines, upstream failures, and incomplete data.
7. Define retention comparison semantics, reads before retention, and initial instrument-load behavior. Decide how memory suitability will be measured for the intended workload without adding unrequested storage or eviction.
8. Reconcile the draft status of budgets, the short closed-candle description versus post-close confirmation, and decimal JSON wording. Record the pinned toolchain verification requirement.

## Verification

- Every open item from section 14 and section 32 has an explicit disposition and dependent implementation gate.
- Every allowed operation can fit at least its most expensive permitted request within all applicable budgets. Pages and retries fit within bounded processing or fail with a defined error.
- API examples include successful, empty/unready where applicable, invalid, overloaded, and incomplete-data outcomes.
- Calendar fixtures include UTC boundaries for weekly and multi-day series; funding evidence distinguishes missing data from request failure.
- Review links and wording. No application tests are claimed in this phase.

## Recorded verification

- Complete YAML example parsed successfully; checked reserved-share sums, five effective common allowances, permitted request costs, and global lane capacities.
- All 17 evidence JSON files and 20 synthetic HTTP examples parsed. The 12 calendar captures match expected UTC anchors, consecutive boundaries, decimal strings, trade counts, and exclusive close times.
- Local Markdown links and referenced heading anchors resolve; git diff --check passes.
- No Go build, unit, race, or lint checks ran: the repository has no Go module or service implementation. Pinned-toolchain execution, adapter replay, and memory/load measurements remain future gates.

## Exit criteria

The decisions needed by the next implementation phase are recorded. Any unresolved item has a specific blocked feature and required evidence, rather than a generic “check SDK” task. Product decisions requiring user input are requested only when their dependent work cannot proceed. Numeric defaults are recorded in the implementation contract. No deployment changes were made.
