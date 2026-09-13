package grpctransport

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestConversionPreservesValuesAndPresence(t *testing.T) {
	at := time.Unix(1789214400, 123456789).UTC()
	zero := decimal.Zero
	duration := 999 * time.Millisecond
	count := int64(9007199254740993)
	row, err := tickerMessage(ticker.ReadModel{LastPrice: decimal.RequireFromString("12345.1234567890123456789"), BidPrice: &zero, NextFundingIn: &duration, FetchedAt: at})
	require.NoError(t, err)
	assert.Equal(t, "12345.1234567890123456789", row.LastPrice)
	require.NotNil(t, row.BidPrice)
	assert.Equal(t, "0", *row.BidPrice)
	assert.Nil(t, row.AskPrice)
	require.NotNil(t, row.NextFundingInSeconds)
	assert.Zero(t, *row.NextFundingInSeconds)
	assert.Equal(t, at, row.FetchedAt.AsTime())
	stats, err := statsMessage(domain.MarketStats{FetchedAt: at, TradeCount: &count})
	require.NoError(t, err)
	assert.Equal(t, count, *stats.TradeCount)
}

func TestDecimalExpansionBound(t *testing.T) {
	cases := []struct {
		name    string
		value   decimal.Decimal
		want    string
		failure bool
	}{
		{"long exact", decimal.RequireFromString(strings.Repeat("9", 1024)), strings.Repeat("9", 1024), false},
		{"fraction", decimal.RequireFromString("0.000000001"), "0.000000001", false},
		{"negative", decimal.RequireFromString("-1.23"), "-1.23", false},
		{"positive overflow", decimal.New(1, math.MaxInt32), "", true},
		{"negative exponent overflow", decimal.New(1, math.MinInt32), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := decimalText(tc.value)
			if tc.failure {
				assert.ErrorIs(t, err, application.ErrInternal)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
		})
	}
}

func TestResponseSizeBoundaries(t *testing.T) {
	row := domain.Instrument{Symbol: "A", UpdatedAt: time.Unix(100, 1)}
	message, err := instrumentMessage(row)
	require.NoError(t, err)
	size := proto.Size(&pb.ListInstrumentsResponse{Instruments: []*pb.Instrument{message}})
	cases := []struct {
		name    string
		limit   int
		failure bool
	}{{"one byte below", size - 1, true}, {"at cap", size, false}, {"one byte above", size + 1, false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := &pb.ListInstrumentsResponse{}
			err := convertRows(t.Context(), []domain.Instrument{row}, tc.limit, 0, instrumentMessage, func(m *pb.Instrument) { result.Instruments = append(result.Instruments, m) })
			if tc.failure {
				assert.ErrorIs(t, err, errResponseTooLarge)
				assert.Empty(t, result.Instruments)
			} else {
				require.NoError(t, err)
				assert.Equal(t, size, proto.Size(result))
			}
		})
	}
}

func TestConversionCancellationAndInvalidTimestamp(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := convertRows(ctx, []domain.Kline{{}}, 100, 0, klineMessage, func(*pb.Kline) { t.Error("Canceled conversion published a row") })
	assert.ErrorIs(t, err, context.Canceled)
	_, err = timestamp(time.Time{})
	assert.ErrorIs(t, err, application.ErrInternal)
	_, err = timestamp(time.Unix(-1, 0))
	assert.ErrorIs(t, err, application.ErrInternal)
}

// BenchmarkKlineConversionAndEncoding separates the bounded mapping path from TCP and storage.
func BenchmarkKlineConversionAndEncoding(b *testing.B) {
	value := decimal.RequireFromString("12345.1234567890123456789")
	count := int64(9007199254740993)
	stamp := time.Unix(1789214400, 123456789).UTC()
	rows := make([]domain.Kline, 1000)
	for i := range rows {
		open := time.Unix(1789154400+int64(i)*60, 0).UTC()
		rows[i] = domain.Kline{OpenTime: open, CloseTime: open.Add(time.Minute), Open: value, High: value, Low: value, Close: value, Volume: value, Turnover: value, TradesCount: &count, FetchedAt: stamp}
	}
	b.ReportAllocs()
	for b.Loop() {
		result := &pb.GetKlinesResponse{Exchange: "binance", Market: "spot", Symbol: "S0000USDT", Interval: "1m"}
		err := convertRows(b.Context(), rows, 16<<20, proto.Size(result), klineMessage, func(row *pb.Kline) { result.Klines = append(result.Klines, row) })
		require.NoError(b, err)
		body, err := proto.Marshal(result)
		require.NoError(b, err)
		require.Len(b, body, 203030)
	}
}
