package exchange_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func candleProvider(t *testing.T, scope application.Scope, server *httptest.Server, cfg config.Config, clock upstream.Clock, observe upstream.Observer) (kline.Provider, *upstream.Controller, upstream.Scope) {
	t.Helper()
	admission, err := upstream.New(cfg, clock)
	require.NoError(t, err)
	id := upstream.Bybit
	if scope.Exchange == domain.ExchangeBinance {
		id = upstream.BinanceSpot
		if scope.Market == domain.MarketLinear {
			id = upstream.BinanceLinear
		}
	}
	transport, err := upstream.NewTransport(admission, id, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, observe)
	require.NoError(t, err)
	if scope.Exchange == domain.ExchangeBybit {
		client, err := bybit.NewClient(server.URL, transport)
		require.NoError(t, err)
		return bybit.NewKlineProvider(client), admission, id
	}
	client, err := binance.NewClient(id, server.URL, transport)
	require.NoError(t, err)
	if scope.Market == domain.MarketSpot {
		return binance.NewKlineProvider(client, nil), admission, id
	}
	return binance.NewKlineProvider(nil, client), admission, id
}

func minuteRequest(scope application.Scope, limit int) kline.Request {
	from := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	return kline.Request{Query: kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: from, To: from.Add(time.Duration(limit) * time.Minute)}, Limit: limit}
}

func candleBody(scope application.Scope, from, to time.Time) string {
	if scope.Exchange == domain.ExchangeBybit {
		return fmt.Sprintf(`{"retCode":0,"result":{"category":%q,"symbol":"BTCUSDT","list":[["%d","2","3","1","2.5","4","10"]]}}`, scope.Market, from.UnixMilli())
	}
	return fmt.Sprintf(`[[%d,"2","3","1","2.5","4",%d,"10",9007199254740993]]`, from.UnixMilli(), to.UnixMilli()-1)
}

