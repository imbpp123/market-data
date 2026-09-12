package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	httptransport "market-data/internal/transport/http"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This fixture deliberately returns exact prices and counts above 2^53.
func candleFixture(exchange domain.Exchange, market domain.Market, from, to time.Time) string {
	rows := make([]string, 0)
	for open := from; open.Before(to); open = open.Add(time.Minute) {
		if exchange == domain.ExchangeBinance {
			rows = append(rows, fmt.Sprintf(`[%d,"1.1234567890123456789","2","1","2","3",%d,"4.1234567890123456789",9007199254740993,"0","0","0"]`, open.UnixMilli(), open.Add(time.Minute).UnixMilli()-1))
		} else {
			rows = append([]string{fmt.Sprintf(`["%d","1.1234567890123456789","2","1","2","3","4.1234567890123456789"]`, open.UnixMilli())}, rows...)
		}
	}
	body := "[" + strings.Join(rows, ",") + "]"
	if exchange == domain.ExchangeBybit {
		body = `{"retCode":0,"result":{"category":"` + string(market) + `","symbol":"BTCUSDT","list":` + body + `}}`
	}
	return body
}

func candleURL(scope application.Scope, from, to time.Time) string {
	return "/api/v1/klines?" + url.Values{
		"exchange": {string(scope.Exchange)}, "market": {string(scope.Market)}, "symbol": {"BTCUSDT"}, "interval": {"1m"},
		"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)},
	}.Encode()
}

func TestKlineColdWarmPartialHTTPThroughExchangeAdapters(t *testing.T) {
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
			t.Run(string(exchange)+"/"+string(market), func(t *testing.T) {
				cfg := config.Defaults()
				cfg.Klines.MaxHistoryCandles = 4
				cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest = config.Markets[int]{Spot: 2, Linear: 2}
				cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest = config.Markets[int]{Spot: 2, Linear: 2}
				now := time.Date(2026, 9, 12, 12, 0, 30, 0, time.UTC)
				end := now.Truncate(time.Minute)
				scope := application.Scope{Exchange: exchange, Market: market}
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					q := r.URL.Query()
					fromKey, toKey := "startTime", "endTime"
					if exchange == domain.ExchangeBybit {
						fromKey, toKey = "start", "end"
						assert.Equal(t, string(market), q.Get("category"))
					}
					from, e1 := strconv.ParseInt(q.Get(fromKey), 10, 64)
					to, e2 := strconv.ParseInt(q.Get(toKey), 10, 64)
					assert.NoError(t, e1)
					assert.NoError(t, e2)
					assert.Equal(t, "BTCUSDT", q.Get("symbol"))
					assert.Equal(t, "2", q.Get("limit"))
					assert.Equal(t, int64(59999), to%60000)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, candleFixture(exchange, market, time.UnixMilli(from).UTC(), time.UnixMilli(to+1).UTC()))
				}))
				defer server.Close()
				base := instrumentTransport(func(r *http.Request) (*http.Response, error) {
					copy := r.Clone(r.Context())
					target, _ := url.Parse(server.URL)
					copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
					return server.Client().Transport.RoundTrip(copy)
				})
				state, err := newLocalState(4, func() time.Time { return now })
				require.NoError(t, err)
				state.exchanges, err = newExchangeClients(cfg, base, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
				require.NoError(t, err)
				require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: exchange, Market: market, Symbol: "BTCUSDT"}}))
				root, cancel := context.WithCancel(t.Context())
				defer cancel()
				service, err := state.klineService(root, cfg, func() time.Time { return now })
				require.NoError(t, err)
				defer func() {
					cancel()
					service.Wait()
				}()
				handler := httptransport.NewAPIHandler(func() bool { return true }, 8192, map[string]http.Handler{"/api/v1/klines": httptransport.NewKlinesHandler(service, time.Second, cfg.Klines.MaxCallers)})
				for _, tc := range []struct {
					name  string
					from  time.Time
					size  int
					calls int64
				}{
					{"cold", end.Add(-2 * time.Minute), 2, 1},
					{"warm", end.Add(-2 * time.Minute), 2, 1},
					{"partial", end.Add(-4 * time.Minute), 4, 2},
				} {
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", candleURL(scope, tc.from, end), nil))
					require.Equal(t, 200, response.Code, "%s: %s", tc.name, response.Body.String())
					assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
					var body struct {
						Data []struct {
							OpenTime       time.Time `json:"open_time"`
							CloseTime      time.Time `json:"close_time"`
							Open, Turnover string
							TradesCount    *int64 `json:"trades_count"`
						}
					}
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
					require.Len(t, body.Data, tc.size)
					assert.Equal(t, tc.from, body.Data[0].OpenTime)
					assert.Equal(t, end, body.Data[tc.size-1].CloseTime)
					for i, row := range body.Data {
						assert.Equal(t, tc.from.Add(time.Duration(i)*time.Minute), row.OpenTime)
						assert.Equal(t, "1.1234567890123456789", row.Open)
						assert.Equal(t, "4.1234567890123456789", row.Turnover)
						if exchange == domain.ExchangeBinance {
							require.NotNil(t, row.TradesCount)
							assert.Equal(t, int64(9007199254740993), *row.TradesCount)
						} else {
							assert.Nil(t, row.TradesCount)
						}
					}
					assert.NotContains(t, response.Body.String(), "RequestStartedAt")
					assert.Equal(t, tc.calls, calls.Load())
				}
				stats := state.klineMetrics.Snapshot()[scope]
				assert.Equal(t, uint64(2), stats.Attempts)
				assert.Equal(t, uint64(4), stats.Downloaded)
				assert.Equal(t, uint64(1), stats.CacheHits)
				assert.Equal(t, uint64(2), stats.CacheMisses)
			})
		}
	}
}

