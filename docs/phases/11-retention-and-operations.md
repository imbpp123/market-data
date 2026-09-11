# 11 — Retention and operations

Status: not started. Dependencies: [06](06-instruments.md), [07](07-tickers-and-market-statistics.md), [10](10-kline-cache-and-api.md). Next: [12](12-release-verification.md).

## Outcome

Complete cleanup, optional observability, and verify the lifecycle of the assembled service. Source: [specification](../technical-specification-v1.md), sections 41 and 46–55.

## Work

- Add a retention worker using the configured cutoff and cleanup interval. Delete only historical klines under the agreed comparison rule. Do not apply retention to instruments, tickers, or statistics snapshots.
- Complete built-in concurrent counters, durations, snapshot sizes, candle count, and last-success times from section 49. Publication success means a published snapshot, not just a successful HTTP response.
- Add periodic aggregate structured logging. Keep operational statistics separate from MarketStats market data. Do not log entire candle arrays or secrets.
- Add optional Prometheus and Sentry adapters behind appropriate boundaries. Metrics endpoints must be absent when disabled; built-in statistics must work without Prometheus. Keep statistics labels scoped by exchange/market/window without symbol cardinality.
- Add the optional debug statistics endpoint, disabled by default. Implement Sentry error/panic/trace integration and bounded flush without leaking SDKs into domain/application.
- Finish startup wiring under the phase 01 policy: immediate instrument/ticker/independent statistics work, no extra Bybit statistics worker, and readiness based on initialized API/storage rather than exchange freshness.
- Verify SIGINT/SIGTERM handling for all workers, active HTTP requests, admission queues, shared fills, retention, and statistics logging. Cancel root work, stop accepting requests, wait within configured bounds, and flush telemetry.

## Tests and checks

- Controlled-clock retention cutoff, no deletion of current snapshots, storage failure handling, cleanup cancellation, and concurrent cache reads/fills.
- Counters and last-success values reflect publication; normalization/write failures retain previous last-success times. A shared Bybit response adds one HTTP request and independent branch outcomes.
- Statistics remain race-safe during updates and aggregate reads. Disabled Prometheus/debug routes are absent; enabling one integration does not become a dependency of another.
- Logging/telemetry contain useful operation context without full arrays or credentials. Use local/fake telemetry sinks; no production Sentry access in tests.
- Initial data failures follow the written startup policy. Exchange outages do not globally change initialized-service readiness; MarketStats scope readiness stays independent.
- Shutdown while workers wait for intervals, admission, cooldown, or shared fills terminates all owned work. Verify graceful HTTP closure and bounded telemetry flush.

## Exit criteria

The assembled service cleans up historical data, reports its state without external monitoring dependencies, and shuts down predictably. Feature phases already own their cancellation paths; this phase verifies them together rather than adding cancellation at the end.
