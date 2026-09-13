# Phase 4. Reject after crossing and resume by time

Status: approved by the user. Implementation has not started. Depends on phase 3.

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
