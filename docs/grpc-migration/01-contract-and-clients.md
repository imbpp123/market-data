# Phase 1. Define the contract and generate clients

Status: complete after independent review on September 13, 2026. One P2 finding was corrected by the implementation agent and verified by the reviewer. See the [phase list](README.md) and the approved [client and schema design](../grpc-migration-specification.md#client-generation).

## Summary / Overview

Create one Protobuf contract, generated Go/Python clients, and an installable Python package in this repository. Capture the HTTP comparison baseline before its handlers are removed.

## Context / Background

The current HTTP handlers still define the implemented wire behavior. Application/domain values and [HTTP examples](../examples/http-contract-v1.json) provide migration fixtures. The approved specification defines the replacement; examples do not override it.

## Problem Statement

There is no schema or client package yet. Without reproducible generation and installation checks, clients can drift from the service. Removing HTTP before collecting comparable evidence would also lose the baseline.

## Goals

- Compile the same four RPC definitions for Go and Python.
- Install Python from a pinned Git revision and the `api/python` subdirectory.
- Establish field presence, exact value encoding, stable field numbers, and compatibility checks.
- Verify normal full-profile message sizes and retain a reproducible HTTP baseline.

## Proposed Solution / Design

1. Add `api/proto/marketdata/v1/market_data.proto` with all requests, responses, `ErrorDetail`, and four unary methods. Assign field numbers and compare every field against the [approved model](../grpc-migration-specification.md#data-model--api--interfaces). Keep series identifiers once per candle response, and preserve optional values and timestamps.
2. Pin compatible compiler, Go/Python generators, runtime, and build-tool versions. Record the supported Python range and check its boundary versions. Keep the existing Go service toolchain. Add only the dependencies required for generation, packaging, and compatibility checks.
3. Generate the dedicated Go module at `api/go`, Python package at `api/python`, and descriptor set. Use the module/import names in the specification. Include Python `.pyi` files, package initializers, and `py.typed` in the wheel.
4. Add `pyproject.toml`, package README, and version metadata. Separate runtime dependencies from generator/build dependencies. Installation must use committed generated files; it must not build the Go service or invoke a generator.
5. Add minimal Go, Python synchronous, and Python async examples with a reused channel, explicit deadline and receive cap, and safe status/detail decoding. Exercise these against a small local contract-fixture Go server in this phase. This fixture proves client interoperability, not market-data application behavior; phase 2 adds the real handlers.
6. Add `make generate-api` and the phase 1 parts of `make check-api`: schema compilation, reproducible output, compatibility checking, nested Go module checks, wheel checks, and Git installation. Use a checked-in baseline descriptor distinct from the newly generated descriptor. A comparison against itself is not a compatibility check. Prove the check with compatible and incompatible schema fixtures.
7. Capture fixed HTTP response and local TCP baseline measurements for the [final matrix](04-validation-and-measurements.md#comparison-method). Record source and harness identity so the old implementation can be rebuilt in an isolated checkout after cutover. Use synthetic market data and existing HTTP handlers; do not keep a second production API for benchmarking.

Expected file areas are `api/`, client examples, Makefile/tool scripts, test fixtures, and `docs/evidence/grpc-migration/`. Any root module adjustment is limited to consuming the generated local Go module. Production startup and configuration remain HTTP in this phase.

## Data Model / API / Interfaces

The new public contract is `marketdata.v1.MarketDataService`; exact fields and error mapping remain in the main specification. Keep semantic validation in the later transport implementation, not custom generated setters. The contract fixture may return fixed responses to verify serialization.

Python consumers use the [pinned Git dependency](../grpc-migration-specification.md#python-installation-from-this-repository). Test it through a temporary local Git repository containing package files at the same path and an explicit fixture commit. Do not create commits or tags in the working project merely to run this test. Install outside the source tree with no `PYTHONPATH` shortcut.

## Failure Modes / Edge Cases

Reject incompatible generator/runtime combinations through dependency resolution or checks. Missing generated files, absent type files, invalid relative imports, or installation that invokes generators are packaging failures, even if imports work inside the repository.

The first schema establishes a baseline, not a published client version. Record why later baseline changes are compatible; do not update the baseline automatically to silence a failure. The proposed 16 MiB cap must fit the agreed fixtures without reducing precision or row counts.

## Testing / Validation

| Case | Expected result |
| --- | --- |
| Generate twice with pinned tools | Messages, stubs, and descriptors are byte-identical; check mode leaves source unchanged. |
| A generated file is edited or missing in an isolated fixture | Reproducibility check fails and identifies the mismatch. |
| New optional field versus changed field type/number, removed method, or reused reserved field | Compatible addition passes; breaking fixtures fail the compatibility check. |
| Omitted scalar versus present empty string or zero | Go/Python preserve presence and value. Encoding success does not imply a semantically valid request. |
| Decimal strings, including a long value at the allowed bound | Exact text survives cross-language serialization; no float conversion. |
| Count `9007199254740993`, timestamp with `123456789` nanoseconds | Both clients decode the exact values. |
| Optional decimal/count/duration absent and present zero | Both clients distinguish absence from zero. |
| Unknown field from a compatible newer fixture | Older readers accept the message and decode known fields. |
| Empty candle response and a multi-row range | Response-level series identifiers survive; rows have the approved fields and order. |
| Small/full snapshots and 1/100/1,000-candle fixtures | Record byte sizes; full 20,000-row snapshots per type and normal 1,000-candle responses fit the 16 MiB default. |
| Python wheel installed outside checkout | All generated modules and typing files are available; imports do not resolve to source files. |
| Git install pinned to a local fixture commit with `#subdirectory=api/python` | Installs the correct distribution/version and calls the Go fixture server without GitHub access. |
| Installation environment has no Go, protoc, or grpcio-tools | Package builds/installs using only its declared Python build/runtime dependencies. |
| ErrorDetail, unknown detail type, and status without details | Both clients decode a known reason and safely handle missing or unknown details. |
| Go/Python examples, including async Python | Four methods are callable against the fixture; deadline/cap options work and channel ownership is explicit. |
| HTTP baseline rerun with fixed fixtures | Commands, full result data, source/harness identity, and environment are recorded for phase 4. |

Run schema/generation checks, explicit `go build`, `go vet`, `go test`, and `go test -race` for the nested client module, Python package checks, `make check-api`, and `make check` / `make vet` for affected service integration. Do not write tests for generated accessors; the cross-language cases verify the shared contract.

Completion requires working packages, meaningful compatibility failures, passing checks, recorded tool versions and message sizes, and a reproducible baseline. The service has not moved to gRPC yet.

## Risks / Trade-offs

Installing from Git still needs repository access and Git; subdirectory selection is a packaging boundary, not a promise to fetch only Python files. Package version and wire package version have different purposes. If fixture sizes exceed the default cap, resolve the cap/capacity issue before completing this phase; do not add pagination or trim the fixtures implicitly.


## Implementation report — September 13, 2026

Phase 1 is implemented and independently reviewed. This is not a release readiness decision. The production entry point, transport, configuration, root module and exchange behavior remain unchanged.

### Delivered

- Added the numbered schema, generated Go/Python messages and RPC clients, Python type files, generated descriptor and separate initial compatibility baseline under `api/`.
- Added the separate Go module and Python `market-data-api` 0.1.0 package. Python 3.13 and 3.14 are the supported, verified boundary minor versions. Runtime/build/generator pins and their trade-offs are documented in `api/README.md`.
- Added `make generate-api`, `make check-api` and `make api-http-baseline`, with isolated tools and no generator needed by a normal service build. Both ordinary and release CI invoke the real API check before publication can proceed.
- Added a local Go contract-fixture server, Go client example, and installed synchronous/async Python examples. Tests cover four methods, exact decimal/count/nanosecond values, optional absent/zero values, a 1,024-character decimal, unknown fields/details, native errors, receive limits, cancellation and deadlines.
- Added Buf FILE compatibility regression fixtures and generated-file drift checks. The initial baseline is separate and is never updated by generation.
- Captured the existing HTTP handlers over real TCP with Go/Python clients, one/four reused connections, full/small snapshots and 1/100/1,000 candles. Fixed response bodies, hashes, message sizes, raw timings, connection counters and process costs are in [phase 1 evidence](../evidence/grpc-migration/README.md).

### Verification

`make check-api` passed: byte-identical regeneration, Buf baseline and breaking/compatible fixtures, nested Go build/vet/test/race, wheel inspection, clean wheel and pinned local-Git installs on Python 3.13.12 and Python 3.14.6, 11 cross-language tests per installation and all three examples. Package installs run outside the source directory with no Go/protoc/grpcio-tools and no `PYTHONPATH` shortcut. The wheel is saved locally in ignored `bin/api-dist/`.

`make check` passed formatting, service build/config, configured lint, root tests and race checks, including the HTTP harness tests. `make vet` and `git diff --check` passed. `make api-http-baseline` passed all nine full-message semantic comparisons and recorded 45 measurement rows / 1,980 latency samples. Exact command results and source identity are in the evidence files.

The maximum full snapshot is 4,240,000 Protobuf bytes; the other full snapshots are 4,160,000 and 3,820,000 bytes. All fit 16 MiB. The 100/1,000-candle responses are 20,330/203,030 Protobuf bytes versus 44,111/441,011 HTTP JSON bytes. The agreed counts and precision were preserved.

### Changed files and scope

Owned changes are `api/**`, `scripts/api/**`, `docs/evidence/grpc-migration/**`, Makefile, `.gitignore`, both GitHub check/release workflows, this phase report, the phase index, and only migration implementation-status wording in the main gRPC specification. Concurrent edits to other specifications, request-budget reports/evidence and the root README were preserved and are not phase 1 changes.

### Limits and next phase

The HTTP baseline uses fixed reader slices and 20 samples per worker. It is transport evidence, not application/storage, overload, partial-fill or 600,000-candle container acceptance. Python allocation counts are not measured; Go allocation and per-process CPU/RSS counters are. Full Linux/container checks and the final traffic/capacity matrix belong to phase 4. Docker build/verification were not run in this phase because no container or production composition changed; they remain later migration acceptance checks.

Independent review found one reconstruction-instruction defect. The implementation agent corrected it and the reviewer verified the fix; no actionable findings remain. Phase 2 must implement real handlers, error mapping, finite sends and admission ownership before phase 3 switches production. No project commit, tag, package publication, image publication or deployment was made.


### Review correction — baseline reconstruction

Independent review found one P2 issue: the documented `git archive` reconstruction had no Git metadata. The harness completed the fixture calls but failed at `git rev-parse HEAD`, before writing the final measurement JSON files. The instructions now use a separate local clone, a detached checkout of the fixed HTTP base, and the phase 1 contract/harness overlay. No implementation code changed.

The exact corrected reconstruction path passed end to end with the default 20 samples per worker: exit 0, both final JSON files written, 45 measurement rows and 1,980 latency samples. All nine response bodies, message sizes, HTTP/Protobuf hashes and relevant source hashes match the original baseline. The clone retained Git metadata and recorded the correct detached base plus overlay. Raw output and assertions are in [reconstruction evidence](../evidence/grpc-migration/reconstruction/verification.json).

Documentation links and `git diff --check` passed. Full Go/Python and container suites were not repeated for this documentation-only fix.

### Independent review result

The high-reasoning implementation agent completed the work; a separate xhigh-reasoning agent reviewed it. The reviewer independently passed `make check-api`, repeated nested Go and HTTP harness tests without cache including race checks, and verified the measurement counts and hashes. After the implementation agent corrected P2, the same reviewer inspected the corrected instructions, retained reconstruction clone, detached base, output files, and matching hashes, then closed the finding. No actionable findings remain.

The reviewer did not repeat the full root `make check` or run Linux CI/container capacity checks. Root checks passed during implementation; production gRPC and container/load acceptance remain later phases. The final review changed no implementation code.
