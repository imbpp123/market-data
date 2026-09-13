package grpctransport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func headersOnly(t *testing.T, address string, extra ...hpack.HeaderField) (net.Conn, *http2.Framer) {
	t.Helper()
	return headersWithWindow(t, address, 65535, extra...)
}

func headersWithWindow(t *testing.T, address string, window uint32, extra ...hpack.HeaderField) (net.Conn, *http2.Framer) {
	t.Helper()
	connection, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	require.NoError(t, connection.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = connection.Write([]byte(http2.ClientPreface))
	require.NoError(t, err)
	framer := http2.NewFramer(connection, connection)
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	require.NoError(t, framer.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: window}))
	var buffer bytes.Buffer
	encoder := hpack.NewEncoder(&buffer)
	fields := []hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "http"}, {Name: ":authority", Value: address}, {Name: ":path", Value: pb.MarketDataService_ListTickers_FullMethodName}, {Name: "content-type", Value: "application/grpc"}, {Name: "te", Value: "trailers"}}
	for _, field := range append(fields, extra...) {
		require.NoError(t, encoder.WriteField(field))
	}
	require.NoError(t, framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: buffer.Bytes(), EndHeaders: true}))
	return connection, framer
}

func rawStatus(t *testing.T, framer *http2.Framer) http.Header {
	t.Helper()
	fields := make(http.Header)
	for {
		frame, err := framer.ReadFrame()
		require.NoError(t, err)
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				require.NoError(t, framer.WriteSettingsAck())
			}
		case *http2.MetaHeadersFrame:
			for _, field := range frame.Fields {
				fields.Add(field.Name, field.Value)
			}
			if frame.StreamEnded() {
				return fields
			}
		}
	}
}

func assertRawReason(t *testing.T, header http.Header, code codes.Code, reason string) {
	t.Helper()
	encoded, err := base64.RawStdEncoding.DecodeString(header.Get("Grpc-Status-Details-Bin"))
	require.NoError(t, err)
	var result statuspb.Status
	require.NoError(t, proto.Unmarshal(encoded, &result))
	require.Equal(t, int32(code), result.Code)
	require.Len(t, result.Details, 1)
	var detail pb.ErrorDetail
	require.NoError(t, result.Details[0].UnmarshalTo(&detail))
	assert.Equal(t, reason, detail.Reason)
}

func TestOverloadRejectsHeadersWithoutReadingBody(t *testing.T) {
	entered := make(chan struct{})
	completed := make(chan Event, 2)
	server, address := startServer(t, (&fakeReaders{list: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }}).readers(), testSettings(), func(context.Context, string) func(Event) { return func(event Event) { completed <- event } })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rpc := client(t, address)
	done := make(chan error, 1)
	go func() { _, err := rpc.ListTickers(ctx, &pb.ListTickersRequest{}); done <- err }()
	<-entered
	_, framer := headersOnly(t, address)
	header := rawStatus(t, framer)
	assertRawReason(t, header, codes.ResourceExhausted, "service_overloaded")
	event := <-completed
	assert.Equal(t, "service_overloaded", event.Reason)
	assert.Len(t, server.snapshots, 1)
	cancel()
	assert.Equal(t, codes.Canceled, status.Code(<-done))
	<-completed
	assert.Empty(t, server.snapshots)
}

func TestHeaderLimitRejectsWithoutReadingBody(t *testing.T) {
	completed := make(chan Event, 1)
	server, address := startServer(t, (&fakeReaders{list: func(context.Context) error { t.Error("Rejected request reached application"); return nil }}).readers(), testSettings(), func(context.Context, string) func(Event) { return func(event Event) { completed <- event } })
	server.settings.MaxHeaderBytes = 100
	_, framer := headersOnly(t, address)
	assertRawReason(t, rawStatus(t, framer), codes.InvalidArgument, "request_too_large")
	event := <-completed
	assert.Equal(t, "request_too_large", event.Reason)
	assert.Empty(t, server.snapshots)
}

type countedRequestBody struct {
	io.ReadCloser
	count *atomic.Int64
}

func (b countedRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.count.Add(int64(n))
	return n, err
}

func TestUnaryTailCannotReachRuntimeOrApplication(t *testing.T) {
	completed := make(chan Event, 1)
	var calls, read atomic.Int64
	settings := testSettings()
	server, err := NewServer(t.Context(), (&fakeReaders{list: func(context.Context) error { calls.Add(1); return nil }}).readers(), settings, func(context.Context, string) func(Event) { return func(event Event) { completed <- event } })
	require.NoError(t, err)
	handler := server.http.Handler
	server.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = countedRequestBody{r.Body, &read}
		handler.ServeHTTP(w, r)
	})
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-done; server.owned.Wait() })
	_, framer := headersOnly(t, listener.Addr().String())
	// A valid empty message followed by unbounded potential DATA. The peer keeps
	// the stream open; rejection must not wait for END_STREAM or the work timer.
	require.NoError(t, framer.WriteData(1, false, make([]byte, 5)))
	require.NoError(t, framer.WriteData(1, false, make([]byte, 16384)))
	header := rawStatus(t, framer)
	assert.Equal(t, "3", header.Get("Grpc-Status"))
	event := <-completed
	assert.Equal(t, codes.InvalidArgument, event.Code)
	assert.Zero(t, calls.Load())
	assert.Equal(t, int64(6), read.Load())
	assert.Empty(t, server.snapshots)
}

