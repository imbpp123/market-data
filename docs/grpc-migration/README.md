# gRPC migration: execution phases

September 13, 2026. The user approved the [main specification](../grpc-migration-specification.md), including Python installation from this repository. Phase 1 is complete after independent review and correction of one finding. Phase 2 is complete after independent review and correction of seven findings. Phase 3 is complete after independent review. Phase 4 is complete after independent review and correction of one finding. The full migration is complete. See the [verification report](../grpc-migration-verification.md).

The specification owns the requirements. These files explain implementation order, affected areas, test cases, and completion evidence. They do not add streaming, an HTTP compatibility period, or a separate client repository.

| Phase | Work | Result to review | Dependency | Status |
| --- | --- | --- | --- | --- |
| [1. Contract and clients](01-contract-and-clients.md) | Protobuf schema, generated Go/Python packages, Git installation, compatibility checks, HTTP baseline | Installable client packages and measured contract sizes | None | Complete; independently reviewed |
| [2. Transport and lifecycle](02-transport-and-lifecycle.md) | Four RPC handlers, errors, bounds, send ownership, telemetry, two-server lifecycle | Real local gRPC calls preserve application behavior and release resources | Phase 1 | Complete; independently reviewed |
| [3. HTTP removal and operational cutover](03-cutover-and-operations.md) | Switch startup, replace config, remove HTTP data handlers, update containers and CI | The executable serves data only through gRPC and operations through HTTP | Phase 2 | Complete; independently reviewed |
| [4. Acceptance and measurements](04-validation-and-measurements.md) | Cross-language acceptance, combined upstream behavior, load and traffic comparison, final documentation | A reproducible verification report and explicit release readiness decision | Phase 3 and completed request-budget rework | Complete; independently reviewed |

## How to execute

Work in order. Keep each phase's implementation and tests together. Phase 1 prepares the contract and clients; continue to later implementation only after its review. Read the current Git state and applicable instructions again before each phase, since the request-budget work is changing some of the same files.

Phases 1–2 prepare and test the replacement. Phase 2 exercises the new composition in local test servers; the production entry point switches once in phase 3. Do not introduce a production mode with both data APIs. These phases are not separate releases, and no phase publishes packages, tags, images, or deployments as part of this plan.

Each phase runs its focused checks and relevant project checks. Phase 4 owns the full acceptance and capacity report; it does not replace tests needed to complete an earlier phase. Newly added checks must run real assertions, not return success through a placeholder or permanent skip.

## Test approach

- Unit tests cover non-trivial mapping, validation, errors, configuration, and resource policy. Use real application behavior with small stateful I/O fakes.
- Local gRPC tests cover serialization, statuses, trailers, cancellation, and transport completion. Python tests use the installed package outside its source directory.
- Use the current Go test's `t.Context()`, controlled time, and explicit synchronization. Wall-clock load measurements are evidence, not deterministic correctness tests.
- No public exchange calls or production credentials are needed. Dependency acquisition is a separate build step. Record any unavailable tool, local-listener permission, or container limitation without claiming the affected checks passed.

## Required completion evidence

Append a short implementation report to the completed phase file: changed files, implemented behavior, exact checks and results, unresolved issues, and the next phase. Update the status table here and the main specification. Do not mark a phase complete with a failing required test or an unresolved phase-specific gate.

Store reproducible machine-readable measurement output with a short index under the planned `docs/evidence/grpc-migration/` directory. Record commands, source revision and any relevant uncommitted changes, fixture identity, tool versions, environment, and limitations. A clean revision alone cannot describe a run made from modified source.

The two engineering gates are owned explicitly: phase 1 fixes compatible tool versions and verifies the 16 MiB response default; phase 2 proves bounded sends and capacity ownership through transport completion. Preserve the approved behavior if either needs an implementation adjustment. A requirement change must be stated explicitly rather than hidden in a phase report.
