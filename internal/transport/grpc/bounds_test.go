package grpctransport

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"market-data/internal/application"
)

func TestRequestMessageExactBoundaries(t *testing.T) {
	cases := []struct {
		name string
		size int
		code codes.Code
	}{{"below", 31, codes.OK}, {"at", 32, codes.OK}, {"above", 33, codes.ResourceExhausted}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := testSettings()
			settings.MaxRequestBytes = 32
			_, address := startServer(t, (&fakeReaders{}).readers(), settings, nil)
			request := &pb.ListTickersRequest{}
			unknown := protowire.AppendTag(nil, 100, protowire.BytesType)
			unknown = protowire.AppendBytes(unknown, make([]byte, tc.size-3))
			request.ProtoReflect().SetUnknown(unknown)
			_, err := client(t, address).ListTickers(t.Context(), request)
			assert.Equal(t, tc.code, status.Code(err))
		})
	}
}

func TestHeaderLimitExactBoundaries(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), "POST", "http://fixture"+pb.MarketDataService_ListTickers_FullMethodName, nil)
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("Te", "trailers")
	size := headerBytes(request)
	// The raw client emits exactly these headers; authority length differs by address.
	cases := []struct {
		name    string
		delta   int
		failure bool
	}{{"below", 1, false}, {"at", 0, false}, {"above", -1, true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			complete := make(chan Event, 1)
			settings := testSettings()
			settings.MaxHeaderBytes = 1024
			server, address := startServer(t, (&fakeReaders{}).readers(), settings, func(context.Context, string) func(Event) { return func(e Event) { complete <- e } })
			server.settings.MaxHeaderBytes = size - len("fixture") + len(address) + tc.delta
			_, framer := rawRequest(t, address, 65535, nil)
			for {
				frame, err := framer.ReadFrame()
				require.NoError(t, err)
				if settings, ok := frame.(*http2.SettingsFrame); ok && !settings.IsAck() {
					require.NoError(t, framer.WriteSettingsAck())
				}
				if headers, ok := frame.(*http2.HeadersFrame); ok && headers.StreamEnded() {
					break
				}
			}
			event := <-complete
			if tc.failure {
				assert.Equal(t, "request_too_large", event.Reason)
			} else {
				assert.Equal(t, codes.OK, event.Code)
			}
		})
	}
}

func TestMalformedWireReturnsNativeFailure(t *testing.T) {
	completed := make(chan Event, 1)
	_, address := startServer(t, (&fakeReaders{}).readers(), testSettings(), func(context.Context, string) func(Event) { return func(e Event) { completed <- e } })
	_, framer := rawRequest(t, address, 65535, []byte{0xff})
	for {
		frame, err := framer.ReadFrame()
		require.NoError(t, err)
		if settings, ok := frame.(*http2.SettingsFrame); ok && !settings.IsAck() {
			require.NoError(t, framer.WriteSettingsAck())
		}
		if headers, ok := frame.(*http2.HeadersFrame); ok && headers.StreamEnded() {
			break
		}
	}
	event := <-completed
	assert.Equal(t, codes.Internal, event.Code)
	assert.True(t, event.TransportFailure)
}

func TestDeadlineIsCheckedBeforeValidation(t *testing.T) {
	server := &Server{settings: testSettings()}
	called := false
	state := &call{deadline: time.Now().Add(-time.Second)}
	ctx := context.WithValue(t.Context(), callKey{}, state)
	_, err := server.intercept(ctx, &pb.ListTickersRequest{}, nil, func(context.Context, any) (any, error) { called = true; return nil, application.ErrInvalidFilter })
	assertReason(t, err, codes.DeadlineExceeded, "request_timeout")
	assert.False(t, called)
}

func TestEarlierClientDeadlineWins(t *testing.T) {
	entered := make(chan struct{})
	completed := make(chan Event, 1)
	server, address := startServer(t, (&fakeReaders{list: func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}).readers(), testSettings(), func(context.Context, string) func(Event) {
		return func(event Event) { completed <- event }
	})
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	done := make(chan error, 1)
	go func() {
		_, err := pb.NewMarketDataServiceClient(connection).ListTickers(ctx, &pb.ListTickersRequest{})
		done <- err
	}()
	<-entered
	err = <-done
	receivedAt := time.Now()
	event := <-completed

	if status.Code(err) == codes.Internal {
		// The hard HTTP/2 write cutoff can beat the local gRPC timer. This
		// exact native reset is permitted only at the already reached bound.
		assert.Equal(t, "stream terminated by RST_STREAM with error code: INTERNAL_ERROR", status.Convert(err).Message())
		assert.False(t, receivedAt.Before(deadline), "native abort preceded the client deadline")
		assert.Empty(t, status.Convert(err).Details())
	} else {
		assert.Equal(t, codes.DeadlineExceeded, status.Code(err), "%v", err)
	}
	assert.Equal(t, codes.DeadlineExceeded, event.Code)
	assert.Equal(t, "request_timeout", event.Reason)
	assert.Empty(t, server.snapshots)
}