func TestKlineRetryAttemptsAreCountedAcrossPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Klines.MaxHistoryCandles = 4
		cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Spot = 2
		cfg.Upstream.LanesPerExchange.Klines.MaxAttempts = 3
		state, err := newLocalState(4, time.Now)
		require.NoError(t, err)
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
		end := time.Now().UTC().Truncate(time.Minute)
		var calls atomic.Int64
		base := instrumentTransport(func(r *http.Request) (*http.Response, error) {
			number := calls.Add(1)
			body, status := `{"code":-1}`, 500
			if number == 1 {
				body, status = candleFixture(scope.Exchange, scope.Market, end.Add(-4*time.Minute), end.Add(-2*time.Minute)), 200
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})
		state.exchanges, err = newExchangeClients(cfg, base, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
		require.NoError(t, err)
		root, cancel := context.WithCancel(t.Context())
		defer cancel()
		service, err := state.klineService(root, cfg, time.Now)
		require.NoError(t, err)
		defer func() {
			cancel()
			service.Wait()
		}()
		handler := httptransport.NewKlinesHandler(service, cfg.Klines.RequestTimeout, cfg.Klines.MaxCallers)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", candleURL(scope, end.Add(-4*time.Minute), end), nil))

		assert.Equal(t, 502, response.Code)
		assert.Contains(t, response.Body.String(), "upstream_attempt_limit")
		assert.NotContains(t, response.Body.String(), `"data"`)
		assert.Equal(t, int64(3), calls.Load())
		assert.Equal(t, uint64(3), state.klineMetrics.Snapshot()[scope].Attempts)
		assert.Equal(t, uint64(2), state.klineMetrics.Snapshot()[scope].Downloaded)
		query := kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: end.Add(-4 * time.Minute), To: end}
		stored, err := state.klines.GetRange(t.Context(), query)
		require.NoError(t, err)
		assert.Len(t, stored, 2)
	})
}

