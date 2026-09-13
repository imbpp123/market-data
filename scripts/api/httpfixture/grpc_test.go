package main

import (
	"testing"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGRPCComparisonRequests(t *testing.T) {
	cases := []struct {
		name   string
		count  int
		method string
	}{
		{"instruments-1", 1, pb.MarketDataService_ListInstruments_FullMethodName},
		{"instruments-20000", 20000, pb.MarketDataService_ListInstruments_FullMethodName},
		{"tickers-1", 1, pb.MarketDataService_ListTickers_FullMethodName},
		{"tickers-20000", 20000, pb.MarketDataService_ListTickers_FullMethodName},
		{"market-stats-1", 1, pb.MarketDataService_ListMarketStats_FullMethodName},
		{"market-stats-20000", 20000, pb.MarketDataService_ListMarketStats_FullMethodName},
		{"klines-1", 1, pb.MarketDataService_GetKlines_FullMethodName},
		{"klines-100", 100, pb.MarketDataService_GetKlines_FullMethodName},
		{"klines-1000", 1000, pb.MarketDataService_GetKlines_FullMethodName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, request, response, err := grpcRequest(tc.name)
			require.NoError(t, err)
			assert.Equal(t, tc.method, method)
			require.NotNil(t, response)
			if candle, ok := request.(*pb.GetKlinesRequest); ok {
				assert.Equal(t, int64(tc.count*60), candle.To.Seconds-candle.From.Seconds)
				assert.Equal(t, "binance", candle.GetExchange())
				assert.Equal(t, "S0000USDT", candle.GetSymbol())
			} else {
				symbol := request.ProtoReflect().Descriptor().Fields().ByName("symbol")
				assert.Equal(t, tc.count == 20000, request.ProtoReflect().Has(symbol))
			}
		})
	}
}

func TestGRPCComparisonRejectsUnknownFixtures(t *testing.T) {
	for _, name := range []string{"", "klines--1", "klines-0", "klines-1001", "tickers-2", "other-1", "instruments-many"} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := grpcRequest(name)
			require.Error(t, err)
		})
	}
}
