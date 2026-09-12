package memory

import (
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlineMergeRetainsCutoffAndDoesNotExtendHistoryAcrossGaps(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(2, func() time.Time { return now })
	require.NoError(t, err)
	expired := minuteCandle(testTime, 1)
	atCutoff := minuteCandle(testTime.Add(time.Minute), 2)
	current := minuteCandle(now, 3)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{current, expired, atCutoff}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{atCutoff, current}, rows)
}

func TestKlineCleanupKeepsRowsAtTheCutoff(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	expired := minuteCandle(testTime, 1)
	atCutoff := minuteCandle(now, 2)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{expired, atCutoff}))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now.Add(time.Minute),
	}

	require.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, now))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{atCutoff}, rows)
}

func TestKlineOlderCleanupCannotLowerAppliedCutoff(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	expired := minuteCandle(testTime, 1)
	atCutoff := minuteCandle(now, 2)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{expired, atCutoff}))
	require.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, now))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now.Add(time.Minute),
	}

	require.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, testTime))
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{expired}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{atCutoff}, rows)
}

func TestKlineClockRollbackCannotReinsertExpiredRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(2, func() time.Time { return now })
	require.NoError(t, err)
	current := minuteCandle(now, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{current}))
	require.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, now))
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now.Add(time.Minute),
	}
	expired := minuteCandle(testTime, 2)

	now = testTime
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{expired}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{current}, rows)
}

func TestKlineMergePrunesPreviouslyStoredExpiredRows(t *testing.T) {
	ctx := t.Context()
	now := testTime
	repository, err := NewKlineRepository(2, func() time.Time { return now })
	require.NoError(t, err)
	old := minuteCandle(now, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{old}))
	now = testTime.Add(3 * time.Minute)
	incoming := minuteCandle(now, 2)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{incoming}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{incoming}, rows)
}

func TestKlineExpiredWritesLeaveNoSeriesMetadata(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	require.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, testTime.Add(time.Minute)))
	unknownSymbol := minuteCandle(testTime, 2)
	unknownSymbol.Candle.Symbol = "ETHUSDT"
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original, unknownSymbol}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Empty(t, rows)
	// The resource bound includes metadata, which is not exposed by range reads.
	assert.Empty(t, repository.(*klineRepository).series)
}

func TestKlineCleanupDeletesEverySymbolInItsScope(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	bitcoin := minuteCandle(testTime, 1)
	ether := minuteCandle(testTime, 2)
	ether.Candle.Symbol = "ETHUSDT"
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{bitcoin, ether}))
	bitcoinRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}
	etherRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "ETHUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, testTime.Add(time.Minute)))

	bitcoinRows, err := repository.GetRange(ctx, bitcoinRange)
	require.NoError(t, err)
	assert.Empty(t, bitcoinRows)
	etherRows, err := repository.GetRange(ctx, etherRange)
	require.NoError(t, err)
	assert.Empty(t, etherRows)
}

func TestKlineSeriesKeysAreIndependent(t *testing.T) {
	tests := []struct {
		name      string
		scope     application.Scope
		symbol    string
		interval  domain.Timeframe
		closeTime time.Time
	}{{name: "other symbol", scope: binanceSpot, symbol: "ETHUSDT", interval: domain.Timeframe1m, closeTime: testTime.Add(time.Minute)},

		{name: "other market", scope: binanceLinear, symbol: "BTCUSDT", interval: domain.Timeframe1m, closeTime: testTime.Add(time.Minute)},
		{name: "other exchange", scope: bybitSpot, symbol: "BTCUSDT", interval: domain.Timeframe1m, closeTime: testTime.Add(time.Minute)},
		{name: "other interval", scope: binanceSpot, symbol: "BTCUSDT", interval: domain.Timeframe1h, closeTime: testTime.Add(time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			now := testTime.Add(time.Hour)
			repository, err := NewKlineRepository(1000, func() time.Time { return now })
			require.NoError(t, err)
			original := minuteCandle(testTime, 1)
			other := minuteCandle(testTime, 2)
			other.Candle.Exchange = tt.scope.Exchange
			other.Candle.Market = tt.scope.Market
			other.Candle.Symbol = tt.symbol
			other.Candle.Interval = tt.interval
			other.Candle.CloseTime = tt.closeTime
			originalRange := kline.Query{
				Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
				From:   testTime,
				To:     testTime.Add(time.Minute),
			}
			otherRange := kline.Query{
				Series: kline.Series{Scope: tt.scope, Symbol: tt.symbol, Interval: tt.interval},
				From:   testTime,
				To:     tt.closeTime,
			}

			require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original, other}))

			originalRows, err := repository.GetRange(ctx, originalRange)
			require.NoError(t, err)
			assert.Equal(t, []kline.Stored{original}, originalRows)
			otherRows, err := repository.GetRange(ctx, otherRange)
			require.NoError(t, err)
			assert.Equal(t, []kline.Stored{other}, otherRows)
		})
	}
}

