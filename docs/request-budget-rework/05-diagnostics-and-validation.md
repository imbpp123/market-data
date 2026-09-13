# Phase 5. Add diagnostics and verify the complete change

Status: complete after local validation and independent review on September 13, 2026. No deployment was performed.

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

## Implementation report

The existing controller now returns one read-only snapshot with window limits, sources, age, usage, reservations, headroom, uncertainty, and per-operation reasons/times. `/debug/stats?view=admission` exposes details without changing the default sample-array response. Prometheus uses fixed labels; additional windows are counted, not assigned arbitrary labels or summed across units/windows. See the [operating guide](../development.md#binance-admission-diagnostics) for exact fields and limits.

Actual catalog failures keep separate error metadata. A valid recovery clears it without resetting usage; out-of-order success/failure cannot hide a newer failure or undo newer accepted recovery. Oversized bodies keep `invalid_upstream_data` and a separate diagnostic reason/counter. Real cooldowns survive body failures. Logs report changed limits, recovery, failed refreshes, and admission transitions with request cost context. Repeated deferral creates no false HTTP attempts, exchange errors, or warning stream.

Changed files:

- `internal/infrastructure/exchange/upstream/diagnostics.go`, `diagnostics_test.go`, `controller.go`, `catalog.go`, and `transport.go`: coherent snapshots, current constraints, transition events, ordered catalog errors, and body-size classification.
- `internal/infrastructure/observability/admission.go`, `admission_test.go`, `exchange.go`, and `statistics.go`: bounded metrics, detailed JSON, and dispatched oversized-body counts.
- `internal/bootstrap/operations.go`, `server.go`, `instruments.go`, and `budget_rejection_test.go`: wire the existing exporters/logs, remove repeated catalog dumps, and test parallel candle/cache behavior plus quiet worker recovery after a real longer cooldown.
- Root `README.md`, `docs/development.md`, `docs/examples/config-v1.yaml`, `docs/implementation-contract-v1.md`, `docs/technical-specification-v1.md`, `docs/specification-decisions-v1.md`, the main rework specification, phase index, and this report: current policy, configuration migration, diagnostic semantics, and evidence. Historical release evidence and the pending gRPC migration scope remain separate.

Validation on September 13, 2026:

- Focused upstream, observability, and bootstrap tests passed. New cases cover custom percentage/cap/source/age, negative headroom clamping, in-flight crossing, strict shares/impossible costs, distinct catalog/body/cooldown states, both response orders, low-history startup, time-only recovery, fixed metric labels with multiple discovered windows, and coherent debug/metrics reads.
- Caller → fill → admission → transport → cache tests allow one parallel crossing, reject later work, and preserve complete cached results. Worker → transport tests keep previous snapshots and counters stable through budget deferral and a real `429` with a 120-second cooldown, then resume ordinary work. Existing phase 4 tests still cover raw/funding windows, equality, strict shares, longer cooldowns, cancellation, retries, and no partial success.
- `make check` passed formatting, build, example validation, lint (0 issues), all tests, and all race tests. `make vet` passed. Existing Bybit, configuration precedence/migration, and HTTP regression suites passed.
- Final-code phase 1 checks passed with `go test ./internal/infrastructure/exchange/binance ./internal/infrastructure/exchange/upstream -run 'Test(SpotExchangeInfo|BodyLimit|ExactBodyLimit)' -count=1 -v`. These cover complete catalogs, snapshot preservation, plain/gzip/deflate/Brotli exact bounds, decoded overflow, broken compression, and truncated data.
- Local Markdown links and whitespace were checked. No commits, public exchange calls, publishing, or deployment were performed. Go tests needed execution outside the default sandbox for local HTTP listeners and build-cache access.

### Final-code catalog replay

The saved phase 1 reduced response remains 6,703,095 decoded bytes with 3,698 symbols. SHA-256 is `3a3e8a5c5598b5f0ebf42b7781239f87bfed4926e7397dca7f3368ba2801b7f5`, matching the original report. Replayed on macOS arm64, Apple M1 Pro, Go 1.27.1, default 16 MiB decoded-body bound:

```sh
go test -c -o /tmp/market-data-binance-phase5.test ./internal/infrastructure/exchange/binance
MDS_SPOT_EXCHANGE_INFO_FILE=/tmp/market-data-spot-reduced.json \
  /usr/bin/time -l /tmp/market-data-binance-phase5.test \
  -test.run '^$' -test.bench '^BenchmarkSpotExchangeInfo$' \
  -test.benchtime 5x -test.benchmem
```

| Measurement | Result |
| --- | --- |
| Complete load and publication | 82,161,525 ns/op |
| Allocated bytes per load | 62,705,982 |
| Allocations per load | 213,991 |
| Maximum process RSS | 72,761,344 bytes |
| Peak memory footprint | 60,801,672 bytes |

All five timed loads passed. The benchmark includes real admitted transport, SDK parsing, normalization, and snapshot publication. Fixture loading/server setup are outside allocation timing but remain in RSS. Allocated bytes are not retained heap or peak RSS. This is a new final-code replay of the earlier capture, not a fresh live-size measurement or a whole-service memory test. Catalog growth and broad workload memory remain subject to the documented limits and historical release audit.

### Independent review

A separate XHIGH reviewer found no confirmed defects. Review covered snapshot calculations and isolation, fixed metric labels and types, request-cost assumptions, catalog response ordering, oversized-body errors, cooldowns, observer locking, quiet transitions, exporter settings, and unchanged Bybit behavior. No production fixes or additional regression tests were needed.

Fresh checks passed:

- `go test -race -count=1 ./internal/infrastructure/exchange/... ./internal/infrastructure/observability ./internal/application/... ./internal/bootstrap`.
- Focused body, catalog, and diagnostic tests with `-count=1`, including the phase 1 exact-size and compressed-body cases.
- Local documentation links and `git diff --check`.

The first race run could not bind local HTTP ports inside the sandbox. The repeated run passed with local-listener access. No production changes followed the recorded memory replay, so it remains the final-code measurement. The reviewer did not repeat the replay or make public exchange requests. Review changed only completion reports in this file, the phase index, the main specification, and the root README.

The invariant is unchanged: **no positive-cost request is admitted when CURRENT accounted usage is already above a stop line**. Crossing is allowed. This is not a hard 90% actual-IP guarantee; external traffic, restarts, delayed counters, unseen limits, and conservative overlap (which may approach twice actual usage) remain limits of the evidence. Phase 5 is complete; deployment remains outside this work.