func TestServeExposesKlinesAndCancelsOwnedFill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		started := make(chan struct{})
		stopped := make(chan struct{})
		state.exchanges, err = newExchangeClients(cfg, instrumentTransport(func(r *http.Request) (*http.Response, error) {
			close(started)
			defer close(stopped)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}), upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
		require.NoError(t, err)
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
		root, cancel := context.WithCancel(t.Context())
		defer cancel()
		listener := newPipeListener()
		serverDone := make(chan error, 1)
		go func() { serverDone <- state.serve(root, cfg, testLogger(), listener) }()
		synctest.Wait()
		transport := &http.Transport{DialContext: listener.dial}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		end := time.Now().UTC().Truncate(time.Minute)
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://local"+candleURL(scope, end.Add(-time.Minute), end), nil)
		require.NoError(t, err)
		responseDone := make(chan struct{})
		go func() {
			defer close(responseDone)
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
			}
		}()
		<-started

		cancel()

		require.NoError(t, <-serverDone)
		assert.True(t, channelClosed(stopped))
		<-responseDone
	})
}

func TestKlineHTTPValidationBeforeCandleAccess(t *testing.T) {
	cases := []struct {
		name   string
		change func(url.Values)
		ready  bool
		status int
		code   string
	}{
		{"unready catalog", func(url.Values) {}, false, 503, "data_not_ready"},
		{"unknown symbol", func(q url.Values) { q.Set("symbol", "UNKNOWN") }, true, 404, "symbol_not_found"},
		{"disabled scope", func(q url.Values) { q.Set("exchange", "bybit") }, true, 400, "invalid_filter"},
		{"unsupported interval", func(q url.Values) {
			q.Set("interval", "1s")
			q.Set("market", "linear")
		}, true, 400, "invalid_interval"},
		{"unaligned start", func(q url.Values) { q.Set("from", "2026-09-12T11:56:01Z") }, true, 400, "invalid_range"},
		{"reversed range", func(q url.Values) { q.Set("from", "2026-09-12T12:01:00Z") }, true, 400, "invalid_range"},
		{"future range", func(q url.Values) { q.Set("to", "2026-09-12T12:02:00Z") }, true, 400, "invalid_range"},
		{"too large before readiness", func(q url.Values) { q.Set("from", "2026-09-12T11:55:00Z") }, false, 400, "request_too_large"},
		{"expired before readiness", func(q url.Values) {
			q.Set("from", "2026-09-12T11:55:00Z")
			q.Set("to", "2026-09-12T11:56:00Z")
		}, false, 400, "range_out_of_retention"},
		{"empty range", func(q url.Values) { q.Set("from", q.Get("to")) }, true, 200, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Klines.MaxHistoryCandles = 4
			cfg.Exchanges.Bybit.Enabled = false
			now := time.Date(2026, 9, 12, 12, 0, 30, 0, time.UTC)
			state, err := newLocalState(4, func() time.Time { return now })
			require.NoError(t, err)
			state.exchanges, err = newExchangeClients(cfg, noExchangeCalls{t}, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
			require.NoError(t, err)
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
			if tc.ready {
				require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
			}
			service, err := state.klineService(t.Context(), cfg, func() time.Time { return now })
			require.NoError(t, err)
			address, err := url.Parse(candleURL(scope, now.Truncate(time.Minute).Add(-4*time.Minute), now.Truncate(time.Minute)))
			require.NoError(t, err)
			parameters := address.Query()
			tc.change(parameters)
			response := httptest.NewRecorder()

			httptransport.NewKlinesHandler(service, time.Second, cfg.Klines.MaxCallers).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+parameters.Encode(), nil))

			assert.Equal(t, tc.status, response.Code)
			if tc.code == "" {
				assert.JSONEq(t, `{"data":[]}`, response.Body.String())
			} else {
				assert.Contains(t, response.Body.String(), `"code":"`+tc.code+`"`)
			}
			assert.Zero(t, state.klineMetrics.Snapshot()[scope].Attempts)
		})
	}
}