func TestCandleSDKPathsAndAllIntervalMappings(t *testing.T) {
	cases := []struct {
		interval            domain.Timeframe
		bybit, spot, linear string
	}{
		{"1s", "", "1s", ""}, {"1m", "1", "1m", "1m"}, {"3m", "3", "3m", "3m"},
		{"5m", "5", "5m", "5m"}, {"15m", "15", "15m", "15m"}, {"30m", "30", "30m", "30m"},
		{"1h", "60", "1h", "1h"}, {"2h", "120", "2h", "2h"}, {"4h", "240", "4h", "4h"},
		{"6h", "360", "6h", "6h"}, {"8h", "", "8h", "8h"}, {"12h", "720", "12h", "12h"},
		{"1d", "D", "1d", "1d"}, {"3d", "", "3d", "3d"}, {"1w", "W", "1w", "1w"}, {"1M", "M", "1M", "1M"},
	}
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
			for _, tt := range cases {
				t.Run(string(exchange)+"/"+string(market)+"/"+string(tt.interval), func(t *testing.T) {
					scope := application.Scope{Exchange: exchange, Market: market}
					want := tt.bybit
					if exchange == domain.ExchangeBinance {
						want = tt.spot
						if market == domain.MarketLinear {
							want = tt.linear
						}
					}
					from := time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC)
					// Confirmed Monday and Binance 3d anchor share this boundary.
					if tt.interval == domain.Timeframe1w || tt.interval == domain.Timeframe3d {
						from = time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC)
					}
					var to time.Time
					if want != "" {
						calendar, err := domain.NewCalendar(exchange, market, tt.interval)
						require.NoError(t, err)
						to, err = calendar.Next(from)
						require.NoError(t, err)
					}
					var calls atomic.Int64
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")

						calls.Add(1)
						assert.Equal(t, http.MethodGet, r.Method)
						assert.Equal(t, "", r.Header.Get("X-MBX-TIME-UNIT"))
						query := r.URL.Query()
						assert.Equal(t, "BTCUSDT", query.Get("symbol"))
						assert.Equal(t, want, query.Get("interval"))
						assert.Equal(t, "1", query.Get("limit"))
						if exchange == domain.ExchangeBybit {
							assert.Equal(t, "/v5/market/kline", r.URL.Path)
							assert.Equal(t, string(market), query.Get("category"))
							assert.Equal(t, strconv.FormatInt(from.UnixMilli(), 10), query.Get("start"))
							assert.Equal(t, strconv.FormatInt(to.UnixMilli()-1, 10), query.Get("end"))
							assert.Len(t, query, 6)
						} else {
							path := "/api/v3/klines"
							if market == domain.MarketLinear {
								path = "/fapi/v1/klines"
							}
							assert.Equal(t, path, r.URL.Path)
							assert.Equal(t, strconv.FormatInt(from.UnixMilli(), 10), query.Get("startTime"))
							assert.Equal(t, strconv.FormatInt(to.UnixMilli()-1, 10), query.Get("endTime"))
							assert.Len(t, query, 5)
						}
						_, _ = io.WriteString(w, candleBody(scope, from, to))
					}))
					defer server.Close()
					provider, admission, id := candleProvider(t, scope, server, config.Defaults(), upstream.SystemClock{}, nil)
					supported, err := provider.SupportedTimeframes(market)
					require.NoError(t, err)
					if want == "" {
						assert.NotContains(t, supported, tt.interval)
					} else {
						assert.Contains(t, supported, tt.interval)
					}
					supported[0] = "broken"
					fresh, err := provider.SupportedTimeframes(market)
					require.NoError(t, err)
					assert.NotContains(t, fresh, domain.Timeframe("broken"))
					ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
					require.NoError(t, err)
					defer cancel()
					request := kline.Request{Query: kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: tt.interval}, From: from, To: to}, Limit: 1}

					rows, err := provider.GetKlines(ctx, request)

					if want == "" {
						assert.ErrorIs(t, err, application.ErrInvalidInterval)
						assert.Nil(t, rows)
						assert.Zero(t, calls.Load())
						return
					}
					require.NoError(t, err)
					require.Len(t, rows, 1)
					row := rows[0].Candle
					assert.Equal(t, exchange, provider.Exchange())
					assert.Equal(t, exchange, row.Exchange)
					assert.Equal(t, market, row.Market)
					assert.Equal(t, "BTCUSDT", row.Symbol)
					assert.Equal(t, tt.interval, row.Interval)
					assert.Equal(t, from, row.OpenTime)
					assert.Equal(t, to, row.CloseTime)
					if tt.interval == domain.Timeframe1M {
						assert.Equal(t, time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC), row.CloseTime)
					}
					assert.Equal(t, "2", row.Open.String())
					assert.Equal(t, "3", row.High.String())
					assert.Equal(t, "1", row.Low.String())
					assert.Equal(t, "2.5", row.Close.String())
					assert.Equal(t, "4", row.Volume.String())
					assert.Equal(t, "10", row.Turnover.String())
					if exchange == domain.ExchangeBybit {
						assert.Nil(t, row.TradesCount)
					} else {
						require.NotNil(t, row.TradesCount)
						assert.Equal(t, int64(9007199254740993), *row.TradesCount)
					}
					assert.False(t, rows[0].RequestStartedAt.IsZero())
					assert.False(t, row.FetchedAt.Before(rows[0].RequestStartedAt))
					assert.Equal(t, int64(1), calls.Load())
					assert.Equal(t, 1, admission.Attempts(ctx))
				})
			}
		}
	}
}

