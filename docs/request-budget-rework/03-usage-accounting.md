# Phase 3. Count local requests and exchange observations

Status: approved by the user. Implementation has not started. Depends on phase 2.

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

## Proposed detail for review

A response does not identify every parallel request included in its counter. During implementation, define which request timing is enough to prove inclusion and test that rule. Where inclusion cannot be proved, use a conservative estimate. Do not present that estimate as exact exchange usage. The 108 example applies only when the stated inclusion facts are known.
