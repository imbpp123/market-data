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

func TestFailedCyclesKeepBackoffUntilFullSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate, err := NewCycleGate(SystemClock{}, func(d time.Duration) time.Duration { return d }, config.Defaults().HTTPClient.Retry, 0)
		require.NoError(t, err)
		require.NoError(t, gate.wait(t.Context()))
		for _, delay := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond, 2 * time.Second, 2 * time.Second} {
			start := time.Now()
			gate.complete(false)
			require.NoError(t, gate.wait(t.Context()))
			assert.Equal(t, delay, time.Since(start))
		}
		gate.complete(true)
		start := time.Now()
		require.NoError(t, gate.wait(t.Context()))
		assert.Equal(t, time.Duration(0), time.Since(start))
		gate.complete(false)
		require.NoError(t, gate.wait(t.Context()))
		assert.Equal(t, 100*time.Millisecond, time.Since(start))
	})
}

func TestCycleIntervalAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate, err := NewCycleGate(SystemClock{}, func(d time.Duration) time.Duration { return d }, config.Defaults().HTTPClient.Retry, 30*time.Second)
		require.NoError(t, err)
		start := time.Now()
		gate.complete(false)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		assert.ErrorIs(t, gate.wait(ctx), context.DeadlineExceeded)
		require.NoError(t, gate.wait(t.Context()))
		assert.Equal(t, 30*time.Second, time.Since(start))
	})
}

func TestJitterAndBackoffBounds(t *testing.T) {
	settings := config.Defaults().HTTPClient.Retry
	for _, tt := range []struct {
		name   string
		jitter Jitter
		want   time.Duration
	}{{"zero", func(time.Duration) time.Duration { return 0 }, 0}, {"half", func(d time.Duration) time.Duration { return d / 2 }, time.Second}, {"negative", func(time.Duration) time.Duration { return -1 }, 0}, {"too large", func(time.Duration) time.Duration { return time.Hour }, 2 * time.Second}} {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, backoff(settings, 100, tt.jitter)) })
	}
	_, err := NewCycleGate(nil, func(d time.Duration) time.Duration { return d }, settings, 0)
	assert.Error(t, err)
	_, err = NewCycleGate(SystemClock{}, nil, settings, 0)
	assert.Error(t, err)
}

func TestWorkerCyclesCannotBypassBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate, err := NewCycleGate(SystemClock{}, func(d time.Duration) time.Duration { return d }, config.Defaults().HTTPClient.Retry, 0)
		require.NoError(t, err)
		start := time.Now()
		for _, elapsed := range []time.Duration{0, 100 * time.Millisecond, 300 * time.Millisecond} {
			err := gate.Run(t.Context(), func(ctx context.Context) error {
				assert.Equal(t, elapsed, time.Since(start))
				nested := gate.Run(ctx, func(context.Context) error { return nil })
				assert.ErrorIs(t, nested, application.ErrServiceOverloaded)
				return application.ErrInvalidUpstreamData
			})
			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
		}
		require.NoError(t, gate.Run(t.Context(), func(context.Context) error { return nil }))
		reset := time.Now()
		require.NoError(t, gate.Run(t.Context(), func(context.Context) error { return nil }))
		assert.Equal(t, reset, time.Now())
	})
}
