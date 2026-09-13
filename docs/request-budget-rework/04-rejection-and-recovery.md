# Phase 4. Reject after crossing and resume by time

Status: complete on September 13, 2026 after independent review. No confirmed defects were found. Phase 5 is not implemented.

See the [phase list](README.md) and [main specification](../request-budget-rework-specification.md).

## What we will change

1. Check current accounted usage before each request. If it is above any applicable common stop line, return an immediate budget error. Equality still allows one crossing request.
2. Check the operation share against the new request cost. Keep strict 60/30/5/5 shares and no borrowing. Both the common check and the share check must pass.
3. Perform checks and reservation together under the controller's concurrency protection. A second caller must see the first caller's reservation.
4. Return `service_overloaded` to candle callers when needed exchange work fails the threshold or share check. Keep the existing response format and complete-result rule.
5. Give background workers the reason and next eligible time. Preserve their previous snapshots and use a timer to defer work. Normal budget shortage must not enter the retry or warning loop.
6. At expiry, recheck all windows, shares, in-flight costs, and exchange cooldowns. A new ordinary caller can run this same local check. Do not require a probe or response to remove a local budget block.
7. Keep pacing, queue limits, HTTP slots, deadlines, and existing 429/418 handling. Reject budget shortages without waiting for those resources to become free.

The main areas are upstream admission, worker scheduling, and the existing candle error path. No new public endpoint is needed.

## Test cases

Use a limit of 6,000 and a 90% setting in the first cases. Give the operation enough remaining share so that the common threshold is the condition under test.

| Case | Expected result |
| --- | --- |
| Current usage 5,398; cost 4 | Send and reserve 4. Total becomes 5,402; the next positive-cost request is rejected. |
| Current usage 5,400; cost 4 | Equality allows the request. Total becomes 5,404. |
| Current usage 5,401; cost 4 | Return immediately without HTTP, a used slot, or a spent retry attempt. |
| Two callers start at usage 5,400 | Only one can reserve the crossing request. The other sees the new usage and fails. |
| Operation cap 270; own usage 250; cost 20 | The request fits exactly and is allowed if other checks pass. |
| Operation cap 270; own usage 251; cost 20 | Reject immediately even if the common window has room. Do not borrow another operation's share. |
| One applicable window has room and another is above its line | Reject. Sending requires every applicable check to pass. |
| Valid lower limit puts current usage above the new line | The next request fails immediately; existing cost is preserved. |
| Request can never fit its operation allowance | Return a clear limit/configuration failure, not an endless budget wait. |
| Rejected candle fill already fetched some valid pages | Keep those pages in cache but return an error, not partial success. |
| Complete cache range or snapshot read | It remains available under normal local API limits. No exchange budget is spent. |
| Worker reaches the threshold | It keeps the old snapshot and waits for the eligible time without repeated attempts or warnings. |
| Blocking minute observation expires at 12:35:20 | No new response is required. At expiry, a caller or worker can pass if all other checks allow it. |
| Repeated callers fail before expiry | The recorded expiry stays unchanged. Failed calls do not extend the block. |
| Another window, share, or in-flight request still blocks at expiry | Continue to reject or defer until all applicable checks pass. |
| HTTP 429 or 418 with Retry-After seconds or date | Preserve the indicated cooldown. Test missing, invalid, and past values with the existing fallbacks. |
| Budget expires before a real ban | The ban stays active. A late response cannot shorten it. Body decode failure does not remove it. |
| Budget is available but spacing or a slot causes a wait | Existing admission and caller deadlines still apply. Cancellation leaves no reservation behind. |
| Worker shutdown during budget deferral | The timer stops and shutdown waits for the worker to exit. |

## Expected result

The API fails quickly on Binance budget shortage. Background work resumes through normal scheduling. Controlled-time tests prove both threshold crossing and recovery without polling. Bybit admission and cooldown behavior remain unchanged.

## Implementation report

### Admission and recovery

Binance checks each positive-cost constraint before joining an admission queue, then checks again under the same lock as reservation. The common check uses current accounted usage, including reservations. Current usage at or below the stop line allows a crossing request. Current usage above the line rejects it. Operation shares still require the new cost to fit and do not borrow unused shares. Dedicated funding limits remain separate constraints.

A shortage returns an error that wraps `service_overloaded`. It does not reserve cost, occupy an HTTP slot, dispatch HTTP, spend an attempt, or wait for retry backoff. An impossible request cost still returns a clear `upstream_unavailable` limit error. Valid catalog reductions remain installed. Pacing, FIFO lanes, slots, deadlines, and the Bybit fit-and-wait rule keep their existing behavior when budget permits admission.

