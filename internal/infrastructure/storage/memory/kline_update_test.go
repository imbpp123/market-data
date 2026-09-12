package memory

import (
	"testing"
	"time"

	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlineOlderRequestCannotReplaceNewerValue(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(20 * time.Second)
	original.Candle.FetchedAt = testTime.Add(30 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(10 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(61 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineNewerRequestReplacesValueDespiteEarlierReceipt(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(20 * time.Second)
	original.Candle.FetchedAt = testTime.Add(30 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(21 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(25 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{incoming}, rows)
}

func TestKlineNewerReceiptBreaksRequestStartTie(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(20 * time.Second)
	original.Candle.FetchedAt = testTime.Add(30 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(20 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(31 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{incoming}, rows)
}

func TestKlineOlderReceiptCannotBreakRequestStartTie(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(20 * time.Second)
	original.Candle.FetchedAt = testTime.Add(30 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(20 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(29 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineEqualMetadataKeepsStoredValue(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(20 * time.Second)
	original.Candle.FetchedAt = testTime.Add(30 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(20 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(30 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineLateReceiptDoesNotGiveOlderRequestPriority(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(20 * time.Second)
	original.Candle.FetchedAt = testTime.Add(30 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(19 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(61 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineRequestAtCloseConfirmsCandle(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(20 * time.Second)
	original.Candle.FetchedAt = testTime.Add(30 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(60 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(61 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{incoming}, rows)
}

func TestKlineFinalCandleRejectsIntermediateUpdate(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(60 * time.Second)
	original.Candle.FetchedAt = testTime.Add(61 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(50 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(80 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineFinalCandleRejectsLaterFinalUpdate(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(2 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.RequestStartedAt = testTime.Add(60 * time.Second)
	original.Candle.FetchedAt = testTime.Add(61 * time.Second)
	incoming := minuteCandle(testTime, 9)
	incoming.RequestStartedAt = testTime.Add(61 * time.Second)
	incoming.Candle.FetchedAt = testTime.Add(62 * time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineClockAdvanceDoesNotConfirmCandle(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(30 * time.Second)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	now = testTime.Add(2 * time.Minute)

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)

	confirmed := minuteCandle(testTime, 9)
	confirmed.RequestStartedAt = now
	confirmed.Candle.FetchedAt = now.Add(time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{confirmed}))
	rows, err = repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{confirmed}, rows)
}

func TestKlineResponseMayCrossTheOpeningBoundary(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(time.Second)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	incoming := minuteCandle(testTime, 1)
	incoming.RequestStartedAt = testTime.Add(-time.Second)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{incoming}, rows)
}

func TestKlineEquivalentTimeZonesUpdateTheSameSlot(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	offset := time.FixedZone("offset", 3600)
	original := minuteCandle(testTime.In(offset), 1)
	incoming := minuteCandle(testTime, 2)
	incoming.RequestStartedAt = testTime.Add(time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{incoming}, rows)
}

func TestKlineOverlappingMergePreservesNeighbors(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	first := minuteCandle(testTime, 1)
	middle := minuteCandle(testTime.Add(time.Minute), 2)
	last := minuteCandle(testTime.Add(2*time.Minute), 3)
	replacement := minuteCandle(middle.Candle.OpenTime, 9)
	replacement.RequestStartedAt = middle.RequestStartedAt.Add(time.Second)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{first, middle, last}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{first, replacement}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	expected := []kline.Stored{first, replacement, last}
	assert.Equal(t, expected, rows)
}
