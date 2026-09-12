package bootstrap

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	httptransport "market-data/internal/transport/http"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperationRoutesAreIndependentAndDisabledByDefault(t *testing.T) {
	cases := []struct {
		name              string
		prometheus, debug bool
	}{
		{name: "disabled"}, {name: "prometheus", prometheus: true}, {name: "debug", debug: true}, {name: "both", prometheus: true, debug: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Observability.Stats.Enabled = false
			cfg.Observability.Stats.EndpointEnabled = tt.debug
			cfg.Observability.Prometheus.Enabled = tt.prometheus
			cfg.Observability.Prometheus.Path = "/custom-metrics"
			state, err := newLocalState(1000, time.Now)
			require.NoError(t, err)
			routes := map[string]http.Handler{}
			workers, err := state.operationWorkers(cfg, testLogger(), routes, time.Now)
			require.NoError(t, err)
			require.Len(t, workers, 1)
			handler := httptransport.NewAPIHandler(func() bool { return true }, cfg.Server.MaxQueryBytes, routes)

			for _, path := range []string{"/custom-metrics", "/debug/stats", "/metrics"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
				expected := http.StatusNotFound
				if path == "/custom-metrics" && tt.prometheus || path == "/debug/stats" && tt.debug {
					expected = http.StatusOK
				}
				assert.Equal(t, expected, response.Code, path)
			}
		})
	}
}

func TestRetentionAndLoggingWorkersPreserveSnapshotsAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Klines.MaxHistoryCandles = 1
		cfg.Storage.CleanupInterval = time.Minute
		cfg.Observability.Stats.LogInterval = 30 * time.Second
		state, err := newLocalState(1, time.Now)
		require.NoError(t, err)
		scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
		now := time.Now().UTC().Truncate(time.Minute)
		candle := kline.Stored{Candle: domain.Kline{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", Interval: domain.Timeframe1m, OpenTime: now.Add(-time.Minute), CloseTime: now, FetchedAt: now}, RequestStartedAt: now}
		require.NoError(t, state.klines.UpsertMany(t.Context(), []kline.Stored{candle}))
		instruments := []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}
		tickers := []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}
		stats := []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", Window: 24 * time.Hour}}
		require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, instruments))
		require.NoError(t, state.tickers.ReplaceSnapshot(t.Context(), scope, tickers))
		require.NoError(t, state.marketStats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, stats))
		filterBefore := application.SnapshotFilter{Scopes: []application.Scope{scope}}
		instruments, err = state.instruments.List(t.Context(), instrument.Filter{SnapshotFilter: filterBefore})
		require.NoError(t, err)
		tickers, err = state.tickers.List(t.Context(), filterBefore)
		require.NoError(t, err)
		stats, err = state.marketStats.List(t.Context(), marketstats.Filter{SnapshotFilter: filterBefore, Window: 24 * time.Hour})
		require.NoError(t, err)
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		workers, err := state.operationWorkers(cfg, logger, map[string]http.Handler{}, time.Now)
		require.NoError(t, err)
		require.Len(t, workers, 2)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, len(workers))
		for _, worker := range workers {
			go func() { done <- worker(ctx) }()
		}
		synctest.Wait()
		counts, err := state.inventory.CandleCounts(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, counts[scope])

		time.Sleep(time.Minute)
		synctest.Wait()
		cancel()
		for range workers {
			assert.ErrorIs(t, <-done, context.Canceled)
		}

		counts, err = state.inventory.CandleCounts(t.Context())
		require.NoError(t, err)
		assert.Empty(t, counts)
		filter := application.SnapshotFilter{Scopes: []application.Scope{scope}}
		savedInstruments, err := state.instruments.List(t.Context(), instrument.Filter{SnapshotFilter: filter})
		require.NoError(t, err)
		assert.Equal(t, instruments, savedInstruments)
		savedTickers, err := state.tickers.List(t.Context(), filter)
		require.NoError(t, err)
		assert.Equal(t, tickers, savedTickers)
		savedStats, err := state.marketStats.List(t.Context(), marketstats.Filter{SnapshotFilter: filter, Window: 24 * time.Hour})
		require.NoError(t, err)
		assert.Equal(t, stats, savedStats)
		assert.Contains(t, logs.String(), "Market data statistics")
		assert.Contains(t, logs.String(), "kline_count")
		assert.NotContains(t, logs.String(), "BTCUSDT")
	})
}

func TestPeriodicWaitsAfterCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var starts []time.Time
		initial := time.Now()
		done := make(chan error, 1)
		go func() {
			done <- periodic(ctx, time.Minute, true, func(context.Context) {
				starts = append(starts, time.Now())
				time.Sleep(10 * time.Second)
				if len(starts) == 2 {
					cancel()
				}
			})
		}()

		assert.ErrorIs(t, <-done, context.Canceled)

		assert.Equal(t, []time.Time{initial, initial.Add(70 * time.Second)}, starts)
	})
}

func TestWorkerPanicStopsServerWithoutLeakingValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newServerState(t, cfg)
		require.NoError(t, err)
		listener := newPipeListener()

		err = state.serve(t.Context(), cfg, testLogger(), listener, func(context.Context) error { panic("secret worker data") })

		assert.ErrorIs(t, err, application.ErrInternal)
		assert.NotContains(t, err.Error(), "secret")
		assert.True(t, channelClosed(listener.closed))
		assert.False(t, state.ready.Load())
	})
}

type shutdownTelemetry struct {
	telemetry
	stopped <-chan struct{}
	waited  bool
}

func (s *shutdownTelemetry) Flush(ctx context.Context) bool {
	s.waited = channelClosed(s.stopped)
	<-ctx.Done()
	return false
}

