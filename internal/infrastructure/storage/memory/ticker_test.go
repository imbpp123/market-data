package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTickerReadsRejectUnreadyScope(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}

	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.False(t, ready)

	rows, err := repository.List(ctx, filter)
	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)

	_, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT")
	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.False(t, found)
}

func TestTickerEmptySnapshotIsReady(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, nil))

	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.True(t, ready)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)

	_, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestTickerReplaceSnapshotRemovesAbsentSymbols(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	original := []domain.Ticker{
		tickerRow(binanceSpot, "BTCUSDT", testTime),
		tickerRow(binanceSpot, "ETHUSDT", testTime),
	}
	replacement := tickerRow(binanceSpot, "BTCUSDT", testTime.Add(time.Second))
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, original))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{replacement}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{replacement}, rows)

	actual, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, replacement, actual)

	_, found, err = repository.Get(ctx, binanceSpot, "ETHUSDT")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestTickerReplaceSnapshotPreservesOtherScopes(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	original := tickerRow(binanceSpot, "BTCUSDT", testTime)
	otherMarket := tickerRow(binanceLinear, "BTCUSDT", testTime)
	otherExchange := tickerRow(bybitSpot, "BTCUSDT", testTime)
	replacement := tickerRow(binanceSpot, "ETHUSDT", testTime)
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, binanceLinear, bybitSpot}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{original}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, []domain.Ticker{otherMarket}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, bybitSpot, []domain.Ticker{otherExchange}))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{replacement}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	expected := []domain.Ticker{otherMarket, replacement, otherExchange}
	assert.Equal(t, expected, rows)
}

func TestTickerEmptyReplacementClearsOnlyItsScope(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	original := tickerRow(binanceSpot, "BTCUSDT", testTime)
	other := tickerRow(binanceLinear, "BTCUSDT", testTime)
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, binanceLinear}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{original}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, []domain.Ticker{other}))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{other}, rows)
	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.True(t, ready)
}

func TestTickerInvalidReplacementPreservesSnapshot(t *testing.T) {
	tests := []struct {
		name string
		rows []domain.Ticker
	}{
		{
			name: "duplicate symbol",
			rows: []domain.Ticker{
				tickerRow(binanceSpot, "ETHUSDT", testTime),
				tickerRow(binanceSpot, "ETHUSDT", testTime),
			},
		},
		{
			name: "wrong market",
			rows: []domain.Ticker{
				tickerRow(binanceSpot, "ETHUSDT", testTime),
				tickerRow(binanceLinear, "BTCUSDT", testTime),
			},
		},
		{
			name: "wrong exchange",
			rows: []domain.Ticker{
				tickerRow(binanceSpot, "ETHUSDT", testTime),
				tickerRow(bybitSpot, "BTCUSDT", testTime),
			},
		},
		{
			name: "empty symbol",
			rows: []domain.Ticker{tickerRow(binanceSpot, "", testTime)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository := NewTickerRepository()
			original := tickerRow(binanceSpot, "BTCUSDT", testTime)
			filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
			require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{original}))

			err := repository.ReplaceSnapshot(ctx, binanceSpot, tt.rows)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			rows, err := repository.List(ctx, filter)
			require.NoError(t, err)
			assert.Equal(t, []domain.Ticker{original}, rows)
		})
	}
}

func TestTickerFailedInitialSnapshotStaysUnready(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	wrongScope := tickerRow(binanceLinear, "BTCUSDT", testTime)

	err := repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{wrongScope})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.False(t, ready)
}

func TestTickerReplaceSnapshotRejectsInvalidScope(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	invalidScope := application.Scope{}

	err := repository.ReplaceSnapshot(ctx, invalidScope, nil)

	assert.ErrorIs(t, err, application.ErrInvalidFilter)
	ready, err := repository.HasSnapshot(ctx, invalidScope)
	require.NoError(t, err)
	assert.False(t, ready)
}

