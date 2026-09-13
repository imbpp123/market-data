# Binance request-limit rework

September 13, 2026. Main design and phase 1 plan approved. Phases 2–5 await user approval; implementation has not started. Proposed details are marked below.

## Summary / Overview

Update Binance limits regularly. Track usage and reject new exchange requests after usage crosses a configured threshold: **90% by default, or the value set in configuration**. Allow requests again when recorded usage expires. A difference between Binance and local counters must not cause a pause by itself.

## Context / Background

The service reads public market data from Binance and Bybit. Binance Spot and USDⓈ-M have separate limits. Requests may consume weight, a request count, or an endpoint-family allowance. Headers report **used weight**, not remaining capacity.

Today, the limiter keeps local request history, gives operations fixed shares, and controls request spacing and parallel work. All state is in memory and is lost on restart.

## Problem Statement

A successful Binance response can report more usage than our local counter. The service then pauses the scope for a full window, even when usage is low. This can happen after restart, when local history is empty. A newly found window with too little history can cause the same pause.

There is also a separate loading problem: the reported full Spot exchangeInfo response exceeds the current 16 MiB decoded-body limit. This prevents instrument and limit loading. Fixing this size issue does not fix the accounting rule.

## Goals

- Use current applicable Binance limits and follow valid increases and decreases.
- Stop requests after the configured threshold is crossed, with immediate rejection.
- Resume by time, without needing a new successful response first.
- Count requests once, including parallel requests and retries.
- Keep failures clear and background waiting quiet.

## Non-Goals

Bybit behavior and settings stay unchanged. This change does not add persistent usage storage, a shared limiter across processes, or new trading endpoints.

## Proposed Solution / Design

### Load limits

Start from reviewed default limits. Fetch exchangeInfo at startup and refresh about once per hour. Reuse a response already loaded by the instrument worker; avoid duplicate calls. Limit loading uses normal request admission and accounting.

Use exchangeInfo for published weight and request-count limits. Keep documentation-only endpoint rules separately. The funding family keeps its 500 requests/5 minutes/IP rule, even though fundingInfo has zero weight. Ignore order limits. Keep endpoint costs from [specification section 14](technical-specification-v1.md).

A valid update changes limits without clearing usage. Built-in defaults are starting values, not permanent caps. An explicit user ceiling still applies. For example, an increase from 6,000 to 10,000 moves the default stop line from 5,400 to 9,000 when there is no user cap.

### Count usage

Combine valid exchange counters with local requests that are not yet known to be included. Keep local rolling history too. Do not add the full exchange counter to the full local total: they can cover the same requests.

Example: a counter of 100 includes our cost-20 request. Two other cost-4 requests are not included yet. Count 108, not 128. Each page and retry has its own cost.

Reserve cost when allowing a request. Check and reserve as one atomic step: the next parallel request must see this cost before any response arrives.

### Reject after crossing

The stop line is the configured percentage of the smaller of the exchange limit and any user ceiling, rounded down. Before sending, check **current usage, including reserved cost**. Reject when it is **greater than** the stop line. Equality allows a request to cross the line, if all other limits allow it.

For a limit of 6,000, a 90% setting, and enough operation allowance:

| Current usage | New cost | Result |
| --- | --- | --- |
| 5,398 | 4 | Send; usage becomes 5,402. The next request fails. |
| 5,400 | 4 | Send; usage becomes 5,404. The next request fails. |
| 5,401 | 4 | Reject without sending. |

Rejection spends no budget, HTTP slot, or retry attempt. Keep **60/30/5/5 operation shares and no borrowing**. Calculate each share from the stop line, rounded down. These operation caps stay strict: the new cost must fit the remaining share. An operation can therefore stop before common usage crosses the configured threshold. External usage affects the common limit; do not assign it to an invented operation.

Keep local sliding windows, request spacing, bounded queues and parallel work, and caller deadlines. The admission timeout still limits waits for spacing or a free slot. Binance threshold and share failures return immediately.

