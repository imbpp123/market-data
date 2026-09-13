# Phase 5. Add diagnostics and verify the complete change

Status: approved by the user. Implementation has not started. Depends on phase 4.

See the [phase list](README.md) and [main specification](../request-budget-rework-specification.md).

## What we will change

1. Extend existing diagnostics with the limit source and age, exchange limit, user cap, percentage, stop line, observed/local usage, reserved cost, remaining allowance, and uncertainty.
2. Show why a request is rejected and when work can next be tried. Keep budget shortage, operation-share shortage, exchange cooldown, refresh error, and body-size error as separate reasons.
3. Report meaningful state changes and refresh failures. Normal background deferral must not create a repeated warning stream or count as an HTTP attempt.
4. Run end-to-end local tests from a caller or worker through admission, transport, response processing, and recovery. Keep cache and snapshot behavior in these tests.
5. Update the configuration example, README, operating guide, implementation contract, and decision register. Replace only the requirements named in the main specification. Keep historical release evidence clearly dated.
6. Record the final checks and any limits of the evidence. Do not describe a local test as proof of all possible shared-IP behavior.

Use the existing logging and metrics components. Metric labels must have a small fixed set of values; do not use symbol names, error text, or timestamps as labels.

## Test cases

| Case | Expected result |
| --- | --- |
| Starting limit changes to a valid exchange limit | Diagnostics show the new source, amount, stop line, and update time. |
| Explicit user cap and custom percentage | Report both inputs and the correct derived line. |
| Current usage is above the stop line | Show the shortage clearly. Remaining allowance does not wrap to a large positive number. |
| Requests are still in flight | Reservations appear in the diagnostic view and agree with admission behavior. |
| Threshold, share, cooldown, catalog, and body-size failures | Each has a distinct reason. A catalog failure does not appear as budget exhaustion. |
| Repeated budget deferral | No warning flood, false HTTP attempts, or exchange-error counts appear. |
| Invalid catalog followed by a valid update | Stale-limit diagnostics clear after recovery. Usage does not reset. |
| Restart, then successful low-usage response | The service keeps working. Diagnostics state the missing-history limitation. |
| Parallel candle calls cross a threshold | One crossing is allowed; later work fails quickly. Valid cached data stays readable. |
| Worker waits through budget expiry and a longer 429/418 cooldown | It resumes only when every condition allows work. No special probe is sent. |
| Bybit regression flows | Existing requests, shares, rate signals, retries, and API results keep their behavior. |
| Configuration migration examples | New defaults and explicit legacy overrides load as documented. Invalid combinations fail clearly. |

## Final checks

- Format changed Go files and run `make check` and `make vet`.
- Run the relevant body-size and memory checks from phase 1 with the final code.
- Check local documentation links, examples, and the final diff for unrelated changes.
- Record any check that could not run and why. Do not mark the affected result as passed.

## Expected result

Each phase has a short report with changed files, test results, and remaining issues. The final report states the guarantee precisely: no new positive-cost request is allowed when its current accounted usage is already above a stop line. Crossing is allowed; unknown traffic and usage still limit what the service can guarantee.

This phase does not deploy the service. Release and rollback actions need their own instruction.
