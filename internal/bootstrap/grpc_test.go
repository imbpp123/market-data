package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/ticker"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/observability"
	"market-data/internal/testfixture/grpcapi"
	grpctransport "market-data/internal/transport/grpc"
	httptransport "market-data/internal/transport/http"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func dualFixture(t *testing.T) (*localState, context.Context, context.CancelFunc, *grpctransport.Server, *http.Server) {
	t.Helper()
	root, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	state, err := newLocalState(1000, func() time.Time { return grpcapi.Now })
	require.NoError(t, err)
	fixture, err := grpcapi.New(root, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cancel(); fixture.Service.Wait() })
	settings := grpctransport.Settings{SnapshotTimeout: time.Second, KlineTimeout: time.Second, WriteGrace: time.Second, MaxSnapshots: 1, MaxKlines: 1, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: time.Second, IdleTimeout: time.Minute}
	data, err := grpctransport.NewServer(root, grpctransport.Readers{Instruments: fixture.InstrumentReader, Tickers: fixture.TickerReader, MarketStats: fixture.StatsReader, Klines: fixture.Service}, settings, nil)
	require.NoError(t, err)
	operations := &http.Server{Handler: httptransport.NewHandler(state.ready.Load, 8192), BaseContext: func(net.Listener) context.Context { return root }}
	return state, root, cancel, data, operations
}

func TestDualListenerBindFailureCleansOwnedResources(t *testing.T) {
	cases := []struct {
		name    string
		failure int
	}{{"first listener", 1}, {"second listener", 2}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, ctx, cancel, data, operations := dualFixture(t)
			failure := errors.New("bind failed")
			var listener net.Listener
			calls := 0
			listen := func(ctx context.Context, network, address string) (net.Listener, error) {
				calls++
				if calls == tc.failure {
					return nil, failure
				}
				var err error
				listener, err = (&net.ListenConfig{}).Listen(ctx, network, address)
				return listener, err
			}
			var workers atomic.Int64
			err := state.runDual(ctx, cancel, time.Second, "127.0.0.1:0", "127.0.0.1:0", data, operations, listen, func(context.Context) error { workers.Add(1); return nil })
			assert.ErrorIs(t, err, failure)
			assert.False(t, state.ready.Load())
			assert.Zero(t, workers.Load())
			if listener != nil {
				_, err = (&net.Dialer{}).DialContext(t.Context(), "tcp", listener.Addr().String())
				assert.Error(t, err)
			}
		})
	}
}

func TestDualListenerServeFailureStopsBothServersAndWorker(t *testing.T) {
	state, ctx, cancel, data, operations := dualFixture(t)
	listeners := make(chan net.Listener, 2)
	listen := func(ctx context.Context, network, address string) (net.Listener, error) {
		l, err := (&net.ListenConfig{}).Listen(ctx, network, address)
		if err == nil {
			listeners <- l
		}
		return l, err
	}
	entered, stopped := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- state.runDual(ctx, cancel, time.Second, "127.0.0.1:0", "127.0.0.1:0", data, operations, listen, func(ctx context.Context) error { close(entered); <-ctx.Done(); close(stopped); return nil })
	}()
	dataListener, httpListener := <-listeners, <-listeners
	<-entered
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+httpListener.Addr().String()+"/ready", nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	assert.Equal(t, 200, response.StatusCode)
	_ = response.Body.Close()
	require.NoError(t, dataListener.Close())
	require.Error(t, <-done)
	<-stopped
	assert.False(t, state.ready.Load())
	_, err = (&net.Dialer{}).DialContext(t.Context(), "tcp", httpListener.Addr().String())
	assert.Error(t, err)
}

func TestDualShutdownUsesOneDeadlineForServersAndWorkers(t *testing.T) {
	state, ctx, cancel, data, operations := dualFixture(t)
	entered, release, workerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- state.runDual(ctx, cancel, 20*time.Millisecond, "127.0.0.1:0", "127.0.0.1:0", data, operations, (&net.ListenConfig{}).Listen, func(context.Context) error { close(entered); <-release; close(workerDone); return nil })
	}()
	<-entered
	cancel()
	err := <-done
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, state.ready.Load())
	close(release)
	<-workerDone
}

