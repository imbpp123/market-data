# gRPC transport implementation

This note describes the runtime decisions behind the [gRPC contract](grpc-migration-specification.md). The implementation is in [server.go](../internal/transport/grpc/server.go) and [request.go](../internal/transport/grpc/request.go). For API use, see the [development guide](development.md#instruments).

## Request bounds

The HTTP wrapper checks headers and acquires a process-wide snapshot or candle slot before reading the body. Overload and header rejection send a rich status without waiting for a body. Accepted requests read exactly one unary frame and require EOF before application dispatch. The framing limit includes five prefix bytes and at most one byte of lookahead. The native runtime receives only the bounded in-memory body; unknown methods receive no live body. Partial bodies expire at the application or earlier client deadline, and root cancellation unblocks reads.

## Send ownership

The server uses pinned grpc-go 1.76.0 `ServeHTTP` with Go 1.27.1 `net/http` unencrypted HTTP/2. Native grpc-go `stats.End` can run while response data remains queued, so it does not release a caller slot. The HTTP wrapper acquires one process-wide snapshot or candle slot before decoding and validation. An interceptor applies the application deadline measured from that admission. The earlier client deadline wins. Incremental row conversion and final Protobuf size checks run inside the slot.

`ResponseController.SetWriteDeadline` sets the absolute admission + request timeout + write grace bound; an earlier client deadline reduces it. Cancellation aborts pending writes. The wrapper captures `CloseNotify` while the response writer is valid, then transfers its slot to one owned completion goroutine. That goroutine releases the slot only after both HTTP/2 stream closure and native RPC processing completion. The synchronous ServeHTTP `stats.ConnBegin` hook arms an expected `stats.End` for a known unary method before native dispatch. End covers application cleanup, conversion and native serialization; stream closure covers the send. Setup failures, rejected admission and unknown methods do not arm an End wait. A closed-call fence prevents late application dispatch. The write deadline stays armed for final trailers. HTTP handler return and request-context cancellation are not used as completion signals. The cancellation callback is stopped and joined before the response writer can be reused.

Real TCP tests use an HTTP/2 client with a zero stream window and no WINDOW_UPDATE. It receives response HEADERS after the unary handler has returned. A second channel is rejected while the response is blocked. Reset, forced close, and the absolute write deadline release capacity after native processing also finishes. The deadline case uses one second of request time plus one second of grace and completes without client reads. These tests verify resource ownership on the pinned runtimes.

There are two runtime trade-offs. grpc-go marks ServeHTTP experimental. CloseNotifier is deprecated, but its pinned HTTP/2 implementation signals stream closure; Request.Context closes earlier and cannot replace it. Changes to either runtime require these flow-control tests again. Do not use grpc.GracefulStop for this transport: handler transport Drain is unsupported. The HTTP servers own graceful shutdown and connection closure; grpc.Stop closes remaining handler transports.

The hard transport cutoff does not abandon application work that has not returned. Such work keeps its slot and shutdown ownership until it finishes, even after cancellation closes the stream. Go cannot forcibly stop a goroutine. This preserves the concurrency limit when a repository lock or cleanup briefly outlives cancellation; the shutdown owner still returns at its configured deadline. A permanently stuck dependency cannot be killed by Go and may require process exit.

## Observed outcome boundary

RPC metrics record bounded method/status/reason labels, active calls, encoded message bytes and duration through processing completion and stream closure, including cleanup that continues after stream abort. Traces, sanitized errors and panic reporting use the existing telemetry boundary. Statistics reach the existing debug and Prometheus handlers. Status means the application or native status submitted to local transport, corrected for observed DATA/Flush errors. CloseNotify does not expose a close cause. A trailer-only failure after the HTTP handler returns cannot be distinguished from normal closure by these supported hooks. No metric confirms remote application consumption. Capacity remains owned until processing completion and closure in both cases. Native HTTP setup failures without gRPC status use the standard HTTP-to-gRPC fallback in observations.

Expected invalid input, missing symbols, unready snapshots, cancellation, and unsupported methods appear in RPC metrics without Sentry error reports. Internal failures, upstream errors, timeouts, and panics retain error reporting. Cancellation before the hard write deadline keeps its cancellation cause; aborting a writer must not relabel it as a timeout.

## Native deadline failures

At a hard transport deadline, Go 1.27 HTTP/2 sends RST_STREAM with INTERNAL_ERROR. grpc-go maps that reset to native INTERNAL; it can arrive before the client's local deadline callback runs. The hard cutoff is unchanged. A writable application service timeout still requires DEADLINE_EXCEEDED with request_timeout. The raw client-deadline/zero-window test requires timeout observation, the native reset, and capacity release at the earlier bound. The generated-client edge accepts INTERNAL only for this exact reset text, with the local deadline already reached, matching timeout observation, and released capacity. Other INTERNAL failures still fail the test. This is the specification's native-transport-failure exception, not a universal guarantee of wire DEADLINE_EXCEEDED at forced abort. It is separate from the trailer-only close-cause limit above.

The [installed-client deadline test](../scripts/api/test_contract.py) separately handles a native `CANCELLED` reset after deadline expiry, with the exact text `Received RST_STREAM with error code 8`. This exception requires the local deadline to have elapsed. Application timeout mapping remains strict.

## Validation

Runtime upgrades must rerun the flow-control, ownership, deadline, and shutdown regressions:

- [Slow-reader tests](../internal/transport/grpc/server_test.go) block HTTP/2 DATA and verify retained capacity, reset, and the absolute write deadline.
- [Ownership tests](../internal/transport/grpc/ownership_test.go) cover application cleanup, native serialization, delayed dispatch, stopped runtimes, and shutdown deadlines.
- [Input and transport regressions](../internal/transport/grpc/review_regression_test.go) cover bounded unary bodies, rejection without body reads, cancellation causes, and native deadline resets.
- [Transport tests](../internal/transport/grpc) and [observability tests](../internal/infrastructure/observability) cover native status mapping, HTTP setup failures, and expected-error reporting.

Run the [project checks](development.md#checks), including installed-client checks, when changing these boundaries. Keep generated check output outside tracked documentation.