### Resume by time

Use a reliable exchange-window boundary when known. Otherwise, expire a counter observation one full window after receipt. A minute counter received at `12:34:20` can expire at `12:35:20`. Rejected calls do not move that time forward. Use elapsed time; a clock jump or smaller counter alone is not proof of a reset.

Remove only expired usage. Recheck all windows, operation shares, requests still in flight, and exchange cooldowns. A worker uses a timer; a new caller can run the same local check. The next ordinary request brings fresh counter data. No probe or successful response is needed to remove the local block.

## Data Model / API / Interfaces

Extend the existing controller, HTTP transport, configuration, worker scheduling, and diagnostics. Keep state in memory. For each applicable window, the controller needs the limit and its source, update time, stop line, local charges, exchange observations, reserved cost, and expiry times. Keep exchange cooldowns separate from usage expiry. No database or event schema is added.

The transport passes request cost, dispatch status, response headers, and response timing to the controller. The controller returns either permission or a reason and next eligible time for the worker. Exact internal types are an implementation detail.

The public API keeps its response format. A candle request that needs rejected exchange work returns `service_overloaded`. Valid cached pages remain stored, but the caller never receives partial success. Complete cache reads and snapshot reads keep working under their existing local limits. Workers keep previous snapshots and defer the next run.

Proposed configuration fields:

```yaml
upstream:
  binance:
    stop_threshold_percent: 90
    catalog_refresh_interval: 1h
```

Missing fields use these defaults. Explicit YAML values and then environment values override them. Proposed validation: an integer percentage from 1 to 99, a positive finite interval, and operation allowances large enough for supported requests.

## Failure Modes / Edge Cases

| Case | Required behavior |
| --- | --- |
| Failed, malformed, or unsupported catalog update | Keep the last valid limits, or starting values before first success. Report the refresh problem. Do not block only because refresh failed. |
| Old catalog response or missing rule | An older request cannot replace a newer accepted catalog. A missing rule does not prove its removal. |
| Valid limit reduction | Apply it to new admissions at once. Keep usage. If a request can never fit its allowance, report a clear configuration/limit error. |
| Restart or unknown window | Use available history. Do not add a full-window pause only because history is missing. |
| Missing, invalid, smaller, or late counter | Keep local accounting. Do not erase usage that has not expired. Ignore the known inaccurate USDⓈ-M price-v2/bookTicker weight headers. |
| Unclear counter overlap or timing | Use a conservative estimate and mark the uncertainty. Weight cannot replace raw-request or funding-family counts. |
| Cancellation before dispatch | Release the reservation; no request cost is spent. |
| Cancellation, timeout, or network error after dispatch | Keep the cost because Binance may have received the request. |
| HTTP 429/418 | Preserve Retry-After, current fallback waits, and existing cooldown rules, even if body processing fails. Usage expiry does not clear a ban. |
| Oversized Spot exchangeInfo | Fail loading clearly and keep previous data. Use the separate fix in phase 1; never accept a partial catalog. |

## Observability

Expose the limit source and age, exchange/user ceiling, configured percentage, stop line, observed and local usage, reserved cost, remaining allowance, and any uncertainty. Show the rejection reason and next eligible time.

Keep threshold rejection, exhausted operation share, exchange cooldown, refresh failure, and oversized response as separate reasons. Normal worker deferral must not create repeated warnings, retry loops, or false HTTP attempt/error counts.

## Migration / Rollout Plan

The [phase plans](request-budget-rework/README.md) describe the work, expected results, and test cases. Each phase waits for user approval before implementation.

1. [Read exchangeInfo](request-budget-rework/01-exchange-info.md): fix the separate response-size issue and check complete catalogs.
2. [Limits and settings](request-budget-rework/02-limits-and-settings.md): add settings, user caps, and catalog updates.
3. [Usage accounting](request-budget-rework/03-usage-accounting.md): combine local costs, in-flight requests, and exchange observations.
4. [Rejection and recovery](request-budget-rework/04-rejection-and-recovery.md): reject after crossing and resume by time.
5. [Diagnostics and final checks](request-budget-rework/05-diagnostics-and-validation.md): verify full flows and update operating documents.

