# 10 — Candle cache fills and API

Status: not started. Dependencies: [04](04-memory-repositories.md), [05](05-upstream-admission-and-retries.md), [08](08-kline-planning.md), [09](09-kline-adapters.md), API decisions from [01](01-specification-decisions.md). Next: [11](11-retention-and-operations.md).

## Outcome

GET /api/v1/klines returns the requested complete range through bounded cache-aside loading, including concurrent misses. Source: [specification](../technical-specification-v1.md), sections 8, 25–33, 38–40, and 57–60.

## Work

- Build the application use case: validate → cache read → coordinate if needed → cache recheck → plan → fetch/save → re-read each caller's range → completeness check.
- Use singleflight for fills keyed by exchange/market/symbol/timeframe, without from/to. Permit at most one fill per series; different series remain independent within shared limits.
- Keep the fill under the service lifecycle context with its own deadline. A canceled caller, including the initiating caller, does not cancel work still needed by others. Bound waiters and active fills before creating work.
- After a shared fill, recheck each caller's own range. Reuse overlap and plan only remaining needs. Do not return the leader's response to callers with different ranges or extend an active fill for later arrivals.
- Track successful refreshes within each HTTP request to avoid repeatedly fetching a still-open candle. Require post-close confirmation when applicable. A later independent HTTP request evaluates refresh needs again.
- Preserve successful pages when a later page fails, but never label an incomplete result complete. On fill failure, return cached data only if that caller's own range is complete. End no-progress loads and exhausted attempts/deadlines with the agreed error.
- Implement the kline HTTP DTO and parameter/error mapping. Enforce request size before planning and expose 400 request_too_large and 503 service_overloaded as specified.
- Add cache-hit/miss, downloaded candle, attempt, and shared-fill statistics at their actual behavior boundaries.

## Tests and checks

- Cold, warm, and partial-cache HTTP integration using real memory storage and local exchange fixtures. A warm final range makes zero upstream requests.
- 50 identical simultaneous misses return full correct ranges after one effective fill; request count matches its plan, which may contain multiple pages.
- Overlapping ranges reuse stored overlap; later fills re-read cache; different series run independently; a final cache hit does not wait for another fill in the same series.
- Leader cancellation, waiter cancellation, all callers leaving, fill deadline, queue overload, attempt exhaustion, and service shutdown. No unbounded waiters or permanent coordination registry entries remain.
- Shared fetch failure does not cause a retry burst. Earlier pages stay cached; incomplete results fail. Empty/no-progress upstream responses terminate within bounds.
- Open-candle sharing refreshes once for that request; a new request may refresh again. Test a close boundary crossed during a fill and prevent stale upserts.
- HTTP validates filters, timestamps, interval support, and range policy before upstream work. Verify decimal strings, explicit null, UTC/exclusive boundaries, sorted output, and stable errors without SDK details.
- History: enforce klines.max_history_candles (default 1000) relative to the latest closed-slot boundary. Reject more than N requested slots with 400 request_too_large and an older start with 400 range_out_of_retention before cache/fill access, including old rows awaiting cleanup. Equality passes the depth check. Cover expiration during waiting, calendar intervals, and gaps without widening the window.
- Run all concurrency tests with controlled event order and go test -race.

## Exit criteria

All four data APIs are implemented. Concurrent candle misses satisfy correctness, reuse, cancellation, and bounded-resource contracts; successful partial responses are never used to hide a failed load.
