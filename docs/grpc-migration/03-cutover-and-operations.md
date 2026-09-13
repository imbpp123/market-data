# Phase 3. Remove HTTP data routes and switch operations

Status: planned; not implemented. Depends on completed [phase 2](02-transport-and-lifecycle.md). See the [phase list](README.md) and approved [configuration contract](../grpc-migration-specification.md#configuration-and-size-bounds).

## Summary / Overview

Switch the executable to gRPC for all market data and a separate HTTP listener for operations. Replace configuration, healthcheck wiring, containers, CI, and active client instructions in one cutover.

## Context / Background

Phase 2 supplies tested handlers and server composition. The executable, configuration example, container probe, and old end-to-end tests still use HTTP data routes. There are no clients requiring compatibility.

## Problem Statement

Leaving old routes, configuration aliases, or container checks behind would create an accidental second API or give false confidence that the new service is ready.

## Goals

- Expose market data only through the four gRPC methods.
- Keep operational HTTP independent, with the approved route semantics.
- Make local runs, containers, and CI use the same new configuration and clients.
- Remove obsolete HTTP data behavior while keeping its business regression coverage.

## Proposed Solution / Design

1. Wire the phase 2 composition into `internal/bootstrap` and the command entry point. Activate both required listeners with the approved defaults, not an optional gRPC flag.
2. Change configuration loading/validation to `server.grpc` and `server.http`. Preserve shared snapshot, candle, fill, and shutdown settings. Reject removed flat keys and environment variables. Update `docs/examples/config-v1.yaml` together with the loader, so `-check-config` and `make run` remain usable.
3. Remove the four HTTP data handlers, JSON DTOs, route registrations, and unused data-error/query helpers. Keep HTTP health/readiness/error responses needed for operations. Inventory the existing tests before removal: port data semantics to gRPC; remove only obsolete HTTP query/verb/JSON-shape assertions. Preserve the recorded HTTP benchmark baseline and historical audit evidence.
4. Limit the operational router to health/readiness and enabled metrics/debug exporters. Validate exporter paths against collisions and `/api/`. Keep existing exporter media types, bounds, and enable flags. Do not add a gateway, redirects, reflection, or a legacy route switch.
5. Update `cmd/market-data-service/health.go` to resolve the operational address from configuration. Keep the healthcheck as one bounded local request with no collectors. Update Dockerfile/Compose for both listeners and the local generated Go module; include required module files in the build context. Preserve current container hardening, memory, shutdown allowance, and local-only published ports.
6. Port the container probe and applicable bootstrap integration tests to fetch market data through gRPC. The isolated container probe must distinguish healthy/locally ready from exchange data not ready. It must not reach public exchanges.
7. Add API generation/client verification to both `.github/workflows/checks.yml` and `release.yml`. Failed API checks must block publication before registry login/build push. Include explicit nested Go-module and installed Python checks; root `go test ./...` is insufficient. Do not publish a release while validating the workflow.
8. Update README, development/config instructions, and client examples to the actual runtime. Replace active HTTP data examples with gRPC examples/descriptors; retain operational curl examples. Phase 4 completes the acceptance register and historical audit links.

Expected areas are `internal/config`, `internal/bootstrap`, `internal/transport/http`, `cmd/market-data-service`, Dockerfile, Compose, Makefile/scripts, workflows, and active operating documents. Remove only migration-owned obsolete code; do not delete unrelated files or revert request-budget changes.

## Data Model / API / Interfaces

No new market-data fields are introduced. The new startup/configuration interfaces are those already approved in the specification: gRPC port 9090, operational HTTP port 8080, distinct size/time bounds, and shared shutdown/admission settings.

The old `MDS_SERVER_PORT` and moved flat YAML fields fail validation rather than silently selecting an endpoint. The healthcheck uses the HTTP address after overrides. A Python installation remains pinned to this repository's `api/python` package, with no need to install the server.

## Failure Modes / Edge Cases

Known conflicting bind addresses fail validation; resolved hostname/wildcard conflicts still fail safely at bind time. Duplicate/unknown configuration, invalid bounds, and metric-path collisions fail before collectors start. No failed configuration path partially starts the process.

Metrics/debug can be disabled independently. A custom metrics path must not bring back any `/api/` data route. Full snapshot/candle admission must not block the operational path.

## Testing / Validation

| Case | Expected result |
| --- | --- |
| Default configuration | Both distinct listeners start on their configured addresses; no compatibility mode exists. |
| YAML plus explicit environment overrides | Precedence is unchanged; gRPC and HTTP address/limit overrides apply independently. |
| Old flat host/port/HTTP fields or old environment names | Startup fails with a useful setting error; no silent aliases. |
| Unknown/duplicate keys, nulls, wrong types, nonpositive or overflowing sizes/durations, invalid ports/hosts | Strict validation fails before listeners/workers. |
| Shutdown budget too short or duration arithmetic overflows | Existing lifetime-plus-grace safety constraint remains enforced. |
| Direct address conflict or hostname/wildcard overlap at bind | Safe startup failure closes any already opened listener. |
| Four old data routes and another `/api/v1/*` path over HTTP | 404, no redirect, no data-reader call, no market-data body. |
| `/health` and `/ready` before/after initialization and during shutdown | Existing bodies/status semantics; readiness includes both server owners, not exchange freshness. |
| Metrics/debug disabled or enabled, including custom metrics path | 404 when disabled; correct existing media type and content when enabled. |
| Metrics path collides with health, ready, debug, or `/api/` namespace | Configuration rejected even if an exporter would otherwise be routable. |
| Non-GET operational request, oversized query/header, GET body | Existing operational rejection/bounds remain; no application data work. |
| Healthcheck with non-default HTTP port and unreachable HTTP listener | It targets the overridden operational port, respects its timeout, and does not start collectors. |
| Container without exchange access | HTTP health/readiness work; gRPC returns the expected unready-data status/detail. |
| Clean Docker build with nested generated Go module | Local module replacement resolves; runtime needs neither Go tools nor Python generators. |
| Container access and SIGTERM | Both host ports are local-only; existing non-root/read-only/1 GB settings and bounded shutdown remain effective. |
| Generated-code drift or broken installed Python package | Both CI paths fail before publication; no real registry push is needed to test the failure. |
| Existing business/integration scenario moved from HTTP | Equivalent gRPC data/error assertions pass; behavior coverage is not replaced with only a route-removal assertion. |

Run focused config/router/healthcheck/bootstrap tests, `make check`, `make vet`, `make check-api`, `make docker-build`, and `make docker-verify`. Inspect generated-client checks and release ordering, plus the diff for stale route/config references. Do not treat historical references as active routes or erase historical evidence to satisfy a text search.

Completion requires a working executable/container with exactly the intended surfaces, passing checks, and current run/install instructions. Performance and full release acceptance remain phase 4.

## Risks / Trade-offs

This is an intentional breaking config and wire change. Mixing an old example config with the new executable must fail clearly. Missing generated-module files in the container context or CI checks that cover only the root module are the main packaging risks.
