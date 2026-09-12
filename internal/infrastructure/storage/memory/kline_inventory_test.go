package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCandleCountsFollowConcurrentWritesAndCleanup(t *testing.T) {
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime.Add(time.Minute) })
	require.NoError(t, err)
	inventory := repository.(kline.Inventory)
	var owned sync.WaitGroup
	for range 20 {
		owned.Go(func() {
			assert.NoError(t, repository.UpsertMany(t.Context(), []kline.Stored{minuteCandle(testTime, 1), minuteCandle(testTime.Add(time.Minute), 2)}))
			_, err := inventory.CandleCounts(t.Context())
			assert.NoError(t, err)
			assert.NoError(t, repository.DeleteBefore(t.Context(), binanceSpot, domain.Timeframe1m, testTime.Add(time.Minute)))
		})
	}
	owned.Wait()

	counts, err := inventory.CandleCounts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[application.Scope]int{binanceSpot: 1}, counts)
	delete(counts, binanceSpot)
	counts, err = inventory.CandleCounts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, counts[binanceSpot])
	require.NoError(t, repository.DeleteBefore(t.Context(), binanceSpot, domain.Timeframe1m, testTime.Add(2*time.Minute)))
	counts, err = inventory.CandleCounts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, counts)
}

func TestCandleCountsHonorCancellation(t *testing.T) {
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	counts, err := repository.(kline.Inventory).CandleCounts(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, counts)
}