func TestKlineCleanupPreservesOtherScopes(t *testing.T) {
	tests := []struct {
		name      string
		scope     application.Scope
		symbol    string
		interval  domain.Timeframe
		closeTime time.Time
	}{
		{name: "other market", scope: binanceLinear, symbol: "BTCUSDT", interval: domain.Timeframe1m, closeTime: testTime.Add(time.Minute)},
		{name: "other exchange", scope: bybitSpot, symbol: "BTCUSDT", interval: domain.Timeframe1m, closeTime: testTime.Add(time.Minute)},
		{name: "other interval", scope: binanceSpot, symbol: "BTCUSDT", interval: domain.Timeframe1h, closeTime: testTime.Add(time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			now := testTime.Add(time.Hour)
			repository, err := NewKlineRepository(1000, func() time.Time { return now })
			require.NoError(t, err)
			original := minuteCandle(testTime, 1)
			other := minuteCandle(testTime, 2)
			other.Candle.Exchange = tt.scope.Exchange
			other.Candle.Market = tt.scope.Market
			other.Candle.Symbol = tt.symbol
			other.Candle.Interval = tt.interval
			other.Candle.CloseTime = tt.closeTime
			originalRange := kline.Query{
				Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
				From:   testTime,
				To:     testTime.Add(time.Minute),
			}
			otherRange := kline.Query{
				Series: kline.Series{Scope: tt.scope, Symbol: tt.symbol, Interval: tt.interval},
				From:   testTime,
				To:     tt.closeTime,
			}
			require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original, other}))

			require.NoError(t, repository.DeleteBefore(ctx, binanceSpot, domain.Timeframe1m, testTime.Add(time.Minute)))

			originalRows, err := repository.GetRange(ctx, originalRange)
			require.NoError(t, err)
			assert.Empty(t, originalRows)
			otherRows, err := repository.GetRange(ctx, otherRange)
			require.NoError(t, err)
			assert.Equal(t, []kline.Stored{other}, otherRows)
		})
	}
}

func TestKlineRetentionUsesTheExchangeCalendar(t *testing.T) {
	tests := []struct {
		name     string
		interval domain.Timeframe
		now      time.Time
		expired  time.Time
		cutoff   time.Time
		next     time.Time
	}{
		{
			name:     "leap month",
			interval: domain.Timeframe1M,
			now:      time.Date(2028, 4, 15, 0, 0, 0, 0, time.UTC),
			expired:  time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC),
			cutoff:   time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC),
			next:     time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "week",
			interval: domain.Timeframe1w,
			now:      time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
			expired:  time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
			cutoff:   time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
			next:     time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "binance three days",
			interval: domain.Timeframe3d,
			now:      time.Date(1970, 1, 11, 12, 0, 0, 0, time.UTC),
			expired:  time.Date(1970, 1, 2, 0, 0, 0, 0, time.UTC),
			cutoff:   time.Date(1970, 1, 5, 0, 0, 0, 0, time.UTC),
			next:     time.Date(1970, 1, 8, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository, err := NewKlineRepository(2, func() time.Time { return tt.now })
			require.NoError(t, err)
			expired := minuteCandle(tt.expired, 1)
			expired.Candle.Interval = tt.interval
			expired.Candle.CloseTime = tt.cutoff
			expired.RequestStartedAt = tt.now
			expired.Candle.FetchedAt = tt.now
			retained := minuteCandle(tt.cutoff, 2)
			retained.Candle.Interval = tt.interval
			retained.Candle.CloseTime = tt.next
			retained.RequestStartedAt = tt.now
			retained.Candle.FetchedAt = tt.now
			requestedRange := kline.Query{
				Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: tt.interval},
				From:   tt.expired,
				To:     tt.next,
			}

			require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{expired, retained}))

			rows, err := repository.GetRange(ctx, requestedRange)
			require.NoError(t, err)
			assert.Equal(t, []kline.Stored{retained}, rows)
		})
	}
}

func TestKlineStorageRetains1000ClosedSlotsAndTheCurrentSlot(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	input := make([]kline.Stored, 0, 1002)
	for slot := -1001; slot <= 0; slot++ {
		input = append(input, minuteCandle(testTime.Add(time.Duration(slot)*time.Minute), 1))
	}
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime.Add(-1001 * time.Minute),
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, input))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	require.Len(t, rows, 1001)
	assert.Equal(t, testTime.Add(-1000*time.Minute), rows[0].Candle.OpenTime)
	assert.Equal(t, testTime, rows[1000].Candle.OpenTime)
}
