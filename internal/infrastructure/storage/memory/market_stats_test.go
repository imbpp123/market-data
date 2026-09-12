package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarketStatsReadsRejectUnreadyScope(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}

	ready, err := repository.HasSnapshot(ctx, binanceSpot, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, ready)

	rows, err := repository.List(ctx, filter)
	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)

	_, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT", 24*time.Hour)
	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.False(t, found)
}

func TestMarketStatsEmptySnapshotIsReady(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, nil))

	ready, err := repository.HasSnapshot(ctx, binanceSpot, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, ready)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)

	_, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT", 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestMarketStatsReplaceSnapshotRemovesAbsentSymbols(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	original := []domain.MarketStats{
		statsRow(binanceSpot, "BTCUSDT", testTime),
		statsRow(binanceSpot, "ETHUSDT", testTime),
	}
	replacement := statsRow(binanceSpot, "BTCUSDT", testTime.Add(time.Second))
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, original))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{replacement}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{replacement}, rows)

	actual, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT", 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, replacement, actual)

	_, found, err = repository.Get(ctx, binanceSpot, "ETHUSDT", 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestMarketStatsReplaceSnapshotPreservesOtherScopes(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	original := statsRow(binanceSpot, "BTCUSDT", testTime)
	otherMarket := statsRow(binanceLinear, "BTCUSDT", testTime)
	otherExchange := statsRow(bybitSpot, "BTCUSDT", testTime)
	replacement := statsRow(binanceSpot, "ETHUSDT", testTime)
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, binanceLinear, bybitSpot}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{original}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, 24*time.Hour, []domain.MarketStats{otherMarket}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, bybitSpot, 24*time.Hour, []domain.MarketStats{otherExchange}))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{replacement}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	expected := []domain.MarketStats{otherMarket, replacement, otherExchange}
	assert.Equal(t, expected, rows)
}

func TestMarketStatsEmptyReplacementClearsOnlyItsScope(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	original := statsRow(binanceSpot, "BTCUSDT", testTime)
	other := statsRow(binanceLinear, "BTCUSDT", testTime)
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, binanceLinear}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{original}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, 24*time.Hour, []domain.MarketStats{other}))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{other}, rows)
	ready, err := repository.HasSnapshot(ctx, binanceSpot, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, ready)
}

func TestMarketStatsInvalidReplacementPreservesSnapshot(t *testing.T) {
	tests := []struct {
		name string
		rows []domain.MarketStats
	}{
		{
			name: "duplicate symbol",
			rows: []domain.MarketStats{
				statsRow(binanceSpot, "ETHUSDT", testTime),
				statsRow(binanceSpot, "ETHUSDT", testTime),
			},
		},
		{
			name: "wrong market",
			rows: []domain.MarketStats{
				statsRow(binanceSpot, "ETHUSDT", testTime),
				statsRow(binanceLinear, "BTCUSDT", testTime),
			},
		},
		{
			name: "wrong exchange",
			rows: []domain.MarketStats{
				statsRow(binanceSpot, "ETHUSDT", testTime),
				statsRow(bybitSpot, "BTCUSDT", testTime),
			},
		},
		{
			name: "empty symbol",
			rows: []domain.MarketStats{statsRow(binanceSpot, "", testTime)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository := NewMarketStatsRepository()
			original := statsRow(binanceSpot, "BTCUSDT", testTime)
			filter := marketstats.Filter{
				SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
				Window:         24 * time.Hour,
			}
			require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{original}))

			err := repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, tt.rows)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			rows, err := repository.List(ctx, filter)
			require.NoError(t, err)
			assert.Equal(t, []domain.MarketStats{original}, rows)
		})
	}
}

func TestMarketStatsFailedInitialSnapshotStaysUnready(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	wrongScope := statsRow(binanceLinear, "BTCUSDT", testTime)

	err := repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{wrongScope})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	ready, err := repository.HasSnapshot(ctx, binanceSpot, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, ready)
}

func TestMarketStatsReplaceSnapshotRejectsInvalidScope(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	invalidScope := application.Scope{}

	err := repository.ReplaceSnapshot(ctx, invalidScope, 24*time.Hour, nil)

	assert.ErrorIs(t, err, application.ErrInvalidFilter)
	ready, err := repository.HasSnapshot(ctx, invalidScope, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, ready)
}

