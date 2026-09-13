# Phase 2. Implement gRPC transport and lifecycle

Status: planned; not implemented. Depends on [phase 1](01-contract-and-clients.md). See the [phase list](README.md) and approved [transport design](../grpc-migration-specification.md#transport-and-application-boundaries).

## Summary / Overview

Implement four RPC handlers on the existing application services. Prove deadlines, complete responses, process-wide admission, send ownership, telemetry, and the lifecycle of separate gRPC and operational HTTP servers.

## Context / Background

The generated contract is available after phase 1. Business readers, candle fill sharing, and upstream admission already exist. Some request policy still lives inside HTTP handlers, so changing transport must move that policy too.

## Problem Statement

A working RPC reply alone does not prove safe migration. An early capacity release, lost cancellation, or unbounded send could exceed memory limits or hang shutdown. These problems must be resolved before the executable switches protocols.

## Goals

- Preserve market-data behavior through real gRPC calls.
- Keep application/domain independent of Protobuf and gRPC.
- Bound work and response sends, including slow clients on multiple channels.
- Exercise both-server startup and shutdown before cutover.

## Proposed Solution / Design

1. Add `internal/transport/grpc` handlers that consume existing application contracts. Separate message/query conversion and error mapping from network I/O. Keep decimal precision, required/optional field rules, ordering, and validation order from the specification.
2. Add shared snapshot admission and candle transport admission, using configurable limits across all connections. Preserve the existing application candle caller/fill limits; a new transport must not weaken or multiply them. Reject excess work without an extra queue.
3. Apply application deadlines at admission. Preserve the independent shared-fill owner; translate caller cancellation separately from timeout. Return only complete, validated data or a mapped error.
4. Bound request/metadata size and response construction. Check encoded response size before a successful send and return `response_too_large` without truncation. Keep conversion and pending sends within owned capacity, rather than checking size after unlimited allocation.
5. Prove the send-lifetime mechanism early with the selected runtime. Record which supported hooks retain/release capacity, where request and write-grace bounds start, and how pending writes abort. Test a client that stops reading until flow control blocks a response. Do not equate unary handler/interceptor return with completed sending or remote application consumption. If the required finite lifetime cannot be enforced simply, resolve that implementation gate before completing the phase.
6. Add gRPC error/panic handling, tracing, active-call and byte/duration observations. Reuse observability boundaries and settings. Preserve original error identity internally; expose only approved reasons and messages. Runtime failures may have no application detail.
7. Prepare bootstrap composition for both listeners and one shared shutdown owner. For this phase use explicit constructor settings and local test servers, not a second runtime configuration mode. Bind both before collectors, clear readiness on failure/shutdown, cancel owned work, and force-close at the shared deadline.
8. Extend `make check-api` to run installed Go/Python clients against these actual handlers. Keep phase 1's packaging fixtures where they test different behavior. Production entry-point activation, config migration, and HTTP route removal happen together in phase 3.

Expected areas are `internal/transport/grpc`, bootstrap composition/tests, observability adapters/tests, and focused application-boundary adjustments only where policies need a real shared owner. Existing HTTP routes remain solely the current executable's entry point until cutover; no supported dual data mode is added.

## Data Model / API / Interfaces

Implement the approved schema and [status/reason mapping](../grpc-migration-specification.md#errors) without new wire fields. New transport failures `response_too_large` and `request_canceled` are delivered through `ErrorDetail` where the transport can still send it.

Convert Timestamp to application time only after validity checks. Use existing application queries/readers for scope selection, calendar validation, readiness, retention, and fill execution. Internal constructor settings describe the approved transport bounds; configuration loading stays outside handlers and is connected in phase 3.

## Failure Modes / Edge Cases

All failed paths must release only resources they own. A canceled waiter must not remove another waiter's fill or capacity. Incomplete/failed fills keep valid cached rows but never return a partial success. Overload and Binance budget rejection must not increase exchange attempt counts without a dispatch.

Test both listener failure orders and the failure of an already serving listener. Cleanup must work before and after workers start. Operational HTTP must stay independent of the data-call limiter. The shared shutdown budget covers both servers and workers, rather than restarting a timer for each component.

## Testing / Validation

### Requests and data

| Case | Expected result |
| --- | --- |
| Each snapshot method with omitted filters | Reads selected enabled scopes, returns all matching sorted rows, and does no upstream work. |
| Empty filter, disabled scope, invalid symbol/status/window | Approved validation reason before storage/exchange work as applicable; missing window defaults to `24h`. |
| Multiple invalid fields | The documented validation stages and field order determine the reason. |
| One selected unready scope hidden by a symbol/status filter | Whole request fails with `UNAVAILABLE / data_not_ready`. |
| Ready empty scope or absent snapshot symbol | Successful empty repeated field; no false readiness failure. |
| Refresh fails after a successful snapshot | Previous values and timestamps remain visible. |
| Optional zeros, long exact decimals, counts above 2^53, nanosecond timestamps | Installed Go/Python clients receive exact values and presence. |
| Funding due in less than one second, expired funding, or absent funding | Preserve existing whole-second/presence behavior and the single-clock read model. |
| Missing timestamp, invalid nanos, pre-epoch time, unaligned/reversed/future range | Approved `INVALID_ARGUMENT` reason; no upstream request. |
| Aligned empty range with ready catalog and valid symbol | Empty candles with series identifiers, no kline fetch; catalog errors still fail. |
| Closed range, explicitly requested open slot, calendar month/week boundary | Preserve existing half-open range and supported calendar rules. |
| Maximum size, one slot above maximum, oldest valid start, one slot too old | Exact boundary results; excessive size and old history have separate stable reasons. |
| Retention boundary moves while a request waits | Existing `range_out_of_retention` behavior, no partial range or extended history. |
| Cold, warm, and partial cache; missing historical rows with no progress | Complete cold/partial results or `incomplete_data`; warm path sends zero upstream requests. Valid fetched rows survive failure. |

### Failures, bounds, and lifecycle

| Case | Expected result |
| --- | --- |
| Each error in the approved mapping, including wrapped errors | Exact status/reason; no internal text or upstream payload leaks. |
| Unexpected repository error or panic | Sanitized `INTERNAL / internal_error`; reporting and capacity cleanup occur. |
| Unknown method, malformed wire input, native message-limit failure | Native non-OK gRPC status; clients tolerate missing details. |
| Request/header bounds and response just below/at/above its cap | Accepted boundary values work; excess is rejected. Oversized application response has `response_too_large`, never truncated success. |
| Snapshot/candle limit reached using several channels | Shared process limit holds; excess fails immediately. A free slot later admits another request. |
| Many identical cache misses | One coordinated fill supplies complete results to surviving callers without duplicate upstream work. |
| One shared-fill caller cancels while another waits | Canceled caller exits; other caller receives the full result. |
| Earlier client deadline or service deadline, including before validation | Earlier applicable bound wins; no reset on retries/pages; owned fill rules remain intact. |
| Slow reader, then cancellation/disconnect/write failure | Pending response stays accounted for until completion/abort; resources are released within the finite transport bound. |
| All data capacity occupied | `/health` and local `/ready` remain callable without acquiring a data slot. |
| First bind fails, second bind fails, or serving listener fails | No orphan listener/worker; readiness is false; whole owner terminates cleanly. |
| Shutdown with idle, waiting, filling, and blocked-send RPCs | Readiness clears; no new data admission; both servers and workers finish under one deadline, with force-close if needed. |
| Success, application failure, transport failure, cancellation | Observations report final outcome/duration/bytes without payload labels, and do not inflate exchange attempts. |

Use unit tests for non-trivial policies, real local gRPC transport tests for sends/trailers, and installed Python calls for interoperability. Use local exchange fixtures where serialization through actual adapters matters. Synchronize concurrency tests explicitly; do not use sleep-based correctness checks.

Run focused transport/application/bootstrap/observability tests, `make check-api`, `make check`, and `make vet`. Review inner-layer imports. Completion requires the real handler tests, resolved send-lifetime gate, and bounded cleanup evidence; it does not mean the executable has completed cutover.

## Risks / Trade-offs

The highest risk is releasing capacity at the wrong transport event or leaving a send outside the service deadline. Verify this before polishing handler code. Bootstrap and observability may also change during request-budget work; integrate its current behavior rather than overwrite it with older assumptions.
