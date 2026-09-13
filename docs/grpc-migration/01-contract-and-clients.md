# Phase 1. Define the contract and generate clients

Status: planned; not implemented. No prior migration phase is required. See the [phase list](README.md) and the approved [client and schema design](../grpc-migration-specification.md#client-generation).

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
