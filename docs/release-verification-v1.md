# v1 release verification

Verification date: September 12–13, 2026. Phase: [12](phases/12-release-verification.md). Requirements: [specification sections 57–65](technical-specification-v1.md#65-definition-of-done).

## Release disposition

**Phase 12 is complete for the agreed v1 workload.** All 41 section 65 items have passing implementation/check evidence under the user-confirmed deployment invariant. On September 13, 2026, the user clarified that the host can be this computer, an internet server, or another host, always with one running instance on one outgoing IP, and confirmed exchange access from deployment hosts. Separate bounded live checks from this computer return HTTP 200 for all four current-data scopes, including Binance Spot bulk `type=FULL`.

D01 is an operating condition, not a claim of dedicated-IP ownership or an audit of unrelated processes. The local limiter assumes no unaccounted exchange traffic on that IP. Future unspecified hosts were not remotely tested here and must preserve the same topology and access assumptions. No external deployment or registry publication was performed.

The agreed conservative workload fits the engineering memory target after removing repeated series identity and open time from each stored candle. API models, decimal ownership, finalization, retention, and repository interfaces are unchanged. Broad demand across every interval does not fit 1 GB; see capacity limits below.

## Executed checks

- `make check`: formatting, executable build, complete config example, golangci-lint **v2.13.2** standard set, unit/integration tests, and race tests pass on Go **1.27.1**, Darwin/arm64.
- `go build ./...`, `make vet`, and `go mod verify`: pass. No dependency versions changed. Inner-layer import/source review found no exchange SDK or floating market values in domain/application code.
- `make docker-build`: passes on Linux/arm64 using Docker Engine **29.7.2**, Compose **v5.5.1**. Builder tag and digest are pinned; `go mod verify`, static compilation, and `-trimpath` run inside the build. Runtime is `scratch`, UID/GID 65532, with CA certificates and an exec-form entry point.
- `make docker-verify`: passes. An isolated temporary Compose project has an internal network and no published ports. An in-container Go probe confirms HTTP health/readiness, unready data without exchange access, actual rejection of config writes, and readable runtime certificates. Docker inspection checks non-root user, read-only root/config, the 1,000,000,000-byte limit, and disabled automatic restarts. SIGTERM exits normally within the 35-second service bound; Compose permits 40 seconds. The test removes its temporary project after completion.
- Integration execution uses local `httptest.Server`, in-memory HTTP connections, or boundary fakes. No production exchange or telemetry credentials are used. Container and load checks have no external network route. Toolchain/module/image downloads are build dependencies, not hidden test network access.
- The initial sandboxed lint run could not access the Go build cache. The same command passed with access to the existing cache and local test ports. This was not a module error; no `go mod tidy` workaround was applied.

Separate [live compatibility evidence](evidence/phase-12/live-compatibility.json) records four public GETs, HTTP status, row counts, required-field checks, and body checksums without storing raw market catalogs. These live checks do not replace local tests.

Raw run evidence is in [phase 12 evidence](evidence/phase-12/README.md). CI now executes the same image and Compose checks after `make check`. Remote CI has not been run from this task, and other image architectures have not been executed locally.

## Section 65 acceptance audit

“Pass” refers to implementation and executed evidence, with the stated user-confirmed operating assumptions. Every row maps one original acceptance item; no required item is silently waived.

| # | Implemented feature | Test/check evidence | Result / limitation |
| --- | --- | --- | --- |
| 1 | Consumer-owned instrument, ticker, statistics, and candle contracts for four scopes | `TestInstrumentsEndToEnd`, `TestCurrentAdaptersPublishSeparateCacheOnlyAPIs`, `TestKlineColdWarmPartialHTTPThroughExchangeAdapters` | Pass |
| 2 | Exchange SDKs are confined to infrastructure adapters | Inner-layer import review; SDK HTTP tests in `internal/infrastructure/exchange` | Pass |
| 3 | Domain prices, quantities, volumes, rates, and turnover use decimal.Decimal | Domain models; adapter precision tests and HTTP decimal assertions | Pass |
| 4 | Raw numeric responses are decoded exactly before entering domain | `klines_precision_test.go`, normalizer tests; no float32/float64 market fields in domain/application | Pass |
| 5 | Independent instrument refresh workers | `TestInstrumentWorkersKeepExchangeFailuresIndependent`; application instrument tests | Pass |
| 6 | Continuous ticker cycles share admission and persistent backoff | `TestTickerWorkerKeepsBackoffAcrossFailedCycles`, `TestWorkerCyclesCannotBypassBackoff` | Pass |
| 7 | Ticker reads use only its repository | `TestCurrentAdaptersPublishSeparateCacheOnlyAPIs`; HTTP read concurrency tests | Pass; stale successful snapshots have no expiry |
| 8 | Candle cache-aside with saved successful pages | `TestServiceColdWarmAndPartialCache`, bootstrap candle HTTP integration | Pass |
| 9 | Pure minimum-request planner | `fetch_planner_test.go`, including exhaustive small-window coverage/optimality | Pass |
| 10 | Gap merging inside provider page bounds | Planner gap/limit tables and partial-cache HTTP integration | Pass |
| 11 | Confirmed closed candles need no new upstream work | `TestServiceFinalHitAndOtherSeriesDoNotWait`; warm HTTP cache path; load pass has zero attempts | Pass; assumes post-close stability |
| 12 | Per-request open refresh and post-close confirmation | `TestServiceOpenCandleSharedOnceAndNextCallerRefreshes`, `TestServiceCrossingCloseRequiresNewAttempt` | Pass |
| 13 | Same-series shared fills and overlapping-range reuse | `TestServiceFiftyIdenticalMissesShareOnePagedFill`, `TestServiceOverlapReusesSavedRange` | Pass |
| 14 | Every page/retry consumes common and operation budgets | `TestRetriesChargeEveryAttemptAndPreserveData`, `TestPagesShareAttemptBound`, `TestKlineRetryAttemptsAreCountedAcrossPages`, `klines_accounting_test.go` | Pass under D01 topology assumption |
| 15 | Storage behind application interfaces | Consumer-owned Repository contracts; bootstrap construction; import review | Pass |
| 16 | Atomic snapshots and race-safe candle storage | Memory repository tests, round-trip/copy regression test, `go test -race ./...` | Pass |
| 17 | Immediate/periodic cleanup plus merge-time pruning | `TestRetentionAndLoggingWorkersPreserveSnapshotsAndStop`, retention unit tests, load cleanup | Pass |
| 18 | Strict YAML loading | `internal/config/load_test.go`; `make check-config` | Pass |
| 19 | Explicit environment overrides YAML | Loader tests; `TestRunPassesFinalSettingsAndPreservesStartupError` | Pass |
| 20 | Private optional Sentry client | `internal/infrastructure/observability/sentry_test.go`; lifecycle flush and panic tests | Pass; no production telemetry sent |
| 21 | Optional Prometheus exporter | `TestOperationRoutesAreIndependentAndDisabledByDefault`, statistics exporter tests | Pass |
| 22 | Built-in statistics independent of exporters | `TestKlineCountersNeedStandaloneStatsOrAnExporter`; observability tests | Pass |
| 23 | JSON structured operation logs | Bootstrap operational tests and observability sanitization tests; container process logs | Pass |
| 24 | Local health/readiness endpoints | HTTP handler tests, process lifecycle tests, `TestReleaseContainerProbe` | Pass; not an exchange readiness guarantee |
| 25 | Owned work cancels and shutdown waits are bounded | `TestServeExposesKlinesAndCancelsOwnedFill`, `TestExchangeCooldownDoesNotBlockReadinessOrShutdown`, `TestServeFlushUsesRemainingShutdownTimeAfterWorkersStop`, Compose SIGTERM | Pass |
| 26 | Behavioral unit tests | `make test`; focused regression tests for compact storage and health probe | Pass |
| 27 | Real local integration paths without Testcontainers | Instruments/current/candles HTTP integration for all four exchange/market pairs | Pass |
| 28 | Test execution does not need production exchanges | Local HTTP fixtures; isolated container/load networks; test source review | Pass |
| 29 | Race verification | `make test-race`, including 50-reader and shared-fill tests | Pass |
| 30 | Multi-stage non-root Docker image | `Dockerfile`; build and container lifecycle checks | Pass on Linux/arm64 |
| 31 | Single-service Compose with read-only config | `compose.yaml`; `make docker-verify` | Pass |
| 32 | Build/run/test/race/lint and Docker commands | `Makefile`; executed check/build/verification targets; Compose up/down lifecycle | Pass |
| 33 | Finite callers/fills/queues/deadlines/ranges/attempts | Config tests, upstream admission tests, candle service saturation and timeout tests | Pass; series cardinality is not globally capped |
| 34 | Reserved operation shares and lanes | `TestReservedSharesAndSlidingBoundary`, `TestConcurrentRequestsRespectEveryShare`, `TestQueueOverflowCancellationAndNoSlotWhileWaiting` | Pass |
| 35 | Explicit overload and oversized-range failures | `TestServiceRejectsOverloadAndReleasesCapacity`, `TestKlineHTTPValidationBeforeCandleAccess`, shared snapshot caller-limit tests | Pass; no successful partial ranges |
| 36 | Pinned budget schema, captured limits, safety margin and topology assumption | Config validation, admission/cost/cooldown tests; [D01](specification-decisions-v1.md) | Pass: user-confirmed deployment invariant and current-host live checks; no unrelated-client traffic audit claimed |
| 37 | Separate MarketStats model and endpoint | `TestCurrentHTTPUsesOneClockAndExactSeparateDTOs`, current integration | Pass; ticker JSON excludes statistics fields |
| 38 | Only 24h HTTP window, independent repository window key | `TestCurrentHTTPFiltersAndReadiness`, memory window isolation tests | Pass |
| 39 | Bybit shared fetch with independent branches; Binance separate collectors | `TestIndependentPublication`, `TestCurrentWorkersAreCapabilityDrivenAndIndependent`, expanded current HTTP integration | Pass; malformed ticker or statistics branch preserves only that branch |
| 40 | Cache-only statistics with unready/empty distinction and retained fetched_at | Current HTTP/filter tests and failed-refresh HTTP integration | Pass |
| 41 | Field mappings, independent publication and public serialization | Exchange `current_test.go`, normalizer tests, ticker read-model tests, current DTO and integration tests | Pass; repository publication failures covered by stateful unit fakes |

Relevant sources: [bootstrap tests](../internal/bootstrap), [HTTP tests](../internal/transport/http), [application tests](../internal/application), [exchange tests](../internal/infrastructure/exchange), [memory tests](../internal/infrastructure/storage/memory), and [observability tests](../internal/infrastructure/observability).

## Capacity method and observations

`TestReleaseLoad` is opt-in: normal `make check` skips the resource-intensive measurement. `make release-load` runs it locally. For Linux RSS and the explicit 800 MB gate, compile and run it in the bounded container as recorded in the evidence instructions.

The main profile has 50 symbols per each of four scopes and three intervals (1m/5m/1h): **600 series and 600,000 retained closed candles**. It preloads 999 slots per series, then four clients each request every full 1000-slot range, filling the last slot. A second four-client pass repeats every range from warm cache. Both passes also serialize full instrument/ticker/statistics snapshots. Each snapshot type holds 5000 synthetic symbols per scope (20,000 total); this is a conservative sizing assumption, not a measured live catalog or a symbol allowlist. All optional snapshot fields are populated. Candle decimal strings contain 25 characters (`12345.1234567890123456789`), with counts above 2^53. Each stored decimal is owned; fixture sharing does not reduce retained decimal allocations.

The fixed clock is September 12, 2100, permitting 1000 synthetic monthly candles without pre-1970 rows in the broad profile. Data is synthetic, not a market-history claim. Clocks and series counts are deterministic; measured elapsed times are observations, not correctness deadlines or an invented SLA. The provider is an application-boundary synthetic page provider, so measured attempts are page dispatches, **not actual exchange HTTP throughput**. Real SDK serialization, costs, retries, cooldowns, saturation, 50 identical misses, and shutdown with active work are covered separately by local integration and controlled-time tests.

Linux `/proc/self/status` supplies process peak RSS (`VmHWM`, KiB × 1024). Go `HeapAlloc` after forced GC measures retained heap; `Sys` includes virtual/runtime reservations and is not process RSS. The measured process is the standalone compiled test executable containing actual repository, planner, fill service, and HTTP handlers, not the compiler or Docker daemon. Container memory is 1,000,000,000 bytes, swap disabled for final measurements, and `GOMEMLIMIT=700MiB`. The full service image has separate process/Compose lifecycle evidence.

Before compact storage, the main run peaked at **854,691,840 bytes**, above the 800,000,000-byte engineering target. Lowering the Go memory target or GOGC alone did not fix it. Removing repeated key fields reduced retained heap by about 67 MB and peak RSS below the target without changing retention depth, symbol scope, or adding eviction.

Final measurements:

| Observation | Result |
| --- | --- |
| Retained heap after reads/fills | 493,075,440 bytes |
| Peak process RSS | 773,951,488 bytes; passes ≤800,000,000 |
| Partial-fill reads | 2400 requests, 600 provider pages; p50 19.42 ms, p95 39.07 ms |
| Warm reads | 2400 requests, zero provider pages; p50 9.04 ms, p95 17.70 ms |
| Rolling retention / full idle cleanup plus rejected late write | 8.11 ms / 50.82 ms |
| Fill owner shutdown / full Compose SIGTERM stop | 3.125 μs / 0.230 s |
| Final broad capacity run | Exit 137, OOMKilled=true during population |

Raw output is recorded in [the evidence index](evidence/phase-12/README.md). The target leaves 200 MB below the 1 GB limit for the agreed workload; it is not a guarantee for all legal request combinations, larger decimal values, catalog growth, or arbitrary snapshot concurrency. Defaults allow more than four clients. No global series-cap/eviction design was added.

The broad profile requests 50 symbols in every supported interval: 16 Binance Spot, 15 Binance linear, and 13 per Bybit market, totaling **2850 series / 2,850,000 potential records**. The bounded run is killed by the container memory limit while populating, before read/fill latency can be measured. This profile is outside confirmed client demand. Its failure establishes a capacity limitation; neither a per-series history bound nor GOMEMLIMIT can make unlimited series fit. Re-measure before increasing workload or changing deployment memory. A larger workload requires an explicit scope/capacity decision.

## Operating assumptions and limits

1. Preserve D01 on each deployment host: one service instance on one outgoing IP, with no unaccounted exchange traffic. The user confirms deployment-host access and current-host live checks pass. Recheck access when changing hosts; unspecified future hosts were not accessed remotely in this task.
2. Keep the host UTC clock synchronized and accept the specified post-close stability assumption. Cache and limiter state are lost on restart; no persistent recovery or automatic restart wait exists.
3. Respect the measured workload and 1 GB memory envelope. Broader interval demand failed capacity verification. No global count limit protects against arbitrary series requests in v1.

For startup, configuration precedence, diagnosis, shutdown, and unsupported features, use the [operating guide](../README.md).
