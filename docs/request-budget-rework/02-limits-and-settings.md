# Phase 2. Load current limits and apply settings

Status: approved by the user. Implementation has not started. Depends on phase 1.

See the [phase list](README.md) and [main specification](../request-budget-rework-specification.md). This phase prepares limit data and refresh scheduling. Phase 4 changes request rejection.

## What we will change

1. Add the proposed Binance settings: `stop_threshold_percent` with default `90`, and `catalog_refresh_interval` with default `1h`.
2. Keep starting values, current exchange limits, and explicit user caps separate. A built-in default is not a permanent cap. Apply the percentage after selecting the smaller of the exchange limit and any user cap.
3. Preserve explicitly supplied legacy window `limit` values as user caps. Missing values remain defaults. Keep YAML and environment precedence. Report invalid or conflicting settings clearly.
4. Load limits at startup. Reuse exchangeInfo from instrument refreshes. If no recent catalog is available when the refresh interval ends, schedule one normal catalog request. Do not add a second request when a usable refresh is already running.
5. Apply a valid catalog as one update. Keep Spot and USDⓈ-M separate. Keep request-count, weight, and funding-family rules separate too.
6. Accept increases and decreases without clearing usage. Keep rules that are absent from a later catalog. Keep the last valid limits on a failed or invalid update and report the reason.
7. Make sure later refreshes can recover from an invalid catalog or an unusable allowance. A catalog error must not create a permanent block on discovery.

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

## Proposed details for review

Use integer percentages from 1 to 99 and a positive finite refresh interval. Reject settings that cannot allow a supported request within its operation share. The proposed field names and legacy mapping must be settled before coding this phase.
