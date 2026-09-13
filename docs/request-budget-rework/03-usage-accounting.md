# Phase 3. Count local requests and exchange observations

Status: implemented on September 13, 2026 after phase 2. Independent review is complete; no confirmed defects were found. Phases 4 and 5 are not implemented.

See the [phase list](README.md) and [main specification](../request-budget-rework-specification.md). This phase prepares the usage state used by admission in phase 4.

## What we will change

Use valid Binance headers for reported usage. Add temporary local accounting because a response may not have arrived yet, may be lost, or may have no useful counter. We also need local totals for the agreed 60/30/5/5 operation shares; Binance does not report these shares for us.

1. Keep small usage entries in memory: request cost, operation, timing, and whether the request was sent or is still waiting for a response. Do not store request URLs, bodies, or response content in these entries. Remove an entry when no active limit window or in-flight request needs it. Each page and retry has its own cost.
2. Connect a response to its own request. Keep requests still in flight visible to parallel calls.
3. Combine valid exchange observations with local requests not known to be included. Keep local sliding history; do not add the full header value to the full local total.
4. Keep separate records for each scope, unit, and window. A weight header cannot report the request-count or funding-family usage.
5. Release a reservation only when the request did not leave the service. Keep its cost after dispatch even when the response is lost or the caller cancels.
6. Expire only records whose time has ended. A smaller or late counter must not delete live local usage. Use the full-window-after-receipt fallback when the exchange window cannot be placed reliably.
7. Record uncertainty where the exchange does not give enough evidence. A restart or an unknown window starts from the available information, without an automatic pause.

The main areas are the controller's usage state and transport response handling. Keep calculations separate from HTTP work and use the existing controlled-clock boundary in tests.

## Test cases

| Case | Expected result |
| --- | --- |
| Header reports 100 including our cost-20 request; two cost-4 requests are known to be outside it | Total is 108. The included cost-20 request is not counted again. |
| No headers on successful responses | Local request costs remain available for every applicable window. |
| Two requests are in flight together | Both reservations are visible before either response arrives. |
| Responses arrive in reverse order | No local charge is lost or counted twice. An older observation cannot erase newer known usage. |
| A lower counter arrives during an active window | It does not by itself prove a reset or remove live usage. |
| Missing, negative, malformed, overflowing, or conflicting counter data | Do not trust the invalid observation. Keep local accounting and report uncertainty where useful. |
| USDⓈ-M price-v2 or bookTicker weight header | Ignore the known inaccurate counter. Keep the request's local cost. |
| Several pages and one retry | Every actual attempt is charged once at its own cost. |
| Cancellation before dispatch | No spent cost or used attempt remains from that reservation. |
| Cancellation, timeout, or connection failure after dispatch | Keep the cost because Binance may have received the request. |
| Response arrives near a window boundary | Do not move or drop live request cost only because the counter is smaller. Use a safe expiry when timing is unclear. |
| Minute observation received at 12:34:20 with no reliable boundary | It remains active before 12:35:20 and expires at that time. Other live records remain. |
| Wall clock moves forward or backward | Elapsed-time expiry still works; a clock jump alone does not clear usage. |
| Restart with no local history; header reports 100 | Record the known usage and the missing-history limit. Do not create a pause merely because the local total was zero. |
| New five-minute window with only one minute of history | Preserve available charges and mark the missing history. Do not pretend that earlier IP usage is known. |
| Weight, raw requests, and funding-family records | Each uses its own unit and scope. Spot usage does not enter USDⓈ-M state. |
| Expired history is removed | Live records remain correct and old records do not grow without a bound. |

## Expected result

The controller has a tested current-usage view, including in-flight requests and uncertainty. The discrepancy and unknown-history rules no longer create a cooldown by themselves. Real exchange cooldowns remain separate.

## Implementation report

### Accounting rule

For each scope and window, `L` is the sum of live local costs. It includes reservations and Binance requests still in flight. Completed requests keep their sliding charge from admission time. Each valid response counter `H` belongs to its own attempt. `C` is that attempt's cost if it is still in `L`, or zero after its local charge expires.

The common estimate is `max(L, max(H + L - C))` over active observations. Integer sums saturate at the largest supported integer. An already saturated local total is not reduced. Operation shares use local costs only. A weight observation never replaces raw-request or funding-family counts.

