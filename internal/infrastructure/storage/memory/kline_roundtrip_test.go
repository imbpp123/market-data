package memory

import (
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlineRoundTripPreservesEveryFieldAcrossSeries(t *testing.T) {
	cases := []struct {
		exchange domain.Exchange
		market   domain.Market
	}{
		{domain.ExchangeBinance, domain.MarketSpot},
		{domain.ExchangeBinance, domain.MarketLinear},
		{domain.ExchangeBybit, domain.MarketSpot},
		{domain.ExchangeBybit, domain.MarketLinear},
	}
	for _, tc := range cases {
		t.Run(string(tc.exchange)+"/"+string(tc.market), func(t *testing.T) {
			now := time.Date(2026, 9, 12, 12, 5, 1, 0, time.UTC)
			repository, err := NewKlineRepository(1000, func() time.Time { return now })
			require.NoError(t, err)
			count := int64(9007199254740993)
			expected := kline.Stored{RequestStartedAt: now.Add(-time.Second), Candle: domain.Kline{
				Exchange: tc.exchange, Market: tc.market, Symbol: "BTCUSDT", Interval: domain.Timeframe5m,
				OpenTime: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC), CloseTime: now.Add(-time.Second), FetchedAt: now,
				Open: decimal.RequireFromString("1.1234567890123456789"), High: decimal.NewFromInt(9), Low: decimal.NewFromInt(1), Close: decimal.NewFromInt(3),
				Volume: decimal.RequireFromString("4.1234567890123456789"), Turnover: decimal.RequireFromString("5.1234567890123456789"), TradesCount: &count,
			}}
			neighbor := expected
			neighbor.Candle.Symbol = "ETHUSDT"
			neighbor.Candle.Interval = domain.Timeframe1m
			neighbor.Candle.CloseTime = expected.Candle.OpenTime.Add(time.Minute)
			neighbor.Candle.TradesCount = nil
			query := kline.Query{Series: kline.Series{Scope: application.Scope{Exchange: tc.exchange, Market: tc.market}, Symbol: "BTCUSDT", Interval: domain.Timeframe5m}, From: expected.Candle.OpenTime, To: expected.Candle.CloseTime}

			require.NoError(t, repository.UpsertMany(t.Context(), []kline.Stored{expected, neighbor}))
			rows, err := repository.GetRange(t.Context(), query)

			require.NoError(t, err)
			require.Equal(t, []kline.Stored{expected}, rows)
			require.NoError(t, rows[0].Candle.Open.UnmarshalText([]byte("8")))
			*rows[0].Candle.TradesCount = 1
			again, err := repository.GetRange(t.Context(), query)
			require.NoError(t, err)
			assert.Equal(t, []kline.Stored{expected}, again)

			query.Symbol, query.Interval, query.To = "ETHUSDT", domain.Timeframe1m, neighbor.Candle.CloseTime
			rows, err = repository.GetRange(t.Context(), query)
			require.NoError(t, err)
			assert.Equal(t, []kline.Stored{neighbor}, rows)
		})
	}
}
