# gRPC and Protobuf API migration

September 13, 2026. Specification only; implementation has not started.

The user confirmed full replacement of the market-data HTTP API with gRPC and Protobuf. HTTP remains only for operations on a separate listener. There are no existing clients or published first version, so no compatibility period is needed. Ports, schema layout, limits, and tooling below are proposed engineering defaults, not earlier user requirements.

## Summary / Overview

Provide all instruments, tickers, 24-hour market statistics, and candles through one versioned gRPC service. Generate Go and Python clients from one Protobuf contract. Keep `/health`, `/ready`, `/metrics`, and `/debug/stats` on a separate HTTP server.

Remove the old data routes in the same migration. Do not add a REST gateway, JSON data endpoint, or protocol selection flag.

## Context / Background

The current implementation uses HTTP/JSON. Application readers, the candle service, memory storage, and exchange adapters already have separate boundaries. Most changes belong to transport, process startup, configuration, clients, and tests.

The agreed workload is 3–4 clients, up to 50 symbols, and the history windows in [specification section 1](technical-specification-v1.md#1-service-goals). The process memory limit remains 1,000,000,000 bytes. The [previous release audit](release-verification-v1.md) is evidence for the HTTP implementation, not acceptance of this migration. Its latency measurements call HTTP handlers in process; they do not measure a network path.

The [Binance request-limit rework](request-budget-rework-specification.md) is a separate change in progress. Preserve its agreed behavior and current work. Changing the client protocol does not change exchange protocols, budgets, refresh schedules, storage, or retention.

## Problem Statement

HTTP DTOs and client response parsing require repeated work across languages. JSON repeats field names and, for candles, series identifiers in every row. There is no generated shared client contract.

The migration should reduce manual integration work and wire overhead. It must not weaken precision, data completeness, cancellation, or resource limits to achieve this.

## Goals

- Use one schema for the Go server and Go/Python clients.
- Reduce repeated data in responses and measure traffic and serialization costs.
- Preserve existing market-data behavior and stable failure reasons.
- Keep operational HTTP available independently of data-request capacity.
- Establish a versioned contract that can later add subscription methods.

## Non-Goals

No streaming subscriptions, pagination, multi-series batch requests, new storage, exchange WebSocket ingestion, browser client, or deployment is included. Do not add a generic SDK framework, schema registry, service mesh, or new exchange behavior. No performance improvement ratio is assumed.

## Proposed Solution / Design

### Transport and application boundaries

Add `internal/transport/grpc`. Its handlers convert generated messages to application queries, call existing readers/services, and convert results and errors back to Protobuf. Keep generated types and gRPC imports outside domain and application packages. Keep the existing domain decimal types.

Construct both servers and shared application services in bootstrap. Keep operational handlers under `internal/transport/http`; remove the four market-data handlers and their JSON DTOs when the replacement passes tests. Move only policies needed by the new transport, such as snapshot admission, out of removed code. Do not route gRPC through an internal HTTP call.

Use two TCP listeners, with proposed defaults:

| Listener | Address | Purpose |
| --- | --- | --- |
| gRPC | `0.0.0.0:9090` | Market-data RPCs over HTTP/2 |
| Operational HTTP | `0.0.0.0:8080` | Health, readiness, metrics, debug statistics |

Both listeners are required. Do not multiplex protocols on one port. Bind both successfully before starting collectors or marking the process ready. A bind failure closes any listener already opened. A serve failure marks the process unready and stops the whole process through its normal shutdown path.

### Operational HTTP

| Route | Required behavior |
| --- | --- |
| `GET /health` | Process liveness; no exchange or cache reads |
| `GET /ready` | 200 after storage, services, both listeners, and worker ownership are initialized; 503 during initialization or shutdown |
| `GET /metrics` | Existing Prometheus exporter when enabled; preserve its configurable path |
| `GET /debug/stats` | Existing operational statistics when enabled |

Readiness does not promise a first snapshot or fresh exchange data. Preserve current health/readiness response bodies and optional exporter settings. Keep operational HTTP timeouts and input bounds. Reject metric-path collisions with health, readiness, debug routes, or the removed `/api/` namespace.

All old `/api/v1/*` routes return 404, with no redirect or proxy. HTTP exposes no market-data payloads. Existing application statistics can still describe collectors, storage, and upstream work. This migration does not add `pprof`, gRPC health, or server reflection; checked-in descriptors support local RPC inspection.

### Request lifetime and resource ownership

Preserve the process-wide limit of 64 snapshot callers shared by all three snapshot RPCs, and 64 candle callers, using the existing configurable values. Reject excess work immediately; do not create a new unbounded queue. Limits apply across all connections, not once per connection.

Start the application request deadline at RPC admission, before semantic validation: 5 seconds for snapshots and 30 seconds for candles by default. An earlier client deadline wins. Preserve the separate owned-fill lifetime, attempt budgets, and fill sharing. One caller's cancellation must not cancel a fill still needed by another caller.

Keep response construction and serialization inside the capacity bound. Retain ownership of pending sends until transport completion or abort. A unary interceptor returning is not proof that serialization and sending have finished. Use the selected gRPC runtime's completion hooks and test slow readers. Give transport completion a finite bound of the request lifetime plus the existing 5-second write grace, measured from admission; an earlier client deadline still wins. If the runtime cannot enforce that bound with a simple supported mechanism, resolve and document the mechanism in phase 2 before proceeding.

Keep the existing bounded upstream work. In particular, Binance threshold rejection still maps to `service_overloaded`, while complete cached reads continue to work. No automatic application retry or hedging policy is configured in generated client examples. A caller may explicitly retry within its own total deadline, but an unchanged invalid or incomplete range must not become an automatic retry loop.

### Client generation

Use `proto3` with explicit `optional` scalar fields where absence matters. Source schemas live under `api/proto/marketdata/v1/`. Start with one `market_data.proto` file; split only if needed for readability.

Generate Go messages and stubs into a dedicated module at `api/go`, with module path `github.com/imbpp123/market-data/api/go` and package path `marketdata/v1`. The server uses a local module replacement during development. A separate module lets clients depend on the contract without downloading exchange SDK dependencies. Do not rename the existing service module for this purpose.

Generate Python messages, type stubs, and RPC clients into an installable `market-data-api` package under `api/python`, preserving the `marketdata.v1` import layout. Provide minimal Go and Python examples for a snapshot and candles, including a reused channel, explicit deadline, response limit, optional-field access, and structured error handling. Include a Python async example for bot use; use the generated client with `grpc.aio`, not a second hand-written SDK.

Pin compatible versions of `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`, `grpcio-tools`, Go/Python Protobuf and gRPC runtimes, and Python `grpcio-status` for error decoding during phase 1. Use standard generators and Makefile/scripts; no remote generation service is required. Commit generated code and a descriptor set. Never edit generated files manually. Normal server builds do not require Python or code generation.

Add `make generate-api` and `make check-api`. The latter checks reproducible generation, schema compatibility against the checked-in base contract, Go client compilation/tests, Python package installation/import/type-stub availability, and local cross-language tests. The root `go test ./...` does not cover a nested Go module; run its checks explicitly. Keep tools and dependencies pinned, and run these checks in both ordinary and release CI. Build local versioned client artifacts; registry publication is outside this task.

## Alternatives Considered

- **Keep HTTP/JSON:** lower migration cost, but does not meet the chosen shared gRPC contract and traffic direction.
- **Expose both data APIs or add a gateway:** adds a second supported surface with no existing clients to justify it.
- **Start with streaming:** adds subscription state, reconnect rules, and partial-delivery behavior. Current range and snapshot reads need none of these.
- **Generate a contract from domain structs:** couples the public wire format to internal storage and calculation changes. Keep an explicit schema instead.

## Data Model / API / Interfaces

### Service and requests

Use package `marketdata.v1` and service `MarketDataService`. All methods are unary: one request and one complete response.

```proto
service MarketDataService {
  rpc ListInstruments(ListInstrumentsRequest) returns (ListInstrumentsResponse);
  rpc ListTickers(ListTickersRequest) returns (ListTickersResponse);
  rpc ListMarketStats(ListMarketStatsRequest) returns (ListMarketStatsResponse);
  rpc GetKlines(GetKlinesRequest) returns (GetKlinesResponse);
}
```

| Request | Fields |
| --- | --- |
| `ListInstrumentsRequest` | Optional strings: `exchange`, `market`, `symbol`, `status` |
| `ListTickersRequest` | Optional strings: `exchange`, `market`, `symbol` |
| `ListMarketStatsRequest` | Optional strings: `exchange`, `market`, `symbol`, `window` |
| `GetKlinesRequest` | Optional strings: `exchange`, `market`, `symbol`, `interval`; `google.protobuf.Timestamp`: `from`, `to`. All are semantically required; presence tracking detects missing fields. |

Keep canonical exchange, market, status, and interval strings from the current application contract. Validate them rather than create a second enum dictionary in this migration. Optional filters distinguish omission from an explicitly empty string. Omitted snapshot filters select all applicable enabled scopes; present empty filters fail. Omitted `window` means `24h`; empty or other values fail with `unsupported_window`.

Preserve symbol rules and case sensitivity from the [existing contract](implementation-contract-v1.md#common-envelope-and-validation). Request messages have no JSON/query-string representation. Accept unknown Protobuf fields for schema evolution and use standard Protobuf duplicate-field decoding rules; do not reproduce HTTP query-shape rules in a custom binary parser.

For `GetKlines`, check required presence/nonempty fields, canonical values and enabled scope, timestamp validity and range shape, slot count, retention, then catalog readiness/symbol existence and execution. For simultaneous errors within a stage, use field order `exchange`, `market`, `symbol`, `status`/`window`, `interval`, `from`, `to`. A present empty interval is `invalid_interval`; a missing timestamp is `invalid_parameter`; an invalid timestamp is `invalid_range`. Snapshot validation follows the same relevant stages. Invalid requests do no upstream work.

### Responses and field types

Snapshot responses contain `repeated Instrument instruments`, `repeated Ticker tickers`, or `repeated MarketStats market_stats`. Keep rows sorted by exchange, market, symbol. Each snapshot row carries its own exchange, market, and symbol because one response can contain several scopes.

All decimal fields use canonical exact strings from the existing decimal conversion. Do not use `float`, `double`, rounding, or a new scale/precision rule. Preserve the current 1,024-character numeric expansion bound. All counters use `int64`. All instants use `google.protobuf.Timestamp`, preserving nanoseconds. Validate timestamps before conversion; request times before Unix epoch are invalid.

| Message | Fields beyond identifiers |
| --- | --- |
| `Instrument` | Strings `base_asset`, `quote_asset`, `status`, `price_tick`, `qty_step`; optional decimal strings `min_qty`, `max_qty`, `min_notional`; optional `int64 funding_interval_seconds`; optional timestamp `delisting_time`; required timestamp `updated_at` |
| `Ticker` | Decimal string `last_price`; optional decimal strings `bid_price`, `bid_size`, `ask_price`, `ask_size`, `funding_rate`; optional `int64 next_funding_in_seconds`; required timestamp `fetched_at` |
| `MarketStats` | String `window` (`24h`); decimal strings `high`, `low`, `volume`, `turnover`; optional decimal string `price_change`; optional `int64 trade_count`; required timestamp `fetched_at` |
| `Kline` | Required timestamps `open_time`, `close_time`, `fetched_at`; decimal strings `open`, `high`, `low`, `close`, `volume`, `turnover`; optional `int64 trades_count` |

Here “required” means an application guarantee, not the Protobuf `required` keyword. Timestamp message fields already track presence. Optional scalar fields must use explicit presence: absent decimal/count/duration is different from a present zero. Preserve whole-second duration conversion and the ticker reader's single-clock funding calculation. Do not expose internal `contract_type` or `next_funding_at`.

`GetKlinesResponse` contains strings `exchange`, `market`, `symbol`, `interval` once, plus `repeated Kline klines`. Each candle omits these repeated series identifiers. This preserves all data while reducing repeated bytes. The response identifies the validated series even for a valid empty range. Candle order is ascending `open_time`.

Assign and review field numbers in phase 1, then keep them stable. Never reuse removed field numbers or names; reserve them. Add compatible fields within `marketdata.v1`. A later incompatible contract requires a new package version. Future subscriptions use new methods with their own specification.

### Preserved data semantics

Snapshot reads remain cache-only. Every selected scope must have a first successful snapshot. A missing symbol in ready snapshot scopes returns an empty list; a missing candle symbol returns `symbol_not_found`. Failed refreshes keep old data and its timestamps. Do not introduce snapshot expiry or a new stale-data policy.

Preserve the [candle range contract](implementation-contract-v1.md#kline-range-contract), replacing RFC 3339 input parsing with Timestamp validation. Ranges remain aligned, half-open `[from, to)`, with calendar-aware retention and at most the configured 1,000 slots by default. Preserve valid empty ranges, explicit current-open-slot inclusion, and the existing boundary-change behavior while waiting. No partial success, synthetic gap rows, historical symbol discovery, or silent range trimming is allowed.

### Errors

Return a gRPC status instead of a successful response containing an error. For application failures attach a generated `ErrorDetail` message with `string reason`, using the stable reasons below. Define it in the same `marketdata.v1` schema and encode it as an `Any` detail in the standard rich gRPC status. Keep the public status message simple and sanitized. Go and Python clients decode the same detail; no custom trailer format is added.

| gRPC status | Stable reason |
| --- | --- |
| `INVALID_ARGUMENT` | `invalid_parameter`, `invalid_filter`, `invalid_status`, `unsupported_window`, `invalid_interval`, `invalid_range`, `request_too_large`, `range_out_of_retention` |
| `NOT_FOUND` | `symbol_not_found` |
| `UNAVAILABLE` | `data_not_ready`, `upstream_unavailable`, `upstream_error` |
| `RESOURCE_EXHAUSTED` | `service_overloaded`, `upstream_attempt_limit`, new `response_too_large` |
| `FAILED_PRECONDITION` | `incomplete_data` |
| `DATA_LOSS` | `invalid_upstream_data` |
| `DEADLINE_EXCEEDED` | `request_timeout` |
| `CANCELLED` | New `request_canceled` for caller cancellation |
| `UNIMPLEMENTED` | `unsupported_operation` if an application path returns it |
| `INTERNAL` | `internal_error`, including unexpected errors and recovered panics |

Wrapped errors retain identity inside the process. Do not expose upstream bodies, internal errors, or stack traces. Unknown RPC methods and runtime-generated failures use native gRPC statuses and may have no `ErrorDetail`. Clients must handle missing/unknown details, including connection failures, invalid wire messages, message-limit failures, and client deadline expiry. Do not promise a detail after the peer disconnects. A gRPC code alone is not a safe retry policy.

### Configuration and size bounds

Replace flat listener settings with this proposed target structure. Unlisted storage, exchange, upstream, and observability settings stay unchanged.

```yaml
server:
  shutdown_timeout: 35s
  max_snapshot_requests: 64
  snapshot_timeout: 5s
  grpc:
    host: 0.0.0.0
    port: 9090
    max_request_bytes: 8192
    max_response_bytes: 16777216
    max_header_bytes: 32768
  http:
    host: 0.0.0.0
    port: 8080
    read_header_timeout: 5s
    idle_timeout: 1m
    write_timeout: 5s
    max_header_bytes: 32768
    max_query_bytes: 8192
```

Keep `klines.request_timeout`, `klines.max_callers`, and all fill settings. Byte limits are positive finite integers within the runtime's supported range; ports are in 1..65535. Preserve checked duration arithmetic and the existing shutdown-lifetime validation. Validate hosts and reject known overlapping listener addresses; binding both remains the final check for hostname and wildcard overlap.

Apply existing defaults → YAML → explicit `MDS_` environment precedence and strict validation. For example, use `MDS_SERVER_GRPC_PORT` and `MDS_SERVER_HTTP_PORT`. Reject removed flat `server.host`, `server.port`, and moved HTTP settings, including old environment names; do not silently alias them. Update the example configuration in the implementation phase, not before the loader supports it.

Limits apply to uncompressed Protobuf message bytes. Check response size before sending a success message; an oversized response returns `RESOURCE_EXHAUSTED / response_too_large`, with no truncation. Limit conversion/buffering as well as the final send; checking size only after unlimited construction is insufficient. Generated client examples set the matching 16 MiB receive limit explicitly. Validate this default against the full synthetic catalogs and 1,000-candle responses before accepting phase 1.

The response cap is a new explicit limit: a valid query with very large values/catalogs may fail. Snapshot clients can narrow filters; candle clients can request a smaller range. Do not add implicit pagination. Per-message and caller limits do not by themselves prove that all legal concurrent requests fit in 1 GB; retain the agreed workload as the capacity acceptance profile.

Reuse client channels. Begin with uncompressed Protobuf; measure gzip as an optional experiment, not a required dependency or default. Reducing repeated full-history polling is a client concern; examples should demonstrate a bounded requested range rather than a polling loop that reloads all history.

## Failure Modes / Edge Cases

- A selected unready snapshot fails the whole combined response, even if filters would hide its rows.
- Cold/partial candle reads retain successful fetched pages after failure, but return no successful partial range.
- Caller cancellation, timeout, overload, oversized responses, and write failure release owned capacity. Other readers and operational HTTP continue working.
- A slow or disconnected client cannot leave a pending response or shutdown wait alive without a finite bound.
- During shutdown, clear readiness, close new RPC admission, cancel root-owned work and active request work, and stop both servers within one shared 35-second default budget. Wait for fills and collectors; force-stop gRPC and close HTTP at the deadline. Do not wait indefinitely in `GracefulStop`. HTTP shutdown and gRPC shutdown run concurrently under that shared budget.
- Operational routes bypass data admission limits. Disabled exporters remain 404. Non-GET operational requests retain 405 behavior.

## Security / Privacy

The current server has no native TLS or client authentication. Do not describe Protobuf as encryption. Default local Compose mappings bind both published ports to `127.0.0.1`; trusted container clients may use the internal gRPC port. Operational HTTP stays on a trusted interface/network.

For calls across an untrusted network, use an authenticated encrypted path, such as an existing TLS/mTLS proxy or private encrypted network. Any proxy must support gRPC HTTP/2 and its trailers. Building native certificate management or a new authentication system is outside this migration. A remote deployment must explicitly choose and verify that path; it is not authorized by this specification.

## Observability

Preserve current collectors, upstream metrics, Sentry settings, and HTTP exporters. Replace data-route HTTP request tracing with gRPC request tracing and panic/error handling at the transport boundary.

Record RPC count, active calls, final status/reason, duration through transport completion, and encoded request/response bytes. Use bounded labels: method, status, and known reason. No symbol, raw payload, or arbitrary metadata labels. Unknown reasons map to one bounded label. Keep application failures separate from transport failures and expected cancellation. Client RPC retries must not be counted as exchange attempts unless actual upstream dispatch occurs.

## Migration / Rollout Plan

These phases are implementation steps, not separate production releases. No phase adds a supported dual data API.

1. **Contract and generation.** Add schema, field numbers, generated Go/Python packages, descriptors, pinned tooling, and client examples. Verify field presence, exact decimals, and full-profile message sizes. Capture an HTTP baseline before removing its handlers, using the same fixed fixtures intended for the final comparison.
2. **gRPC transport and lifecycle.** Add four handlers, mapping, admission/deadline/send ownership, tracing, and both listeners. Prove slow-client and cancellation behavior. Reuse application services and completed request-budget work.
3. **Cutover.** Remove HTTP market-data routes, DTOs, and obsolete route-shape tests. Port meaningful behavior and integration assertions to gRPC. Update config, healthcheck target, Docker/Compose, Makefile, both CI workflows, and operating examples. Keep the image's operational healthcheck on HTTP.
4. **Acceptance.** Run the checks below, install both generated clients, measure network/serialization costs and memory, and publish a new verification report in the repository. Update the main specification, implementation contract, and decision register to point to the final contract. The first release remains pending until these checks pass.

Do not rewrite or undo unrelated local changes. Complete and verify the request-budget rework independently; acceptance must exercise the combined final behavior. This document does not approve incomplete intermediate code for release.

## Rollback Plan

Before release, return to a known earlier revision together with its matching configuration if the migration fails acceptance. Memory-only cache state is rebuilt on restart. There is no client migration, persistent schema migration, HTTP fallback flag, or simultaneous legacy API to maintain. Do not reset or remove unrelated work as part of rollback.

## Testing / Validation

Use the existing Go test rules: deterministic unit tests for non-trivial logic, stateful boundary fakes, `t.Context()`, and no exchange network access. Add real local gRPC integration tests and Python-to-Go calls; direct handler tests alone do not verify Protobuf serialization or trailers.

Acceptance requires:

1. **Contract:** all four methods work from generated Go and Python clients. Check omitted versus empty filters, absent versus zero optional values, decimal precision including long values, counts above 2^53, nanosecond timestamps, invalid timestamps, canonical strings, and unknown fields. Compare normalized results across languages. Generated code and descriptors reproduce from pinned tools.
2. **Data behavior:** preserve snapshot readiness/empty/stale distinctions, filtering and sorting, closed/open/empty candle ranges, retention edges, maximum/oversized requests, no-progress gaps, and exact complete responses. Warm reads make no upstream requests. Shared misses avoid duplicate fill work.
3. **Failures:** exercise every error-mapping row, sanitized unexpected errors and panic recovery, absent details from native gRPC failures, unknown methods, oversized request/response messages, invalid wire input, client deadlines, and explicit cancellation. New `response_too_large` and `request_canceled` are documented and decodable in both languages.
4. **Concurrency and lifecycle:** use multiple channels to prove global caller limits, shared-fill independence, capacity release through send completion, slow readers, saturated data traffic with responsive health, both bind-failure orders, serve failure, and bounded SIGTERM/forced shutdown. Use controlled synchronization; no arbitrary sleeps for correctness.
5. **Operational surface:** old HTTP data routes return 404 and never call application readers. Health/readiness and optional metrics/debug preserve semantics, media types, and route validation. Container healthcheck targets the operational listener. Compose still enforces non-root/read-only execution and the 1 GB limit.
6. **Checks:** run `make check`, `make vet`, new `make check-api`, `make docker-build`, and `make docker-verify`. Extend CI so generated-client checks cannot be skipped in releases. Run the migrated `make release-load` profile with the same 600 series / 600,000 candles and full 20,000-row snapshots per type.
7. **Measurement:** compare fixed HTTP baseline fixtures and gRPC results over real local TCP with reused connections and the same encryption/compression mode. Include Go and Python clients, 1/100/1,000 candles, small and full snapshots, four concurrent clients, and a separate overload profile. Record message bytes and network bytes separately, client/server CPU, allocations, peak RSS, and p50/p95/p99 latency. Separate encoding cost from storage work and network timing. Do not compare the old in-process p95 directly to new network results.

Require exact semantic equivalence except the explicit wire/error/config changes above. Confirm lower encoded candle-response size than the equivalent uncompressed JSON baseline for 100 and 1,000 rows. Report snapshot results and any CPU/latency regressions without inventing a gain target. The agreed workload must complete under the existing 800,000,000-byte peak-RSS engineering target inside the 1 GB container. Resolve failures or record an explicit requirement change before release; do not silently reduce fixtures, precision, or history to pass.

## Risks / Trade-offs

Generation removes repeated client serialization work, not business validation or tests. Most retained memory belongs to cached domain data; a smaller wire response does not remove that memory. New gRPC dependencies, buffers, and Python runtime compatibility need verification.

Large snapshots and high concurrency remain a capacity risk. The proposed response cap is intentional but needs fixture evidence. Standard unary handlers need careful send ownership; an interceptor-only limiter may release capacity too early. Keep this as an explicit acceptance item rather than assume HTTP/2 flow control solves process memory and time bounds.

## Open Questions

No product decision blocks specification work. Phase 1 must record the exact compatible generator/runtime versions and measured suitability of the proposed response cap. Phase 2 must record the supported mechanism for bounded sends and capacity release. These are implementation verification gates, not permission to omit the requirements.

## Earlier requirements replaced

This document supersedes the HTTP data transport requirements in [main specification](technical-specification-v1.md) sections 34–40, related transport configuration in 42–44, and data-request observability, startup/shutdown, tests, packaging, and acceptance references in 46–65. Preserve their business behavior unless an explicit change is stated here.

The [implementation contract](implementation-contract-v1.md#http-contract), JSON examples, and [development guide](development.md) describe the current HTTP implementation until cutover. At cutover, replace active client examples and API contracts with the generated schema and this specification; historical audit files remain labeled as HTTP evidence. The Binance rework's phrase “public API keeps its response format” no longer applies to the client wire format; its business outcomes and failure reasons still apply.

## Sources

Official references checked on September 13, 2026:

- [Go generation](https://grpc.io/docs/languages/go/quickstart/) and [Python generation](https://grpc.io/docs/languages/python/quickstart/): language clients and generators.
- [Protobuf field presence](https://protobuf.dev/programming-guides/field_presence/) and [schema evolution](https://protobuf.dev/programming-guides/proto3/): optional values and compatible field changes.
- [Runtime compatibility](https://protobuf.dev/support/cross-version-runtime-guarantee/): generated-code/runtime version constraints.
- [gRPC status codes](https://grpc.io/docs/guides/status-codes/) and [error details](https://grpc.io/docs/guides/error/): status transport; the application mapping above is a project decision.
- [Deadlines](https://grpc.io/docs/guides/deadlines/) and [graceful shutdown](https://grpc.io/docs/guides/server-graceful-stop/): finite request and server lifetimes.
- [grpc-go options](https://pkg.go.dev/google.golang.org/grpc): message limits; client defaults must not be assumed to match this service.
- [Performance](https://grpc.io/docs/guides/performance/) and [authentication](https://grpc.io/docs/guides/auth/): channel reuse and transport security.