func TestPartialUnaryBodyUsesWorkDeadlineAndCancellation(t *testing.T) {
	cases := []struct {
		name   string
		cancel bool
		prefix []byte
		code   codes.Code
	}{{"missing body", false, nil, codes.DeadlineExceeded}, {"partial prefix", false, []byte{0}, codes.DeadlineExceeded}, {"root cancellation", true, nil, codes.Canceled}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, cancel := context.WithCancel(t.Context())
			defer cancel()
			admitted := make(chan struct{})
			completed := make(chan Event, 1)
			settings := testSettings()
			settings.SnapshotTimeout = time.Second
			server, err := NewServer(root, (&fakeReaders{list: func(context.Context) error { t.Error("Partial request reached application"); return nil }}).readers(), settings, func(context.Context, string) func(Event) {
				close(admitted)
				return func(event Event) { completed <- event }
			})
			require.NoError(t, err)
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close(); <-done; server.owned.Wait() })
			_, framer := headersOnly(t, listener.Addr().String())
			if tc.prefix != nil {
				require.NoError(t, framer.WriteData(1, false, tc.prefix))
			}
			<-admitted
			if tc.cancel {
				cancel()
			} else {
				assertRawReason(t, rawStatus(t, framer), codes.DeadlineExceeded, "request_timeout")
			}
			event := <-completed
			assert.Equal(t, tc.code, event.Code)
			assert.Empty(t, server.snapshots)
		})
	}
}

func TestRootAndClientCancellationKeepCanceledObservation(t *testing.T) {
	cases := []struct {
		name string
		root bool
	}{{"root", true}, {"client", false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, stop := context.WithCancel(t.Context())
			defer stop()
			entered := make(chan struct{})
			completed := make(chan Event, 1)
			server, err := NewServer(root, (&fakeReaders{list: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }}).readers(), testSettings(), func(context.Context, string) func(Event) { return func(event Event) { completed <- event } })
			require.NoError(t, err)
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)
			served := make(chan error, 1)
			go func() { served <- server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close(); <-served; server.owned.Wait() })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rpc := client(t, listener.Addr().String())
			done := make(chan error, 1)
			go func() { _, err := rpc.ListTickers(ctx, &pb.ListTickersRequest{}); done <- err }()
			<-entered
			if tc.root {
				stop()
			} else {
				cancel()
			}
			require.Error(t, <-done)
			event := <-completed
			assert.Equal(t, codes.Canceled, event.Code)
			assert.Equal(t, "request_canceled", event.Reason)
			assert.ErrorIs(t, event.Error, context.Canceled)
		})
	}
}

func TestReadUnaryFrameBounds(t *testing.T) {
	cases := []struct {
		name   string
		length int
		extra  bool
		code   codes.Code
	}{{"at maximum", 8192, false, codes.OK}, {"above maximum", 8193, false, codes.ResourceExhausted}, {"second message", 0, true, codes.InvalidArgument}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := make([]byte, 5+tc.length)
			binary.BigEndian.PutUint32(body[1:], uint32(tc.length))
			if tc.extra {
				body = append(body, make([]byte, 5)...)
			}
			result, err := readUnary(bytes.NewReader(body), 8192)
			assert.Equal(t, tc.code, status.Code(err))
			if err == nil {
				assert.Equal(t, body, result)
			}
		})
	}
}

func TestClientDeadlineHasTimeoutObservation(t *testing.T) {
	entered := make(chan struct{})
	completed := make(chan Event, 1)
	server, address := startServer(t, (&fakeReaders{list: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }}).readers(), testSettings(), func(context.Context, string) func(Event) { return func(event Event) { completed <- event } })
	_, framer := headersOnly(t, address, hpack.HeaderField{Name: "grpc-timeout", Value: "1S"})
	require.NoError(t, framer.WriteData(1, true, make([]byte, 5)))
	<-entered
	event := <-completed
	assert.Equal(t, codes.DeadlineExceeded, event.Code)
	assert.Equal(t, "request_timeout", event.Reason)
	assert.Empty(t, server.snapshots)
}

func TestEarlierClientDeadlineAbortsFlowControlledSend(t *testing.T) {
	completed := make(chan Event, 1)
	settings := testSettings()
	server, address := startServer(t, largeReaders().readers(), settings, func(context.Context, string) func(Event) {
		return func(event Event) { completed <- event }
	})
	_, framer := headersWithWindow(t, address, 0, hpack.HeaderField{Name: "grpc-timeout", Value: "1S"})
	require.NoError(t, framer.WriteData(1, true, make([]byte, 5)))
	for {
		frame, err := framer.ReadFrame()
		require.NoError(t, err)
		if settings, ok := frame.(*http2.SettingsFrame); ok && !settings.IsAck() {
			require.NoError(t, framer.WriteSettingsAck())
		}
		if _, ok := frame.(*http2.MetaHeadersFrame); ok {
			break
		}
	}
	require.Len(t, server.snapshots, 1)
	for {
		frame, err := framer.ReadFrame()
		require.NoError(t, err)
		if reset, ok := frame.(*http2.RSTStreamFrame); ok && reset.StreamID == 1 {
			assert.Equal(t, http2.ErrCodeInternal, reset.ErrCode)
			break
		}
	}
	event := <-completed
	assert.Equal(t, codes.DeadlineExceeded, event.Code)
	assert.Equal(t, "request_timeout", event.Reason)
	assert.True(t, event.TransportFailure)
	assert.GreaterOrEqual(t, event.Duration, time.Second)
	assert.Less(t, event.Duration, settings.SnapshotTimeout)
	assert.Empty(t, server.snapshots)
}
