package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstrumentReadsRejectUnreadyScope(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}

	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.False(t, ready)

	rows, err := repository.List(ctx, filter)
	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)
}

func TestInstrumentEmptySnapshotIsReady(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, nil))

	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.True(t, ready)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)
}

func TestInstrumentReplaceSnapshotRemovesAbsentSymbols(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	original := []domain.Instrument{
		instrumentRow(binanceSpot, "BTCUSDT", testTime),
		instrumentRow(binanceSpot, "ETHUSDT", testTime),
	}
	replacement := instrumentRow(binanceSpot, "BTCUSDT", testTime.Add(time.Second))
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, original))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{replacement}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{replacement}, rows)
}

func TestInstrumentReplaceSnapshotPreservesOtherScopes(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	otherMarket := instrumentRow(binanceLinear, "BTCUSDT", testTime)
	otherExchange := instrumentRow(bybitSpot, "BTCUSDT", testTime)
	replacement := instrumentRow(binanceSpot, "ETHUSDT", testTime)
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, binanceLinear, bybitSpot}},
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, []domain.Instrument{otherMarket}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, bybitSpot, []domain.Instrument{otherExchange}))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{replacement}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	expected := []domain.Instrument{otherMarket, replacement, otherExchange}
	assert.Equal(t, expected, rows)
}

func TestInstrumentEmptyReplacementClearsOnlyItsScope(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	other := instrumentRow(binanceLinear, "BTCUSDT", testTime)
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, binanceLinear}},
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, []domain.Instrument{other}))

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{other}, rows)
	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.True(t, ready)
}

func TestInstrumentInvalidReplacementPreservesSnapshot(t *testing.T) {
	tests := []struct {
		name string
		rows []domain.Instrument
	}{
		{
			name: "duplicate symbol",
			rows: []domain.Instrument{
				instrumentRow(binanceSpot, "ETHUSDT", testTime),
				instrumentRow(binanceSpot, "ETHUSDT", testTime),
			},
		},
		{
			name: "wrong market",
			rows: []domain.Instrument{
				instrumentRow(binanceSpot, "ETHUSDT", testTime),
				instrumentRow(binanceLinear, "BTCUSDT", testTime),
			},
		},
		{
			name: "wrong exchange",
			rows: []domain.Instrument{
				instrumentRow(binanceSpot, "ETHUSDT", testTime),
				instrumentRow(bybitSpot, "BTCUSDT", testTime),
			},
		},
		{
			name: "empty symbol",
			rows: []domain.Instrument{instrumentRow(binanceSpot, "", testTime)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository := NewInstrumentRepository()
			original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
			filter := instrument.Filter{
				SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
			}
			require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))

			err := repository.ReplaceSnapshot(ctx, binanceSpot, tt.rows)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			rows, err := repository.List(ctx, filter)
			require.NoError(t, err)
			assert.Equal(t, []domain.Instrument{original}, rows)
		})
	}
}

func TestInstrumentFailedInitialSnapshotStaysUnready(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	wrongScope := instrumentRow(binanceLinear, "BTCUSDT", testTime)

	err := repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{wrongScope})

	assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	ready, err := repository.HasSnapshot(ctx, binanceSpot)
	require.NoError(t, err)
	assert.False(t, ready)
}

func TestInstrumentReplaceSnapshotRejectsInvalidScope(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	invalidScope := application.Scope{}

	err := repository.ReplaceSnapshot(ctx, invalidScope, nil)

	assert.ErrorIs(t, err, application.ErrInvalidFilter)
	ready, err := repository.HasSnapshot(ctx, invalidScope)
	require.NoError(t, err)
	assert.False(t, ready)
}

func TestInstrumentListSortsRowsAndIgnoresRepeatedScopes(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	bitcoin := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	ether := instrumentRow(binanceSpot, "ETHUSDT", testTime)
	otherMarket := instrumentRow(binanceLinear, "BTCUSDT", testTime)
	otherExchange := instrumentRow(bybitSpot, "BTCUSDT", testTime)
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{ether, bitcoin}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceLinear, []domain.Instrument{otherMarket}))
	require.NoError(t, repository.ReplaceSnapshot(ctx, bybitSpot, []domain.Instrument{otherExchange}))
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{bybitSpot, binanceSpot, binanceLinear, binanceSpot}},
	}

	rows, err := repository.List(ctx, filter)

	require.NoError(t, err)
	expected := []domain.Instrument{otherMarket, bitcoin, ether, otherExchange}
	assert.Equal(t, expected, rows)
}

func TestInstrumentListFiltersBySymbol(t *testing.T) {
	tests := []struct {
		name     string
		symbol   string
		expected []domain.Instrument
	}{
		{
			name:     "existing symbol",
			symbol:   "BTCUSDT",
			expected: []domain.Instrument{instrumentRow(binanceSpot, "BTCUSDT", testTime)},
		},
		{
			name:     "absent symbol",
			symbol:   "missing",
			expected: []domain.Instrument{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repository := NewInstrumentRepository()
			input := []domain.Instrument{
				instrumentRow(binanceSpot, "BTCUSDT", testTime),
				instrumentRow(binanceSpot, "ETHUSDT", testTime),
			}
			require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, input))
			filter := instrument.Filter{
				SnapshotFilter: application.SnapshotFilter{
					Scopes: []application.Scope{binanceSpot},
					Symbol: pointer(tt.symbol),
				},
			}

			rows, err := repository.List(ctx, filter)

			require.NoError(t, err)
			assert.Equal(t, tt.expected, rows)
		})
	}
}

