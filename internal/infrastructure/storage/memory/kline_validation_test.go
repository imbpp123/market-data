package memory

import (
	"math"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlineInvalidBatchWithUnknownExchangePreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.Exchange = "unknown"
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithUnknownMarketPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.Market = "inverse"
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithEmptySymbolPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.Symbol = ""
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithUnknownIntervalPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.Interval = "2m"
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithUnconfirmedCalendarPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.Exchange = domain.ExchangeBybit
	invalid.Candle.Interval = domain.Timeframe3d
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithUnalignedOpenTimePreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.OpenTime = testTime.Add(time.Second)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithWrongCloseTimePreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.CloseTime = testTime.Add(2 * time.Minute)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithRequestAfterReceiptPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.RequestStartedAt = invalid.Candle.FetchedAt.Add(time.Second)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithMissingRequestStartPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.RequestStartedAt = time.Time{}
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithMissingReceiptPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime, 2)
	invalid.Candle.FetchedAt = time.Time{}
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithPreEpochSlotPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(time.Unix(-60, 0).UTC(), 2)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchWithFutureSlotPreservesRows(t *testing.T) {
	ctx := t.Context()
	now := testTime.Add(3 * time.Minute)
	repository, err := NewKlineRepository(1000, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	valid := minuteCandle(testTime.Add(time.Minute), 3)
	invalid := minuteCandle(testTime.Add(time.Hour), 2)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now,
	}

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func TestKlineInvalidBatchPreservesEverySeries(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	bitcoin := minuteCandle(testTime, 1)
	ether := minuteCandle(testTime, 2)
	ether.Candle.Symbol = "ETHUSDT"
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{bitcoin, ether}))
	replacement := minuteCandle(testTime, 9)
	replacement.RequestStartedAt = testTime.Add(time.Second)
	invalid := minuteCandle(testTime, 3)
	invalid.Candle.Symbol = ""
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

	err = repository.UpsertMany(ctx, []kline.Stored{replacement, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	bitcoinRows, err := repository.GetRange(ctx, bitcoinRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{bitcoin}, bitcoinRows)
	etherRows, err := repository.GetRange(ctx, etherRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{ether}, etherRows)
}

func TestKlineInvalidBatchDoesNotAdvanceRetention(t *testing.T) {
	ctx := t.Context()
	now := testTime
	repository, err := NewKlineRepository(1, func() time.Time { return now })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	now = testTime.Add(3 * time.Minute)
	valid := minuteCandle(now, 2)
	invalid := minuteCandle(now, 3)
	invalid.Candle.Symbol = ""

	err = repository.UpsertMany(ctx, []kline.Stored{valid, invalid})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     now.Add(time.Minute),
	}
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)

	// An earlier valid merge proves that the failed batch saved no newer cutoff.
	now = testTime
	retained := minuteCandle(testTime.Add(-time.Minute), 4)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{retained}))
	requestedRange.From = retained.Candle.OpenTime
	rows, err = repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{retained, original}, rows)
}

func TestKlineBatchRejectsDuplicateTimeInstants(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	first := minuteCandle(testTime, 1)
	duplicate := minuteCandle(testTime.In(time.FixedZone("offset", 3600)), 2)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	err = repository.UpsertMany(ctx, []kline.Stored{first, duplicate})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestKlineRepositoryRejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name    string
		history int64
		clock   func() time.Time
	}{
		{name: "zero history", history: 0, clock: time.Now},
		{name: "negative history", history: -1, clock: time.Now},
		{name: "missing clock", history: 1, clock: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository, err := NewKlineRepository(tt.history, tt.clock)

			assert.ErrorIs(t, err, application.ErrInvalidParameter)
			assert.Nil(t, repository)
		})
	}
}

func TestKlineHistoryOverflowLeavesStorageEmpty(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(math.MaxInt64, func() time.Time { return testTime })
	require.NoError(t, err)
	incoming := minuteCandle(testTime, 1)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	err = repository.UpsertMany(ctx, []kline.Stored{incoming})

	assert.ErrorIs(t, err, domain.ErrTimeOverflow)
	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestKlineGetRangeRejectsUnknownScope(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}
	requestedRange.Exchange = "unknown"

	rows, err := repository.GetRange(ctx, requestedRange)

	assert.ErrorIs(t, err, application.ErrInvalidFilter)
	assert.Nil(t, rows)
}

func TestKlineGetRangeRejectsEmptySymbol(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}
	requestedRange.Symbol = ""

	rows, err := repository.GetRange(ctx, requestedRange)

	assert.ErrorIs(t, err, application.ErrInvalidFilter)
	assert.Nil(t, rows)
}

func TestKlineGetRangeRejectsUnknownInterval(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}
	requestedRange.Interval = "bad"

	rows, err := repository.GetRange(ctx, requestedRange)

	assert.ErrorIs(t, err, application.ErrInvalidInterval)
	assert.Nil(t, rows)
}

func TestKlineGetRangeRejectsReversedBounds(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}
	requestedRange.From = testTime.Add(2 * time.Minute)

	rows, err := repository.GetRange(ctx, requestedRange)

	assert.ErrorIs(t, err, application.ErrInvalidRange)
	assert.Nil(t, rows)
}

func TestKlineGetRangeRejectsUnalignedStart(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}
	requestedRange.From = testTime.Add(time.Second)

	rows, err := repository.GetRange(ctx, requestedRange)

	assert.ErrorIs(t, err, application.ErrInvalidRange)
	assert.Nil(t, rows)
}

func TestKlineGetRangeRejectsUnalignedEnd(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}
	requestedRange.To = testTime.Add(61 * time.Second)

	rows, err := repository.GetRange(ctx, requestedRange)

	assert.ErrorIs(t, err, application.ErrInvalidRange)
	assert.Nil(t, rows)
}

func TestKlineInvalidCleanupPreservesRows(t *testing.T) {
	tests := []struct {
		name          string
		scope         application.Scope
		interval      domain.Timeframe
		cutoff        time.Time
		expectedError error
	}{
		{name: "unknown scope", scope: application.Scope{}, interval: domain.Timeframe1m, cutoff: testTime, expectedError: application.ErrInvalidFilter},
		{name: "unknown interval", scope: binanceSpot, interval: "bad", cutoff: testTime, expectedError: application.ErrInvalidInterval},
		{name: "unaligned cutoff", scope: binanceSpot, interval: domain.Timeframe1m, cutoff: testTime.Add(time.Second), expectedError: application.ErrInvalidRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
			require.NoError(t, err)
			original := minuteCandle(testTime, 1)
			require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
			requestedRange := kline.Query{
				Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
				From:   testTime,
				To:     testTime.Add(time.Minute),
			}

			err = repository.DeleteBefore(ctx, tt.scope, tt.interval, tt.cutoff)

			assert.ErrorIs(t, err, tt.expectedError)
			rows, err := repository.GetRange(ctx, requestedRange)
			require.NoError(t, err)
			assert.Equal(t, []kline.Stored{original}, rows)
		})
	}
}
