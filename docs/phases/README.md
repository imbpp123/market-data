# v1 development plan

Status: proposed plan, September 11, 2026. All implementation phases are not started.

Source of requirements: [Technical specification v1](../technical-specification-v1.md). Engineering rules: [AGENTS.md](../../AGENTS.md). The specification remains authoritative; these files define delivery order, not new approved product requirements.

## Current project state

The repository contains AGENTS.md, a title-only README.md, and the specification. There is no Go module, application code, test suite, Makefile, or deployment configuration. Section 14 reports earlier SDK checks, but their executable tests and fixtures are not present here. Those results are useful evidence, not an implemented exchange adapter or a reproducible project test suite.

This review assesses the supplied project documentation. It does not independently verify live exchange behavior, external release versions, or the historical SDK test results.

## Technical assessment

The specification is strong enough to plan implementation. It is not yet a complete implementation contract. Its strongest parts are exact decimal handling, field mappings and null semantics, independent snapshot publication, candle finalization, and explicit concurrency failure cases. Consumer-owned interfaces and memory-only storage keep v1 reasonably contained.

The main complexity is in request admission and candle cache fills. A single requests-per-second limiter cannot satisfy the specified weighted sliding windows, operation shares, cooldowns, queues, and attempt limits. Likewise, singleflight alone cannot implement overlapping-range reuse, independent caller cancellation, or open-candle finalization. These need isolated logic and deterministic tests before full service integration.

The following items need explicit decisions or clarification. They are not silently resolved by this plan.

| Item | Assessment and required action | Delivery gate |
| --- | --- | --- |
| Budgets and configuration | Sections 32–33 and 42–44 lack numeric budgets, queue/concurrency bounds, cooldown fallbacks, bootstrap limits, and restart policy. Define them for the deployment topology. The header calls no-borrowing budgets a proposal while section 32 specifies them as v1 behavior; reconcile that status. | 02 configuration and 05 admission |
| Deployment scope | One instance with a dedicated outgoing IP is only a working assumption. A local limiter cannot account for other processes sharing that IP. Confirm the assumption; do not add distributed coordination by default. | 05 and release |
| Binance metadata | Funding interval fallback is proposed; the delisting source is unconfirmed. Keep the specified null behavior until evidence supports a change. | 06 |
| Candle alignment | Weekly and multi-day anchor fixtures remain open. Duration alone cannot define valid slots. | 03 alignment completion, 08–09 |
| Binance Spot statistics | Section 14 records a documentation conflict about bulk 24hr requests. SDK tests do not prove production behavior. Resolve bulk access or a complete bounded batching path. | 07 |
| Public API gaps | Instruments, tickers, and klines lack some response, filter, and error details. Kline timestamp input format, unaligned/future ranges, missing historical slots, and errors for incomplete data need a written contract. MarketStats already has explicit readiness semantics; do not copy them to other APIs without a decision. | 06–07 and 10 |
| Startup | Section 54 lists initial instruments before workers and HTTP, while section 6 says missing instrument data must not block ticker. Define initial-load failure and waiting behavior without making exchange availability a global readiness condition. | 02 lifecycle and 06 |
| Retention and memory | Retention does not by itself bound memory across all requested series. Define the deletion boundary and treatment of requests older than retention; measure memory for the expected workload before release. New eviction policies need a specification change. | 04, 10–12 |
| Reproducible build | The specification pins Go 1.27.1; prior SDK checks used Go 1.26.0. Verify toolchain availability and rebuild the relevant tests with pinned dependencies. Do not silently switch versions. | 02 and 12 |

The detailed rules in sections 8 and 31 qualify the simpler “closed candles are immutable” wording in section 26: data fetched before close still needs a request started after close. Sections 5–8 require decimal JSON strings even though section 39 uses softer recommendation wording. Keep these rules consistent when updating the specification.

## Phase order

Numbers define the recommended delivery order. Dependencies describe what must be available before a phase can finish. An unresolved decision blocks only its dependent work; domain rules, fixtures, and other independent work can continue.

