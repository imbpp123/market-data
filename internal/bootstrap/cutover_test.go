package bootstrap

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
)

func startDualTest(t *testing.T, state *localState, cfg config.Config) (pb.MarketDataServiceClient, *pipeListener, context.CancelFunc, <-chan error) {
	t.Helper()
	root, cancel := context.WithCancel(t.Context())
	data, operations := newPipeListener(), newPipeListener()
	settings := configuredGRPC(cfg)
	listen := func(_ context.Context, _, address string) (net.Listener, error) {
		if address == settings.Address {
			return data, nil
		}
		return operations, nil
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- state.serveGRPC(root, cfg, testLogger(), settings, listen, func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() })
	}()
	<-started
	connection, err := grpc.NewClient("passthrough:///cutover", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return data.dial(ctx, "tcp", "cutover") }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close(); cancel() })
	return pb.NewMarketDataServiceClient(connection), operations, cancel, done
}

func TestDualCompositionKeepsLocalReadySeparateFromAllData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newServerState(t, cfg)
		require.NoError(t, err)
		api, operations, cancel, done := startDualTest(t, state, cfg)
		defer func() { cancel(); require.NoError(t, <-done) }()
		transport := &http.Transport{DialContext: operations.dial}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		for _, path := range []string{"/health", "/ready", "/api/v1/instruments", "/api/v1/tickers", "/api/v1/market-stats", "/api/v1/klines", "/api/v1/other"} {
			request, err := http.NewRequestWithContext(t.Context(), "GET", "http://local"+path, nil)
			require.NoError(t, err)
			response, err := client.Do(request)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			if strings.HasPrefix(path, "/api/") {
				assert.Equal(t, 404, response.StatusCode)
				assert.NotContains(t, string(body), `"data"`)
				assert.Empty(t, response.Header.Get("Location"))
			} else {
				assert.Equal(t, 200, response.StatusCode)
				assert.JSONEq(t, `{"status":"ok"}`, string(body))
			}
		}
		_, err = api.ListInstruments(t.Context(), &pb.ListInstrumentsRequest{})
		assertUnreadyRPC(t, err)
		_, err = api.ListTickers(t.Context(), &pb.ListTickersRequest{})
		assertUnreadyRPC(t, err)
		_, err = api.ListMarketStats(t.Context(), &pb.ListMarketStatsRequest{})
		assertUnreadyRPC(t, err)
		end := time.Now().UTC().Truncate(time.Minute)
		_, err = api.GetKlines(t.Context(), klineRequest(application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}, end.Add(-time.Minute), end))
		assertUnreadyRPC(t, err)
	})
}

func assertUnreadyRPC(t *testing.T, err error) {
	t.Helper()
	require.Equal(t, codes.Unavailable, status.Code(err))
	details := status.Convert(err).Details()
	require.Len(t, details, 1)
	require.IsType(t, &pb.ErrorDetail{}, details[0])
	assert.Equal(t, "data_not_ready", details[0].(*pb.ErrorDetail).Reason)
}

func TestDualCompositionCancelsOwnedKlineFill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		started, stopped := make(chan struct{}), make(chan struct{})
		state.exchanges, err = newExchangeClients(cfg, instrumentTransport(func(r *http.Request) (*http.Response, error) {
			close(started)
			defer close(stopped)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}), upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
		require.NoError(t, err)
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
		api, _, cancel, done := startDualTest(t, state, cfg)
		end := time.Now().UTC().Truncate(time.Minute)
		response := make(chan error, 1)
		go func() {
			_, err := api.GetKlines(t.Context(), klineRequest(scope, end.Add(-time.Minute), end))
			response <- err
		}()
		<-started
		cancel()
		require.NoError(t, <-done)
		assert.True(t, channelClosed(stopped))
		assert.False(t, state.ready.Load())
		assert.Error(t, <-response)
	})
}

func TestOperationalHTTPRejectsOversizedHeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Server.HTTP.MaxHeaderBytes = 128
		state, err := newServerState(t, cfg)
		require.NoError(t, err)
		_, listener, cancel, done := startDualTest(t, state, cfg)
		defer func() { cancel(); require.NoError(t, <-done) }()
		connection, err := listener.dial(t.Context(), "tcp", "local")
		require.NoError(t, err)
		defer func() { _ = connection.Close() }()
		sent := make(chan error, 1)
		go func() {
			_, err := io.WriteString(connection, "GET /health HTTP/1.1\r\nHost: local\r\nX-Long: "+strings.Repeat("x", 8192)+"\r\n\r\n")
			sent <- err
		}()
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, response.StatusCode)
		_ = response.Body.Close()
		_ = connection.Close()
		<-sent
	})
}
