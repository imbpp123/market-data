package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogScheduleReusesInstrumentResponses(t *testing.T) {
	cases := []struct {
		name                        string
		instrumentInterval, elapsed time.Duration
		wantCalls, wantSnapshots    int
	}{
		{"hourly catalog and two hour instruments", 2 * time.Hour, 2 * time.Hour, 3, 2},
		{"frequent instruments reuse catalogs", 10 * time.Minute, time.Hour, 7, 7},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := catalogWorkerConfig()
				cfg.Exchanges.Binance.Instruments.RefreshInterval = tt.instrumentInterval
				var calls atomic.Int64
				state, worker := catalogWorker(t, cfg, func(r *http.Request) (*http.Response, error) {
					assert.Equal(t, "/api/v3/exchangeInfo", r.URL.Path)
					assert.Equal(t, "false", r.URL.Query().Get("showPermissionSets"))
					calls.Add(1)
					return catalogResponse(6000), nil
				})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- worker(ctx) }()
				synctest.Wait()

				time.Sleep(tt.elapsed)
				synctest.Wait()

				assert.Equal(t, int64(tt.wantCalls), calls.Load())
				scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
				assert.Equal(t, uint64(tt.wantSnapshots), state.instrumentMetrics.Snapshot()[scope].RefreshTotal)
				assert.Equal(t, 6000, state.exchanges.admission.Limits(upstream.BinanceSpot)[1].ExchangeLimit)
				cancel()
				assert.ErrorIs(t, <-done, context.Canceled)
			})
		})
	}
}

func TestCatalogScheduleRecoversAfterFailureWithoutBacklog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := catalogWorkerConfig()
		cfg.Exchanges.Binance.Instruments.RefreshInterval = 4 * time.Hour
		var calls atomic.Int64
		state, worker := catalogWorker(t, cfg, func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			if calls.Load() == 2 {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"rateLimits":[`))}, nil
			}
			return catalogResponse(10000), nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker(ctx) }()
		synctest.Wait()
		before := state.exchanges.admission.Limits(upstream.BinanceSpot)

		time.Sleep(time.Hour)
		synctest.Wait()
		assert.Equal(t, int64(2), calls.Load())
		failed := state.exchanges.admission.Limits(upstream.BinanceSpot)
		assert.Equal(t, before[1].UpdatedAt, failed[1].UpdatedAt)
		assert.Equal(t, before[1].StopLine, failed[1].StopLine)
		time.Sleep(time.Hour)
		synctest.Wait()

		assert.Equal(t, int64(3), calls.Load())
		after := state.exchanges.admission.Limits(upstream.BinanceSpot)
		assert.Equal(t, 9000, after[1].StopLine)
		assert.True(t, after[1].UpdatedAt.After(before[1].UpdatedAt))
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestCatalogFreshnessSurvivesInstrumentNormalizationFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := catalogWorkerConfig()
		cfg.Exchanges.Binance.Instruments.RefreshInterval = 2 * time.Hour
		var calls atomic.Int64
		state, worker := catalogWorker(t, cfg, func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			response := catalogResponse(10000)
			response.Body = io.NopCloser(strings.NewReader(`{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":10000}],"symbols":[{}]}`))
			return response, nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker(ctx) }()
		synctest.Wait()

		assert.Equal(t, int64(1), calls.Load())
		assert.Equal(t, 9000, state.exchanges.admission.Limits(upstream.BinanceSpot)[1].StopLine)
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		ready, err := state.instruments.HasSnapshot(t.Context(), scope)
		require.NoError(t, err)
		assert.False(t, ready)
		time.Sleep(time.Hour - time.Second)
		synctest.Wait()
		assert.Equal(t, int64(1), calls.Load())
		time.Sleep(time.Second)
		synctest.Wait()
		assert.Equal(t, int64(2), calls.Load())
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestScheduledCatalogHasOneOwnerAndCancelsInflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := catalogWorkerConfig()
		cfg.Upstream.Binance.CatalogRefreshInterval = 10 * time.Second
		cfg.HTTPClient.Timeout = time.Minute
		var calls atomic.Int64
		_, worker := catalogWorker(t, cfg, func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			if calls.Load() > 1 {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			return catalogResponse(6000), nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker(ctx) }()
		synctest.Wait()
		time.Sleep(30 * time.Second)
		synctest.Wait()

		assert.Equal(t, int64(2), calls.Load())
		cancel()

		assert.ErrorIs(t, <-done, context.Canceled)
		assert.Equal(t, int64(2), calls.Load())
	})
}

func TestCatalogAdmissionDelayRemainsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := catalogWorkerConfig()
		cfg.Upstream.Binance.CatalogRefreshInterval = time.Minute
		cfg.Exchanges.Binance.Instruments.RefreshInterval = time.Hour
		var calls atomic.Int64
		state, worker := catalogWorker(t, cfg, func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return catalogResponse(10000), nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker(ctx) }()
		synctest.Wait()
		state.exchanges.admission.Cooldown(upstream.BinanceSpot, time.Now().Add(2*time.Minute))

		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, int64(1), calls.Load())
		time.Sleep(time.Minute)
		synctest.Wait()

		assert.Equal(t, int64(2), calls.Load())
		assert.Equal(t, 9000, state.exchanges.admission.Limits(upstream.BinanceSpot)[1].StopLine)
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}

