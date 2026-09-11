# 02 — Bootstrap and configuration

Status: not started. Dependencies: relevant decisions in [01](01-specification-decisions.md). Next: [03](03-domain-and-contracts.md).

## Outcome

A buildable Go process with explicit settings, a basic HTTP lifecycle, and repeatable checks. Source: [specification](../technical-specification-v1.md), sections 2, 10–11, 42–44, 52–56, and 63.

## Work

- Create go.mod and the executable entry point with the specified Go version. Verify toolchain availability; record a blocker if the pin cannot be obtained instead of choosing a different version silently.
- Add packages only as they gain behavior. Compose dependencies at the entry point; keep environment access and concrete SDK types outside domain/application.
- Implement defaults → YAML → environment overrides → validation. Include klines.request_timeout (30s) and klines.max_history_candles (1000). Use one count for API history depth, request slots, and retention across all supported intervals. Do not implement the superseded per-interval duration map.
- Cover the full agreed config schema, not only the current example. Validate exchange/market combinations, storage driver, windows, intervals, budgets, attempt/queue/concurrency bounds, and observability options. Centralize kline defaults and maxima.
- Provide a runnable example without secrets. Reject unsupported settings such as a separate Bybit statistics interval. Validate capabilities at composition once the providers exist.
- Add structured logging, a root context, owned worker shutdown hooks, and a minimal HTTP server. Health means the process is alive; readiness becomes successful only after HTTP/API and storage initialization. Complete data workers are wired in their feature phases.
- Add Makefile build/run/test/test-race/lint targets and CI for build, vet, tests, race checks, and the chosen lint configuration. Add Docker targets when their implementation exists in phase 12.

## Tests and checks

- Defaults, partial YAML, environment precedence, malformed input, unsupported driver/market, invalid durations, and invalid environment values.
- History count: omission uses 1000; YAML and MDS_KLINES_MAX_HISTORY_CANDLES override it in order. Reject null, empty, zero, negative, fractional, boolean, malformed, and overflowing values, as well as the obsolete storage.retention.klines setting. Test request timeout separately from per-attempt HTTP timeouts.
- Kline limits: each pair's default, lower overrides, 1, documented maximum, zero, negative, fractional, and excessive values.
- Only [24h] is valid; missing instrument interval uses 10m; independent Binance stats uses 30s by default. Reject invalid windows and nonpositive intervals.
- Operation shares: 60/30/5/5 defaults, per-key YAML/environment overrides, positive integer values totaling 100, floor-derived allowances, separate Binance allocations, and the combined 65% Bybit ticker/statistics allocation. Reject impossible request costs at every allocation window, including threshold-minus-one cases. Check finite bounds, cooldown values, and invalid Sentry sample rate.
- Startup failure on invalid config occurs before workers or upstream I/O. Cancellation shuts down the process scaffold within its configured bound.

## Exit criteria

Build, vet, unit, race, and configured lint checks pass on the pinned toolchain. The sample config is loadable and validated. The process scaffold is runnable, but no market-data feature is declared implemented yet.
