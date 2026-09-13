# Phase 4. Validate behavior, traffic, and capacity

Status: planned; not implemented. Depends on [phase 3](03-cutover-and-operations.md) and the completed [Binance request-limit validation](../release-verification-v1.md#binance-request-limit-validation). See the [phase list](README.md) and approved [acceptance criteria](../grpc-migration-specification.md#testing--validation).

## Summary / Overview

Verify the combined service using installed Go/Python clients, real local gRPC connections, and the agreed load. Measure traffic and resource costs against the phase 1 HTTP baseline. Record a new acceptance report without publishing a release.

## Context / Background

The executable now serves gRPC data and operational HTTP. Earlier phases already test their logic. The existing [HTTP release audit](../release-verification-v1.md) remains historical; its in-process latency numbers are not a gRPC network baseline.

## Problem Statement

Passing isolated transport tests does not prove that generated packages, exchange-budget behavior, containers, and a 600,000-candle cache work together under the memory limit. A traffic benefit must be measured on equivalent inputs.

## Goals

- Demonstrate semantic agreement across installed clients and actual handlers.
- Verify combined cache, budget, overload, and shutdown behavior.
- Measure encoded/network bytes, client/server cost, and latency with clear limits.
- Record acceptance and remaining risks without weakening fixtures or requirements.

## Proposed Solution / Design

1. Run the complete acceptance suite on the final source state. Extend missing boundary scenarios rather than copy already sufficient unit tests. Reuse local exchange fixtures and actual adapters where exchange serialization/accounting matters.
2. Install Python through the pinned local-Git/subdirectory path, outside its source tree. Build a separate Go consumer against the generated module. Call the actual service composition from both, including Python async calls, statuses/details, and native failures without details.
3. Exercise the completed Binance budget work through gRPC. Prove cache-only reads remain available under rejection/cooldown, cold requests report the approved reason, and failed fills preserve useful cached pages. Keep Bybit behavior independent. Do not mark combined acceptance complete while upstream work is still unverified.
4. Migrate the existing release-load harness to actual gRPC serialization and client calls. Keep fixture counts, precision, cache occupancy, four-client work, and memory limits. Add a separate overload/slow-client profile; do not present it as the ordinary agreed workload.
5. Run the comparison below using phase 1's fixtures and HTTP baseline source. If the environment has changed, rerun the baseline in an isolated checkout with the matching harness. Do not restore HTTP data handlers in the active service.
6. Store raw results and reproduction commands under `docs/evidence/grpc-migration/`. Add a concise `docs/grpc-migration-verification.md` covering acceptance, measured values, source/tool/environment identity, and limitations.
7. Update the main specification, implementation contract, decision register, README, and development guide to the verified gRPC contract. Label previous HTTP evidence explicitly. Record actual implemented defaults and any explicit approved changes; do not leave proposed values presented as measured facts.

### Comparison method

| Dimension | Required coverage |
| --- | --- |
| Data | 1, 100, and 1,000 candles; small and full instrument/ticker/statistics snapshots |
| Clients | Go and Python with reused connections/channels; Python async path for bot usage |
| Work | Warm reads and bounded partial-fill reads, four concurrent clients; separate overload profile |
| Wire | Real local TCP, same encryption and compression mode for both protocols; uncompressed baseline |
| Cost | Encoded request/response bytes and actual network bytes separately; client/server CPU, allocations where supported, retained heap and peak RSS |
| Timing | p50/p95/p99 latency; distinguish encoding, application/storage work, and network measurements |

Use fixed input/expected-output fixtures and verify their semantic identity before timing. Record hardware, runtime versions, warmup, sample counts, concurrency, measurement boundary, and commands. Network-byte collection must measure the connection rather than relabel message length; state whether connection setup is included. Keep client memory and server RSS separate. Optional gzip runs must be reported separately, not used to claim an uncompressed improvement.

Use the existing main capacity profile: 600 series, 600,000 retained closed candles, full 20,000-row snapshots per type with populated optionals, 25-character candle decimals, and large counters. Run inside the 1,000,000,000-byte Linux container with the existing runtime memory settings. The required peak process RSS is at most 800,000,000 bytes for that workload. Preserve the distinction between retained heap, process RSS, and container memory.

## Data Model / API / Interfaces

No feature or wire change is planned. Findings may require implementation fixes with regression tests, not silent contract changes. Install/import paths, error reasons, message caps, operational routes, and release-check commands must match the executable and generated artifacts.

The new verification report is the acceptance record for this migration. Keep the old report as earlier evidence rather than overwrite its measurements with different methodology.

## Failure Modes / Edge Cases

An OOM kill, required response above its cap, failed client install, leaked caller capacity, partial successful range, or deadline/shutdown violation fails acceptance. Do not lower symbol counts, decimal precision, or retention to pass. If measurements cannot run because Docker or instrumentation is unavailable, report the exact missing evidence and leave acceptance incomplete.

Latency/CPU differences are observations, not deterministic test assertions. Investigate and report regressions. The approved design does not promise a fixed speedup; it does require smaller uncompressed encoded candle responses for the 100- and 1,000-row fixtures.

## Testing / Validation

| Case | Expected result |
| --- | --- |
| Installed Go/Python consumers call all four methods | Equivalent values, ordering, presence, and errors from the final service. |
| Repository Git installation from an exact local fixture commit | Package installs without source-path leakage or server/generator build dependencies. |
| Shared cold miss, partial cache, warm cache | Complete results with bounded shared fills; warm reads generate no exchange attempts. |
| Binance threshold/share rejection with warm snapshots/candles and cold candles | Cached reads work; required rejected exchange work returns `RESOURCE_EXHAUSTED / service_overloaded`, without fake attempt counts. |
| Exchange cooldown, budget recovery, and Bybit requests | Existing reasons/recovery semantics preserved; unrelated exchange behavior remains independent. |
| Application error and native timeout/disconnect without details | Both clients handle status safely; no assumption that ErrorDetail always exists. |
| Repeated overload, cancellation, large responses, and slow-reader completion | Capacity returns to its expected state; no unbounded response queue or hanging owner. |
| Saturated gRPC data traffic with health/ready/metrics/debug requests | Operational routes keep their approved semantics and do not depend on data admission. |
| SIGTERM with active requests, fills, and pending sends | One bounded shutdown; no surviving workers/listeners; readiness clears. |
| Full agreed capacity profile | Complete responses and at most 800,000,000-byte peak RSS inside the 1 GB container. |
| 100- and 1,000-candle equivalent uncompressed messages | Protobuf encoded responses are smaller than JSON; exact row values remain equivalent. |
| Snapshot and candle TCP comparisons | Report bytes, CPU, memory, and latency with reproducible methodology, including regressions. |
| Root checks pass but nested client/package check fails | Overall acceptance fails; neither CI path can publish based only on root checks. |
| Active documentation and descriptors versus running container | Addresses, config, methods, error reasons, and installation instructions agree. |

Run `make check`, `make vet`, `make check-api`, `make docker-build`, `make docker-verify`, and the migrated `make release-load` plus the bounded Linux capacity/comparison runs. Record exact checks and source identity in the report. Corrections need focused regression tests and the affected broader checks again; unchanged successful checks do not need pointless repetition.

Completion requires all acceptance items passing, raw measurement evidence, updated contracts/docs, and explicit limits. Mark the phases and main specification complete only then. Release publishing and deployment remain separate actions.

## Risks / Trade-offs

Smaller wire messages do not reduce the domain cache by the same amount. Results on four synthetic clients do not guarantee that all allowed concurrent requests or arbitrary catalog growth fit in memory. Keep these limits visible in the final report, including any difference between local plaintext testing and a future secured remote deployment.