func TestTickerListSortsRowsAndIgnoresRepeatedScopes(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	bitcoin := tickerRow(binanceSpot, "BTCUSDT", testTime)
	ether := tickerRow(binanceSpot, "ETHUSDT", testTime)
	otherMarket := tickerRow(binanceLinear, "BTCUSDT", testTime)
	otherExchange := tickerRow(bybitSpot, "BTCUSDT", testTime)
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{ether, bitcoin}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, []domain.Ticker{otherMarket}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, bybitSpot, []domain.Ticker{otherExchange}))
	filter := application.SnapshotFilter{Scopes: []application.Scope{bybitSpot, binanceSpot, binanceLinear, binanceSpot}}

	rows, err := repository.List(ctx, filter)

	require.NoError(t, err)
	expected := []domain.Ticker{otherMarket, bitcoin, ether, otherExchange}
	assert.Equal(t, expected, rows)
}

func TestTickerListFiltersBySymbol(t *testing.T) {
	tests := []struct {
		name     string
		symbol   string
		expected []domain.Ticker
	}{
		{
			name:     "existing symbol",
			symbol:   "BTCUSDT",
			expected: []domain.Ticker{tickerRow(binanceSpot, "BTCUSDT", testTime)},
		},
		{
			name:     "absent symbol",
			symbol:   "missing",
			expected: []domain.Ticker{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository := NewTickerRepository()
			input := []domain.Ticker{
				tickerRow(binanceSpot, "BTCUSDT", testTime),
				tickerRow(binanceSpot, "ETHUSDT", testTime),
			}
			require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, input))
			filter := application.SnapshotFilter{
				Scopes: []application.Scope{binanceSpot},
				Symbol: pointer(tt.symbol),
			}

			rows, err := repository.List(ctx, filter)

			require.NoError(t, err)
			assert.Equal(t, tt.expected, rows)
		})
	}
}

func TestTickerListWithoutScopesSelectsNothing(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	original := tickerRow(binanceSpot, "BTCUSDT", testTime)
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{original}))
	filter := application.SnapshotFilter{Scopes: nil}

	rows, err := repository.List(ctx, filter)

	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)
}

func TestTickerSymbolFilterCannotHideUnreadyScope(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, nil))
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, bybitSpot}}
	filter.Symbol = pointer("missing")

	rows, err := repository.List(ctx, filter)

	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)
}

func TestTickerReplaceSnapshotCopiesInput(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	input := []domain.Ticker{tickerRow(binanceSpot, "BTCUSDT", testTime)}
	expected := tickerRow(binanceSpot, "BTCUSDT", testTime)
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, input))

	*input[0].BidPrice = decimal.NewFromInt(100)
	*input[0].BidSize = decimal.NewFromInt(200)
	*input[0].AskPrice = decimal.NewFromInt(300)
	*input[0].AskSize = decimal.NewFromInt(400)
	*input[0].FundingRate = decimal.NewFromInt(500)
	*input[0].NextFundingAt = testTime.Add(24 * time.Hour)
	input[0] = domain.Ticker{}

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{expected}, stored)
}

func TestTickerListReturnsOwnedValues(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	input := []domain.Ticker{tickerRow(binanceSpot, "BTCUSDT", testTime)}
	expected := tickerRow(binanceSpot, "BTCUSDT", testTime)
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, input))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, []domain.Ticker{expected}, rows)

	*rows[0].BidPrice = decimal.NewFromInt(100)
	*rows[0].BidSize = decimal.NewFromInt(200)
	*rows[0].AskPrice = decimal.NewFromInt(300)
	*rows[0].AskSize = decimal.NewFromInt(400)
	*rows[0].FundingRate = decimal.NewFromInt(500)
	*rows[0].NextFundingAt = testTime.Add(24 * time.Hour)
	rows[0] = domain.Ticker{}

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{expected}, stored)
}

