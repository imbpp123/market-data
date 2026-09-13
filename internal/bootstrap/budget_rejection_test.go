package bootstrap

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/marketstats"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	httptransport "market-data/internal/transport/http"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type budgetTelemetry struct {
	telemetry
	failures atomic.Int64
}

func (t *budgetTelemetry) Report(err error, _ map[string]string) {
	if err != nil {
		t.failures.Add(1)
	}
}

func TestBackgroundBudgetDeferralIsQuietAndKeepsSnapshots(t *testing.T) {
	cases := []struct {
		name   string
		resume bool
	}{
		{name: "shutdown during deferral"},
		{name: "ordinary work resumes at expiry", resume: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := catalogWorkerConfig()
				cfg.Upstream.Binance.CatalogRefreshInterval = 10 * time.Second
				clock := upstream.SystemClock{}
				jitter := func(time.Duration) time.Duration { return 0 }
				state, err := newLocalState(1000, time.Now)
				require.NoError(t, err)
				scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
				old := time.Now().Add(-time.Hour)
				oldInstruments := []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", PriceTick: decimal.NewFromInt(1), QtyStep: decimal.NewFromInt(1), UpdatedAt: old}}
				oldTickers := []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", LastPrice: decimal.NewFromInt(1), FetchedAt: old}}
				oldStats := []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", High: decimal.NewFromInt(3), Low: decimal.NewFromInt(1), Volume: decimal.NewFromInt(2), Turnover: decimal.NewFromInt(2), FetchedAt: old, Window: 24 * time.Hour}}
				require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, oldInstruments))
				require.NoError(t, state.tickers.ReplaceSnapshot(t.Context(), scope, oldTickers))
				require.NoError(t, state.marketStats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, oldStats))
				var calls atomic.Int64
				base := instrumentTransport(func(r *http.Request) (*http.Response, error) {
					call := calls.Add(1)
					response := catalogResponse(6000)
					if r.URL.Path != "/api/v3/exchangeInfo" {
						response.Body = io.NopCloser(strings.NewReader(`[]`))
					}
					if call == 1 {
						response.Header.Set("X-Mbx-Used-Weight-1m", "5401")
					}
					return response, nil
				})
				state.exchanges, err = newExchangeClients(cfg, base, clock, jitter, nil)
				require.NoError(t, err)
				seed, stop, err := state.exchanges.admission.Begin(t.Context(), upstream.BinanceSpot, upstream.Instruments)
				require.NoError(t, err)
				defer stop()
				_, err = state.exchanges.binance[upstream.BinanceSpot].Fetch(seed, "/api/v3/exchangeInfo", nil)
				require.NoError(t, err)
				reports := &budgetTelemetry{telemetry: state.telemetry}
				state.telemetry = reports
				var logs bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&logs, nil))
				workers, err := state.instrumentWorkers(cfg, logger, clock, jitter)
				require.NoError(t, err)
				current, err := state.currentWorkers(cfg, logger, clock, jitter)
				require.NoError(t, err)
				workers = append(workers, current...)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, len(workers))
				for _, worker := range workers {
					go func() { done <- worker(ctx) }()
				}
				synctest.Wait()

				time.Sleep(time.Minute - time.Nanosecond)
				synctest.Wait()

				assert.Equal(t, int64(1), calls.Load())
				assert.Zero(t, reports.failures.Load())
				assert.NotContains(t, logs.String(), `"level":"WARN"`)
				assert.Zero(t, state.instrumentMetrics.Snapshot()[scope].RefreshTotal)
				assert.Empty(t, state.currentMetrics.Snapshot())
				filter := application.SnapshotFilter{Scopes: []application.Scope{scope}}
				instruments, err := state.instruments.List(t.Context(), instrument.Filter{SnapshotFilter: filter})
				require.NoError(t, err)
				assert.Equal(t, oldInstruments, instruments)
				tickers, err := state.tickers.List(t.Context(), filter)
				require.NoError(t, err)
				assert.Equal(t, oldTickers, tickers)
				stats, err := state.marketStats.List(t.Context(), marketstats.Filter{SnapshotFilter: filter, Window: 24 * time.Hour})
				require.NoError(t, err)
				assert.Equal(t, oldStats, stats)
				if tt.resume {
					time.Sleep(100 * time.Millisecond)
					synctest.Wait()
					assert.Greater(t, calls.Load(), int64(1))
					assert.Positive(t, state.instrumentMetrics.Snapshot()[scope].RefreshTotal)
					assert.Zero(t, reports.failures.Load())
				}
				cancel()
				for range workers {
					assert.ErrorIs(t, <-done, context.Canceled)
				}
			})
		})
	}
}

func TestKlineBudgetRejectionKeepsPagesWithoutPartialHTTPSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Klines.MaxHistoryCandles = 4
		cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Spot = 2
		state, err := newLocalState(4, time.Now)
		require.NoError(t, err)
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
		end := time.Now().UTC().Truncate(time.Minute)
		var fetchedFrom, fetchedTo time.Time
		var calls atomic.Int64
		base := instrumentTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			from, err := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
			require.NoError(t, err)
			to, err := strconv.ParseInt(r.URL.Query().Get("endTime"), 10, 64)
			require.NoError(t, err)
			fetchedFrom, fetchedTo = time.UnixMilli(from).UTC(), time.UnixMilli(to+1).UTC()
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Mbx-Used-Weight-1m": {"5401"}}, Body: io.NopCloser(strings.NewReader(candleFixture(scope.Exchange, scope.Market, fetchedFrom, fetchedTo)))}, nil
		})
		state.exchanges, err = newExchangeClients(cfg, base, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
		require.NoError(t, err)
		root, cancel := context.WithCancel(t.Context())
		defer cancel()
		service, err := state.klineService(root, cfg, time.Now)
		require.NoError(t, err)
		defer func() {
			cancel()
			service.Wait()
		}()
		handler := httptransport.NewKlinesHandler(service, time.Second, cfg.Klines.MaxCallers)
		started := time.Now()
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", candleURL(scope, end.Add(-4*time.Minute), end), nil))

		assert.Equal(t, 503, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"service_overloaded"`)
		assert.NotContains(t, response.Body.String(), `"data"`)
		assert.Equal(t, started, time.Now())
		assert.Equal(t, int64(1), calls.Load())
		metrics := state.klineMetrics.Snapshot()[scope]
		assert.Equal(t, uint64(1), metrics.Attempts)
		assert.Equal(t, uint64(2), metrics.Downloaded)
		cached := httptest.NewRecorder()

		handler.ServeHTTP(cached, httptest.NewRequestWithContext(t.Context(), "GET", candleURL(scope, fetchedFrom, fetchedTo), nil))

		assert.Equal(t, 200, cached.Code, "%s", cached.Body.String())
		assert.Contains(t, cached.Body.String(), "1.1234567890123456789")
		assert.Equal(t, int64(1), calls.Load())
		assert.Equal(t, uint64(1), state.klineMetrics.Snapshot()[scope].Attempts)
	})
}