func TestCandleRequestValidationBeforeIO(t *testing.T) {
	cases := []struct {
		name   string
		change func(*kline.Request)
		want   error
	}{
		{"wrong exchange", func(r *kline.Request) { r.Exchange = "unknown" }, application.ErrInvalidFilter},
		{"inverse market", func(r *kline.Request) { r.Market = "inverse" }, application.ErrInvalidFilter},
		{"missing symbol", func(r *kline.Request) { r.Symbol = "" }, application.ErrInvalidFilter},
		{"invalid symbol", func(r *kline.Request) { r.Symbol = "BTC USDT" }, application.ErrInvalidFilter},
		{"unknown interval", func(r *kline.Request) { r.Interval = "1H" }, application.ErrInvalidInterval},
		{"missing limit", func(r *kline.Request) { r.Limit = 0 }, application.ErrInvalidParameter},
		{"negative limit", func(r *kline.Request) { r.Limit = -1 }, application.ErrInvalidParameter},
		{"limit below slots", func(r *kline.Request) { r.Limit = 1 }, application.ErrInvalidRange},
		{"limit above slots", func(r *kline.Request) { r.Limit = 4 }, application.ErrInvalidRange},
		{"empty range", func(r *kline.Request) { r.To = r.From }, application.ErrInvalidRange},
		{"reversed range", func(r *kline.Request) { r.To = r.From.Add(-time.Minute) }, application.ErrInvalidRange},
		{"pre epoch", func(r *kline.Request) { r.From = time.Unix(-60, 0) }, application.ErrInvalidRange},
		{"start unaligned", func(r *kline.Request) { r.From = r.From.Add(time.Millisecond) }, application.ErrInvalidRange},
		{"end unaligned", func(r *kline.Request) { r.To = r.To.Add(time.Millisecond) }, application.ErrInvalidRange},
		{"year overflow", func(r *kline.Request) { r.To = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, application.ErrInvalidRange},
	}
	for _, provider := range []kline.Provider{binance.NewKlineProvider(nil, nil), bybit.NewKlineProvider(nil)} {
		for _, tt := range cases {
			t.Run(string(provider.Exchange())+"/"+tt.name, func(t *testing.T) {
				request := minuteRequest(application.Scope{Exchange: provider.Exchange(), Market: domain.MarketSpot}, 3)
				tt.change(&request)

				rows, err := provider.GetKlines(t.Context(), request)

				assert.ErrorIs(t, err, tt.want)
				assert.Nil(t, rows)
			})
		}
	}
}

func TestCandleUnsupportedMarketsAndAbsentClients(t *testing.T) {
	for _, provider := range []kline.Provider{binance.NewKlineProvider(nil, nil), bybit.NewKlineProvider(nil)} {
		t.Run(string(provider.Exchange()), func(t *testing.T) {
			supported, err := provider.SupportedTimeframes("inverse")
			assert.ErrorIs(t, err, application.ErrInvalidFilter)
			assert.Nil(t, supported)
			request := minuteRequest(application.Scope{Exchange: provider.Exchange(), Market: domain.MarketSpot}, 1)

			rows, err := provider.GetKlines(t.Context(), request)

			assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
			assert.Nil(t, rows)
		})
	}
}

func TestCandleEnvelopeAndPageFailures(t *testing.T) {
	cases := []struct {
		name     string
		exchange domain.Exchange
		body     string
		want     error
	}{
		{"Binance empty", domain.ExchangeBinance, `[]`, nil},
		{"Binance null", domain.ExchangeBinance, `null`, application.ErrInvalidUpstreamData},
		{"Binance object", domain.ExchangeBinance, `{}`, application.ErrInvalidUpstreamData},
		{"Binance short row", domain.ExchangeBinance, `[[1789171200000,"1"]]`, application.ErrInvalidUpstreamData},
		{"Binance malformed", domain.ExchangeBinance, `[`, application.ErrInvalidUpstreamData},
		{"Bybit empty", domain.ExchangeBybit, `{"retCode":0,"result":{"category":"spot","symbol":"BTCUSDT","list":[]}}`, nil},
		{"Bybit null", domain.ExchangeBybit, `null`, application.ErrInvalidUpstreamData},
		{"Bybit wrong category", domain.ExchangeBybit, `{"retCode":0,"result":{"category":"linear","symbol":"BTCUSDT","list":[]}}`, application.ErrInvalidUpstreamData},
		{"Bybit wrong symbol", domain.ExchangeBybit, `{"retCode":0,"result":{"category":"spot","symbol":"ETHUSDT","list":[]}}`, application.ErrInvalidUpstreamData},
		{"Bybit missing list", domain.ExchangeBybit, `{"retCode":0,"result":{"category":"spot","symbol":"BTCUSDT"}}`, application.ErrInvalidUpstreamData},
		{"Bybit null list", domain.ExchangeBybit, `{"retCode":0,"result":{"category":"spot","symbol":"BTCUSDT","list":null}}`, application.ErrInvalidUpstreamData},
		{"Bybit missing code", domain.ExchangeBybit, `{"result":{"category":"spot","symbol":"BTCUSDT","list":[]}}`, application.ErrInvalidUpstreamData},
		{"Bybit error code", domain.ExchangeBybit, `{"retCode":10001,"result":{"category":"spot","symbol":"BTCUSDT","list":[]}}`, application.ErrUpstream},
		{"Bybit short row", domain.ExchangeBybit, `{"retCode":0,"result":{"category":"spot","symbol":"BTCUSDT","list":[["1789171200000","1"]]}}`, application.ErrInvalidUpstreamData},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			scope := application.Scope{Exchange: tt.exchange, Market: domain.MarketSpot}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			provider, admission, id := candleProvider(t, scope, server, config.Defaults(), upstream.SystemClock{}, nil)
			ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
			require.NoError(t, err)
			defer cancel()

			rows, err := provider.GetKlines(ctx, minuteRequest(scope, 1))

			if tt.want == nil {
				require.NoError(t, err)
				assert.Empty(t, rows)
			} else {
				assert.ErrorIs(t, err, tt.want)
				assert.Nil(t, rows)
			}
			assert.Equal(t, 1, admission.Attempts(ctx))
		})
	}
}