Detailed work and test cases are in the linked plans to keep this specification short. Intermediate phases are not separate production releases. Deployment is outside this plan.

## Testing / Validation

Use deterministic unit tests and local HTTP integration tests with controlled clocks and response order. Acceptance requires:

- Default 90%, configured values (for example, 80% and 85%), legacy user caps, invalid settings, and limit increases/reductions behave as described.
- All three threshold examples pass. Parallel calls see reserved cost before replies arrive. A rejected call sends no HTTP request.
- The 100 + 4 + 4 example gives 108. Pages, retries, cancellations, network errors, and reversed replies do not lose or duplicate local charges.
- Restart, a new window, and a low exchange counter above the local total do not cause an automatic pause. Invalid refreshes keep usable limits.
- Recovery works without new responses. Repeated rejection does not extend expiry. Other active windows, strict shares, and longer cooldowns still block work.
- Candle errors, complete cache reads, background deferral, and diagnostics follow their contracts. Bybit behavior stays unchanged.
- Full Spot catalogs load within the selected body and memory bounds. Oversized or incomplete bodies fail clearly.

During implementation, run formatting, `make check`, `make vet`, and relevant memory checks. Live response-size measurements are separate from deterministic tests. This documentation change does not claim those implementation checks have passed.

## Risks / Trade-offs

The checkable guarantee is: **no new positive-cost request enters a constraint whose current accounted usage is already above its stop line**. The configured threshold is not a hard ceiling; a crossing request is allowed.

Other traffic on the same IP, unknown usage after restart, delayed charging, and unseen limit changes prevent an absolute guarantee about actual IP usage. Continuing with the last valid catalog improves availability but cannot enforce an unknown new rule. Conservative estimates may delay work longer than needed. Strict operation shares may reject work while the common window still has room.

## Open Questions

The following engineering details still need verification. They do not change the agreed threshold behavior:

- Is `showPermissionSets=false` enough to fit the complete Spot response? If not, which measured body limit or bounded decoder should be used?
- Which response timing proves that a local request is included in a counter? Define and test the conservative fallback for unclear cases.
- Confirm final configuration field names, validation, and handling of legacy overrides before implementation.

## Earlier requirements replaced

The current code and historical release evidence remain unchanged until implementation.

- [Implementation contract](implementation-contract-v1.md), “Bootstrap, discovered limits, and cooldown”: replace discrepancy/new-window pauses, malformed-catalog blocking and permanent bootstrap caps with the rules above.
- [Specification](technical-specification-v1.md), sections 32–33: replace Binance's hard common 80% budget and budget waiting with a configurable stop line, permitted crossing and immediate rejection. Keep strict operation caps.
- Sections 42–44 and the [configuration example](examples/config-v1.yaml): add Binance settings and migration. Sections 45–52, 57–60 and section 65 item 14: update scheduling, diagnostics, tests and the stated guarantee.
- [Decision register](specification-decisions-v1.md), D05/D07: update these policies. Preserve topology, operation shares, restart behavior and deadlines. Bybit is unchanged.

## Sources

Official sources were checked on September 13, 2026 during the original review; this rewrite makes no new live-verification claim.

- [Binance Spot REST](https://github.com/binance/binance-spot-api-docs/blob/master/rest-api.md): catalogs, costs, counters, Retry-After and permission-set suppression.
- [USDⓈ-M general rules](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/general-info#limits) and [market data](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data): separate limits, funding family and unreliable-header exceptions.
- [Bybit rules](https://bybit-exchange.github.io/docs/v5/rate-limit): unchanged IP and endpoint/UID behavior. [Captured Binance limits](evidence/phase-01/exchange-limits.json): bootstrap evidence, not current runtime ceilings.
