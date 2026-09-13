package observability

import (
	"bytes"
	"context"
	"crypto/tls"
	"golang.org/x/net/http2"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"market-data/internal/testfixture/grpcapi"
	grpctransport "market-data/internal/transport/grpc"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestNativeRPCFailuresReachObserverAndExporter(t *testing.T) {
	cases := []struct{ name, path string }{{"unknown service", "/unknown.Service/Method"}, {"unknown method", "/marketdata.v1.MarketDataService/Missing"}, {"malformed path", "/malformed"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metrics := NewRPC()
			complete := make(chan grpctransport.Event, 1)
			fixture, err := grpcapi.New(t.Context(), nil)
			require.NoError(t, err)
			defer fixture.Service.Wait()
			server, err := grpctransport.NewServer(t.Context(), grpctransport.Readers{Instruments: fixture.InstrumentReader, Tickers: fixture.TickerReader, MarketStats: fixture.StatsReader, Klines: fixture.Service}, grpctransport.Settings{SnapshotTimeout: time.Second, KlineTimeout: time.Second, WriteGrace: time.Second, MaxSnapshots: 1, MaxKlines: 1, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: time.Second, IdleTimeout: time.Minute}, func(ctx context.Context, method string) func(grpctransport.Event) {
				finish := metrics.Start(ctx, method)
				return func(event grpctransport.Event) { finish(event); complete <- event }
			})
			require.NoError(t, err)
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() { _ = server.Close(); <-done }()
			connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer func() { _ = connection.Close() }()
			err = connection.Invoke(t.Context(), tc.path, &pb.ListTickersRequest{}, &pb.ListTickersResponse{})
			assert.Equal(t, codes.Unimplemented, status.Code(err))
			assert.Empty(t, status.Convert(err).Details())
			event := <-complete
			assert.Equal(t, codes.Unimplemented, event.Code)
			assert.Equal(t, "transport_error", event.Reason)
			assert.True(t, event.TransportFailure)
			assert.Contains(t, metrics.Samples(), Sample{Name: "rpc_total", Labels: map[string]string{"method": "unknown", "status": "Unimplemented", "reason": "transport_error"}, Value: 1})
		})
	}
}

func TestNativeHTTPFailuresReachObserverAndExporter(t *testing.T) {
	cases := []struct {
		name       string
		header     http.Header
		httpStatus int
		code       codes.Code
	}{
		{"malformed timeout", http.Header{"Content-Type": {"application/grpc"}, "Grpc-Timeout": {"bad"}}, http.StatusBadRequest, codes.Internal},
		{"malformed binary metadata", http.Header{"Content-Type": {"application/grpc"}, "Broken-Bin": {"%%%"}}, http.StatusBadRequest, codes.Internal},
		{"wrong content type", http.Header{"Content-Type": {"text/plain"}}, http.StatusUnsupportedMediaType, codes.Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metrics := NewRPC()
			complete := make(chan grpctransport.Event, 1)
			fixture, err := grpcapi.New(t.Context(), nil)
			require.NoError(t, err)
			defer fixture.Service.Wait()
			server, err := grpctransport.NewServer(t.Context(), grpctransport.Readers{Instruments: fixture.InstrumentReader, Tickers: fixture.TickerReader, MarketStats: fixture.StatsReader, Klines: fixture.Service}, grpctransport.Settings{SnapshotTimeout: time.Second, KlineTimeout: time.Second, WriteGrace: time.Second, MaxSnapshots: 1, MaxKlines: 1, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: time.Second, IdleTimeout: time.Minute}, func(ctx context.Context, method string) func(grpctransport.Event) {
				finish := metrics.Start(ctx, method)
				return func(event grpctransport.Event) { finish(event); complete <- event }
			})
			require.NoError(t, err)
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() { _ = server.Close(); <-done }()
			transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}}
			defer transport.CloseIdleConnections()
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+listener.Addr().String()+pb.MarketDataService_ListTickers_FullMethodName, bytes.NewReader(make([]byte, 5)))
			require.NoError(t, err)
			request.Header = tc.header
			response, err := transport.RoundTrip(request)
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			assert.Equal(t, tc.httpStatus, response.StatusCode)
			event := <-complete
			assert.Equal(t, tc.code, event.Code)
			assert.Equal(t, "transport_error", event.Reason)
			assert.True(t, event.TransportFailure)
			assert.Contains(t, metrics.Samples(), Sample{Name: "rpc_total", Labels: map[string]string{"method": "ListTickers", "status": tc.code.String(), "reason": "transport_error"}, Value: 1})
		})
	}
}