func TestMarketStatsListSortsRowsAndIgnoresRepeatedScopes(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	bitcoin := statsRow(binanceSpot, "BTCUSDT", testTime)
	ether := statsRow(binanceSpot, "ETHUSDT", testTime)
	otherMarket := statsRow(binanceLinear, "BTCUSDT", testTime)
	otherExchange := statsRow(bybitSpot, "BTCUSDT", testTime)
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{ether, bitcoin}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, 24*time.Hour, []domain.MarketStats{otherMarket}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, bybitSpot, 24*time.Hour, []domain.MarketStats{otherExchange}))
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{bybitSpot, binanceSpot, binanceLinear, binanceSpot}},
		Window:         24 * time.Hour,
	}

	rows, err := repository.List(ctx, filter)

	require.NoError(t, err)
	expected := []domain.MarketStats{otherMarket, bitcoin, ether, otherExchange}
	assert.Equal(t, expected, rows)
}

func TestMarketStatsListFiltersBySymbol(t *testing.T) {
	tests := []struct {
		name     string
		symbol   string
		expected []domain.MarketStats
	}{
		{
			name:     "existing symbol",
			symbol:   "BTCUSDT",
			expected: []domain.MarketStats{statsRow(binanceSpot, "BTCUSDT", testTime)},
		},
		{
			name:     "absent symbol",
			symbol:   "missing",
			expected: []domain.MarketStats{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository := NewMarketStatsRepository()
			input := []domain.MarketStats{
				statsRow(binanceSpot, "BTCUSDT", testTime),
				statsRow(binanceSpot, "ETHUSDT", testTime),
			}
			require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, input))
			filter := marketstats.Filter{
				SnapshotFilter: application.SnapshotFilter{
					Scopes: []application.Scope{binanceSpot},
					Symbol: pointer(tt.symbol),
				},
				Window: 24 * time.Hour,
			}

			rows, err := repository.List(ctx, filter)

			require.NoError(t, err)
			assert.Equal(t, tt.expected, rows)
		})
	}
}

func TestMarketStatsListWithoutScopesSelectsNothing(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	original := statsRow(binanceSpot, "BTCUSDT", testTime)
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{original}))
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: nil},
		Window:         24 * time.Hour,
	}

	rows, err := repository.List(ctx, filter)

	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)
}

func TestMarketStatsSymbolFilterCannotHideUnreadyScope(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, nil))
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, bybitSpot}},
		Window:         24 * time.Hour,
	}
	filter.Symbol = pointer("missing")

	rows, err := repository.List(ctx, filter)

	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)
}

func TestMarketStatsReplaceSnapshotCopiesInput(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	input := []domain.MarketStats{statsRow(binanceSpot, "BTCUSDT", testTime)}
	expected := statsRow(binanceSpot, "BTCUSDT", testTime)
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, input))

	*input[0].PriceChange = decimal.NewFromInt(100)
	*input[0].TradeCount = 200
	input[0] = domain.MarketStats{}

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{expected}, stored)
}

func TestMarketStatsListReturnsOwnedValues(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	input := []domain.MarketStats{statsRow(binanceSpot, "BTCUSDT", testTime)}
	expected := statsRow(binanceSpot, "BTCUSDT", testTime)
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, input))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, []domain.MarketStats{expected}, rows)

	*rows[0].PriceChange = decimal.NewFromInt(100)
	*rows[0].TradeCount = 200
	rows[0] = domain.MarketStats{}

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{expected}, stored)
}

func TestMarketStatsGetReturnsOwnedValues(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	input := []domain.MarketStats{statsRow(binanceSpot, "BTCUSDT", testTime)}
	expected := statsRow(binanceSpot, "BTCUSDT", testTime)
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, input))

	actual, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT", 24*time.Hour)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, expected, actual)

	*actual.PriceChange = decimal.NewFromInt(100)
	*actual.TradeCount = 200

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{expected}, stored)
}

func TestMarketStatsSnapshotPreservesAbsentOptionalValues(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	original := statsRow(binanceSpot, "BTCUSDT", testTime)
	original.PriceChange = nil
	original.TradeCount = nil
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{original}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{original}, rows)

	actual, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT", 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, original, actual)
}

func TestMarketStatsOperationsHonorCancellation(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithCancel(ctx)
	cancel()
	repository := NewMarketStatsRepository()
	original := statsRow(binanceSpot, "BTCUSDT", testTime)
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{original}))

	err := repository.ReplaceSnapshot(failed, binanceSpot, 24*time.Hour, nil)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = repository.HasSnapshot(failed, binanceSpot, 24*time.Hour)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = repository.List(failed, filter)
	assert.ErrorIs(t, err, context.Canceled)
	_, _, err = repository.Get(failed, binanceSpot, "BTCUSDT", 24*time.Hour)
	assert.ErrorIs(t, err, context.Canceled)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{original}, rows)
}