func catalogWorkerConfig() config.Config {
	cfg := config.Defaults()
	cfg.Exchanges.Bybit.Enabled = false
	cfg.Exchanges.Binance.Markets = []string{"spot"}
	cfg.HTTPClient.Retry.MaxAttempts = 1
	return cfg
}

func catalogResponse(limit int) *http.Response {
	body := fmt.Sprintf(`{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":%d}],"symbols":[]}`, limit)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func catalogWorker(t *testing.T, cfg config.Config, handle instrumentTransport) (*localState, Worker) {
	t.Helper()
	state, err := newLocalState(1000, time.Now)
	require.NoError(t, err)
	clock := upstream.SystemClock{}
	jitter := func(time.Duration) time.Duration { return 0 }
	state.exchanges, err = newExchangeClients(cfg, handle, clock, jitter, nil)
	require.NoError(t, err)
	workers, err := state.instrumentWorkers(cfg, testLogger(), clock, jitter)
	require.NoError(t, err)
	require.Len(t, workers, 1)
	return state, workers[0]
}

func TestLinearCatalogRefreshDoesNotFetchFundingOrPublishInstruments(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := catalogWorkerConfig()
		cfg.Exchanges.Binance.Markets = []string{"linear"}
		cfg.Exchanges.Binance.Instruments.RefreshInterval = 2 * time.Hour
		var paths []string
		var pathsMu sync.Mutex
		state, worker := catalogWorker(t, cfg, func(r *http.Request) (*http.Response, error) {
			pathsMu.Lock()
			paths = append(paths, r.URL.Path)
			pathsMu.Unlock()
			assert.Empty(t, r.URL.RawQuery)
			response := catalogResponse(2400)
			if r.URL.Path == "/fapi/v1/fundingInfo" {
				response.Body = io.NopCloser(strings.NewReader(`[]`))
			}
			return response, nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker(ctx) }()
		synctest.Wait()
		// The initial funding request waits for the configured dispatch spacing.
		time.Sleep(cfg.Upstream.Limits.BinanceLinear.MinRequestSpacing)
		synctest.Wait()
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}
		before := state.instrumentMetrics.Snapshot()[scope]

		time.Sleep(time.Hour)
		synctest.Wait()

		pathsMu.Lock()
		assert.Equal(t, []string{"/fapi/v1/exchangeInfo", "/fapi/v1/fundingInfo", "/fapi/v1/exchangeInfo"}, paths)
		pathsMu.Unlock()
		assert.Equal(t, uint64(1), before.RefreshTotal)
		assert.Equal(t, before, state.instrumentMetrics.Snapshot()[scope])
		assert.Equal(t, 2160, state.exchanges.admission.Limits(upstream.BinanceLinear)[1].StopLine)
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestFailedFirstCatalogKeepsBootstrapAndRetriesOnSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := catalogWorkerConfig()
		cfg.Exchanges.Binance.Instruments.RefreshInterval = 2 * time.Hour
		var calls atomic.Int64
		state, worker := catalogWorker(t, cfg, func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			if calls.Load() == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			return catalogResponse(10000), nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker(ctx) }()
		synctest.Wait()

		assert.Equal(t, int64(1), calls.Load())
		assert.Equal(t, "bootstrap", state.exchanges.admission.Limits(upstream.BinanceSpot)[1].Source)
		time.Sleep(time.Hour)
		synctest.Wait()

		assert.Equal(t, int64(2), calls.Load())
		assert.Equal(t, 9000, state.exchanges.admission.Limits(upstream.BinanceSpot)[1].StopLine)
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}
