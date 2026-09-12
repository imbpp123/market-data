package ticker

import (
	"testing"
	"time"

	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFundingCountdown(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 58, 30, 0, time.UTC)
	future := now.Add(90 * time.Second)
	soon := now.Add(999 * time.Millisecond)
	past := now.Add(-time.Nanosecond)
	cases := []struct {
		name string
		at   *time.Time
		want *time.Duration
	}{
		{"absent", nil, nil},
		{"future", &future, durationPointer(90 * time.Second)},
		{"less than one second", &soon, durationPointer(999 * time.Millisecond)},
		{"reached", &now, nil},
		{"past", &past, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := []domain.Ticker{{NextFundingAt: tc.at, FetchedAt: now.Add(-time.Minute)}}
			builder := NewReadModelBuilder(func() time.Time { return now })
			result := builder.Build(rows)
			require.Len(t, result, 1)
			assert.Equal(t, tc.want, result[0].NextFundingIn)
			assert.Equal(t, rows[0].FetchedAt, result[0].FetchedAt)
			assert.Equal(t, tc.at, rows[0].NextFundingAt)
		})
	}
}

func TestFundingCountdownUsesOneClockValuePerResponse(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 58, 30, 0, time.UTC)
	fundingAt := now.Add(90 * time.Second)
	fetchedAt := now.Add(-time.Minute)
	rows := []domain.Ticker{
		{Symbol: "BTCUSDT", NextFundingAt: &fundingAt, FetchedAt: fetchedAt},
		{Symbol: "ETHUSDT", NextFundingAt: &fundingAt, FetchedAt: fetchedAt},
	}
	calls := 0
	builder := NewReadModelBuilder(func() time.Time {
		value := now.Add(time.Duration(calls) * time.Minute)
		calls++
		return value
	})
	for _, expected := range []*time.Duration{durationPointer(90 * time.Second), durationPointer(30 * time.Second), nil} {
		result := builder.Build(rows)
		require.Len(t, result, 2)
		for i, row := range result {
			assert.Equal(t, expected, row.NextFundingIn)
			assert.Equal(t, rows[i].Symbol, row.Symbol)
			assert.Equal(t, fetchedAt, row.FetchedAt)
		}
	}
	assert.Equal(t, 3, calls)
	assert.Equal(t, now.Add(90*time.Second), fundingAt)
	assert.Equal(t, fetchedAt, rows[0].FetchedAt)
	assert.Equal(t, fetchedAt, rows[1].FetchedAt)
}

func TestReadModelPreservesExactValuesAndOwnership(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	rate := decimal.RequireFromString("-0.0001000000000000000000000001")
	price := decimal.RequireFromString("12345678901234567890.1234567890123456789")
	size := decimal.RequireFromString("0.0000000000000000000000001")
	rows := []domain.Ticker{{
		Exchange: domain.ExchangeBybit, Market: domain.MarketLinear, Symbol: "BTCUSDT",
		LastPrice: price, BidPrice: &price, BidSize: &size, AskPrice: &price, AskSize: &size,
		FundingRate: &rate, FetchedAt: now,
	}}
	builder := NewReadModelBuilder(func() time.Time { return now })
	result := builder.Build(rows)
	require.Len(t, result, 1)
	row := result[0]
	assert.Equal(t, domain.ExchangeBybit, row.Exchange)
	assert.Equal(t, domain.MarketLinear, row.Market)
	assert.Equal(t, "BTCUSDT", row.Symbol)
	assert.Equal(t, "12345678901234567890.1234567890123456789", row.LastPrice.String())
	assert.Nil(t, row.NextFundingIn)
	for _, pair := range []struct{ input, output *decimal.Decimal }{
		{&price, row.BidPrice}, {&price, row.AskPrice}, {&size, row.BidSize}, {&size, row.AskSize}, {&rate, row.FundingRate},
	} {
		require.NotNil(t, pair.output)
		assert.Equal(t, pair.input.String(), pair.output.String())
		assert.NotSame(t, pair.input, pair.output)
		*pair.output = decimal.Zero
	}
	row.LastPrice = decimal.Zero
	assert.Equal(t, "12345678901234567890.1234567890123456789", rows[0].LastPrice.String())
	assert.Equal(t, "12345678901234567890.1234567890123456789", price.String())
	assert.Equal(t, "0.0000000000000000000000001", size.String())
	assert.Equal(t, "-0.0001000000000000000000000001", rate.String())
	assert.Equal(t, now, rows[0].FetchedAt)
}

func TestReadModelEmptyAndMissingOptionalValues(t *testing.T) {
	builder := NewReadModelBuilder(func() time.Time { return time.Time{} })
	assert.Equal(t, []ReadModel{}, builder.Build(nil))
	assert.Equal(t, []ReadModel{}, builder.Build([]domain.Ticker{}))
	rows := builder.Build([]domain.Ticker{{}})
	require.Len(t, rows, 1)
	assert.Nil(t, rows[0].BidPrice)
	assert.Nil(t, rows[0].BidSize)
	assert.Nil(t, rows[0].AskPrice)
	assert.Nil(t, rows[0].AskSize)
	assert.Nil(t, rows[0].FundingRate)
	assert.Nil(t, rows[0].NextFundingIn)
}

func durationPointer(value time.Duration) *time.Duration { return &value }