The error implements the application-owned `RefreshDeferral` contract. It gives the reason (`common_threshold` or `operation_share`), a next eligible time, and a cancelable wait. The time is a prediction from current state, not reserved permission. The controller searches known local and observation expiry times and checks all applicable constraints. It uses sorting and binary search, with O(n log n) work per blocking window. Rejected calls never change usage timestamps. If in-flight costs prevent a known recovery time, the time is zero and the wait uses completion or state-change notifications. It does not poll.

At a timer or state change, the controller rechecks current costs, all windows, strict shares, pacing, and cooldown. An ordinary caller performs the same local admission check. Recovery needs no probe or new response. Actual dispatch still checks queue and HTTP capacity. The phase 3 conservative usage formula and receipt-based observation lifetime are unchanged; budget expiry cannot clear a real ban.

### Background work and API behavior

Cycle gates retain a deferral across worker runs. Waiting happens before starting the next bounded operation, so a long budget wait does not spend the next cycle's deadline. Deferral does not increment failure backoff or report a failed snapshot refresh. Existing snapshots and their timestamps remain available. If limits become impossible while waiting, the next refresh reports the normal limit error and uses failure backoff.

The single instrument worker compares both instrument and catalog schedules while waiting. A funding-family block can defer instruments while a due exchangeInfo request still runs. Catalog-only shortage follows the same quiet deferral path. Cancellation stops timers or notification waits and lets the owner join the worker.

A candle fill returns HTTP 503 `service_overloaded` if a later page is rejected. Valid pages already written remain in cache. The response contains no partial success. Complete final cache reads and snapshot reads continue without upstream budget usage.

### Validation

Passed on September 13, 2026:

- Focused upstream, exchange, application, and bootstrap tests.
- `make check`: formatting, build, example configuration, configured linter, all tests, and race tests.
- `make vet`: all packages.
- `git diff --check`: no whitespace errors.

New controlled-time tests cover 5,398/5,400/5,401 with cost 4, one concurrent crossing, 250/251 with cost 20 and share 270, rejection before unavailable queues/slots/cooldown, retry rejection without extra attempts, unchanged expiry, several active windows, recovery without responses, unknown in-flight expiry, shutdown, fresh cycle deadlines, and later impossible catalog limits. A scheduler test proves that a due catalog refresh continues during a funding-family block.

Bootstrap integration tests exercise real adapters, workers, repositories, and HTTP handlers. They check quiet instrument/ticker/statistics and catalog deferral, unchanged snapshots and metrics, recovery by time, bounded shutdown, and a rejected multi-page candle fill followed by a successful cache read. Existing tests still cover HTTP 429/418, Retry-After and fallbacks, body failures, late responses, canceled admission, valid catalog reductions, and Bybit behavior. Earlier Binance wait tests now assert fast rejection while preserving their accounting checks.

One first race run found an unsynchronized counter in a new test. The test now uses an atomic counter and a locked controller-state read. The final full check passed after this test fix. Tests use virtual time and local transports; no public exchange traffic was needed.

### Independent review

A separate XHIGH reviewer found no confirmed defects. The reviewer ran a fresh `go test -race -count=1 ./internal/infrastructure/exchange/... ./internal/application/... ./internal/bootstrap` and `git diff --check`; both passed. No production fixes or additional regression tests were needed after review.

### Changed files and remaining work

- `internal/application/refresh.go` and the instrument, ticker, and marketstats services: deferral contract and quiet refresh observation.
- `internal/infrastructure/exchange/upstream/controller.go`, `deferral.go`, `retry.go`, and `catalog_cycle.go`: immediate admission rejection, recovery, and worker scheduling.
- `internal/bootstrap/instruments.go`: quiet catalog deferral.
- Upstream `rejection_test.go`, `deferral_test.go`, and updated admission/catalog/usage tests: deterministic boundary and recovery checks.
- `internal/bootstrap/budget_rejection_test.go` and exchange `klines_accounting_test.go`: worker and candle integration evidence.
- This report, the phase index, main specification, and root README: implementation status.

Phase 4 is complete after independent review. Phase 5 diagnostics, final full-flow validation, and operating-document reconciliation remain pending. The next eligible time can change when new usage, limits, completion, or cooldown arrives. Conservative overlap can defer work longer than actual exchange usage requires. Deployment and live load measurements are outside this phase.
