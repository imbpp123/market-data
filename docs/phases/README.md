# v1 development plan

Status: delivery plan, September 12, 2026. Phases 01–07 are complete; phases 08–12 are not started. See the [decision register](../specification-decisions-v1.md) for decisions, evidence, and future verification gates.

Source of requirements: [Technical specification v1](../technical-specification-v1.md). Engineering rules: [AGENTS.md](../../AGENTS.md). The specification remains authoritative; these files define delivery order, not new approved product requirements.

## Current project state

The repository contains specifications, a phase plan, configuration/HTTP examples, phase 01 discovery fixtures, and the phase 02 Go process scaffold. The pinned module, configuration loader, health HTTP lifecycle, unit tests, Makefile, and CI baseline are implemented. Phase 03 adds exact domain models, tested UTC calendars, funding read models, and consumer-owned application contracts. Phase 04 adds independent atomic snapshots, candle storage with finalization and retention, and storage initialization before HTTP startup. Phase 05 adds shared upstream admission, scoped cooldowns, retry and cycle backoff, exact raw response capture, and pinned SDK clients with local HTTP integration tests. Phase 06 adds instrument normalization for both exchanges, independent bounded refresh workers, publication counters, and the instruments read API. Phase 07 adds continuous ticker collection, independent Binance FULL statistics collection, shared Bybit response normalization with independent publication, and cache-only ticker/statistics APIs. Candle loading, the kline API, observability exporters, and deployment packaging are not implemented. Section 14 reports earlier SDK checks; phase 05 recreates HTTP-boundary checks, while phase 06 verifies instrument normalization and the remaining field tests stay assigned to their feature phases.

The initial review assessed the supplied project documentation. Phase 01 source checks are recorded in the decision register. Phase 01 includes bounded live discovery and an official release-catalog check. It does not reproduce historical SDK tests or verify deployment behavior.

## Technical assessment

Phase 01 provides an implementable [contract](../implementation-contract-v1.md) and complete configuration defaults. Its strongest parts are exact decimal handling, field mappings and null semantics, independent snapshot publication, candle finalization, and explicit concurrency failure cases. Consumer-owned interfaces and memory-only storage keep v1 reasonably contained.

The main complexity is in request admission and candle cache fills. A single requests-per-second limiter cannot satisfy the specified weighted sliding windows, operation shares, cooldowns, queues, and attempt limits. Likewise, singleflight alone cannot implement overlapping-range reuse, independent caller cancellation, or open-candle finalization. These need isolated logic and deterministic tests before full service integration.

The following decisions are recorded; implementation checks remain assigned to their phases.

| Item | Assessment and required action | Delivery gate |
| --- | --- | --- |
| Budgets and configuration | 20% margin, common ceilings, reserved shares, pacing, finite queues/deadlines, and in-memory restart behavior are defined in the contract. Test expensive-call feasibility and admission. | 02 configuration and 05 admission |
| Deployment scope | One instance is confirmed. The working interpretation is that no other exchange clients share its outgoing IP; verify that condition at deployment. A local limiter cannot account for other processes. See D01. | 05 and release |
| Binance metadata | Explicit fundingInfo interval or null on absence; failed sources retain the prior snapshot. Binance delisting_time stays null. Replay captured and synthetic cases. | 06 |
| Candle alignment | Domain tests replay expected boundaries from all twelve captured examples for weekly and Binance 3d anchors. Raw adapter normalization remains to be verified. | 08–09 |
| Binance Spot statistics | The official REST API source resolves the documentation conflict in favor of FULL bulk access (decision D11). Phase 07 adapter and HTTP contract tests pass; deployment access verification remains required. | release |
| Public API gaps | Contract and examples define readiness for all snapshots, strict filters, aligned half-open ranges, missing slots, and errors. Implement and test them. | 06–07 and 10 |
| Startup | Bind HTTP after local initialization and schedule independent workers immediately. Exchange availability does not determine global readiness. | 02 lifecycle and 06 |
| Retention and memory | D13 defines a configurable 1,000-slot window for every supported interval. Calendar evidence is captured; measure total memory within 1 GB before release. | 03–04, 08, 10–12 |
| Reproducible build | Phase 02 build, unit, race, vet, and formatting checks pass on Go 1.27.1. Recreate pinned SDK tests in adapter phases. | 02 and 12 |

The detailed rules in sections 8 and 31 qualify the simpler “closed candles are immutable” wording in section 26: data fetched before close still needs a request started after close. Sections 5–8 and 39 consistently require decimal JSON strings.

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

Calendar estimates are intentionally absent: staffing and deployment constraints are not fixed. The highest implementation uncertainty is in phases 05 and 08–10. Estimate implementation dates after phase 01, using the acceptance cases below as the work breakdown.
