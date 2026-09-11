# 01 — Close specification decisions

Status: not started. Dependencies: none. Next: [02](02-bootstrap-and-configuration.md).

## Outcome

Make the draft implementable without treating examples and open questions as approved requirements. Source: [specification](../technical-specification-v1.md), sections 5, 9, 14, 26, 32–44, and 54.

## Work

1. Record each decision, its evidence, status, and dependent phase in the specification or a linked decision document. Separate confirmed requirements from proposed defaults.
2. Confirm the deployment instance count, outgoing IP ownership, enabled markets, and expected request workload. The plan covers the documented spot/linear mappings for both exchanges; this does not enable every market by default.
3. Define the complete limit/config schema: scopes, units, sliding windows, common limits and margins, operation shares without borrowing if confirmed, burst smoothing, HTTP concurrency, waiting callers, active fills, deadlines, candle count, and total upstream attempts. Include cooldown fallbacks, Binance bootstrap limits, later limit updates, and restart safety.
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

## Exit criteria

The decisions needed by the next implementation phase are recorded. Any unresolved item has a specific blocked feature and required evidence, rather than a generic “check SDK” task. Product decisions requiring user input are requested only when their dependent work cannot proceed. This planning document does not approve numeric budget defaults or deployment changes.