func TestMarketStatsOperationsHonorDeadline(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithDeadline(ctx, time.Unix(0, 0))
	defer cancel()
	repository := NewMarketStatsRepository()
	original := statsRow(binanceSpot, "BTCUSDT", testTime)
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{original}))

	err := repository.ReplaceSnapshot(failed, binanceSpot, 24*time.Hour, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = repository.HasSnapshot(failed, binanceSpot, 24*time.Hour)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = repository.List(failed, filter)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, _, err = repository.Get(failed, binanceSpot, "BTCUSDT", 24*time.Hour)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{original}, rows)
}

func TestMarketStatsConcurrentReadersSeeCompleteSnapshots(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         24 * time.Hour,
	}
	first := []domain.MarketStats{
		statsRow(binanceSpot, "A", testTime),
		statsRow(binanceSpot, "B", testTime),
	}
	second := []domain.MarketStats{
		statsRow(binanceSpot, "C", testTime.Add(time.Second)),
		statsRow(binanceSpot, "D", testTime.Add(time.Second)),
		statsRow(binanceSpot, "E", testTime.Add(time.Second)),
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, first))

	const readerCount = 50
	const iterations = 50
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range readerCount {
		workers.Go(func() {
			<-start
			for range iterations {
				rows, err := repository.List(ctx, filter)
				if !assert.NoError(t, err) {
					return
				}
				assert.Contains(t, [][]domain.MarketStats{first, second}, rows, "read returned a partial snapshot")
			}
		})
	}
	workers.Go(func() {
		<-start
		for range iterations {
			if !assert.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, second)) {
				return
			}
			if !assert.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, first)) {
				return
			}
		}
	})
	close(start)
	workers.Wait()
}

func TestMarketStatsWindowReadinessIsIndependent(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	daily := statsRow(binanceSpot, "BTCUSDT", testTime)

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{daily}))

	ready, err := repository.HasSnapshot(ctx, binanceSpot, time.Hour)
	require.NoError(t, err)
	assert.False(t, ready)
	_, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT", time.Hour)
	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.False(t, found)
}

func TestMarketStatsReplacementPreservesOtherWindows(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	daily := statsRow(binanceSpot, "BTCUSDT", testTime)
	hourly := statsRow(binanceSpot, "ETHUSDT", testTime)
	hourly.Window = time.Hour
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{daily}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, time.Hour, []domain.MarketStats{hourly}))
	filter := marketstats.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Window:         time.Hour,
	}

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, nil))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.MarketStats{hourly}, rows)
	ready, err := repository.HasSnapshot(ctx, binanceSpot, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, ready)
}

func TestMarketStatsWrongWindowPreservesSnapshot(t *testing.T) {
	ctx := t.Context()
	repository := NewMarketStatsRepository()
	daily := statsRow(binanceSpot, "BTCUSDT", testTime)
	hourly := statsRow(binanceSpot, "ETHUSDT", testTime)
	hourly.Window = time.Hour
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{daily}))

	err := repository.ReplaceSnapshot(ctx, binanceSpot, 24*time.Hour, []domain.MarketStats{hourly})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	actual, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT", 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, daily, actual)
}

func TestMarketStatsReplaceSnapshotRejectsNonpositiveWindow(t *testing.T) {
	tests := []struct {
		name   string
		window time.Duration
	}{
		{name: "zero", window: 0},
		{name: "negative", window: -time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository := NewMarketStatsRepository()

			err := repository.ReplaceSnapshot(ctx, binanceSpot, tt.window, nil)

			assert.ErrorIs(t, err, application.ErrUnsupportedWindow)
			ready, err := repository.HasSnapshot(ctx, binanceSpot, tt.window)
			require.NoError(t, err)
			assert.False(t, ready)
		})
	}
}

func statsRow(scope application.Scope, symbol string, fetchedAt time.Time) domain.MarketStats {
	return domain.MarketStats{
		Exchange:    scope.Exchange,
		Market:      scope.Market,
		Symbol:      symbol,
		Window:      24 * time.Hour,
		High:        decimal.NewFromInt(10),
		Low:         decimal.NewFromInt(1),
		Volume:      decimal.NewFromInt(2),
		Turnover:    decimal.NewFromInt(3),
		PriceChange: pointer(decimal.NewFromInt(4)),
		TradeCount:  pointer(int64(5)),
		FetchedAt:   fetchedAt,
	}
}
