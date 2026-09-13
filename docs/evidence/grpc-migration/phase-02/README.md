# Phase 2 evidence

Status: complete; independently reviewed after correction of seven findings. Production still serves HTTP. No deployment or commit was made.

Final re-review found no further actionable defects. The reviewer verified all 37 source hashes and independently passed transport and observability race tests; see the [final review log](final-review.log). Only documentation status and evidence metadata changed after that review.

[checks.json](checks.json) records the phase 1 base revision, hashes of the uncommitted phase 2 source, tool/runtime versions, exact commands and results. All required Makefile checks passed. The latest standalone focused run hit the already reproduced baseline budget test flake:

- [Focused transport, application, bootstrap and observability tests](focused.log): all phase 2 tests passed; the unchanged budget-resume test failed at line 144. The [earlier focused pass](focused-before-shutdown.log) is retained. The final full `make check` passed after the shutdown correction.
- [make check-api](check-api.log): generation/compatibility, isolated nested Go checks, Python 3.13/3.14 wheel and pinned Git installations. Installed clients call actual handlers and compare all four RPC responses with the standalone Go client.
- [make check](check.log): formatting, build, example configuration, lint, unit tests and race tests.
- [make vet](vet.log).
- [make docker-build](docker-build.log): the current HTTP image builds with the local generated module. This is a build prerequisite; it does not activate gRPC or run a new container workload.

The initial sandbox refused local TCP binds. Tests and Docker were then run with approved local access. No required check was skipped. Existing optional release/load/container probes remain opt-in; phase 4 owns the migrated workload measurements.

The raw HTTP/2 tests advertise a zero stream window. Response HEADERS prove the unary result reached sending; a second channel is rejected while DATA remains blocked. Reset, forced server close and a two-second absolute request-plus-grace deadline all release the slot. Ownership combines native processing completion with CloseNotify stream closure, including final trailers. See the [implementation report](../../../grpc-migration/02-transport-and-lifecycle.md#send-ownership-decision) for the supported-hook decision and the precise telemetry limit for trailer-only post-return failures.

The focused log also includes unchanged application behavior checks for moving retention boundaries, monthly slots, fifty shared misses and per-caller attempt budgets. Historical phase 1 evidence was not changed.

## Independent review corrections

Seven findings were corrected with dedicated regressions: total unary input bounds, body-independent admission/header rejection, native error observations, cancellation cause, expected-error reporting, application/serialization ownership after cancellation, and native HTTP setup failure observations. See the [correction report](../../../grpc-migration/02-transport-and-lifecycle.md#independent-review-corrections-pending-re-review). Status remains pending independent re-review.

The [initial full check](review-initial-check.log) and [initial API check](review-initial-check-api.log) failed in pre-existing tests. Clean phase 1 reproduction logs prove the [budget-resume race flake](baseline-budget-flake.log) and [Python timeout=0 ambiguity](baseline-python-deadline.log). The budget test is unchanged. The active Python deadline test now uses a ready channel and positive timeout with the same strict DEADLINE_EXCEEDED assertion.

The [native deadline probe](native-client-deadline-race.log) records the exact HTTP/2 INTERNAL_ERROR reset at a hard deadline. The final tests keep strict application timeout mapping, prove the earlier raw-flow-control hard abort, and narrowly recognize this native reset only after the local client deadline with matching timeout observations and cleanup. No grace interval was added and no work deadline was moved earlier.

The [re-review race checks](rereview-race.log) cover all transport and observability tests. Controlled application cleanup, blocked native serialization and delayed native dispatch prove capacity remains owned until processing ends after stream closure. Native HTTP 400 and 415 tests verify non-OK metrics. No timer releases work that is still running; the shutdown owner still returns at its deadline. A permanently stuck application dependency may require process exit.
