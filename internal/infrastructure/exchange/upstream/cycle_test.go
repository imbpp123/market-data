package upstream

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundedCycleIncludesPublicationDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Upstream.LanesPerExchange.Instruments.CycleTimeout = time.Second
		controller, err := New(cfg, SystemClock{})
		require.NoError(t, err)
		gate, err := NewCycleGate(SystemClock{}, func(time.Duration) time.Duration { return 0 }, cfg.HTTPClient.Retry, time.Minute)
		require.NoError(t, err)
		cycle := NewBoundedCycle(gate, controller, BinanceSpot, Instruments)
		start := time.Now()

		err = cycle.Run(t.Context(), func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			assert.True(t, ok)
			assert.Equal(t, start.Add(time.Second), deadline)
			<-ctx.Done()
			return ctx.Err()
		})

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, time.Second, time.Since(start))
	})
}

func TestBoundedCycleWaitDoesNotSpendOperationLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		controller, err := New(cfg, SystemClock{})
		require.NoError(t, err)
		gate, err := NewCycleGate(SystemClock{}, func(time.Duration) time.Duration { return 0 }, cfg.HTTPClient.Retry, 10*time.Minute)
		require.NoError(t, err)
		cycle := NewBoundedCycle(gate, controller, BinanceSpot, Instruments)
		require.NoError(t, cycle.Run(t.Context(), func(context.Context) error { return nil }))
		start := time.Now()

		err = cycle.Run(t.Context(), func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			assert.True(t, ok)
			assert.Equal(t, cfg.Upstream.LanesPerExchange.Instruments.CycleTimeout, time.Until(deadline))
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 10*time.Minute, time.Since(start))
	})
}

func TestBoundedCycleRejectsUnsupportedOperation(t *testing.T) {
	cfg := config.Defaults()
	controller, err := New(cfg, SystemClock{})
	require.NoError(t, err)
	gate, err := NewCycleGate(SystemClock{}, func(time.Duration) time.Duration { return 0 }, cfg.HTTPClient.Retry, time.Minute)
	require.NoError(t, err)
	cycle := NewBoundedCycle(gate, controller, Bybit, MarketStats)

	err = cycle.Run(t.Context(), func(context.Context) error { require.Fail(t, "unsupported cycle ran"); return nil })

	assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
}
