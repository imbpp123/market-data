package memory

import (
	"testing"
	"time"

	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlineGetRangeReturnsEmptyForMissingSeries(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	query := kline.Query{
		Series: kline.Series{
			Scope:    binanceSpot,
			Symbol:   "BTCUSDT",
			Interval: domain.Timeframe1m,
		},
		From: testTime,
		To:   testTime.Add(time.Minute),
	}

	rows, err := repository.GetRange(ctx, query)

	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)
}

func TestKlineGetRangeUsesOrderedHalfOpenRanges(t *testing.T) {
	first := minuteCandle(testTime, 1)
	second := minuteCandle(testTime.Add(time.Minute), 2)
	fourth := minuteCandle(testTime.Add(3*time.Minute), 3)
	tests := []struct {
		name     string
		from     time.Time
		to       time.Time
		expected []kline.Stored
	}{
		{
			name:     "sorted rows with a gap",
			from:     testTime,
			to:       testTime.Add(4 * time.Minute),
			expected: []kline.Stored{first, second, fourth},
		},
		{
			name:     "exclusive end",
			from:     testTime,
			to:       testTime.Add(time.Minute),
			expected: []kline.Stored{first},
		},
		{
			name:     "inclusive start",
			from:     testTime.Add(time.Minute),
			to:       testTime.Add(3 * time.Minute),
			expected: []kline.Stored{second},
		},
		{
			name:     "gap",
			from:     testTime.Add(2 * time.Minute),
			to:       testTime.Add(3 * time.Minute),
			expected: []kline.Stored{},
		},
		{
			name:     "empty range",
			from:     testTime,
			to:       testTime,
			expected: []kline.Stored{},
		},
		{
			name:     "missing range",
			from:     testTime.Add(10 * time.Minute),
			to:       testTime.Add(11 * time.Minute),
			expected: []kline.Stored{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository, err := NewKlineRepository(1000, func() time.Time { return testTime.Add(5 * time.Minute) })
			require.NoError(t, err)
			require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{fourth, first, second}))
			query := kline.Query{
				Series: kline.Series{
					Scope:    binanceSpot,
					Symbol:   "BTCUSDT",
					Interval: domain.Timeframe1m,
				},
				From: tt.from,
				To:   tt.to,
			}

			rows, err := repository.GetRange(ctx, query)

			require.NoError(t, err)
			assert.Equal(t, tt.expected, rows)
		})
	}
}

func TestKlineUpsertManyCopiesInput(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	input := []kline.Stored{minuteCandle(testTime, 1)}
	expected := minuteCandle(testTime, 1)
	query := kline.Query{
		Series: kline.Series{
			Scope:    binanceSpot,
			Symbol:   "BTCUSDT",
			Interval: domain.Timeframe1m,
		},
		From: testTime,
		To:   testTime.Add(time.Minute),
	}
	require.NoError(t, repository.UpsertMany(ctx, input))

	*input[0].Candle.TradesCount = 100
	input[0] = kline.Stored{}

	rows, err := repository.GetRange(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{expected}, rows)
}

func TestKlineGetRangeReturnsOwnedValues(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	input := []kline.Stored{minuteCandle(testTime, 1)}
	expected := minuteCandle(testTime, 1)
	query := kline.Query{
		Series: kline.Series{
			Scope:    binanceSpot,
			Symbol:   "BTCUSDT",
			Interval: domain.Timeframe1m,
		},
		From: testTime,
		To:   testTime.Add(time.Minute),
	}
	require.NoError(t, repository.UpsertMany(ctx, input))
	rows, err := repository.GetRange(ctx, query)
	require.NoError(t, err)
	require.Equal(t, []kline.Stored{expected}, rows)

	*rows[0].Candle.TradesCount = 200
	rows[0] = kline.Stored{}

	stored, err := repository.GetRange(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{expected}, stored)
}

func TestKlineEmptyUpsertPreservesRows(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))
	query := kline.Query{
		Series: kline.Series{
			Scope:    binanceSpot,
			Symbol:   "BTCUSDT",
			Interval: domain.Timeframe1m,
		},
		From: testTime,
		To:   testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, nil))

	rows, err := repository.GetRange(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}

func minuteCandle(openTime time.Time, price int64) kline.Stored {
	return kline.Stored{
		Candle: domain.Kline{
			Exchange:    domain.ExchangeBinance,
			Market:      domain.MarketSpot,
			Symbol:      "BTCUSDT",
			Interval:    domain.Timeframe1m,
			OpenTime:    openTime,
			CloseTime:   openTime.Add(time.Minute),
			Open:        decimal.NewFromInt(price),
			High:        decimal.NewFromInt(price),
			Low:         decimal.NewFromInt(price),
			Close:       decimal.NewFromInt(price),
			Volume:      decimal.NewFromInt(1),
			Turnover:    decimal.NewFromInt(2),
			TradesCount: pointer(int64(3)),
			FetchedAt:   openTime.Add(time.Second),
		},
		RequestStartedAt: openTime,
	}
}

func TestKlineStoragePreservesAbsentTradeCount(t *testing.T) {
	ctx := t.Context()
	repository, err := NewKlineRepository(1000, func() time.Time { return testTime })
	require.NoError(t, err)
	original := minuteCandle(testTime, 1)
	original.Candle.TradesCount = nil
	requestedRange := kline.Query{
		Series: kline.Series{Scope: binanceSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m},
		From:   testTime,
		To:     testTime.Add(time.Minute),
	}

	require.NoError(t, repository.UpsertMany(ctx, []kline.Stored{original}))

	rows, err := repository.GetRange(ctx, requestedRange)
	require.NoError(t, err)
	assert.Equal(t, []kline.Stored{original}, rows)
}