| Phase | Deliverable | Dependencies |
| --- | --- | --- |
| [01 — Close specification decisions](01-specification-decisions.md) | Decision register and implementation contract | None |
| [02 — Bootstrap and configuration](02-bootstrap-and-configuration.md) | Buildable process, validated config, CI baseline | Relevant 01 decisions |
| [03 — Domain and application contracts](03-domain-and-contracts.md) | Exact models, timeframes, consumer-owned boundaries | 02; 01 calendar decisions |
| [04 — In-memory repositories](04-memory-repositories.md) | Atomic snapshots and safe candle storage | 03; 01 retention boundary |
| [05 — Upstream admission and retries](05-upstream-admission-and-retries.md) | Bounded, accounted HTTP attempts | 02–03; 01 budget decisions |
| [06 — Instruments end to end](06-instruments.md) | Both exchange adapters, refresh workers, read API | 04–05; 01 metadata/API decisions |
| [07 — Tickers and market statistics](07-tickers-and-market-statistics.md) | Independent snapshots and read APIs | 06; 01 statistics/API decisions |
| [08 — Candle planning](08-kline-planning.md) | Pure gap detection and minimal fetch plans | 03–04; 01 range/alignment decisions |
| [09 — Candle exchange adapters](09-kline-adapters.md) | Exact normalized candle pages for both exchanges | 05–06, 08 |
| [10 — Candle cache fills and API](10-kline-cache-and-api.md) | Bounded concurrent cache-aside API | 04–05, 08–09 |
| [11 — Retention and operations](11-retention-and-operations.md) | Cleanup, full observability, lifecycle verification | 06–07, 10 |
| [12 — Release verification and packaging](12-release-verification.md) | Verified v1 image, Compose setup, operating guide | 01–11 |

Phase 06 is the first market-data path from an exchange fixture through storage to HTTP. Phase 07 adds continuous current data. Phase 10 completes the four data APIs. Only phase 12 is the v1 release gate.

## Verification throughout development

Each phase includes tests for its own non-trivial behavior. Phase 12 consolidates evidence; it is not the first testing phase. Unit tests use controlled clocks and stateful boundary fakes. Integration tests use httptest.Server and real memory repositories, without production credentials, production exchange access, or Testcontainers.

For Go changes, run gofmt and focused tests, then the configured build, vet, unit, race, and lint checks from AGENTS.md. Use Makefile targets when present. Check documentation links and whitespace for documentation-only changes. Do not add tests for pure data containers or create empty architecture packages.

## Specification coverage

| Specification sections | Primary phases |
| --- | --- |
| 1–3: goals, technology, decimal precision | 01–03, 06–09, 12 |
| 4–9: models and timeframes | 03, 06–10 |
| 10–13: architecture and contracts | 02–03, 06–10 |
| 14–16: SDKs and exchange requirements | 01–02, 05–07, 09 |
| 17–20: storage and instruments | 04, 06 |
| 21–24: collectors and snapshots | 04, 07 |
| 25–31: candles, planner, coordination | 04, 08–10 |
| 32–33: budgets and overload | 01–02, 05, 10 |
| 34–40: HTTP, serialization, errors | 01, 06–07, 10 |
| 41–45: retention, config, retry | 01–02, 05, 11 |
| 46–55: observability and lifecycle | 02, 05–07, 10–11 |
| 56: project structure | 02–11 |
| 57–60: unit, concurrency, integration tests | Every implementation phase, final audit in 12 |
| 61–63: Docker, Compose, Makefile | 02, 12 |
| 64–65: scope and acceptance | 01, 12 |

No phase adds orders, accounts, trading strategies, MCP, WebSocket ingestion, Redis, PostgreSQL, ticker/statistics history, or statistics windows other than 24h. Future extensibility is provided by real boundaries, not unused implementations.

Calendar estimates are intentionally absent: staffing, deployment constraints, and the open decisions are not fixed. The highest uncertainty is in phases 01, 05, and 08–10. Estimate implementation dates after phase 01, using the acceptance cases below as the work breakdown.