func TestServeFlushUsesRemainingShutdownTimeAfterWorkersStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newServerState(t, cfg)
		require.NoError(t, err)
		stopped := make(chan struct{})
		telemetry := &shutdownTelemetry{telemetry: state.telemetry, stopped: stopped}
		state.telemetry = telemetry
		root, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		worker := func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			time.Sleep(5 * time.Second)
			close(stopped)
			return nil
		}
		listener := newPipeListener()
		done := make(chan error, 1)
		go func() { done <- state.serve(root, cfg, testLogger(), listener, worker) }()
		<-started
		at := time.Now()

		cancel()
		require.NoError(t, <-done)

		assert.True(t, telemetry.waited)
		assert.Equal(t, cfg.Server.ShutdownTimeout, time.Since(at), "flush must not start a new shutdown timeout")
		assert.True(t, channelClosed(listener.closed))
	})
}

func TestExchangeCooldownDoesNotBlockReadinessOrShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		state.exchanges, err = newExchangeClients(cfg, instrumentTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(`{"code":-1003,"retCode":10006}`)), Request: r}, nil
		}), upstream.SystemClock{}, func(d time.Duration) time.Duration { return d }, state.exchangeMetrics.Observe)
		require.NoError(t, err)
		instruments, err := state.instrumentWorkers(cfg, testLogger(), upstream.SystemClock{}, func(d time.Duration) time.Duration { return d })
		require.NoError(t, err)
		current, err := state.currentWorkers(cfg, testLogger(), upstream.SystemClock{}, func(d time.Duration) time.Duration { return d })
		require.NoError(t, err)
		root, cancel := context.WithCancel(t.Context())
		defer cancel()
		listener := newPipeListener()
		done := make(chan error, 1)
		go func() { done <- state.serve(root, cfg, testLogger(), listener, append(instruments, current...)...) }()
		synctest.Wait()
		transport := &http.Transport{DialContext: listener.dial}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://local/ready", nil)
		require.NoError(t, err)

		response, err := client.Do(request)

		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, response.StatusCode)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		request, err = http.NewRequestWithContext(t.Context(), "GET", "http://local/api/v1/market-stats", nil)
		require.NoError(t, err)
		response, err = client.Do(request)
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.NotEmpty(t, state.exchangeMetrics.Snapshot())
		for _, stats := range state.exchangeMetrics.Snapshot() {
			assert.Equal(t, stats.Requests, stats.Errors)
		}
		at := time.Now()
		cancel()
		require.NoError(t, <-done)
		assert.Less(t, time.Since(at), time.Second, "shutdown must cancel cooldown and admission waits")
		assert.True(t, channelClosed(listener.closed))
	})
}

func TestKlinePanicReleasesAdmissionForNextFill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Upstream.LanesPerExchange.Klines.HTTPSlots = 1
		cfg.Klines.MaxActiveFillsPerExchange = 1
		cfg.Klines.MaxActiveFills = 1
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		end := time.Now().UTC().Truncate(time.Minute)
		var attempts atomic.Int64
		state.exchanges, err = newExchangeClients(cfg, instrumentTransport(func(r *http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				panic("secret upstream panic")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(candleFixture(scope.Exchange, scope.Market, end.Add(-time.Minute), end))), Request: r}, nil
		}), upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, state.exchangeMetrics.Observe)
		require.NoError(t, err)
		require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
		root, cancel := context.WithCancel(t.Context())
		defer cancel()
		service, err := state.klineService(root, cfg, time.Now)
		require.NoError(t, err)
		defer func() {
			cancel()
			service.Wait()
		}()
		query := kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: end.Add(-time.Minute), To: end}

		_, err = service.Get(t.Context(), query)

		require.ErrorIs(t, err, application.ErrInternal)
		assert.NotContains(t, err.Error(), "secret")
		rows, err := service.Get(t.Context(), query)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, query.From, rows[0].OpenTime)
		assert.Equal(t, uint64(2), state.klineMetrics.Snapshot()[scope].Duration.Count)
		exchangeStats := state.exchangeMetrics.Snapshot()
		require.Len(t, exchangeStats, 1)
		for _, stats := range exchangeStats {
			assert.Equal(t, uint64(2), stats.Requests)
			assert.Equal(t, uint64(1), stats.Errors)
		}
	})
}

func TestKlineCountersNeedStandaloneStatsOrAnExporter(t *testing.T) {
	cases := []struct {
		name                     string
		stats, prometheus, debug bool
		hits                     uint64
	}{
		{name: "disabled"}, {name: "standalone", stats: true, hits: 1},
		{name: "prometheus only", prometheus: true, hits: 1}, {name: "debug only", debug: true, hits: 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Observability.Stats.Enabled = tt.stats
			cfg.Observability.Stats.EndpointEnabled = tt.debug
			cfg.Observability.Prometheus.Enabled = tt.prometheus
			state, err := newServerState(t, cfg)
			require.NoError(t, err)
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
			require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
			end := time.Now().UTC().Truncate(time.Minute)
			query := kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: end.Add(-time.Minute), To: end}
			require.NoError(t, state.klines.UpsertMany(t.Context(), []kline.Stored{{Candle: domain.Kline{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", Interval: domain.Timeframe1m, OpenTime: query.From, CloseTime: end, FetchedAt: end}, RequestStartedAt: end}}))
			service, err := state.klineService(t.Context(), cfg, time.Now)
			require.NoError(t, err)
			defer service.Wait()

			rows, err := service.Get(t.Context(), query)

			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, tt.hits, state.klineMetrics.Snapshot()[scope].CacheHits)
		})
	}
}
