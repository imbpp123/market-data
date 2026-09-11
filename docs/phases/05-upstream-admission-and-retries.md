# 05 — Upstream admission and retries

Status: not started. Dependencies: [02](02-bootstrap-and-configuration.md), [03](03-domain-and-contracts.md), approved limit settings from [01](01-specification-decisions.md). Next: [06](06-instruments.md).

## Outcome

Every upstream HTTP attempt is accounted for and bounded before data collectors run. Source: [specification](../technical-specification-v1.md), sections 14–16, 30, 32–33, and 45.

## Work

- Pin the selected SDK revisions/module versions. Disable Binance SDK retries and verify injectable base URLs, HTTP clients, and contexts with local servers.
- Implement endpoint/parameter cost resolution, including Binance kline limit bands and fundingInfo's separate request-count limit despite zero weight.
- Implement atomic admission across all applicable sliding windows, common budgets, operation shares, cooldowns, and HTTP slots. Apply the no-borrowing policy once phase 01 resolves its draft status. Use configured bootstrap and restart rules.
- Bound admission queues, concurrent attempts, waiting time, and total attempts. Keep FIFO within an operation type while allowing another type with budget to proceed. Waiting for budget never holds an HTTP slot.
- Capture status and headers per request before SDK error handling loses them. Preserve exact body data where the Bybit generic decoder would lose a required number. Do not store a shared mutable last-response field.
- Handle Bybit retCode errors even on HTTP 200, scoped Binance 429/418 cooldowns, and the specific Bybit access-too-frequent 403 condition. Ignore known inaccurate headers; late responses cannot relax existing limits.
- Implement bounded temporary-error retries with exponential backoff and injected jitter/time. Retry-After and shared cooldown override shorter local backoff. A new worker cycle cannot bypass backoff.
- Expose the request/error/duration events needed by built-in statistics; wire feature-specific counters as features arrive.

## Tests and checks

- Concurrent admissions obey every window and share at boundaries. Ticker exhaustion leaves capacity for instruments, klines, and independent statistics; the other exchange continues during a scoped cooldown.
- Every page/retry consumes its own actual cost; unknown costs and impossible budgets fail before sending. Shared Bybit collection consumes one request.
- Canceled waiting work spends nothing and releases resources; errors after transport dispatch do not refund usage. No partial budget consumption while waiting for another budget.
- Queue overflow, deadline/attempt exhaustion, restart/bootstrap behavior, limit updates, and no HTTP slot held during waits.
- Local SDK paths preserve error headers, propagate context, disable nested retries, and reject retCode=10006 as success.
- Missing/invalid reset headers, out-of-order headers, minimum Bybit IP-ban wait, and cooldown beyond the operation deadline. Use fake clocks and transports, not real-minute sleeps.

## Exit criteria

All adapter HTTP paths, including any exact-decoding fallback, must use this mechanism. Deterministic unit and local integration tests prove admission behavior and retry accounting. The current YAML example's requests_per_second value is not treated as an exchange budget.