func TestTickerGetReturnsOwnedValues(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	input := []domain.Ticker{tickerRow(binanceSpot, "BTCUSDT", testTime)}
	expected := tickerRow(binanceSpot, "BTCUSDT", testTime)
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, input))

	actual, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, expected, actual)

	*actual.BidPrice = decimal.NewFromInt(100)
	*actual.BidSize = decimal.NewFromInt(200)
	*actual.AskPrice = decimal.NewFromInt(300)
	*actual.AskSize = decimal.NewFromInt(400)
	*actual.FundingRate = decimal.NewFromInt(500)
	*actual.NextFundingAt = testTime.Add(24 * time.Hour)

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{expected}, stored)
}

func TestTickerSnapshotPreservesAbsentOptionalValues(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	original := tickerRow(binanceSpot, "BTCUSDT", testTime)
	original.BidPrice = nil
	original.BidSize = nil
	original.AskPrice = nil
	original.AskSize = nil
	original.FundingRate = nil
	original.NextFundingAt = nil
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{original}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{original}, rows)

	actual, found, err := repository.Get(ctx, binanceSpot, "BTCUSDT")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, original, actual)
}

func TestTickerOperationsHonorCancellation(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithCancel(ctx)
	cancel()
	repository := NewTickerRepository()
	original := tickerRow(binanceSpot, "BTCUSDT", testTime)
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{original}))

	err := repository.ReplaceSnapshot(failed, binanceSpot, nil)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = repository.HasSnapshot(failed, binanceSpot)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = repository.List(failed, filter)
	assert.ErrorIs(t, err, context.Canceled)
	_, _, err = repository.Get(failed, binanceSpot, "BTCUSDT")
	assert.ErrorIs(t, err, context.Canceled)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{original}, rows)
}

func TestTickerOperationsHonorDeadline(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithDeadline(ctx, time.Unix(0, 0))
	defer cancel()
	repository := NewTickerRepository()
	original := tickerRow(binanceSpot, "BTCUSDT", testTime)
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Ticker{original}))

	err := repository.ReplaceSnapshot(failed, binanceSpot, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = repository.HasSnapshot(failed, binanceSpot)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = repository.List(failed, filter)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, _, err = repository.Get(failed, binanceSpot, "BTCUSDT")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Ticker{original}, rows)
}

func TestTickerConcurrentReadersSeeCompleteSnapshots(t *testing.T) {
	ctx := t.Context()
	repository := NewTickerRepository()
	filter := application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}}
	first := []domain.Ticker{
		tickerRow(binanceSpot, "A", testTime),
		tickerRow(binanceSpot, "B", testTime),
	}
	second := []domain.Ticker{
		tickerRow(binanceSpot, "C", testTime.Add(time.Second)),
		tickerRow(binanceSpot, "D", testTime.Add(time.Second)),
		tickerRow(binanceSpot, "E", testTime.Add(time.Second)),
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, first))

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
				assert.Contains(t, [][]domain.Ticker{first, second}, rows, "read returned a partial snapshot")
			}
		})
	}
	workers.Go(func() {
		<-start
		for range iterations {
			if !assert.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, second)) {
				return
			}
			if !assert.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, first)) {
				return
			}
		}
	})
	close(start)
	workers.Wait()
}

func tickerRow(scope application.Scope, symbol string, fetchedAt time.Time) domain.Ticker {
	return domain.Ticker{
		Exchange:      scope.Exchange,
		Market:        scope.Market,
		Symbol:        symbol,
		LastPrice:     decimal.NewFromInt(1),
		BidPrice:      pointer(decimal.NewFromInt(2)),
		BidSize:       pointer(decimal.NewFromInt(3)),
		AskPrice:      pointer(decimal.NewFromInt(4)),
		AskSize:       pointer(decimal.NewFromInt(5)),
		FundingRate:   pointer(decimal.NewFromInt(6)),
		NextFundingAt: pointer(fetchedAt.Add(time.Hour)),
		FetchedAt:     fetchedAt,
	}
}
