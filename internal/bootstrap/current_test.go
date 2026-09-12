package bootstrap

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/observability"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentWorkersAreCapabilityDrivenAndIndependent(t *testing.T) {
	cases := []struct {
		name           string
		binance, bybit bool
		workers        int
	}{
		{"all scopes", true, true, 6}, {"Binance only", true, false, 4}, {"Bybit only", false, true, 2},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				cfg.Exchanges.Binance.Enabled, cfg.Exchanges.Bybit.Enabled = tt.binance, tt.bybit
				state, err := newLocalState(1000, time.Now)
				require.NoError(t, err)
				clock := upstream.SystemClock{}
				jitter := func(d time.Duration) time.Duration { return d }
				transport := instrumentTransport(func(r *http.Request) (*http.Response, error) {
					body, status := `[]`, 200
					if r.URL.Path == "/v5/market/tickers" {
						body = `{"retCode":0,"result":{"category":"` + r.URL.Query().Get("category") + `","list":[]}}`
					}

					if strings.Contains(r.URL.Path, "24hr") {
						body, status = `{"code":-1}`, 400
					}

					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})
				state.exchanges, err = newExchangeClients(cfg, transport, clock, jitter, nil)
				require.NoError(t, err)
				workers, err := state.currentWorkers(cfg, testLogger(), clock, jitter)
				require.NoError(t, err)
				require.Len(t, workers, tt.workers)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, len(workers))
				for _, worker := range workers {
					go func() { done <- worker(ctx) }()
				}

				time.Sleep(time.Second)
				synctest.Wait()

				for _, scope := range enabledScopes(cfg) {
					ready, err := state.tickers.HasSnapshot(t.Context(), scope)
					require.NoError(t, err)
					assert.True(t, ready)
					ready, err = state.marketStats.HasSnapshot(t.Context(), scope, 24*time.Hour)
					require.NoError(t, err)
					assert.Equal(t, scope.Exchange == domain.ExchangeBybit, ready)
					ready, err = state.instruments.HasSnapshot(t.Context(), scope)
					require.NoError(t, err)
					assert.False(t, ready, "instrument readiness must not gate current data")
					metrics := state.currentMetrics.Snapshot()
					assert.Positive(t, metrics[observability.CurrentScope{Scope: scope}].RefreshTotal)
					if scope.Exchange == domain.ExchangeBinance {
						assert.Equal(t, uint64(1), metrics[observability.CurrentScope{Scope: scope, Window: 24 * time.Hour}].RefreshErrorsTotal)
					}
				}

				cancel()
				for range workers {
					assert.ErrorIs(t, <-done, context.Canceled)
				}
			})
		})
	}
}

func TestStatisticsWorkerWaitsAfterCompletionAndStopsDuringWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Exchanges.Bybit.Enabled = false
		cfg.Exchanges.Binance.Markets = []string{"spot"}
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		clock := upstream.SystemClock{}
		jitter := func(d time.Duration) time.Duration { return d }
		var mu sync.Mutex
		var starts []time.Time
		transport := instrumentTransport(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/api/v3/ticker/24hr", r.URL.Path)
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}

			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`[]`)), Request: r}, nil
		})
		state.exchanges, err = newExchangeClients(cfg, transport, clock, jitter, nil)
		require.NoError(t, err)
		workers, err := state.currentWorkers(cfg, testLogger(), clock, jitter)
		require.NoError(t, err)
		require.Len(t, workers, 2)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		started := time.Now()
		go func() { done <- workers[1](ctx) }()
		synctest.Wait()
		mu.Lock()
		assert.Equal(t, []time.Time{started}, starts)
		mu.Unlock()
		time.Sleep(31 * time.Second)
		synctest.Wait()
		mu.Lock()
		assert.Len(t, starts, 1)
		mu.Unlock()

		time.Sleep(time.Second)
		synctest.Wait()

		mu.Lock()
		assert.Equal(t, []time.Time{started, started.Add(32 * time.Second)}, starts)
		mu.Unlock()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		assert.Equal(t, uint64(2), state.currentMetrics.Snapshot()[observability.CurrentScope{Scope: scope, Window: 24 * time.Hour}].RefreshTotal)
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestTickerWorkerKeepsBackoffAcrossFailedCycles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Exchanges.Bybit.Enabled = false
		cfg.Exchanges.Binance.Markets = []string{"spot"}
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		var mu sync.Mutex
		var starts []time.Time
		transport := instrumentTransport(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":-1}`)), Request: r}, nil
		})
		jitter := func(d time.Duration) time.Duration { return d }
		state.exchanges, err = newExchangeClients(cfg, transport, upstream.SystemClock{}, jitter, nil)
		require.NoError(t, err)
		workers, err := state.currentWorkers(cfg, testLogger(), upstream.SystemClock{}, jitter)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		now := time.Now()
		go func() { done <- workers[0](ctx) }()
		synctest.Wait()
		mu.Lock()
		assert.Equal(t, []time.Time{now}, starts)
		mu.Unlock()
		first := cfg.HTTPClient.Retry.InitialBackoff

		time.Sleep(first)
		synctest.Wait()
		mu.Lock()
		assert.Equal(t, []time.Time{now, now.Add(first)}, starts)
		mu.Unlock()
		time.Sleep(2 * first)
		synctest.Wait()

		mu.Lock()
		assert.Equal(t, []time.Time{now, now.Add(first), now.Add(3 * first)}, starts)
		mu.Unlock()
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}