func TestGRPCCompositionServesOperationsAndMetrics(t *testing.T) {
	cfg := config.Defaults()
	cfg.Observability.Prometheus.Enabled = true
	cfg.Observability.Stats.EndpointEnabled = true
	state, err := newLocalState(1000, time.Now)
	require.NoError(t, err)
	state.exchanges, err = newExchangeClients(cfg, http.DefaultTransport, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
	// Exchange construction requires an explicit clock. No collectors are started.
	require.NoError(t, err)
	root, cancel := context.WithCancel(t.Context())
	defer cancel()
	listeners := make(chan net.Listener, 2)
	listen := func(ctx context.Context, network, address string) (net.Listener, error) {
		l, err := (&net.ListenConfig{}).Listen(ctx, network, address)
		if err == nil {
			listeners <- l
		}
		return l, err
	}
	settings := grpcSettings{Address: "127.0.0.1:0", HTTPAddress: "127.0.0.1:0", Transport: grpctransport.Settings{SnapshotTimeout: time.Second, KlineTimeout: time.Second, WriteGrace: time.Second, MaxSnapshots: 1, MaxKlines: 1, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: time.Second, IdleTimeout: time.Minute}}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- state.serveGRPC(root, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), settings, listen, func(ctx context.Context) error { close(started); <-ctx.Done(); return nil })
	}()
	dataListener, httpListener := <-listeners, <-listeners
	<-started
	connection, err := grpc.NewClient(dataListener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	_, err = pb.NewMarketDataServiceClient(connection).ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.Error(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+httpListener.Addr().String()+cfg.Observability.Prometheus.Path, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	require.NoError(t, err)
	assert.Contains(t, string(body), "rpc_active")
	assert.Contains(t, response.Header.Get("Content-Type"), "text/plain")
	cancel()
	require.NoError(t, <-done)
}

type waitingTickers struct {
	ticker.Repository
	entered chan struct{}
}

func (r *waitingTickers) List(ctx context.Context, _ application.SnapshotFilter) ([]domain.Ticker, error) {
	close(r.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestOperationalHTTPStaysAvailableDuringSaturatedRPCAndShutdown(t *testing.T) {
	cfg := config.Defaults()
	state, err := newLocalState(1000, time.Now)
	require.NoError(t, err)
	state.exchanges, err = newExchangeClients(cfg, http.DefaultTransport, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	waiting := &waitingTickers{Repository: state.tickers, entered: make(chan struct{})}
	state.tickers = waiting
	root, cancel := context.WithCancel(t.Context())
	defer cancel()
	listeners := make(chan net.Listener, 2)
	listen := func(ctx context.Context, network, address string) (net.Listener, error) {
		l, err := (&net.ListenConfig{}).Listen(ctx, network, address)
		if err == nil {
			listeners <- l
		}
		return l, err
	}
	settings := grpcSettings{Address: "127.0.0.1:0", HTTPAddress: "127.0.0.1:0", Transport: grpctransport.Settings{SnapshotTimeout: 5 * time.Second, KlineTimeout: 5 * time.Second, WriteGrace: time.Second, MaxSnapshots: 1, MaxKlines: 1, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: time.Second, IdleTimeout: time.Minute}}
	done := make(chan error, 1)
	go func() {
		done <- state.serveGRPC(root, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), settings, listen)
	}()
	dataListener, httpListener := <-listeners, <-listeners
	connection, err := grpc.NewClient(dataListener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := pb.NewMarketDataServiceClient(connection).ListTickers(t.Context(), &pb.ListTickersRequest{})
		rpcDone <- err
	}()
	<-waiting.entered
	for _, path := range []string{"/health", "/ready"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+httpListener.Addr().String()+path, nil)
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		assert.Equal(t, 200, response.StatusCode)
		_ = response.Body.Close()
	}
	cancel()
	require.NoError(t, <-done)
	require.Error(t, <-rpcDone)
	assert.False(t, state.ready.Load())
}

type rpcReportingTelemetry struct {
	telemetry
	errors []error
	panics int
}

func (t *rpcReportingTelemetry) Report(err error, _ map[string]string) {
	t.errors = append(t.errors, err)
}
func (t *rpcReportingTelemetry) Panic(map[string]string) { t.panics++ }

func TestRPCExpectedFailuresStayInMetricsWithoutErrorReports(t *testing.T) {
	cases := []struct {
		name            string
		event           grpctransport.Event
		reports, panics int
	}{
		{"invalid input", grpctransport.Event{Code: codes.InvalidArgument, Reason: "invalid_filter", Error: application.ErrInvalidFilter}, 0, 0},
		{"missing symbol", grpctransport.Event{Code: codes.NotFound, Reason: "symbol_not_found", Error: application.ErrSymbolNotFound}, 0, 0},
		{"unready snapshot", grpctransport.Event{Code: codes.Unavailable, Reason: "data_not_ready", Error: fmt.Errorf("reader: %w", application.ErrDataNotReady)}, 0, 0},
		{"canceled caller", grpctransport.Event{Code: codes.Canceled, Reason: "request_canceled", Error: context.Canceled}, 0, 0},
		{"unknown method", grpctransport.Event{Code: codes.Unimplemented, Reason: "transport_error", Error: status.Error(codes.Unimplemented, "unknown method")}, 0, 0},
		{"internal error", grpctransport.Event{Code: codes.Internal, Reason: "internal_error", Error: application.ErrInternal}, 1, 0},
		{"upstream error", grpctransport.Event{Code: codes.Unavailable, Reason: "upstream_error", Error: application.ErrUpstream}, 1, 0},
		{"request timeout", grpctransport.Event{Code: codes.DeadlineExceeded, Reason: "request_timeout", Error: context.DeadlineExceeded}, 1, 0},
		{"panic", grpctransport.Event{Code: codes.Internal, Reason: "internal_error", Error: application.ErrInternal, Panic: true}, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, err := newLocalState(1000, time.Now)
			require.NoError(t, err)
			reporter := &rpcReportingTelemetry{telemetry: state.telemetry}
			state.telemetry = reporter
			state.rpcMetrics = observability.NewRPC()
			cfg := config.Defaults()
			cfg.Observability.Stats.Enabled = true
			finish := state.rpcObserver(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))(t.Context(), "ListTickers")
			finish(tc.event)
			assert.Len(t, reporter.errors, tc.reports)
			assert.Equal(t, tc.panics, reporter.panics)
			assert.Contains(t, state.rpcMetrics.Samples(), observability.Sample{Name: "rpc_total", Labels: map[string]string{"method": "ListTickers", "status": tc.event.Code.String(), "reason": tc.event.Reason}, Value: 1})
		})
	}
}
