# 05 — Upstream admission and retries

Status: complete, September 12, 2026. Dependencies: [02](02-bootstrap-and-configuration.md), [03](03-domain-and-contracts.md), approved limit settings from [01](01-specification-decisions.md). Next: [06](06-instruments.md).

## Outcome

Every upstream HTTP attempt is accounted for and bounded before data collectors run. Source: [specification](../technical-specification-v1.md), sections 14–16, 30, 32–33, and 45.

## Work

- Pin the selected SDK revisions/module versions. Disable Binance SDK retries and verify injectable base URLs, HTTP clients, and contexts with local servers.
- Implement endpoint/parameter cost resolution, including Binance kline limit bands and fundingInfo's separate request-count limit despite zero weight.
- Implement atomic admission across all applicable sliding windows, common budgets, operation shares, cooldowns, and HTTP slots. Apply the confirmed no-borrowing policy and 60/30/5/5 default shares. Combine shares at composition for the shared Bybit ticker/statistics path (65% by default), without runtime borrowing, a second request, or a double charge. Use configured bootstrap ceilings. Keep usage and cooldowns only in memory; restart resets local state without an automatic wait or resetting exchange-side limits.
- Bound admission queues, concurrent attempts, waiting time, and total attempts. Keep FIFO within an operation type while allowing another type with budget to proceed. Waiting for budget never holds an HTTP slot.
- Capture status and headers per request before SDK error handling loses them. Preserve exact body data where the Bybit generic decoder would lose a required number. Do not store a shared mutable last-response field.
- Handle Bybit retCode errors even on HTTP 200, scoped Binance 429/418 cooldowns, and the specific Bybit access-too-frequent 403 condition. Ignore known inaccurate headers; late responses cannot relax existing limits.
- Implement bounded temporary-error retries with exponential backoff and injected jitter/time. Retry-After and shared cooldown override shorter local backoff. A new worker cycle cannot bypass backoff.
- Expose the request/error/duration events needed by built-in statistics; wire feature-specific counters as features arrive.

## Tests and checks

- Concurrent admissions obey every window and share at boundaries. Ticker exhaustion leaves capacity for instruments, klines, and independent statistics; the other exchange continues during a scoped cooldown.
- Every page/retry consumes its own actual cost; unknown costs and impossible budgets fail before sending. Shared Bybit collection consumes one request.
- Canceled waiting work spends nothing and releases resources; errors after transport dispatch do not refund usage. No partial budget consumption while waiting for another budget.
- Queue overflow, deadline/attempt exhaustion, restart loss of local counters/cooldowns without persistence, bootstrap behavior, limit updates, and no HTTP slot held during waits.
- Local SDK paths preserve error headers, propagate context, disable nested retries, and reject retCode=10006 as success.
- Missing/invalid reset headers, out-of-order headers, minimum Bybit IP-ban wait, and cooldown beyond the operation deadline. Use fake clocks and transports, not real-minute sleeps.

## Exit criteria

All adapter HTTP paths, including any exact-decoding fallback, must use this mechanism. Deterministic unit and local integration tests prove admission behavior and retry accounting. The current YAML example's requests_per_second value is not treated as an exchange budget.


## Implementation

- `internal/infrastructure/exchange/upstream` owns admission and the HTTP transport. One controller serves the two separate Binance API scopes and the shared Bybit scope. It checks common windows, fixed operation shares, funding-family requests, pacing, cooldowns, queue bounds, and lane/global slots under one mutex. Dispatch selection is round-robin across ready lanes, FIFO within each exchange/operation lane. Waiting does not reserve an HTTP slot or partially consume a budget.
- `Controller.Begin` creates one bounded operation context for a full cycle or fill. Every page and retry reuses it. Costs come from the actual GET path and parameters, with explicit Binance kline limits and bulk-only snapshot paths. Unknown costs, mismatched operation contexts, disabled scopes, and impossible runtime allowances fail before dispatch. A canceled reservation is rolled back; dispatched failures retain their usage.
- Bootstrap ceilings are capped by captured exchange ceilings and the configured safety margin. Shared Bybit ticker/statistics percentages combine before rounding. Successful Binance catalogs update recognized windows in request-start order, retain omitted configured windows, ignore order limits, and block the scope on malformed or unusable applicable limits. A new window without enough history pauses the scope for its full duration. Headers never refund usage or shorten cooldowns; known inaccurate USDⓈ-M price/book headers are ignored. Public Bybit responses do not import trading UID budgets.
- The transport reads bounded bodies and status/headers before SDK processing, including failed HTTP responses. Trustworthy usage headers and supported 5xx Retry-After values apply before body decoding, including when reading fails. Gzip, zlib-wrapped HTTP deflate, and Brotli are decoded before the response-size check. Each SDK invocation has its own response capture; no shared client last-response state exists. Captures preserve exact JSON bytes, error identity, and attempt/receipt timestamps. HTTP 200 with a Bybit error code cannot return successful data.
- Retries use configured attempt limits and exponential full jitter, with injected clocks/timers and jitter. Each attempt supplies a non-rewindable empty body to prevent implicit GET replay inside net/http; retries return through admission while connection reuse stays enabled. Valid Retry-After/reset signals and shared cooldowns override shorter backoff. Fallbacks and the Bybit access-too-frequent ten-minute minimum follow the implementation contract. `CycleGate.Run` preserves failed-cycle backoff, waits at least the normal refresh interval, rejects overlapping cycles, and resets failure state only after the whole callback succeeds.
- `binance.Client.Fetch` uses each pinned REST module's request path with raw JSON decoding; Binance nested retries and SDK request timeouts are disabled because this transport owns both. `bybit.Client.Fetch` uses the three supported market SDK methods and returns the captured body rather than its generic floating-point result. Feature normalization and repository publication remain phase 06–09 work.
- `internal/bootstrap/exchanges.go` creates enabled clients with the same controller before HTTP startup and performs no discovery calls during construction. Request/error/duration events are exposed for statistics consumers; bootstrap logs failed attempts. Full built-in statistics aggregation remains phase 11 work. Restart creates empty local ledgers and no automatic quiet period; the existing startup warning records the exchange-side continuity limitation.

Pinned module versions, resolved from specification section 14 revisions:

| Module | Version |
| --- | --- |
| Binance Spot | `v1.14.2-0.20260910112333-9803fd1c7ed7` |
| Binance USDⓈ-M | `v1.20.2` |
| Binance common/v2 | `v2.8.1` |
| Bybit | `v1.1.2-0.20260724042240-a58e14c6fd93` |

Unit tests use `testing/synctest` with the injected clock boundary. They cover cost bands, shares at sliding-window boundaries, FIFO/round-robin dispatch, concurrent attempts, cancellation, queues, deadlines, no partial consumption, funding request counts, HTTP errors, body/decompression bounds, missing/invalid/late limit metadata, catalog updates, restart behavior, retries, and persistent cycle backoff. Local SDK tests cover all twelve Binance paths and all three Bybit paths, injectable URLs/clients, in-flight and pre-dispatch cancellation, error headers, no nested retries, retCode rejection, concurrent request-local metadata, and exact integers above 2^53.

Executed checks: `make check` passed on Go 1.27.1, including formatting, build, example configuration validation, standard lint with govet (zero issues), unit/local integration tests, and race tests. Checks used writable temporary Go/linter caches and local loopback servers. No live exchange calls or credentials were used. Domain normalization, collector lifecycle integration, and deployment access remain their planned feature/release gates.
