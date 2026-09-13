package bootstrap

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
)

func TestGRPCBudgetRejectionPreservesPagesSnapshotsAndOtherExchange(t *testing.T) {
	cases := []struct {
		name  string
		share bool
	}{{name: "common threshold"}, {name: "operation share", share: true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				history, fetched, attempts := 4, 2, int64(1)
				cfg.Klines.MaxHistoryCandles = history
				if tc.share {
					window := cfg.Upstream.Limits.BinanceSpot.Windows["request_weight_1m"]
					window.Limit = 2000
					cfg.Exchanges.Binance.MarketStats.RefreshInterval = time.Minute
					cfg.Upstream.OperationSharePercent.Klines = 1
					cfg.Upstream.OperationSharePercent.Tickers = 89
					history, fetched, attempts = 20, 18, 9
					cfg.Klines.MaxHistoryCandles = history
					cfg.Upstream.Limits.BinanceSpot.Windows["request_weight_1m"] = window
				}
				cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Spot = 2
				state, err := newLocalState(int64(history), time.Now)
				require.NoError(t, err)
				end := time.Now().UTC().Truncate(time.Minute)
				for _, scope := range enabledScopes(cfg) {
					require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", UpdatedAt: end}}))
					require.NoError(t, state.tickers.ReplaceSnapshot(t.Context(), scope, []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", FetchedAt: end}}))
					require.NoError(t, state.marketStats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", Window: 24 * time.Hour, FetchedAt: end}}))
				}
				var binanceCalls, bybitCalls atomic.Int64
				base := instrumentTransport(func(r *http.Request) (*http.Response, error) {
					exchange, fromKey, toKey := domain.ExchangeBinance, "startTime", "endTime"
					headers := http.Header{"Content-Type": {"application/json"}}
					if strings.HasPrefix(r.URL.Path, "/v5/") {
						exchange, fromKey, toKey = domain.ExchangeBybit, "start", "end"
						bybitCalls.Add(1)
					} else {
						binanceCalls.Add(1)
						if !tc.share {
							headers.Set("X-Mbx-Used-Weight-1m", "5401")
						}
					}
					from, err := strconv.ParseInt(r.URL.Query().Get(fromKey), 10, 64)
					require.NoError(t, err)
					to, err := strconv.ParseInt(r.URL.Query().Get(toKey), 10, 64)
					require.NoError(t, err)
					return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(candleFixture(exchange, domain.MarketSpot, time.UnixMilli(from).UTC(), time.UnixMilli(to+1).UTC())))}, nil
				})
				state.exchanges, err = newExchangeClients(cfg, base, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
				require.NoError(t, err)
				api, _, cancel, done := startDualTest(t, state, cfg)
				defer func() { cancel(); require.NoError(t, <-done) }()
				scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
				response, err := api.GetKlines(t.Context(), klineRequest(scope, end.Add(-time.Duration(history)*time.Minute), end))
				require.Nil(t, response)
				assertRPCReason(t, err, codes.ResourceExhausted, "service_overloaded")
				assert.Equal(t, attempts, binanceCalls.Load())
				cached, err := api.GetKlines(t.Context(), klineRequest(scope, end.Add(-time.Duration(history)*time.Minute), end.Add(-2*time.Minute)))
				require.NoError(t, err)
				require.Len(t, cached.Klines, fetched)
				assert.Equal(t, "1.1234567890123456789", cached.Klines[0].Open)
				assert.Equal(t, int64(9007199254740993), cached.Klines[0].GetTradesCount())
				instruments, err := api.ListInstruments(t.Context(), &pb.ListInstrumentsRequest{})
				require.NoError(t, err)
				assert.Len(t, instruments.Instruments, 4)
				tickers, err := api.ListTickers(t.Context(), &pb.ListTickersRequest{})
				require.NoError(t, err)
				assert.Len(t, tickers.Tickers, 4)
				stats, err := api.ListMarketStats(t.Context(), &pb.ListMarketStatsRequest{})
				require.NoError(t, err)
				assert.Len(t, stats.MarketStats, 4)
				assert.Equal(t, attempts, binanceCalls.Load())
				other := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
				independent, err := api.GetKlines(t.Context(), klineRequest(other, end.Add(-time.Minute), end))
				require.NoError(t, err)
				require.Len(t, independent.Klines, 1)
				assert.Equal(t, "1.1234567890123456789", independent.Klines[0].Open)
				assert.Nil(t, independent.Klines[0].TradesCount)
				assert.Equal(t, int64(1), bybitCalls.Load())
				assert.Equal(t, uint64(attempts), state.klineMetrics.Snapshot()[scope].Attempts)
				time.Sleep(time.Minute)
				recovered, err := api.GetKlines(t.Context(), klineRequest(scope, end.Add(-2*time.Minute), end))
				require.NoError(t, err)
				assert.Len(t, recovered.Klines, 2)
				assert.Equal(t, attempts+1, binanceCalls.Load())
			})
		})
	}
}

