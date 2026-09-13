package bootstrap

import (
	"context"
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

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	grpctransport "market-data/internal/transport/grpc"

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

func TestKlineColdWarmPartialGRPCThroughExchangeAdapters(t *testing.T) {
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
				api := startTestAPI(t, grpctransport.Readers{Klines: service}, configuredGRPC(cfg).Transport)
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
					response, err := api.GetKlines(t.Context(), klineRequest(scope, tc.from, end))
					require.NoError(t, err, tc.name)
					require.Len(t, response.Klines, tc.size)
					assert.Equal(t, tc.from, response.Klines[0].OpenTime.AsTime())
					assert.Equal(t, end, response.Klines[tc.size-1].CloseTime.AsTime())
					for i, row := range response.Klines {
						assert.Equal(t, tc.from.Add(time.Duration(i)*time.Minute), row.OpenTime.AsTime())
						assert.Equal(t, "1.1234567890123456789", row.Open)
						assert.Equal(t, "4.1234567890123456789", row.Turnover)
						if exchange == domain.ExchangeBinance {
							require.NotNil(t, row.TradesCount)
							assert.Equal(t, int64(9007199254740993), *row.TradesCount)
						} else {
							assert.Nil(t, row.TradesCount)
						}
					}

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
		query := kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: end.Add(-4 * time.Minute), To: end}
		response, err := service.Get(t.Context(), query)
		assert.ErrorIs(t, err, application.ErrUpstreamAttemptLimit)
		assert.Empty(t, response)

		assert.Equal(t, int64(3), calls.Load())
		assert.Equal(t, uint64(3), state.klineMetrics.Snapshot()[scope].Attempts)
		assert.Equal(t, uint64(2), state.klineMetrics.Snapshot()[scope].Downloaded)
		stored, err := state.klines.GetRange(t.Context(), query)
		require.NoError(t, err)
		assert.Len(t, stored, 2)
	})
}

func TestKlineGRPCValidationBeforeCandleAccess(t *testing.T) {
	cases := []struct {
		name   string
		change func(*pb.GetKlinesRequest)
		ready  bool
		status codes.Code
		code   string
	}{
		{"unready catalog", func(*pb.GetKlinesRequest) {}, false, codes.Unavailable, "data_not_ready"},
		{"unknown symbol", func(q *pb.GetKlinesRequest) { q.Symbol = proto.String("UNKNOWN") }, true, codes.NotFound, "symbol_not_found"},
		{"disabled scope", func(q *pb.GetKlinesRequest) { q.Exchange = proto.String("bybit") }, true, codes.InvalidArgument, "invalid_filter"},
		{"unsupported interval", func(q *pb.GetKlinesRequest) {
			q.Interval = proto.String("1s")
			q.Market = proto.String("linear")
		}, true, codes.InvalidArgument, "invalid_interval"},
		{"unaligned start", func(q *pb.GetKlinesRequest) { q.From = timestamppb.New(time.Date(2026, 9, 12, 11, 56, 1, 0, time.UTC)) }, true, codes.InvalidArgument, "invalid_range"},
		{"reversed range", func(q *pb.GetKlinesRequest) { q.From = timestamppb.New(time.Date(2026, 9, 12, 12, 1, 0, 0, time.UTC)) }, true, codes.InvalidArgument, "invalid_range"},
		{"future range", func(q *pb.GetKlinesRequest) { q.To = timestamppb.New(time.Date(2026, 9, 12, 12, 2, 0, 0, time.UTC)) }, true, codes.InvalidArgument, "invalid_range"},
		{"too large before readiness", func(q *pb.GetKlinesRequest) { q.From = timestamppb.New(time.Date(2026, 9, 12, 11, 55, 0, 0, time.UTC)) }, false, codes.InvalidArgument, "request_too_large"},
		{"expired before readiness", func(q *pb.GetKlinesRequest) {
			q.From = timestamppb.New(time.Date(2026, 9, 12, 11, 55, 0, 0, time.UTC))
			q.To = timestamppb.New(time.Date(2026, 9, 12, 11, 56, 0, 0, time.UTC))
		}, false, codes.InvalidArgument, "range_out_of_retention"},
		{"empty range", func(q *pb.GetKlinesRequest) { q.From = q.To }, true, codes.OK, ""},
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
			request := klineRequest(scope, now.Truncate(time.Minute).Add(-4*time.Minute), now.Truncate(time.Minute))
			tc.change(request)
			api := startTestAPI(t, grpctransport.Readers{Klines: service}, configuredGRPC(cfg).Transport)
			response, err := api.GetKlines(t.Context(), request)
			assert.Equal(t, tc.status, status.Code(err))
			if tc.code == "" {
				require.NoError(t, err)
				assert.Empty(t, response.Klines)
			} else {
				require.Error(t, err)
				assert.Equal(t, tc.code, status.Convert(err).Details()[0].(*pb.ErrorDetail).Reason)
			}

			assert.Zero(t, state.klineMetrics.Snapshot()[scope].Attempts)
		})
	}
}
