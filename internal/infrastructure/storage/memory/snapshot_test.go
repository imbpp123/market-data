package memory

import (
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	binanceSpot   = application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	binanceLinear = application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}
	bybitSpot     = application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
	testTime      = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
)

func pointer[T any](value T) *T { return &value }

func TestInstrumentPublicationDoesNotMakeOtherRepositoriesReady(t *testing.T) {
	ctx := t.Context()
	instruments := NewInstrumentRepository()
	tickers := NewTickerRepository()
	stats := NewMarketStatsRepository()

	require.NoError(t, instruments.ReplaceSnapshot(ctx, binanceSpot, nil))

	instrumentReady, err := instruments.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.True(t, instrumentReady)
	tickerReady, err := tickers.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.False(t, tickerReady)
	statsReady, err := stats.HasSnapshot(ctx, binanceSpot, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, statsReady)
}

func TestTickerPublicationDoesNotMakeMarketStatsReady(t *testing.T) {
	ctx := t.Context()
	tickers := NewTickerRepository()
	stats := NewMarketStatsRepository()

	require.NoError(t, tickers.ReplaceSnapshot(ctx, binanceSpot, nil))

	tickerReady, err := tickers.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.True(t, tickerReady)
	statsReady, err := stats.HasSnapshot(ctx, binanceSpot, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, statsReady)
}

func TestFailedTickerReplacementPreservesMarketStats(t *testing.T) {
	ctx := t.Context()
	tickers := NewTickerRepository()
	stats := NewMarketStatsRepository()
	ticker := tickerRow(binanceSpot, "BTCUSDT", testTime)
	statistic := statsRow(binanceSpot, "BTCUSDT", testTime)
	require.NoError(t, tickers.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{ticker}))
	require.NoError(t, stats.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{statistic}))

	err := tickers.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{ticker, ticker})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	actual, found, err := stats.Get(ctx, binanceSpot, "BTCUSDT", 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, statistic, actual)
}

func TestFailedMarketStatsReplacementPreservesTicker(t *testing.T) {
	ctx := t.Context()
	tickers := NewTickerRepository()
	stats := NewMarketStatsRepository()
	ticker := tickerRow(binanceSpot, "BTCUSDT", testTime)
	statistic := statsRow(binanceSpot, "BTCUSDT", testTime)
	require.NoError(t, tickers.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{ticker}))
	require.NoError(t, stats.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{statistic}))

	err := stats.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{statistic, statistic})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	actual, found, err := tickers.Get(ctx, binanceSpot, "BTCUSDT")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, ticker, actual)
}
