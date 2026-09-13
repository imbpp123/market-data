package exchange_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type candleHTTP func(*http.Request) (*http.Response, error)

func (f candleHTTP) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestCandleActualPlannedLimitDeterminesAdmissionCost(t *testing.T) {
	cases := []struct{ limit, weight, accepted int }{
		{1, 1, 21}, {99, 1, 21}, {100, 2, 10}, {499, 2, 10}, {500, 5, 4}, {1000, 5, 4}, {1001, 10, 2}, {1500, 10, 2},
	}
	for _, tt := range cases {
		t.Run(strconv.Itoa(tt.limit), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				// A 1% kline share leaves 21 of the 2,160 common weight units.
				cfg.Upstream.OperationSharePercent.Tickers = 88
				cfg.Upstream.OperationSharePercent.Klines = 1
				cfg.Upstream.OperationSharePercent.MarketStats = 6
				cfg.Upstream.LanesPerExchange.Klines.MaxAttempts = 64
				scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}
				request := minuteRequest(scope, tt.limit)
				calls := 0
				base := candleHTTP(func(r *http.Request) (*http.Response, error) {
					calls++
					assert.Equal(t, strconv.Itoa(tt.limit), r.URL.Query().Get("limit"))
					// One returned row must still spend the planned page's full weight.
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(candleBody(scope, request.From, request.From.Add(time.Minute))))}, nil
				})
				admission, err := upstream.New(cfg, upstream.SystemClock{})
				require.NoError(t, err)
				transport, err := upstream.NewTransport(admission, upstream.BinanceLinear, base, cfg, func(time.Duration) time.Duration { return 0 }, nil)
				require.NoError(t, err)
				client, err := binance.NewClient(upstream.BinanceLinear, "http://exchange.test", transport)
				require.NoError(t, err)
				provider := binance.NewKlineProvider(nil, client)
				ctx, cancel, err := admission.Begin(t.Context(), upstream.BinanceLinear, upstream.Klines)
				require.NoError(t, err)
				defer cancel()
				for range tt.accepted {
					rows, err := provider.GetKlines(ctx, request)
					require.NoError(t, err)
					require.Len(t, rows, 1)
					assert.Equal(t, "10", rows[0].Candle.Turnover.String())
				}
				started := time.Now()
				rows, err := provider.GetKlines(ctx, request)

				assert.Nil(t, rows)
				assert.ErrorIs(t, err, application.ErrServiceOverloaded)
				assert.Equal(t, started, time.Now())
				assert.Equal(t, tt.accepted, calls)
				assert.Equal(t, tt.accepted, admission.Attempts(ctx))
				assert.LessOrEqual(t, tt.accepted*tt.weight, 21)
			})
		})
	}
}

func TestCandleConfiguredPageLimitBeforeDispatch(t *testing.T) {
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
			t.Run(string(exchange)+"/"+string(market), func(t *testing.T) {
				scope := application.Scope{Exchange: exchange, Market: market}
				request := minuteRequest(scope, 99)
				cfg := config.Defaults()
				cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Spot = 99
				cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Linear = 99
				cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest.Spot = 99
				cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest.Linear = 99
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					calls.Add(1)
					assert.Equal(t, "99", r.URL.Query().Get("limit"))
					_, _ = io.WriteString(w, candleBody(scope, request.From, request.From.Add(time.Minute)))
				}))
				defer server.Close()
				provider, admission, id := candleProvider(t, scope, server, cfg, upstream.SystemClock{}, nil)
				ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
				require.NoError(t, err)
				defer cancel()

				rows, err := provider.GetKlines(ctx, request)

				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "10", rows[0].Candle.Turnover.String())
				assert.Equal(t, int64(1), calls.Load())
				assert.Equal(t, 1, admission.Attempts(ctx))
			})
		}
	}
}

func TestCandleOversizedPageBeforeDispatch(t *testing.T) {
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
			t.Run(string(exchange)+"/"+string(market), func(t *testing.T) {
				scope := application.Scope{Exchange: exchange, Market: market}
				cfg := config.Defaults()
				cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Spot = 99
				cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Linear = 99
				cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest.Spot = 99
				cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest.Linear = 99
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
				defer server.Close()
				provider, admission, id := candleProvider(t, scope, server, cfg, upstream.SystemClock{}, nil)
				ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
				require.NoError(t, err)
				defer cancel()

				rows, err := provider.GetKlines(ctx, minuteRequest(scope, 100))

				assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
				assert.Nil(t, rows)
				assert.Zero(t, calls.Load())
				assert.Zero(t, admission.Attempts(ctx))
			})
		}
	}
}
