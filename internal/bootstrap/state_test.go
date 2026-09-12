package bootstrap

import (
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalStorageStartsWithUnreadySnapshots(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}

	state, err := newLocalState(1, func() time.Time { return now })

	require.NoError(t, err)
	assert.False(t, state.ready.Load())
	instrumentReady, err := state.instruments.HasSnapshot(ctx, scope)
	require.NoError(t, err)
	assert.False(t, instrumentReady)
	tickerReady, err := state.tickers.HasSnapshot(ctx, scope)
	require.NoError(t, err)
	assert.False(t, tickerReady)
	statsReady, err := state.marketStats.HasSnapshot(ctx, scope, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, statsReady)
}

func TestLocalStorageSnapshotReadinessIsIndependent(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	state, err := newLocalState(1, func() time.Time { return now })
	require.NoError(t, err)
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}

	require.NoError(t, state.instruments.ReplaceSnapshot(ctx, scope, nil))

	instrumentReady, err := state.instruments.HasSnapshot(ctx, scope)
	require.NoError(t, err)
	assert.True(t, instrumentReady)
	tickerReady, err := state.tickers.HasSnapshot(ctx, scope)
	require.NoError(t, err)
	assert.False(t, tickerReady)
	statsReady, err := state.marketStats.HasSnapshot(ctx, scope, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, statsReady)
}

func TestLocalStorageUsesConfiguredHistoryAndClock(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	state, err := newLocalState(1, func() time.Time { return now })
	require.NoError(t, err)
	series := kline.Series{
		Scope:    application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot},
		Symbol:   "BTCUSDT",
		Interval: domain.Timeframe1m,
	}
	expired := kline.Stored{
		Candle: domain.Kline{
			Exchange:  domain.ExchangeBinance,
			Market:    domain.MarketSpot,
			Symbol:    "BTCUSDT",
			Interval:  domain.Timeframe1m,
			OpenTime:  now.Add(-2 * time.Minute),
			CloseTime: now.Add(-time.Minute),
			FetchedAt: now,
		},
		RequestStartedAt: now,
	}
	retained := kline.Stored{
		Candle: domain.Kline{
			Exchange:  domain.ExchangeBinance,
			Market:    domain.MarketSpot,
			Symbol:    "BTCUSDT",
			Interval:  domain.Timeframe1m,
			OpenTime:  now.Add(-time.Minute),
			CloseTime: now,
			FetchedAt: now,
		},
		RequestStartedAt: now,
	}
	query := kline.Query{
		Series: series,
		From:   now.Add(-2 * time.Minute),
		To:     now,
	}

	require.NoError(t, state.klines.UpsertMany(ctx, []kline.Stored{expired, retained}))

	rows, err := state.klines.GetRange(ctx, query)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, retained.Candle.OpenTime, rows[0].Candle.OpenTime)
	assert.Equal(t, retained.Candle.CloseTime, rows[0].Candle.CloseTime)
	assert.Equal(t, now, rows[0].RequestStartedAt)
}

func TestLocalStorageRejectsInvalidHistory(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	state, err := newLocalState(0, func() time.Time { return now })

	assert.ErrorIs(t, err, application.ErrInvalidParameter)
	assert.Nil(t, state)
}