func assertRPCReason(t *testing.T, err error, code codes.Code, reason string) {
	t.Helper()
	require.Equal(t, code, status.Code(err))
	details := status.Convert(err).Details()
	require.Len(t, details, 1)
	require.IsType(t, &pb.ErrorDetail{}, details[0])
	assert.Equal(t, reason, details[0].(*pb.ErrorDetail).Reason)
}

func TestGRPCCooldownKeepsCacheReadableAndBybitIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.HTTPClient.Retry.MaxAttempts = 1
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		end := time.Now().UTC().Truncate(time.Minute)
		for _, scope := range enabledScopes(cfg) {
			require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", UpdatedAt: end}}))
		}
		var calls atomic.Int64
		state.exchanges, err = newExchangeClients(cfg, instrumentTransport(func(r *http.Request) (*http.Response, error) {
			count := calls.Add(1)
			if count == 1 {
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(strings.NewReader(`{"code":-1003,"msg":"rate limited"}`))}, nil
			}
			exchange, fromKey, toKey := domain.ExchangeBinance, "startTime", "endTime"
			if strings.HasPrefix(r.URL.Path, "/v5/") {
				exchange, fromKey, toKey = domain.ExchangeBybit, "start", "end"
			}
			from, err := strconv.ParseInt(r.URL.Query().Get(fromKey), 10, 64)
			require.NoError(t, err)
			to, err := strconv.ParseInt(r.URL.Query().Get(toKey), 10, 64)
			require.NoError(t, err)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(candleFixture(exchange, domain.MarketSpot, time.UnixMilli(from).UTC(), time.UnixMilli(to+1).UTC())))}, nil
		}), upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
		require.NoError(t, err)
		api, _, cancel, done := startDualTest(t, state, cfg)
		defer func() { cancel(); require.NoError(t, <-done) }()
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		warm, err := loadRows(kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: end.Add(-2 * time.Minute), To: end.Add(-time.Minute)}, time.Now())
		require.NoError(t, err)
		require.NoError(t, state.klines.UpsertMany(t.Context(), warm))
		_, err = api.GetKlines(t.Context(), klineRequest(scope, end.Add(-time.Minute), end))
		assertRPCReason(t, err, codes.Unavailable, "upstream_error")
		cached, err := api.GetKlines(t.Context(), klineRequest(scope, end.Add(-2*time.Minute), end.Add(-time.Minute)))
		require.NoError(t, err)
		require.Len(t, cached.Klines, 1)
		assert.Equal(t, "12345.1234567890123456789", cached.Klines[0].Open)
		assert.Equal(t, int64(1), calls.Load())
		ctx, stop := context.WithCancel(t.Context())
		failed := make(chan error, 1)
		go func() { _, err := api.GetKlines(ctx, klineRequest(scope, end.Add(-time.Minute), end)); failed <- err }()
		synctest.Wait()
		instruments, err := api.ListInstruments(t.Context(), &pb.ListInstrumentsRequest{Exchange: proto.String("binance")})
		require.NoError(t, err)
		assert.Len(t, instruments.Instruments, 2)
		other := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
		rows, err := api.GetKlines(t.Context(), klineRequest(other, end.Add(-time.Minute), end))
		require.NoError(t, err)
		assert.Len(t, rows.Klines, 1)
		assert.Equal(t, int64(2), calls.Load())
		stop()
		assertRPCReason(t, <-failed, codes.Unavailable, "upstream_unavailable")
		time.Sleep(2 * time.Minute)
		recovered, err := api.GetKlines(t.Context(), klineRequest(scope, end.Add(-time.Minute), end))
		require.NoError(t, err)
		assert.Len(t, recovered.Klines, 1)
		assert.Equal(t, int64(3), calls.Load())
	})
}