func TestCandleCancellationDuringHTTP(t *testing.T) {
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		t.Run(string(exchange), func(t *testing.T) {
			scope := application.Scope{Exchange: exchange, Market: domain.MarketSpot}
			started, stopped := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				close(started)
				<-r.Context().Done()
				close(stopped)
			}))
			defer server.Close()
			provider, admission, id := candleProvider(t, scope, server, config.Defaults(), upstream.SystemClock{}, nil)
			ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
			require.NoError(t, err)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				rows, err := provider.GetKlines(ctx, minuteRequest(scope, 1))
				assert.Nil(t, rows)
				result <- err
			}()
			select {
			case <-started:
			case err := <-result:
				require.FailNow(t, "request ended before dispatch", "%v", err)
			}

			cancel()

			assert.ErrorIs(t, <-result, context.Canceled)
			<-stopped
			rows, err := provider.GetKlines(ctx, minuteRequest(scope, 1))
			assert.ErrorIs(t, err, context.Canceled)
			assert.Nil(t, rows)
			assert.Equal(t, 1, admission.Attempts(ctx))
		})
	}
}

func TestCandleGapsAndUnorderedRows(t *testing.T) {
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		t.Run(string(exchange), func(t *testing.T) {
			scope := application.Scope{Exchange: exchange, Market: domain.MarketLinear}
			request := minuteRequest(scope, 4)
			tuples := make([]json.RawMessage, 0, 3)
			for _, offset := range []int{3, 0, 2} {
				from := request.From.Add(time.Duration(offset) * time.Minute)
				body := candleBody(scope, from, from.Add(time.Minute))
				var list []json.RawMessage
				if exchange == domain.ExchangeBybit {
					var envelope struct {
						Result struct{ List []json.RawMessage }
					}
					require.NoError(t, json.Unmarshal([]byte(body), &envelope))
					list = envelope.Result.List
				} else {
					require.NoError(t, json.Unmarshal([]byte(body), &list))
				}
				tuples = append(tuples, list[0])
			}
			list, err := json.Marshal(tuples)
			require.NoError(t, err)
			body := string(list)
			if exchange == domain.ExchangeBybit {
				body = `{"retCode":0,"result":{"category":"linear","symbol":"BTCUSDT","list":` + body + `}}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			provider, admission, id := candleProvider(t, scope, server, config.Defaults(), upstream.SystemClock{}, nil)
			ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
			require.NoError(t, err)
			defer cancel()

			rows, err := provider.GetKlines(ctx, request)

			require.NoError(t, err)
			require.Len(t, rows, 3)
			for i, offset := range []int{0, 2, 3} {
				assert.Equal(t, request.From.Add(time.Duration(offset)*time.Minute), rows[i].Candle.OpenTime)
				assert.Equal(t, request.From.Add(time.Duration(offset+1)*time.Minute), rows[i].Candle.CloseTime)
			}
			assert.Equal(t, 1, admission.Attempts(ctx))
		})
	}
}