func TestInstrumentListWithoutScopesSelectsNothing(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: nil},
	}

	rows, err := repository.List(ctx, filter)

	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)
}

func TestInstrumentSymbolFilterCannotHideUnreadyScope(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, nil))
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, bybitSpot}},
	}
	filter.Symbol = pointer("missing")

	rows, err := repository.List(ctx, filter)

	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)
}

func TestInstrumentReplaceSnapshotCopiesInput(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	input := []domain.Instrument{instrumentRow(binanceSpot, "BTCUSDT", testTime)}
	expected := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, input))

	*input[0].MinQty = decimal.NewFromInt(100)
	*input[0].MaxQty = decimal.NewFromInt(200)
	*input[0].MinNotional = decimal.NewFromInt(300)
	*input[0].FundingInterval = time.Hour
	*input[0].DelistingTime = testTime.Add(24 * time.Hour)
	input[0] = domain.Instrument{}

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{expected}, stored)
}

func TestInstrumentListReturnsOwnedValues(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	input := []domain.Instrument{instrumentRow(binanceSpot, "BTCUSDT", testTime)}
	expected := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, input))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, []domain.Instrument{expected}, rows)

	*rows[0].MinQty = decimal.NewFromInt(100)
	*rows[0].MaxQty = decimal.NewFromInt(200)
	*rows[0].MinNotional = decimal.NewFromInt(300)
	*rows[0].FundingInterval = time.Hour
	*rows[0].DelistingTime = testTime.Add(24 * time.Hour)
	rows[0] = domain.Instrument{}

	stored, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{expected}, stored)
}

func TestInstrumentSnapshotPreservesAbsentOptionalValues(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	original.MinQty = nil
	original.MaxQty = nil
	original.MinNotional = nil
	original.FundingInterval = nil
	original.DelistingTime = nil
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}

	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{original}, rows)
}

func TestInstrumentOperationsHonorCancellation(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithCancel(ctx)
	cancel()
	repository := NewInstrumentRepository()
	original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))

	err := repository.ReplaceSnapshot(failed, binanceSpot, nil)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = repository.HasSnapshot(failed, binanceSpot)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = repository.List(failed, filter)
	assert.ErrorIs(t, err, context.Canceled)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{original}, rows)
}

func TestInstrumentOperationsHonorDeadline(t *testing.T) {
	ctx := t.Context()
	failed, cancel := context.WithDeadline(ctx, time.Unix(0, 0))
	defer cancel()
	repository := NewInstrumentRepository()
	original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))

	err := repository.ReplaceSnapshot(failed, binanceSpot, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = repository.HasSnapshot(failed, binanceSpot)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = repository.List(failed, filter)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	rows, err := repository.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{original}, rows)
}

func TestInstrumentConcurrentReadersSeeCompleteSnapshots(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
	}
	first := []domain.Instrument{
		instrumentRow(binanceSpot, "A", testTime),
		instrumentRow(binanceSpot, "B", testTime),
	}
	second := []domain.Instrument{
		instrumentRow(binanceSpot, "C", testTime.Add(time.Second)),
		instrumentRow(binanceSpot, "D", testTime.Add(time.Second)),
		instrumentRow(binanceSpot, "E", testTime.Add(time.Second)),
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
				assert.Contains(t, [][]domain.Instrument{first, second}, rows, "read returned a partial snapshot")
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

func TestInstrumentListFiltersByStatus(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	trading := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	closed := instrumentRow(binanceSpot, "ETHUSDT", testTime)
	closed.Status = domain.InstrumentStatusClosed
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{closed, trading}))
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot}},
		Status:         pointer(domain.InstrumentStatusTrading),
	}

	rows, err := repository.List(ctx, filter)

	require.NoError(t, err)
	assert.Equal(t, []domain.Instrument{trading}, rows)
}

func TestInstrumentStatusFilterCannotHideUnreadyScope(t *testing.T) {
	ctx := t.Context()
	repository := NewInstrumentRepository()
	original := instrumentRow(binanceSpot, "BTCUSDT", testTime)
	require.NoError(t, repository.ReplaceSnapshot(ctx, binanceSpot, []domain.Instrument{original}))
	filter := instrument.Filter{
		SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{binanceSpot, bybitSpot}},
		Status:         pointer(domain.InstrumentStatusClosed),
	}

	rows, err := repository.List(ctx, filter)

	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)
}

func instrumentRow(scope application.Scope, symbol string, updatedAt time.Time) domain.Instrument {
	return domain.Instrument{
		Exchange:        scope.Exchange,
		Market:          scope.Market,
		Symbol:          symbol,
		BaseAsset:       "BTC",
		QuoteAsset:      "USDT",
		Status:          domain.InstrumentStatusTrading,
		PriceTick:       decimal.NewFromInt(1),
		QtyStep:         decimal.NewFromInt(2),
		MinQty:          pointer(decimal.NewFromInt(3)),
		MaxQty:          pointer(decimal.NewFromInt(4)),
		MinNotional:     pointer(decimal.NewFromInt(5)),
		FundingInterval: pointer(8 * time.Hour),
		DelistingTime:   pointer(updatedAt.Add(time.Hour)),
		UpdatedAt:       updatedAt,
	}
}
