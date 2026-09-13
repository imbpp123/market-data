# Phase 2. Load current limits and apply settings

Status: implemented on September 13, 2026. Depends on completed phase 1. Validation is recorded below.

See the [phase list](README.md) and [main specification](../request-budget-rework-specification.md). This phase prepares limit data and refresh scheduling. Phase 4 changes request rejection.

## What we will change

1. Add the proposed Binance settings: `stop_threshold_percent` with default `90`, and `catalog_refresh_interval` with default `1h`.
2. Keep starting values, current exchange limits, and explicit user caps separate. A built-in default is not a permanent cap. Apply the percentage after selecting the smaller of the exchange limit and any user cap.
3. Preserve explicitly supplied legacy window `limit` values as user caps. Missing values remain defaults. Keep YAML and environment precedence. Report invalid or conflicting settings clearly.
4. Load limits at startup. Reuse exchangeInfo from instrument refreshes. If no recent catalog is available when the refresh interval ends, schedule one normal catalog request. Do not add a second request when a usable refresh is already running.
5. Apply a valid catalog as one update. Keep Spot and USDⓈ-M separate. Keep request-count, weight, and funding-family rules separate too.
6. Accept increases and decreases without clearing usage. Keep rules that are absent from a later catalog. Keep the last valid limits on a failed or invalid update and report the reason.
7. Allow later refreshes to recover from an invalid catalog. After a valid reduction, discovery continues when its own request cost fits. Do not keep an older ceiling only to make a request fit.

The main areas are configuration loading and validation, the upstream catalog, and instrument refresh scheduling. Keep worker cancellation and shutdown under the current worker owner. Do not add a new scheduler framework.

## Test cases

| Case | Expected result |
| --- | --- |
| No new settings | Percentage is 90 and refresh interval is one hour. |
| Explicit 80 or 85 | The supplied percentage is used; neither is replaced by 90. |
| YAML value plus environment override | The environment value wins. An omitted value still uses the default. |
| Invalid percentage or interval | Validation fails with the field name and reason. Proposed bounds are listed below. |
| No user cap; exchange limit changes from 6,000 to 10,000 | At 90%, the stop line changes from 5,400 to 9,000. Existing usage stays recorded. |
| User cap 5,000; exchange limit 10,000 | At 90%, the stop line is 4,500. |
| Exchange limit falls from 6,000 to 4,000 | The stop line becomes 3,600 immediately. Usage is not reset. Phase 4 will test the resulting rejection. |
| Spot limit falls from 6,000 to 1,000 | Apply stop line 900. Keep spent usage. Statistics cost 80 cannot fit its share 45, but exchangeInfo cost 20 still fits and can fetch a later increase. |
| A positive limit rounds down to zero allowance | Keep the new limit. Reject positive-cost requests immediately; do not restore the older ceiling after usage expires. |
| Explicit legacy limit versus an omitted default | Only the explicit legacy value becomes a user cap. Test YAML and environment inputs. |
| Conflicting old/new settings | Startup fails clearly instead of choosing silently. |
| First catalog load fails | Reviewed starting limits remain available. A later valid load can replace them. |
| Network error, malformed JSON, invalid interval, duplicate rule, or unknown applicable type | Keep the whole last valid catalog. Report the update error; do not install only part of the update. |
| Old response arrives after a newer accepted response | The old response cannot replace the accepted catalog. |
| A rule is missing in a later response | Keep the known rule. Ignore order limits without blocking market data. |
| New or longer applicable window | Store the rule and available history. Missing history alone is not a reason for a full-window pause. |
| Spot update | USDⓈ-M limits do not change. The funding-family rule is not replaced by weight counters. |
| Instruments already refreshed the catalog, or a refresh is in flight | No duplicate catalog call is sent. |
| Refresh becomes due, fails, or is delayed by admission | Scheduling stays bounded. No busy loop or request backlog appears. A later allowed refresh can succeed. |
| Shutdown during a scheduled refresh | The request and timer stop with their owner. No worker remains running. |
| Bybit settings | Existing defaults, limits, and behavior remain unchanged. |

## Expected result

Tests prove how each limit is selected and updated. Configuration examples explain the new fields and legacy mapping. The user can see the source of each limit without assuming that a default is a user choice.

## Implemented settings and migration

The fields are `upstream.binance.stop_threshold_percent` (default `90`) and `upstream.binance.catalog_refresh_interval` (default `1h`). The percentage is an integer from 1 to 99. The interval is a positive Go duration that fits `time.Duration`. Startup also checks that each enabled operation can run with the derived bootstrap allowance and configured statistics schedule.