Only the response's own dispatched attempt is proven included. A counter below that attempt's cost is inconsistent and is ignored. Earlier and overlapping requests are not proven included: these responses do not identify an exchange window or all requests covered by a counter. Observations are compared by maximum, not added together. A lower or older observation cannot erase an active higher estimate or local charge.

This is a conservative estimate, not exact IP usage. For the agreed example, `H=100`, own cost `20`, and two unmatched costs `4+4` give `108`. For three serial cost-4 requests with headers `4`, `8`, and `12`, local usage is `12` and the estimate is `20`. Some earlier costs may already be included in the last header. With many serial requests, this fallback can approach twice the local total. It can delay work longer than needed, but it does not create a discrepancy-only cooldown.

### Timing and lifetime

There is no verified exchange-window identity in these responses. Every accepted observation therefore expires one full window after header receipt. HTTP `Date` does not set that expiry. Reprocessing exchangeInfo headers after installing a new window keeps the original receipt time. Body processing does not extend it.

The controller reserves each page and retry atomically. It uses the attempt entry itself to connect dispatch, response, completion, and rollback; equal timestamps cannot select the wrong request. The final context check is immediately before the dispatch boundary, the attempt callback, and the underlying transport call. Cancellation before that boundary releases the reservation, HTTP slot, and attempt count. Cancellation after it keeps the charge because delivery is uncertain.

A Binance request still in flight remains in each applicable window, even after its normal sliding expiry. Completion ends this reservation; a completed charge expires at admission plus the window duration. If that time has already passed, the local charge can expire at completion. Any valid observation still has its own receipt-based expiry. Bybit keeps its previous sliding behavior, including requests in flight for longer than its window.

All times keep Go's monotonic clock component; elapsed-time calculations do not convert them to Unix timestamps. A new process or longer window records incomplete history and continues with available usage. It does not create a pause merely because history is missing.

Entries contain only cost, operation, state, local times, and numeric observations by window. They contain no URL, request body, or response body. Pruning removes an entry once no active local window, observation, or in-flight request needs it. For fixed installed windows, memory is bounded by their durations, request spacing, HTTP timeout, and in-flight slots. Each attempt has at most one observation per installed weight window. There is no persistent ledger or new global memory cap; unusually long catalog windows retain more history.

### Validation

Passed on September 13, 2026:

- `go test ./internal/infrastructure/exchange/upstream`: accounting, ordered transport, catalog, cancellation, retry, cooldown, and Bybit tests.
- `make check`: formatting, build, example configuration, configured linter (zero issues), all tests, and race tests.
- `make vet`: all packages.
- `git diff --check`: no whitespace errors.

New deterministic tests cover the 108 example, serial conservative estimates, reversed responses, lower counters, missing and invalid counters, repeated/conflicting header values, integer overflow, receipt expiry, new windows in the same catalog response, separate units/scopes, pages and retries, post-dispatch errors, strict shares, long in-flight requests, exact reservation rollback, and pruning. Existing tests cover the documented inaccurate USD-M endpoints, HTTP 429/418, body failures, and Bybit behavior. Old discrepancy-cooldown expectations now check retained usage and continued admission at low usage.

Tests use controlled elapsed time and local fake transports. No public exchange calls were needed. The expiry test ignores an unrelated HTTP `Date`; the host wall clock was not changed. Resistance to actual operating-system clock jumps relies on Go's monotonic clock contract. This phase does not claim a full production memory or load measurement.

### Independent review

A separate reviewer found no confirmed defects. A fresh `go test -race -count=1` run passed for all exchange packages. The parent added a transport test with two cases: waiting ends at observation expiry without another response, and a longer exchange cooldown remains active. Both cases passed with and without the race detector. No production fixes were needed after review.

### Changed files and remaining work

- `internal/infrastructure/exchange/upstream/usage.go`: calculation, strict header parsing, uncertainty, and pruning.
- `controller.go`, `transport.go`, and `response.go` in the same directory: attempt identity, dispatch/completion, and use of the current usage view.
- `usage_test.go`, `usage_wait_test.go`, `response_test.go`, `capture_test.go`, and `catalog_test.go` in the same directory: new accounting cases and existing regressions.
- This report, the phase index, main specification, and root README: current status and accounting limits.

Phase 3 keeps admission waiting and the existing strict fit check. Immediate threshold rejection, permitted crossing, and worker recovery belong to phase 4. Exposing the internal usage view in diagnostics and full-flow validation belong to phase 5. Independent review found no confirmed defects.
