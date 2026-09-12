package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlineConcurrentReadsWritesAndCleanup(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	expired := minuteCandle(testTime, 1)
	retained := minuteCandle(testTime.Add(time.Minute), 2)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{expired, retained}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}
	expectedSnapshots := [][]kline.Stored{
		{expired, retained},
		{retained},
	}

	const readerCount = 50
	const iterations = 30
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range readerCount {
		workers.Go(func() {
			<-start
			for range iterations {
				rows, err := repository.GetRange(ctx, requestedRange)
				if !assert.NoError(t, err) {
					return
				}
				assert.Contains(t, expectedSnapshots, rows)
			}
		})
	}
	workers.Go(func() {
		<-start
		for range iterations {
			assert.NoError(t, repository.UpsertMany(ctx, []kline.Stored{expired, retained}))
		}
	})
	workers.Go(func() {
		<-start
		for range iterations {
			assert.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, retained.Candle.OpenTime))
		}
	})
	close(start)
	workers.Wait()

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{retained}, rows)
}

func TestKlineCleanupPreventsAPendingFillFromRestoringExpiredRows(t *testing.T) {
	ctx := t.Context()
	clockEntered := make(chan struct{})
	releaseClock := make(chan struct{})
	now := testTime.Add(2 * time.Minute)
	clock := func() time.Time {
		close(clockEntered)
		<-releaseClock
		return now
	}
	repository, err := NewKlineRepository(1000, clock)
	require.NoError(t, err)
	expired := minuteCandle(testTime, 1)
	retained := minuteCandle(testTime.Add(time.Minute), 2)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}
	result := make(chan error, 1)
	go func() {
		result <- repository.UpsertMany(ctx, []kline.Stored{expired, retained})
	}()
	<-clockEntered

	cleanupError := repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, retained.Candle.OpenTime)
	close(releaseClock)

	require.NoError(t, cleanupError)
	require.NoError(t, <-result)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{retained}, rows)
}

func TestKlineCanceledFillPreservesRowsAndRetention(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	clockEntered := make(chan struct{})
	releaseClock := make(chan struct{})
	now := testTime.Add(3 * time.Minute)
	var pauseFirstRead sync.Once
	clock := func() time.Time {
		pauseFirstRead.Do(func() {
			close(clockEntered)
			<-releaseClock
		})
		return now
	}
	repository, err := NewKlineRepository(1, clock)
	require.NoError(t, err)
	incoming := minuteCandle(now, 1)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now.Add(time.Minute),
	}
	result := make(chan error, 1)
	go func() {
		result <- repository.UpsertMany(ctx, []kline.Stored{incoming})
	}()
	<-clockEntered

	cancel()
	close(releaseClock)

	assert.ErrorIs(t, <-result, context.Canceled)
	rows, err := repository.GetRange(t.Context(), requestedRange)
	require.NoError(t, err)
	assert.Empty(t, rows)

	// The canceled fill must not prevent a later valid write at an earlier clock value.
	now = testTime
	retained := minuteCandle(now, 2)
	require.NoError(t, repository.UpsertMany(t.Context(), []kline.Stored{retained}))
	rows, err = repository.GetRange(t.Context(), requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{retained}, rows)
}

func TestKlineOperationsHonorCancellation(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithCancel(ctx)
	cancel()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	err = repository.UpsertMany(failed, nil)
	assert.ErrorIs(t, err, context.Canceled)
	err = repository.UpsertMany(failed, []kline.Stored{original})
	assert.ErrorIs(t, err, context.Canceled)
	err = repository.DeleteBefore(failed, binanceSpot, domain.Timeframe1m, testTime.Add(time.Minute))
	assert.ErrorIs(t, err, context.Canceled)
	_, err = repository.GetRange(failed, requestedRange)
	assert.ErrorIs(t, err, context.Canceled)

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineOperationsHonorDeadline(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithDeadline(ctx, time.Unix(0, 0))
	defer cancel()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	err = repository.UpsertMany(failed, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	err = repository.UpsertMany(failed, []kline.Stored{original})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	err = repository.DeleteBefore(failed, binanceSpot, domain.Timeframe1m, testTime.Add(time.Minute))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = repository.GetRange(failed, requestedRange)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}
