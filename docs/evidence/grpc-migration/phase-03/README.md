# Phase 3 evidence

Status: complete after independent review. No project commit, push, package publication, exchange call, or deployment was made. The production entry point now uses gRPC data and separate operational HTTP. Phase 4 owns full acceptance and workload measurements.

[checks.json](checks.json) records the base revision, final source hashes, commands, results and limitations. Required gates passed:

- [Focused tests](focused.log): config, operational router, CLI/healthcheck, bootstrap, gRPC, and archived HTTP fixture.
- [make check](check.log): formatting, service build, example config, lint, root unit tests and race tests.
- [make vet](vet.log).
- [make check-api](check-api.log): generation/drift, compatibility, nested Go build/vet/unit/race and isolated installed Python 3.13/3.14 wheel/Git clients.
- [make docker-build](docker-build.log): final local image with the nested Go module.
- [make docker-verify](docker-verify.log): internal network, all four gRPC methods return `UNAVAILABLE/data_not_ready`, old HTTP routes return 404, health/readiness return 200, existing hardening and 1 GB memory settings remain. SIGTERM completed in 0.140 seconds with exit code 0.

The [initial check](check-initial.log) failed on an unused obsolete HTTP test helper; it was removed. The [next check](check-budget-flake.log) hit the already reproduced phase 2 baseline budget-resume flake at `TestBackgroundBudgetDeferralIsQuietAndKeepsSnapshots/real_cooldown_outlasts_budget`. That test's logic and production admission behavior were not changed. The final full check passed after adding the archival byte-equivalence regression. These earlier failures remain visible; a later pass does not remove the known baseline timing risk.

After the successful full check, one unchanged historical startup warning was restored in the new entry point. Final focused tests and the final Docker build/probe include that line. No lifecycle/send algorithm changed after the full check. API check, vet and full check had the same functional implementation. Documentation and evidence metadata were finalized after the checks.

Local tests needed approved Go cache and loopback access; Docker needed local daemon access. No required gate was skipped. Existing CI already ran `make check-api` in both paths before publication; this phase verified that ordering and kept it. Root `go test ./...` alone is not treated as client verification.

## Coverage retained across HTTP removal

| Removed HTTP coverage | Current coverage |
| --- | --- |
| `instruments_integration_test.go`: SDK adapters, four scopes, exact values/presence, failed-refresh preservation, ready empty versus unready and cache-only reads | `internal/transport/grpc/instruments_integration_test.go` uses generated clients over local TCP, with the same exchange fixtures and failure cases. |
| `current_integration_test.go`: separate ticker/statistics publication, adapter attempt budgets, branch failure preservation and repeated cache-only reads | `internal/transport/grpc/current_integration_test.go` uses generated clients over local TCP. |
| `current_test.go`: a single funding clock, zero versus absent countdown, exact values, sorted rows, concurrent whole-snapshot reads | `internal/transport/grpc/snapshot_regression_test.go`; phase 2 application/conversion tests retain readiness/filter/precision cases. |
| HTTP kline DTOs, failure mapping, validation, deadlines | Phase 2 gRPC conversion/error/bounds/application tests; `internal/bootstrap/klines_test.go` ports four-scope cold/warm/partial real adapter reads, exact decimals/counts, and validation without upstream work. |
| Bootstrap kline retry/budget/crossing/cache invariants inside virtual time | The same bootstrap tests call the real service directly to preserve exact no-time-advance, attempt, cache and no-partial-success assertions. GRPC tests separately verify the wire error mapping and real adapter output. |
| HTTP write ownership and validation-before-capacity assertions | Phase 2 native HTTP/2 tests cover response/error sends, reset, cancellation cleanup, hard deadlines and release. The approved pre-decode admission order supersedes the old HTTP validation-before-capacity rule. |
| HTTP startup/worker/cleanup and fill-cancellation tests | Existing operational lifecycle tests now call dual composition. `cutover_test.go` proves all four unready RPCs independently of local readiness, removed routes, header bounds and cancellation of a real owned fake-upstream fill through the production composition. |
| Data-route HTTP Sentry error mapping | Phase 2 gRPC error mapping, panic/sanitization, observer and reporting tests. The operational unclassified HTTP error regression remains. |

Removed query-string/verb/RFC 3339 parser/JSON-shape tests describe a removed protocol. They are not business requirements. The active wire is generated Protobuf; operational GET/body/query/header bounds remain covered.

The five historical HTTP implementation files are frozen byte-for-byte from `201289fa7ca2e240cbbf8f6d0df2e4e1fb854482` under `scripts/api/httpfixture/legacyhttp`. Only the benchmark fixture imports them; production and Docker exclude them. `TestArchivedHTTPMatchesRecordedBaseline` checks all nine response bodies against the saved phase 1 `.gz` files. The benchmark manifest now includes its actual archived sources. No phase 1/2 evidence was changed.

The opt-in release-load profile now uses generated RPCs on a controlled in-memory connection and still checks 600 series / 600,000 candles and full 20,000-row snapshots. It was not run in phase 3. Its timings are not TCP measurements or migration capacity acceptance; phase 4 must run and report the required workload and real-network comparison.

## Independent review

The separate `phase3_review` agent completed an xhigh review with no actionable findings. It found no lost business coverage. No production or test correction was required.

The reviewer inspected configuration validation, production dual-listener wiring, operational routes, healthcheck/container migration, CI ordering, removed HTTP coverage, archived fixture isolation and the recorded required-check evidence. Independent tests passed for config and operational HTTP, the complete CLI, gRPC and historical HTTP fixture suites, and selected bootstrap cutover, lifecycle, candle and budget cases. An initial sandbox run could not bind loopback sockets; the same affected tests passed with local access. `git diff --check` was clean.

The reviewer did not edit files or repeat all broad Makefile gates. Their passing results remain the implementation evidence above. This review is not phase 4 performance/capacity acceptance and does not authorize publication or deployment. The earlier failed checks and known baseline flake remain recorded.

After review, only status documentation and evidence metadata changed. Local Markdown links, whitespace and source-hash agreement were checked; Go tests were not repeated for these documentation-only updates.
