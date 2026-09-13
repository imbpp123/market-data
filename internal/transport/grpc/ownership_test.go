package grpctransport

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

// The channel marks both HTTP handler return and stream closure. Application
// cleanup and serialization can still be active after this transport boundary.
func ownershipServer(t *testing.T, readers Readers, options ...grpc.ServerOption) (*Server, string, <-chan struct{}, <-chan Event) {
	t.Helper()
	complete := make(chan Event, 8)
	server, err := NewServer(t.Context(), readers, testSettings(), func(context.Context, string) func(Event) {
		return func(event Event) { complete <- event }
	})
	require.NoError(t, err)
	server.grpc.Stop()
	options = append([]grpc.ServerOption{grpc.UnaryInterceptor(server.intercept), grpc.StatsHandler(rpcStats{})}, options...)
	server.grpc = grpc.NewServer(options...)
	pb.RegisterMarketDataServiceServer(server.grpc, &handlers{readers: readers, maximum: testSettings().MaxResponseBytes})
	closed := make(chan struct{}, 8)
	server.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamClosed := w.(http.CloseNotifier).CloseNotify() //nolint:staticcheck // Observe the same pinned stream boundary as the production owner.
		forwarded, returned := make(chan bool), make(chan struct{})
		go func() { <-streamClosed; close(forwarded); <-returned; closed <- struct{}{} }()
		server.serveHTTP(&closureWriter{ResponseWriter: w, notify: forwarded}, r)
		close(returned)
	})
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { require.NoError(t, server.Close()); <-done; server.owned.Wait() })
	return server, listener.Addr().String(), closed, complete
}

func TestCanceledRPCKeepsCapacityThroughApplicationCleanup(t *testing.T) {
	entered, cleanup := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls, active atomic.Int64
	readers := (&fakeReaders{list: func(ctx context.Context) error {
		active.Add(1)
		defer active.Add(-1)
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(cleanup)
			<-release
			return ctx.Err()
		}
		return nil
	}}).readers()
	server, address, closed, complete := ownershipServer(t, readers)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, second := client(t, address), client(t, address)
	done := make(chan error, 1)
	go func() { _, err := first.ListTickers(ctx, &pb.ListTickersRequest{}); done <- err }()
	<-entered
	cancel()
	require.Equal(t, codes.Canceled, status.Code(<-done))
	<-cleanup
	<-closed

	_, err := second.ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Equal(t, int64(1), active.Load())
	assert.Len(t, server.snapshots, 1)
	assert.Equal(t, "service_overloaded", (<-complete).Reason)
	releaseOnce.Do(func() { close(release) })
	assert.Equal(t, codes.Canceled, (<-complete).Code)
	assert.Zero(t, active.Load())
	_, err = second.ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.NoError(t, err)
	assert.Equal(t, codes.OK, (<-complete).Code)
}

type blockedCodec struct {
	encoding.CodecV2
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockedCodec) Marshal(value any) (mem.BufferSlice, error) {
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.CodecV2.Marshal(value)
}

func TestCanceledRPCKeepsCapacityThroughNativeSerialization(t *testing.T) {
	codec := &blockedCodec{CodecV2: encoding.GetCodecV2("proto"), entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(codec.release) })
	server, address, closed, complete := ownershipServer(t, (&fakeReaders{}).readers(), grpc.ForceServerCodecV2(codec))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, second := client(t, address), client(t, address)
	done := make(chan error, 1)
	go func() { _, err := first.ListTickers(ctx, &pb.ListTickersRequest{}); done <- err }()
	<-codec.entered
	cancel()
	require.Equal(t, codes.Canceled, status.Code(<-done))
	<-closed

	_, err := second.ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Len(t, server.snapshots, 1)
	assert.Equal(t, "service_overloaded", (<-complete).Reason)
	releaseOnce.Do(func() { close(codec.release) })
	assert.Equal(t, codes.Canceled, (<-complete).Code)
	_, err = second.ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.NoError(t, err)
	assert.Equal(t, codes.OK, (<-complete).Code)
}

type delayedDispatch struct {
	rpcStats
	entered chan struct{}
	release chan struct{}
}