Explicit legacy Binance `upstream.limits.*.windows.*.limit` values are user caps, including values equal to the built-in defaults. Missing limits do not become caps. YAML values are merged with explicit environment overrides. No second user-cap alias is added, so there are no competing old/new cap fields. Unknown keys, duplicate keys, and invalid types still fail loading. The old `safety_margin_percent` applies to Bybit only; it does not override the new Binance percentage. An old file that needs an 80% Binance threshold must set the new field to `80`.

The [configuration example](../examples/config-v1.yaml) comments out Binance window limits so it follows exchange increases. Uncomment a limit only to set a user cap. For Go callers that construct settings directly, `Window.ExplicitLimit` marks an explicit default-valued cap. A value different from the built-in default is also treated as a cap.

## Implementation report

The controller keeps the current exchange ceiling, optional user cap, derived stop line, source, and update time separately. Bootstrap values remain active before the first accepted catalog. A complete supported update may raise or lower an exchange ceiling without clearing history. Missing rules retain their previous values and timestamps. Order limits are ignored. Spot, USD-M, raw-request, and funding-family rules stay separate.

Malformed catalogs, invalid numeric fields, duplicate rules, and unknown applicable types return a catalog error without changing any installed window or the accepted catalog timestamp. Valid numeric reductions are installed even when some operations no longer fit. Admission rejects each request whose cost exceeds its new allowance and reports the window, cost, and allowance. A later ordinary exchangeInfo request can recover through the same instruments share, spacing, HTTP slots, attempts, deadlines, and cooldowns. An older response cannot replace an accepted newer catalog. Adding a longer window does not create a cooldown. Its available local history is used; the oldest available history is included in the read-only limit snapshot and logs.

One instrument worker owns both schedules for each Binance scope. Startup uses its instrument request. A successful exchangeInfo response resets catalog freshness even if later instrument normalization or funding metadata fails. The worker schedules catalog-only work when needed and gives due instrument work priority. Catalog-only work does not publish instruments or fetch funding metadata. Requests never overlap in this worker, missed intervals do not create a backlog, and failures wait for the configured interval or longer backoff. Shutdown cancels the request and timer with the worker.

Changed files:

- `internal/config/{config,load,validate}.go` and their tests: settings, explicit-cap provenance, precedence, validation, and new default allowances.
- `internal/infrastructure/exchange/upstream/{controller,catalog,catalog_cycle}.go`, `catalog_test.go`, and `response_test.go`: dynamic limits, failure recovery, sources, snapshots, and the shared worker schedule.
- `internal/bootstrap/instruments.go` and `catalog_test.go`: catalog-only adapter calls, reporting, and deterministic worker integration tests.
- `internal/infrastructure/exchange/klines_accounting_test.go`: existing cost/share expectations now use the default 90% allowance.
- `docs/examples/config-v1.yaml`, this plan, the phase index, and the main rework specification: settings and phase status.

Validation completed on September 13, 2026:

- Focused config, controller, catalog, and worker tests passed. Tests cover 80% and 85%, explicit caps equal to defaults, increases/reductions, atomic failures, stale responses, missing rules, new windows, normal-admission recovery, schedule reuse, both Binance paths, delayed work, and shutdown.
- `make check` passed: formatting, build, runnable example validation, configured lint (0 issues), all unit/integration tests, and all race tests.
- `make vet` and `git diff --check` passed.
- Local HTTP integration checks needed execution outside the default sandbox because the sandbox denies local listeners and some Go cache access. No public exchange calls were made. No new response-size/RSS measurement was needed: phase 2 does not change the phase 1 body bound or retain catalog bodies between refreshes.

## Limits of this phase

A valid smaller limit is always installed. If exchangeInfo itself no longer fits, normal discovery cannot send another request under that limit. Time alone cannot fix a request cost above the allowance. The service reports this failure; it does not bypass admission or restore an older ceiling. Other traffic on the same IP and unknown changes remain outside the local guarantee.

The new default 90% and selected limits are active now. Common usage is still checked by the earlier admission rule. Threshold crossing, immediate budget rejection, exchange-counter merging, and the removal of usage-header discrepancy pauses belong to phases 3–4. Existing usage-header cooldowns and HTTP 429/418 behavior remain. Expanded metrics and final operating-document migration belong to phase 5. This intermediate phase is not a production release.

## Review fix: apply valid reductions

The first implementation rejected a valid catalog if any operation could not fit its share. This was incorrect. The catalog now accepts valid reductions, while request admission checks each operation separately. Bootstrap configuration still needs to support the configured operations.

Regression tests first failed on the old catalog rule. They now cover Spot and USD-M reductions, preserved spending, immediate rejection of an expensive request, a waiting request rechecked after reduction, normal discovery of a later increase, and a positive limit that rounds down to zero. Invalid zero and negative exchange limits remain errors.

Fix validation: focused regression tests, `make check` (including all tests and race tests), `make vet`, and `git diff --check` passed. The linter reported 0 issues. No public exchange calls were needed.
