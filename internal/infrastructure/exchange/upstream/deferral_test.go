package upstream

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerDefersWithoutRetryAndResumesWithFreshDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		base := &recorder{}
		c, transport := setup(t, cfg, BinanceSpot, base)
		start := time.Now()
		c.scopes[BinanceSpot].history = []*entry{{at: start, cost: cost{operation: MarketStats, weight: 240}, dispatched: true}}
		gate, err := NewCycleGate(SystemClock{}, func(time.Duration) time.Duration { return 0 }, cfg.HTTPClient.Retry, time.Second)
		require.NoError(t, err)
		cycle := NewBoundedCycle(gate, c, BinanceSpot, MarketStats)
		var cycles atomic.Int64
		refresh := func(ctx context.Context) error {
			cycles.Add(1)
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			assert.Equal(t, cfg.Upstream.LanesPerExchange.MarketStats.CycleTimeout, time.Until(deadline))
			_, err := send(ctx, transport, "/api/v3/ticker/24hr")
			return err
		}
		err = cycle.Run(t.Context(), refresh)
		require.ErrorIs(t, err, application.ErrServiceOverloaded)
		assert.Zero(t, gate.failures)
		assert.True(t, gate.next.IsZero())
		done := make(chan error, 1)
		go func() { done <- cycle.Run(t.Context(), refresh) }()
		synctest.Wait()
		time.Sleep(time.Minute - time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, int64(1), cycles.Load())
		assert.Empty(t, base.sent())
		c.mu.Lock()
		assert.Zero(t, c.queued)
		c.mu.Unlock()

		time.Sleep(time.Nanosecond)
		require.NoError(t, <-done)

		assert.Equal(t, int64(2), cycles.Load())
		require.Len(t, base.sent(), 1)
		assert.Equal(t, start.Add(time.Minute), base.sent()[0].at)
		assert.Zero(t, gate.failures)
		assert.Equal(t, start.Add(time.Minute+time.Second), gate.next)
	})
}

func TestWorkerShutdownStopsBudgetTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		c, transport := setup(t, cfg, BinanceSpot, &recorder{})
		c.scopes[BinanceSpot].history = []*entry{{at: time.Now(), cost: cost{operation: MarketStats, weight: 240}, dispatched: true}}
		gate, err := NewCycleGate(SystemClock{}, func(time.Duration) time.Duration { return 0 }, cfg.HTTPClient.Retry, time.Second)
		require.NoError(t, err)
		cycle := NewBoundedCycle(gate, c, BinanceSpot, MarketStats)
		var cycles atomic.Int64
		refresh := func(ctx context.Context) error {
			cycles.Add(1)
			_, err := send(ctx, transport, "/api/v3/ticker/24hr")
			return err
		}
		require.ErrorIs(t, cycle.Run(t.Context(), refresh), application.ErrServiceOverloaded)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- cycle.Run(ctx, refresh) }()
		synctest.Wait()
		start := time.Now()

		cancel()

		assert.ErrorIs(t, <-done, context.Canceled)
		assert.Equal(t, start, time.Now())
		assert.Equal(t, int64(1), cycles.Load())
		assert.False(t, gate.running)
		assert.Zero(t, c.active)
		assert.Zero(t, c.queued)
	})
}

func TestDeferredWorkerReportsLaterImpossibleLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		c, transport := setup(t, cfg, BinanceSpot, &recorder{})
		c.scopes[BinanceSpot].history = []*entry{{at: time.Now(), cost: cost{operation: MarketStats, weight: 240}, dispatched: true}}
		gate, err := NewCycleGate(SystemClock{}, func(d time.Duration) time.Duration { return d }, cfg.HTTPClient.Retry, time.Second)
		require.NoError(t, err)
		cycle := NewBoundedCycle(gate, c, BinanceSpot, MarketStats)
		refresh := func(ctx context.Context) error {
			_, err := send(ctx, transport, "/api/v3/ticker/24hr")
			return err
		}
		require.ErrorIs(t, cycle.Run(t.Context(), refresh), application.ErrServiceOverloaded)
		require.NoError(t, c.updateCatalog(BinanceSpot, time.Now(), catalogWeight(100)))

		err = cycle.Run(t.Context(), refresh)

		assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
		assert.Nil(t, gate.deferred)
		assert.Equal(t, 1, gate.failures)
		assert.True(t, gate.next.After(time.Now()))
	})
}

func TestCatalogScheduleContinuesDuringFundingDeferral(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		base := &recorder{}
		c, transport := setup(t, cfg, BinanceLinear, base)
		start := time.Now()
		require.NoError(t, c.updateCatalog(BinanceLinear, start, []byte(`{"rateLimits":[]}`)))
		// The dedicated funding-family stop line is 450 requests per five minutes.
		for range 451 {
			c.scopes[BinanceLinear].history = append(c.scopes[BinanceLinear].history, &entry{at: start, cost: cost{operation: Instruments, funding: true}, dispatched: true})
		}
		catalogs := 0
		cycle, err := NewCatalogCycle(c, BinanceLinear, SystemClock{}, func(time.Duration) time.Duration { return 0 }, cfg.HTTPClient.Retry, 10*time.Minute, 2*time.Second, func(ctx context.Context) error {
			catalogs++
			_, err := send(ctx, transport, "/fapi/v1/exchangeInfo")
			return err
		})
		require.NoError(t, err)
		instruments := 0
		refresh := func(ctx context.Context) error {
			instruments++
			_, err := send(ctx, transport, "/fapi/v1/fundingInfo")
			return err
		}
		require.ErrorIs(t, cycle.Run(t.Context(), refresh), application.ErrServiceOverloaded)
		assert.Zero(t, cycle.instruments.failures)
		assert.Zero(t, cycle.catalog.failures)

		require.NoError(t, cycle.Run(t.Context(), refresh))

		assert.Equal(t, start.Add(2*time.Second), time.Now())
		assert.Equal(t, 1, instruments)
		assert.Equal(t, 1, catalogs)
		require.Len(t, base.sent(), 1)
		assert.Equal(t, "/fapi/v1/exchangeInfo", base.sent()[0].path)
		assert.NotNil(t, cycle.instruments.deferred)
		assert.Zero(t, cycle.instruments.failures)
	})
}
