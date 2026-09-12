package upstream

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEndpointCosts(t *testing.T) {
	cases := []struct {
		scope     Scope
		path      string
		operation Operation
		weight    int
		funding   bool
	}{
		{BinanceSpot, "/api/v3/exchangeInfo", Instruments, 20, false},
		{BinanceSpot, "/api/v3/ticker/price", Tickers, 4, false},
		{BinanceSpot, "/api/v3/ticker/bookTicker", Tickers, 4, false},
		{BinanceSpot, "/api/v3/ticker/24hr?type=FULL", MarketStats, 80, false},
		{BinanceSpot, "/api/v3/klines?limit=1000", Klines, 2, false},
		{BinanceLinear, "/fapi/v1/exchangeInfo", Instruments, 1, false},
		{BinanceLinear, "/fapi/v2/ticker/price", Tickers, 2, false},
		{BinanceLinear, "/fapi/v1/ticker/bookTicker", Tickers, 5, false},
		{BinanceLinear, "/fapi/v1/premiumIndex", Tickers, 10, false},
		{BinanceLinear, "/fapi/v1/ticker/24hr", MarketStats, 40, false},
		{BinanceLinear, "/fapi/v1/fundingInfo", Instruments, 0, true},
		{BinanceLinear, "/fapi/v1/klines?limit=1", Klines, 1, false},
		{BinanceLinear, "/fapi/v1/klines?limit=99", Klines, 1, false},
		{BinanceLinear, "/fapi/v1/klines?limit=100", Klines, 2, false},
		{BinanceLinear, "/fapi/v1/klines?limit=499", Klines, 2, false},
		{BinanceLinear, "/fapi/v1/klines?limit=500", Klines, 5, false},
		{BinanceLinear, "/fapi/v1/klines?limit=1000", Klines, 5, false},
		{BinanceLinear, "/fapi/v1/klines?limit=1001", Klines, 10, false},
		{BinanceLinear, "/fapi/v1/klines?limit=1500", Klines, 10, false},
		{Bybit, "/v5/market/instruments-info?category=linear", Instruments, 0, false},
		{Bybit, "/v5/market/tickers?category=spot", Tickers, 0, false},
		{Bybit, "/v5/market/kline?category=linear&limit=1000", Klines, 0, false},
	}
	for _, tt := range cases {
		t.Run(string(tt.scope)+tt.path, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://exchange.test"+tt.path, nil)
			require.NoError(t, err)
			result, err := resolveCost(tt.scope, req, 1500)
			require.NoError(t, err)
			assert.Equal(t, cost{operation: tt.operation, weight: tt.weight, funding: tt.funding}, result)
		})
	}
}

func TestUnknownOrAmbiguousCostsFail(t *testing.T) {
	for _, path := range []string{"/unknown", "/fapi/v1/klines", "/fapi/v1/klines?limit=0", "/fapi/v1/klines?limit=-1", "/fapi/v1/klines?limit=1501", "/fapi/v1/klines?limit=2.5", "/fapi/v1/klines?limit=1&limit=2", "/fapi/v1/klines?limit=%xx", "/fapi/v2/ticker/price?symbol=BTCUSDT"} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://exchange.test"+path, nil)
			require.NoError(t, err)
			_, err = resolveCost(BinanceLinear, req, 1500)
			assert.Error(t, err)
		})
	}
}
