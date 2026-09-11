# 02 — Bootstrap and configuration

Status: complete, September 12, 2026. Dependencies: relevant decisions in [01](01-specification-decisions.md). Next: [03](03-domain-and-contracts.md).

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

## Delivery evidence

- Added the Go 1.27.1 module, executable entry point, typed configuration, Koanf source merging, strict YAML/ENV input checks, and semantic validation for the complete agreed schema.
- Added JSON logging, health/readiness routes, root cancellation, owned workers, and bounded HTTP shutdown. Repositories, market-data routes, exchange workers, and observability integrations remain assigned to later phases.
- Added Makefile checks and a GitHub Actions workflow. Lint uses pinned golangci-lint v2.13.2 with its default `standard` set; `gofmt` runs separately. Tests use `testify/assert` and `testify/require`.
- Verified on `go version go1.27.1 darwin/arm64`: `make check` passed (formatting, build, example validation, standard lint including govet, unit tests, race tests). CI is configured with the same toolchain; a remote CI run has not been observed.
- The complete YAML example passed the binary's `-check-config` command and matches built-in defaults in tests.
- A local binary smoke check returned 200 from `/health` and `/ready`; SIGTERM produced a clean exit with status 0. Lifecycle unit tests cover cancellation, worker failure, listener failure, and the configured shutdown deadline using controlled time.

The configuration loader uses a library for merging and decoding. Project code checks strict input rules and service-specific bounds. It does not implement exchange admission or claim production throughput. Provider capability checks will be connected at composition when providers exist.

### Changed files

| Area | Files |
| --- | --- |
| Configuration | `internal/config/config.go`, `internal/config/load.go`, `internal/config/validate.go`, `internal/config/load_test.go`, `internal/config/validate_test.go` |
| Process and HTTP | `cmd/market-data-service/main.go`, `cmd/market-data-service/main_test.go`, `internal/bootstrap/server.go`, `internal/bootstrap/server_test.go`, `internal/transport/http/handler.go`, `internal/transport/http/handler_test.go` |
| Build and checks | `go.mod`, `go.sum`, `Makefile`, `.golangci.yml`, `.github/workflows/checks.yml`, `.gitignore` |
| Rules and documentation | `AGENTS.md`, `README.md`, `docs/examples/config-v1.yaml`, `docs/phases/README.md`, `docs/phases/02-bootstrap-and-configuration.md` |
