# Binance request-limit rework: phase plans

Status: phase 1 is approved by the user. Phases 2–5 await approval. No phase has started.

The [main specification](../request-budget-rework-specification.md) defines the behavior. These plans explain the work and tests. They do not add new agreed requirements. Proposed details are marked in each phase.

| Phase | Work | Expected result | Status |
| --- | --- | --- | --- |
| [1. Read exchangeInfo](01-exchange-info.md) | Reduce the Spot response and check body limits. | Complete catalogs load within memory bounds. | Approved; not started |
| [2. Limits and settings](02-limits-and-settings.md) | Separate starting limits from user caps and add catalog refresh. | Current limits and configuration produce the right stop lines. | Awaiting approval |
| [3. Usage accounting](03-usage-accounting.md) | Combine exchange counters with local requests. | Parallel and failed requests keep correct local charges. | Awaiting approval |
| [4. Rejection and recovery](04-rejection-and-recovery.md) | Apply immediate rejection and resume by time. | Callers fail quickly; workers resume without polling. | Awaiting approval |
| [5. Diagnostics and final checks](05-diagnostics-and-validation.md) | Add diagnostics, test complete flows, and update documents. | The change has clear evidence and migration instructions. | Awaiting approval |

## How we will work

Implement phases in order, after the user approves the relevant plan. Keep each phase's changes and tests together. After each phase, record the changed files, checks, results, and any remaining issue here. Do not mark a phase complete only because its code compiles.

The middle phases prepare parts of one change. They are not separate production releases. Run focused tests in each phase and the full project checks in phase 5. Deployment is outside this plan.

## Test approach

Use unit tests for calculations and local HTTP integration tests for real component boundaries. Use controlled clocks and ordered responses for time and parallel work. Do not use arbitrary sleeps or public exchange calls in correctness tests.

Each test table below gives an input or event and the expected result. Live response-size checks in phase 1 are separate measurements. Keep Bybit regression tests unchanged unless a shared test needs a clearer setup.
