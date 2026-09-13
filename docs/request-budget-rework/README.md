# Binance request-limit rework: phase plans

Status: all five phase plans are approved by the user. Phases 1 and 2 are complete on September 13, 2026. Phase 3 is complete after independent review. Phases 4–5 have not started.

The [main specification](../request-budget-rework-specification.md) defines the behavior. These plans explain the work and tests. They do not add new agreed requirements. Proposed details are marked in each phase.

| Phase | Work | Expected result | Status |
| --- | --- | --- | --- |
| [1. Read exchangeInfo](01-exchange-info.md) | Reduce the Spot response and check body limits. | Complete catalogs load within memory bounds. | Complete |
| [2. Limits and settings](02-limits-and-settings.md) | Separate starting limits from user caps and add catalog refresh. | Current limits and configuration produce the right stop lines. | Complete |
| [3. Usage accounting](03-usage-accounting.md) | Combine exchange counters with local requests. | Parallel and failed requests keep correct local charges. | Complete |
| [4. Rejection and recovery](04-rejection-and-recovery.md) | Apply immediate rejection and resume by time. | Callers fail quickly; workers resume without polling. | Approved; not started |
| [5. Diagnostics and final checks](05-diagnostics-and-validation.md) | Add diagnostics, test complete flows, and update documents. | The change has clear evidence and migration instructions. | Approved; not started |

## How we will work

Implement phases in order, after the user approves the relevant plan. Keep each phase's changes and tests together. After each phase, record the changed files, checks, results, and any remaining issue here. Do not mark a phase complete only because its code compiles.

The middle phases prepare parts of one change. They are not separate production releases. Run focused tests in each phase and the full project checks in phase 5. Deployment is outside this plan.

## Test approach

Use unit tests for calculations and local HTTP integration tests for real component boundaries. Use controlled clocks and ordered responses for time and parallel work. Do not use arbitrary sleeps or public exchange calls in correctness tests.

Each test table below gives an input or event and the expected result. Live response-size checks in phase 1 are separate measurements. Keep Bybit regression tests unchanged unless a shared test needs a clearer setup.

## Phase 1 result

Spot exchangeInfo now sends `showPermissionSets=false`. The live decoded response fell from 17,589,070 to 6,703,095 bytes, with the same 3,698 symbols, instrument fields, and rate limits. The 16 MiB bound stays in place. Local replay used 75,874,304 bytes of peak process RSS. `make check`, `make vet`, and whitespace checks passed. Changed files, reproducible memory checks, and measurement limits are in the [phase report](01-exchange-info.md#implementation-report). Future catalog growth is the remaining size risk. Phase 2 implements settings and catalog updates; phase 3 adds usage accounting; fast rejection remains pending.

## Phase 2 result

Binance now defaults to 90%, uses explicit legacy limits as user caps, and follows supported exchange limit increases and decreases. One instrument worker owns both refresh schedules and reuses exchangeInfo. Invalid catalogs preserve the last valid limits. Valid reductions apply even when an operation no longer fits; discovery remains possible when its own cost fits the new allowance. New windows do not cause a pause solely because history is missing. Sources and update times are available in logs and an independent controller snapshot. `make check`, `make vet`, and whitespace checks passed. See the [implementation report](02-limits-and-settings.md#implementation-report) for files, checks, migration, and limits. Phase 3 now supplies accounting; phases 4–5 remain unimplemented.

## Phase 3 result

Each attempt now owns its local charge and valid weight observations. Common usage combines each observation with unmatched local costs; strict operation shares remain local. Invalid, lower, late, or missing headers do not erase live charges. Unknown overlap is explicitly conservative and can overestimate serial usage. In-flight Binance requests remain visible; Bybit behavior and real exchange cooldowns remain unchanged. Discrepancy-only pauses are removed. `make check`, `make vet`, and whitespace checks passed. See the [implementation report](03-usage-accounting.md#implementation-report) for the formula, numerical limits, files, and exact validation. Independent review found no confirmed defects. Budget waits remain until phase 4; diagnostics and full-flow validation remain in phase 5.
