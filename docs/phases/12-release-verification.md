# 12 — Release verification and packaging

Status: complete, September 13, 2026. The user confirmed one running instance per outgoing IP and deployment-host access; current-host live compatibility checks also pass. See the [41-item audit and capacity report](../release-verification-v1.md). Dependencies: [01](01-specification-decisions.md) through [11](11-retention-and-operations.md).

## Outcome

A reproducible, operable v1 release that meets the specification's Definition of Done. Source: [specification](../technical-specification-v1.md), sections 57–65.

## Work

- Audit all 41 acceptance items in section 65. Record the implementing feature, test/check evidence, and any unresolved limitation for each item. A documented limitation does not automatically waive a required item.
- Run full integration paths for all four data types and both exchanges using local HTTP servers, real repositories, and the actual transport serialization. Complete missing boundary coverage rather than repeating only happy paths.
- Add a multi-stage Dockerfile with the same pinned Go version as build/CI, a minimal non-root runtime, required runtime certificates, mounted config, healthcheck, and correct SIGTERM handling.
- Add single-service compose.yaml using memory storage and a read-only config mount. Do not add Redis/PostgreSQL containers or Testcontainers.
- Complete make docker-build, docker-up, and docker-down alongside the build/run/test/test-race/lint targets. Ensure CI and local commands use consistent dependencies and toolchain versions.
- Replace the title-only project README with setup, config/ENV precedence, enabled markets, API examples, checks, Docker/Compose usage, graceful shutdown, and diagnosis of stale or unready data.
- Document restart cache and limiter-state loss, the absence of persistence and automatic restart waits, IP/topology assumptions, budget/cooldown behavior, retention policy, clock synchronization needs, and the post-close stability assumption. Keep unsupported v1 features explicit.
- Measure representative concurrent reads, cache fills, many-series retention load, and shutdown against the workload and finite bounds agreed in phase 01. Record memory/latency/attempt observations; do not invent an SLA or an unrequested eviction design.

## Required checks

- gofmt on changed Go files; go build ./...; go vet ./...; go test ./...; go test -race ./...; configured lint. Use the equivalent Makefile targets when available.
- Unit and integration tests do not require production exchange access, credentials, or real-time sleeps for correctness. Any separate live compatibility evidence is explicitly identified and does not replace local tests.
- 50 identical candle misses, overlapping ranges, 50 snapshot readers during replacement, shared Bybit branch failures, Binance independent worker failures, saturation, cooldown, and cancellation all pass.
- Container builds and starts with mounted config as non-root; health/readiness behave correctly, config is read-only, and SIGTERM exits within the documented bound. Test process/container lifecycle with controlled local upstreams where needed.
- Review the diff, dependency pins, documentation links, config examples, and secrets. Record unavailable checks with their exact reason; do not report them as passed.

## Exit criteria

Every required item in section 65 has passing evidence, the image and Compose setup are verified, and all release-blocking decisions are closed. There are no hidden public-network test dependencies or unapproved scope additions. Packaging a runnable service does not itself authorize deployment to an external environment.

## Delivered evidence

- Digest-pinned multi-stage Dockerfile, static non-root runtime with certificates and local health probe, read-only single-service Compose setup, Docker Make targets, and CI lifecycle verification.
- Expanded current-data integration tests for failures in either branch on both exchanges/markets; health probe tests; compact candle storage with full-field ownership regression tests.
- Four-client capacity measurement at 600,000 retained candles, broad-interval capacity failure, automatic Linux peak-RSS target check, and isolated container SIGTERM verification.
- Full operating README and a [section 65 acceptance matrix](../release-verification-v1.md), with [raw run evidence](../evidence/phase-12/README.md).

D01 is recorded as a user-confirmed deployment invariant. This computer passes the separate live exchange checks, including Binance Spot FULL. The user also confirmed access from their deployment hosts; unspecified future hosts were not tested here. Broad interval demand exceeds the confirmed 1 GB workload envelope. This capacity limitation does not introduce a new eviction policy or expand the approved workload. No external deployment was performed.