func (*delayedDispatch) HandleRPC(context.Context, stats.RPCStats) {}

func (d *delayedDispatch) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	close(d.entered)
	<-d.release
	return ctx
}

func TestCanceledRPCBeforeNativeDispatchCannotStartApplication(t *testing.T) {
	delay := &delayedDispatch{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(delay.release) })
	var calls atomic.Int64
	server, address, closed, complete := ownershipServer(t, (&fakeReaders{list: func(context.Context) error { calls.Add(1); return nil }}).readers(), grpc.StatsHandler(delay))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rpc := client(t, address)
	done := make(chan error, 1)
	go func() { _, err := rpc.ListTickers(ctx, &pb.ListTickersRequest{}); done <- err }()
	<-delay.entered
	cancel()
	require.Equal(t, codes.Canceled, status.Code(<-done))
	<-closed
	assert.Len(t, server.snapshots, 1)
	assert.Zero(t, calls.Load())
	releaseOnce.Do(func() { close(delay.release) })
	assert.Equal(t, codes.Canceled, (<-complete).Code)
	assert.Empty(t, server.snapshots)
	assert.Zero(t, calls.Load())
}

func TestClosedRPCFencePreventsApplicationDispatch(t *testing.T) {
	server := &Server{settings: testSettings()}
	state := &call{closed: true, deadline: time.Now().Add(time.Second)}
	ctx := context.WithValue(t.Context(), callKey{}, state)
	_, err := server.intercept(ctx, &pb.ListTickersRequest{}, nil, func(context.Context, any) (any, error) {
		t.Error("closed RPC entered application")
		return &pb.ListTickersResponse{}, nil
	})
	assert.Equal(t, codes.Canceled, status.Code(err))
}

type closureWriter struct {
	http.ResponseWriter
	notify <-chan bool
}

func (w *closureWriter) CloseNotify() <-chan bool    { return w.notify }
func (w *closureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestStoppedNativeRuntimeDoesNotWaitForMissingProcessingEnd(t *testing.T) {
	complete := make(chan Event, 1)
	server, address := startServer(t, (&fakeReaders{}).readers(), testSettings(), func(context.Context, string) func(Event) {
		return func(event Event) { complete <- event }
	})
	server.grpc.Stop()
	_, err := client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.Error(t, err)
	event := <-complete
	assert.NotEqual(t, codes.OK, event.Code)
	assert.True(t, event.TransportFailure)
	assert.Empty(t, server.snapshots)
}

func TestHTTPFailureCode(t *testing.T) {
	cases := []struct {
		status int
		code   codes.Code
	}{
		{400, codes.Internal}, {401, codes.Unauthenticated}, {403, codes.PermissionDenied}, {404, codes.Unimplemented},
		{429, codes.Unavailable}, {502, codes.Unavailable}, {503, codes.Unavailable}, {504, codes.Unavailable},
		{415, codes.Unknown}, {200, codes.Unknown},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			assert.Equal(t, tc.code, httpFailureCode(tc.status))
		})
	}
}

func TestShutdownDeadlineDoesNotReleaseActiveApplicationCapacity(t *testing.T) {
	entered, cleanup := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server, address, closed, complete := ownershipServer(t, (&fakeReaders{list: func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(cleanup)
		<-release
		return ctx.Err()
	}}).readers())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rpc := client(t, address)
	done := make(chan error, 1)
	go func() { _, err := rpc.ListTickers(ctx, &pb.ListTickersRequest{}); done <- err }()
	<-entered
	cancel()
	require.Equal(t, codes.Canceled, status.Code(<-done))
	<-cleanup
	<-closed
	shutdown, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()
	require.ErrorIs(t, server.Shutdown(shutdown), context.DeadlineExceeded)
	assert.Len(t, server.snapshots, 1)
	require.NoError(t, server.Close())
	assert.Len(t, server.snapshots, 1)
	releaseOnce.Do(func() { close(release) })
	assert.Equal(t, codes.Canceled, (<-complete).Code)
	assert.Empty(t, server.snapshots)
}
