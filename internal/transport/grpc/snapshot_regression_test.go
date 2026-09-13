package grpctransport

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"
)

func TestTickersUseOneClockAndPreserveFundingPresence(t *testing.T) {
	cases := []struct {
		name      string
		elapsed   time.Duration
		remaining int64
		present   bool
	}{
		{"90 seconds", 0, 90, true}, {"30 seconds", time.Minute, 30, true}, {"subsecond", 90*time.Second - time.Millisecond, 0, true}, {"arrived", 90 * time.Second, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 9, 12, 12, 0, 0, 123, time.UTC)
			next := at.Add(90 * time.Second)
			repo := memory.NewTickerRepository()
			scopes := []application.Scope{{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}, {Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}}
			for _, scope := range scopes {
				require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, []domain.Ticker{
					{Exchange: scope.Exchange, Market: scope.Market, Symbol: "B", LastPrice: decimal.RequireFromString("12345678901234567890.123456789"), NextFundingAt: &next, FetchedAt: at},
					{Exchange: scope.Exchange, Market: scope.Market, Symbol: "A", LastPrice: decimal.NewFromInt(1), NextFundingAt: &next, FetchedAt: at},
				}))
			}
			var calls atomic.Int64
			readers := fixtureReaders(applicationFixture(t, nil))
			readers.Tickers = ticker.NewReader(repo, scopes, func() time.Time { calls.Add(1); return at.Add(tc.elapsed) })
			_, address := startServer(t, readers, testSettings(), nil)
			response, err := client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{})
			require.NoError(t, err)
			assert.Equal(t, int64(1), calls.Load())
			require.Len(t, response.Tickers, 4)
			assert.Equal(t, "binance", response.Tickers[0].Exchange)
			assert.Equal(t, "A", response.Tickers[0].Symbol)
			assert.Equal(t, "B", response.Tickers[1].Symbol)
			assert.Equal(t, "12345678901234567890.123456789", response.Tickers[1].LastPrice)
			for _, row := range response.Tickers {
				assert.Equal(t, tc.present, row.NextFundingInSeconds != nil)
				assert.Equal(t, tc.remaining, row.GetNextFundingInSeconds())
				assert.Equal(t, at, row.FetchedAt.AsTime())
				assert.Nil(t, row.BidPrice)
			}
		})
	}
}

func TestConcurrentRPCReadsSeeWholeSnapshots(t *testing.T) {
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	scopes := []application.Scope{scope}
	tickers, stats := memory.NewTickerRepository(), memory.NewMarketStatsRepository()
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	first := []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "A", FetchedAt: at}, {Exchange: scope.Exchange, Market: scope.Market, Symbol: "B", FetchedAt: at}}
	second := []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "C", FetchedAt: at}}
	statsFirst := []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "A", Window: 24 * time.Hour, FetchedAt: at}, {Exchange: scope.Exchange, Market: scope.Market, Symbol: "B", Window: 24 * time.Hour, FetchedAt: at}}
	statsSecond := []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "C", Window: 24 * time.Hour, FetchedAt: at}}
	require.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, first))
	require.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, statsFirst))
	readers := fixtureReaders(applicationFixture(t, nil))
	readers.Tickers = ticker.NewReader(tickers, scopes, time.Now)
	readers.MarketStats = marketstats.NewReader(stats, scopes)
	settings := testSettings()
	settings.MaxSnapshots = 100
	_, address := startServer(t, readers, settings, nil)
	api := client(t, address)
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 50 {
			assert.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, second))
			assert.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, statsSecond))
			assert.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, first))
			assert.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, statsFirst))
		}
	})
	for range 50 {
		workers.Go(func() {
			response, err := api.ListTickers(t.Context(), &pb.ListTickersRequest{})
			if !assert.NoError(t, err) {
				return
			}
			var symbols []string
			for _, row := range response.Tickers {
				symbols = append(symbols, row.Symbol)
			}
			assert.Contains(t, []string{"A,B", "C"}, strings.Join(symbols, ","))
			statistics, err := api.ListMarketStats(t.Context(), &pb.ListMarketStatsRequest{})
			if !assert.NoError(t, err) {
				return
			}
			symbols = nil
			for _, row := range statistics.MarketStats {
				symbols = append(symbols, row.Symbol)
			}
			assert.Contains(t, []string{"A,B", "C"}, strings.Join(symbols, ","))
		})
	}
	workers.Wait()
}
