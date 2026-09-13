package bootstrap

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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
		name     string
		resume   bool
		cooldown bool
	}{
		{name: "shutdown during deferral"},
		{name: "ordinary work resumes at expiry", resume: true},
		{name: "real cooldown outlasts budget", resume: true, cooldown: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := catalogWorkerConfig()
				cfg.Upstream.Binance.CatalogRefreshInterval = 10 * time.Second
				cfg.HTTPClient.Retry.MaxAttempts = 1
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
						if tt.cooldown {
							response.StatusCode = 429
							response.Header.Set("Retry-After", "120")
						}
					}
					return response, nil
				})
				state.exchanges, err = newExchangeClients(cfg, base, clock, jitter, state.exchangeMetrics.Observe)
				require.NoError(t, err)
				seed, stop, err := state.exchanges.admission.Begin(t.Context(), upstream.BinanceSpot, upstream.Instruments)
				require.NoError(t, err)
				defer stop()
				_, err = state.exchanges.binance[upstream.BinanceSpot].Fetch(seed, "/api/v3/exchangeInfo", nil)
				if tt.cooldown {
					require.ErrorIs(t, err, application.ErrUpstream)
				} else {
					require.NoError(t, err)
				}
				exchangeBefore := state.exchangeMetrics.Snapshot()
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
				assert.Equal(t, exchangeBefore, state.exchangeMetrics.Snapshot())
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
					if tt.cooldown {
						time.Sleep(time.Minute)
						synctest.Wait()
						assert.Equal(t, int64(1), calls.Load(), "budget expiry cannot clear the exchange cooldown")
						assert.Equal(t, exchangeBefore, state.exchangeMetrics.Snapshot())
						assert.Zero(t, reports.failures.Load())
					}
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

func TestKlineBudgetRejectionKeepsPagesWithoutPartialSuccess(t *testing.T) {
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
		started := time.Now()
		response, err := service.Get(t.Context(), kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: end.Add(-4 * time.Minute), To: end})
		assert.ErrorIs(t, err, application.ErrServiceOverloaded)
		assert.Empty(t, response)

		assert.Equal(t, started, time.Now())
		assert.Equal(t, int64(1), calls.Load())
		metrics := state.klineMetrics.Snapshot()[scope]
		assert.Equal(t, uint64(1), metrics.Attempts)
		assert.Equal(t, uint64(2), metrics.Downloaded)
		cached, err := service.Get(t.Context(), kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: fetchedFrom, To: fetchedTo})
		require.NoError(t, err)
		require.Len(t, cached, 2)
		assert.Equal(t, "1.1234567890123456789", cached[0].Open.String())

		assert.Equal(t, int64(1), calls.Load())
		assert.Equal(t, uint64(1), state.klineMetrics.Snapshot()[scope].Attempts)
	})
}

func TestParallelKlineCallersAllowOneCrossingAndKeepCacheReadable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{
			{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"},
			{Exchange: scope.Exchange, Market: scope.Market, Symbol: "ETHUSDT"},
		}))
		end := time.Now().UTC().Truncate(time.Minute)
		reply := make(chan struct{})
		var calls atomic.Int64
		base := instrumentTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			if r.URL.Path == "/api/v3/exchangeInfo" {
				result := catalogResponse(6000)
				result.Header.Set("X-Mbx-Used-Weight-1m", "5400")
				return result, nil
			}
			<-reply
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(candleFixture(scope.Exchange, scope.Market, end.Add(-time.Minute), end)))}, nil
		})
		state.exchanges, err = newExchangeClients(cfg, base, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, state.exchangeMetrics.Observe)
		require.NoError(t, err)
		seed, stop, err := state.exchanges.admission.Begin(t.Context(), upstream.BinanceSpot, upstream.Instruments)
		require.NoError(t, err)
		defer stop()
		_, err = state.exchanges.binance[upstream.BinanceSpot].Fetch(seed, "/api/v3/exchangeInfo", nil)
		require.NoError(t, err)
		time.Sleep(20 * time.Millisecond)
		root, cancel := context.WithCancel(t.Context())
		defer cancel()
		service, err := state.klineService(root, cfg, time.Now)
		require.NoError(t, err)
		defer func() {
			cancel()
			service.Wait()
		}()
		type outcome struct {
			query kline.Query
			rows  []domain.Kline
			err   error
		}
		results := make(chan outcome, 2)
		for _, symbol := range []string{"BTCUSDT", "ETHUSDT"} {
			query := kline.Query{Series: kline.Series{Scope: scope, Symbol: symbol, Interval: domain.Timeframe1m}, From: end.Add(-time.Minute), To: end}
			go func() { rows, err := service.Get(t.Context(), query); results <- outcome{query, rows, err} }()
		}
		synctest.Wait()
		rejected := <-results
		assert.ErrorIs(t, rejected.err, application.ErrServiceOverloaded)
		assert.Empty(t, rejected.rows)

		assert.Equal(t, int64(2), calls.Load(), "one seed and one crossing request")
		for _, w := range state.exchanges.admission.Diagnostics().Scopes[0].Windows {
			if w.Name == "request_weight_1m" {
				assert.Equal(t, 5402, w.Accounted)
				assert.Equal(t, 2, w.Reserved)
			}
		}
		close(reply)
		accepted := <-results
		require.NoError(t, accepted.err)
		require.Len(t, accepted.rows, 1)
		cached, err := service.Get(t.Context(), accepted.query)
		require.NoError(t, err)
		assert.Equal(t, accepted.rows, cached)

		assert.Equal(t, int64(2), calls.Load())
		assert.Equal(t, uint64(1), state.klineMetrics.Snapshot()[scope].Attempts)
		for _, stats := range state.exchangeMetrics.Snapshot() {
			assert.Zero(t, stats.Errors)
		}
	})
}
