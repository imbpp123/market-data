package grpctransport

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"market-data/internal/application"
)

func TestRPCFailureAndPanicAreSanitized(t *testing.T) {
	cases := []struct {
		name     string
		list     func(context.Context) error
		code     codes.Code
		reason   string
		panicked bool
	}{
		{"repository failure", func(context.Context) error { return errors.New("secret body") }, codes.Internal, "internal_error", false},
		{"panic", func(context.Context) error { panic("secret panic") }, codes.Internal, "internal_error", true},
		{"application failure", func(context.Context) error { return application.ErrIncompleteData }, codes.FailedPrecondition, "incomplete_data", false},
		{"service deadline", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, codes.DeadlineExceeded, "request_timeout", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := testSettings()
			if tc.name == "service deadline" {
				settings.SnapshotTimeout = time.Second
			}
			complete := make(chan Event, 1)
			server, address := startServer(t, (&fakeReaders{list: tc.list}).readers(), settings, func(context.Context, string) func(Event) { return func(event Event) { complete <- event } })
			_, err := client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{})
			require.Equal(t, tc.code, status.Code(err))
			assert.NotContains(t, err.Error(), "secret")
			event := <-complete
			assert.Equal(t, tc.reason, event.Reason)
			assert.Equal(t, tc.panicked, event.Panic)
			assert.Empty(t, server.snapshots)
		})
	}
}

func TestNativeRPCFailures(t *testing.T) {
	cases := []struct {
		name string
		run  func(context.Context, *grpc.ClientConn) error
		code codes.Code
	}{
		{"unknown method", func(ctx context.Context, c *grpc.ClientConn) error {
			return c.Invoke(ctx, "/unknown.Service/Method", &pb.ListTickersRequest{}, &pb.ListTickersResponse{})
		}, codes.Unimplemented},
		{"message too large", func(ctx context.Context, c *grpc.ClientConn) error {
			return c.Invoke(ctx, pb.MarketDataService_ListTickers_FullMethodName, &pb.ListTickersRequest{Symbol: proto.String(string(make([]byte, 9000)))}, &pb.ListTickersResponse{})
		}, codes.ResourceExhausted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, address := startServer(t, (&fakeReaders{}).readers(), testSettings(), nil)
			connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer func() { _ = connection.Close() }()
			err = tc.run(t.Context(), connection)
			assert.Equal(t, tc.code, status.Code(err))
			assert.Empty(t, status.Convert(err).Details())
		})
	}
}

func TestClientCancellationReleasesCapacity(t *testing.T) {
	entered := make(chan struct{})
	complete := make(chan Event, 1)
	server, address := startServer(t, (&fakeReaders{list: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }}).readers(), testSettings(), func(context.Context, string) func(Event) { return func(event Event) { complete <- event } })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := client(t, address).ListTickers(ctx, &pb.ListTickersRequest{}); done <- err }()
	<-entered
	cancel()
	assert.Equal(t, codes.Canceled, status.Code(<-done))
	<-complete
	assert.Empty(t, server.snapshots)
}

func TestClientTimeoutParsing(t *testing.T) {
	cases := []struct {
		input    string
		duration time.Duration
		valid    bool
	}{{"1H", time.Hour, true}, {"12M", 12 * time.Minute, true}, {"9S", 9 * time.Second, true}, {"3m", 3 * time.Millisecond, true}, {"4u", 4 * time.Microsecond, true}, {"0n", 0, true}, {"99999999H", time.Duration(1<<63 - 1), true}, {"", 0, false}, {"-1S", 0, false}, {"100000000S", 0, false}, {"1x", 0, false}, {"+1S", 0, false}}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			duration, ok := clientTimeout(tc.input)
			assert.Equal(t, tc.valid, ok)
			assert.Equal(t, tc.duration, duration)
		})
	}
}
