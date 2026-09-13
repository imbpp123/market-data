# Phase 2. Implement gRPC transport and lifecycle

Status: complete; independently reviewed after correction of seven findings. Depends on [phase 1](01-contract-and-clients.md). See the [phase list](README.md) and approved [transport design](../grpc-migration-specification.md#transport-and-application-boundaries).

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

## Implementation report (September 13, 2026)

The four RPCs now use the existing readers and candle service. The production command and configuration still serve HTTP. This phase adds an explicit internal two-listener composition for local tests; it does not enable a second production data mode.

### Send ownership decision

The server uses pinned grpc-go 1.76.0 `ServeHTTP` with Go 1.27.1 `net/http` unencrypted HTTP/2. Native grpc-go `stats.End` can run while response data remains queued, so it does not release a caller slot. The HTTP wrapper acquires one process-wide snapshot or candle slot before decoding and validation. An interceptor applies the application deadline measured from that admission. The earlier client deadline wins. Incremental row conversion and final Protobuf size checks run inside the slot.

`ResponseController.SetWriteDeadline` sets the absolute admission + request timeout + write grace bound; an earlier client deadline reduces it. Cancellation aborts pending writes. The wrapper captures `CloseNotify` while the response writer is valid, then transfers its slot to one owned completion goroutine. That goroutine releases the slot only after both HTTP/2 stream closure and native RPC processing completion. The synchronous ServeHTTP `stats.ConnBegin` hook arms an expected `stats.End` for a known unary method before native dispatch. End covers application cleanup, conversion and native serialization; stream closure covers the send. Setup failures, rejected admission and unknown methods do not arm an End wait. A closed-call fence prevents late application dispatch. The write deadline stays armed for final trailers. HTTP handler return and request-context cancellation are not used as completion signals. The cancellation callback is stopped and joined before the response writer can be reused.

Real TCP tests use an HTTP/2 client with a zero stream window and no WINDOW_UPDATE. It receives response HEADERS after the unary handler has returned. A second channel is rejected while the response is blocked. Reset, forced close, and the absolute write deadline release capacity after native processing also finishes. The deadline case uses one second of request time plus one second of grace and completes without client reads. This resolves the resource-ownership gate on the pinned runtimes.

There are two runtime trade-offs. grpc-go marks ServeHTTP experimental. CloseNotifier is deprecated, but its pinned HTTP/2 implementation signals stream closure; Request.Context closes earlier and cannot replace it. Changes to either runtime require these flow-control tests again. Do not use grpc.GracefulStop for this transport: handler transport Drain is unsupported. The HTTP servers own graceful shutdown and connection closure; grpc.Stop closes remaining handler transports.

### Observed outcome boundary

RPC metrics record bounded method/status/reason labels, active calls, encoded message bytes and duration through processing completion and stream closure, including cleanup that continues after stream abort. Traces, sanitized errors and panic reporting use the existing telemetry boundary. Statistics reach the existing debug and Prometheus handlers. Status means the application or native status submitted to local transport, corrected for observed DATA/Flush errors. CloseNotify does not expose a close cause. A trailer-only failure after the HTTP handler returns cannot be distinguished from normal closure by these supported hooks. No metric confirms remote application consumption. Capacity remains owned until processing completion and closure in both cases. Native HTTP setup failures without gRPC status use the standard HTTP-to-gRPC fallback in observations.

### Validation and preserved behavior

New tests cover real RPC serialization, all stable error rows and wrapping, panic cleanup, exact values and presence, snapshot filtering/readiness/stale data, empty/open/maximum candle ranges, week/month alignment, warm cache, no-progress failure with retained rows, shared-fill cancellation, separate process-wide limits, native wire failures, exact request/header/response byte boundaries, earlier deadlines, both bind-failure positions, serve failure, operational availability, and shared bounded shutdown.

Existing application tests continue to own unchanged algorithms: `TestServiceRetentionExpiresWhileWaiting`, `TestServiceMonthlyWindowUsesCalendarSlots`, `TestServiceFiftyIdenticalMissesShareOnePagedFill`, `TestServiceCallerAttemptBudgetSurvivesOverlappingFills`, and planner/retention calendar tests. Existing bootstrap request-budget tests continue to prove Binance rejection, retained pages and real dispatch accounting. No domain or application file changed, and neither layer imports Protobuf or gRPC.

`make check-api` now starts the actual transport with real readers, memory storage and the candle service. A standalone generated Go client and installed Python clients compare normalized results for all four RPCs. Python 3.13 and 3.14 each pass wheel and pinned local Git installation checks outside the source tree, plus synchronous and async RPC tests. The older package fixtures remain separate.

Changed areas: `internal/transport/grpc`, `internal/bootstrap/grpc.go` and tests, RPC observability and exporter wiring, the shared local application fixture, API client check scripts, and root module dependencies. The Dockerfile and build allowlist now include the local generated Go module, because otherwise the existing HTTP image would fail to build after adding the module replacement. Listener ports, healthcheck, Compose and production startup are unchanged.

Checks and source identity are recorded in [phase 2 evidence](../evidence/grpc-migration/phase-02/README.md). Independent re-review closed all seven findings and found no further actionable defects. The final independent transport and observability race tests passed. Phase 3 remains the configuration, HTTP removal and operational cutover; phase 4 still owns full container workload and performance acceptance.

### Independent review corrections (verified)

The independent reviewer found five initial defects and two additional defects in re-review. All seven have targeted regression tests:

| Finding | Correction and regression |
| --- | --- |
| Unbounded DATA after the first unary message | Read one frame and request EOF before application dispatch. The runtime receives only a bounded memory reader; unknown methods receive no live body. The maximum payload remains exact, with five framing bytes and at most one lookahead byte. `TestUnaryTailCannotReachRuntimeOrApplication` consumes six bytes from an empty-frame-plus-tail request, rejects it immediately, and proves no application dispatch and released capacity. |
| Overload and header rejection waited for a body | Write the rich rejection before any body read. HEADERS-only tests verify the exact reasons and prompt stream closure. Missing or partial accepted bodies expire at the application/client deadline; root cancellation also unblocks the read. |
| Native unknown methods were observed as OK | Capture the locally written native gRPC status when interceptor/stats callbacks are absent. Unknown service, unknown method and malformed path tests compare client, observer and exported counters. |
| Forced cancellation was observed as timeout | Cancellation before the hard write deadline takes precedence over the timeout used to abort the writer. Root/client cancellation, service deadlines, partial-body deadlines and hard write deadlines have separate regression checks. |
| Expected RPC failures created error reports | Keep invalid input, missing symbols, unready snapshots, cancellation and unsupported methods in RPC metrics without Sentry error reports. Internal failures, upstream errors, timeouts and panics retain reporting. |
| Cancellation released capacity before processing finished | Wait for both native `stats.End` and stream closure, with a dispatch fence. Controlled cleanup, blocked native Marshal, delayed native dispatch and stopped-runtime tests prove retained capacity, no late application start, and eventual cleanup. |
| Native HTTP setup failures were observed as OK | Capture HTTP status when no gRPC status exists. Malformed timeout and binary metadata return HTTP 400/Internal; wrong content type returns HTTP 415/Unknown. Tests verify observer and exported counters. |

The deadline tests exposed a separate native-runtime boundary. At its exact write deadline, Go 1.27 HTTP/2 sends RST_STREAM with INTERNAL_ERROR. grpc-go maps that reset to native INTERNAL; it can arrive before the client's local deadline callback runs. The hard cutoff is unchanged. A writable application service timeout still requires DEADLINE_EXCEEDED with request_timeout. The raw client-deadline/zero-window test requires timeout observation, the native reset, and capacity release at the earlier bound. The generated-client edge accepts INTERNAL only for this exact reset text, with the local deadline already reached, matching timeout observation, and released capacity. Other INTERNAL failures still fail the test. This is the specification's native-transport-failure exception, not a universal guarantee of wire DEADLINE_EXCEEDED at forced abort. It is separate from the trailer-only close-cause limit above.

Validation also reproduced two pre-existing test issues on the clean phase 1 revision. The budget-resume test can fail under repeated race runs; it is unchanged. The Python synthetic-client test used timeout=0 on a new channel and could return either CANCELLED or DEADLINE_EXCEEDED. The active test now waits for channel readiness and uses a positive deadline on the existing blocking fixture, still requiring DEADLINE_EXCEEDED. Historical phase 1 evidence is preserved. Baseline proofs, initial failures and final checks are stored in the phase 2 evidence directory.

The hard transport cutoff does not abandon application work that has not returned. Such work keeps its slot and shutdown ownership until it finishes, even after cancellation closes the stream. Go cannot forcibly stop a goroutine. This preserves the concurrency limit when a repository lock or cleanup briefly outlives cancellation; the shutdown owner still returns at its configured deadline. A permanently stuck dependency cannot be killed by Go and may require process exit.
