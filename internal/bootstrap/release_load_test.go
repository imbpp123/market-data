package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	httptransport "market-data/internal/transport/http"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This opt-in measurement uses real storage, fill coordination, and HTTP encoding.
// Synthetic providers exclude exchange pacing and network latency from timings.
func TestReleaseLoad(t *testing.T) {
	profile := os.Getenv("MDS_RELEASE_LOAD")
	if profile == "" {
		t.Skip("set MDS_RELEASE_LOAD=main or broad to measure release capacity")
	}
	require.Contains(t, []string{"main", "broad"}, profile)
	now := time.Date(2100, 9, 12, 12, 0, 0, 0, time.UTC)
	cfg := config.Defaults()
	state, err := newLocalState(1000, func() time.Time { return now })
	require.NoError(t, err)
	scopes := enabledScopes(cfg)
	var settings []kline.ScopeSettings
	var queries []kline.Query
	var retentionScopes []kline.RetentionScope
	var attempts atomic.Int64
	started := time.Now()
	for _, scope := range scopes {
		publishLoadSnapshots(t, state, scope, now)
		provider := &loadProvider{exchange: scope.Exchange, now: now, attempts: &attempts}
		settings = append(settings, kline.ScopeSettings{Scope: scope, Provider: provider, PageLimit: 1000})
		intervals := []domain.Timeframe{domain.Timeframe1m, domain.Timeframe5m, domain.Timeframe1h}
		if profile == "broad" {
			intervals, err = provider.SupportedTimeframes(scope.Market)
			require.NoError(t, err)
		}
		for _, interval := range intervals {
			calendar, err := domain.NewCalendar(scope.Exchange, scope.Market, interval)
			require.NoError(t, err)
			end, err := calendar.Floor(now)
			require.NoError(t, err)
			from, err := calendar.Shift(end, -1000)
			require.NoError(t, err)
			last, err := calendar.Shift(end, -1)
			require.NoError(t, err)
			retentionScopes = append(retentionScopes, kline.RetentionScope{Scope: scope, Interval: interval})
			for symbol := range 50 {
				query := kline.Query{Series: kline.Series{Scope: scope, Symbol: fmt.Sprintf("S%04dUSDT", symbol), Interval: interval}, From: from, To: end}
				preload := query
				preload.To = last
				rows, err := loadRows(preload, now)
				require.NoError(t, err)
				require.NoError(t, state.klines.UpsertMany(t.Context(), rows))
				queries = append(queries, query)
			}
		}
	}
	t.Logf("profile=%s series=%d preloaded_rows=%d snapshots_per_type=20000 populate=%s", profile, len(queries), len(queries)*999, time.Since(started))
	logLoadMemory(t, "retained")
	root, cancel := context.WithCancel(t.Context())
	defer cancel()
	service, err := kline.NewService(root, state.klines, state.instruments, loadOperations{}, settings, kline.Settings{
		HistoryCandles: 1000, MaxCallers: cfg.Klines.MaxCallers, MaxActiveFills: cfg.Klines.MaxActiveFills,
		MaxActiveFillsPerExchange: cfg.Klines.MaxActiveFillsPerExchange, MaxAttempts: cfg.Upstream.LanesPerExchange.Klines.MaxAttempts, FillTimeout: cfg.Klines.FillTimeout,
	}, func() time.Time { return now }, nil)
	require.NoError(t, err)
	defer service.Wait()
	defer cancel()
	routes := httptransport.NewSnapshotHandlers(instrument.NewReader(state.instruments, scopes), ticker.NewReader(state.tickers, scopes, func() time.Time { return now }), marketstats.NewReader(state.marketStats, scopes), cfg.Server.SnapshotTimeout, cfg.Server.MaxSnapshotRequests)
	routes["/api/v1/klines"] = httptransport.NewKlinesHandler(service, cfg.Klines.RequestTimeout, cfg.Klines.MaxCallers)
	handler := httptransport.NewAPIHandler(func() bool { return true }, cfg.Server.MaxQueryBytes, routes)
	for _, pass := range []string{"partial fills", "warm reads"} {
		before := attempts.Load()
		var wg sync.WaitGroup
		timings := make([][]time.Duration, 4)
		for client := range 4 {
			wg.Go(func() {
				for i, query := range queries {
					values := url.Values{"exchange": {string(query.Exchange)}, "market": {string(query.Market)}, "symbol": {query.Symbol}, "interval": {string(query.Interval)}, "from": {query.From.Format(time.RFC3339)}, "to": {query.To.Format(time.RFC3339)}}
					begin := time.Now()
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+values.Encode(), nil))
					timings[client] = append(timings[client], time.Since(begin))
					if !assert.Equal(t, http.StatusOK, response.Code, "%s", response.Body.String()) {
						return
					}
					assert.Equal(t, 1000, bytes.Count(response.Body.Bytes(), []byte(`"open_time":`)))
					if i%100 == 0 {
						for _, path := range []string{"/api/v1/instruments", "/api/v1/tickers", "/api/v1/market-stats"} {
							snapshot := httptest.NewRecorder()
							handler.ServeHTTP(snapshot, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
							assert.Equal(t, http.StatusOK, snapshot.Code)
						}
					}
				}
			})
		}
		wg.Wait()
		all := slices.Concat(timings...)
		require.NotEmpty(t, all)
		slices.Sort(all)
		t.Logf("pass=%s requests=%d attempts=%d p50=%s p95=%s max=%s", pass, len(all), attempts.Load()-before, all[len(all)/2], all[len(all)*95/100], all[len(all)-1])
		if pass == "partial fills" {
			assert.Equal(t, int64(len(queries)), attempts.Load()-before)
		} else {
			assert.Equal(t, before, attempts.Load())
		}
	}
	counts, err := state.inventory.CandleCounts(t.Context())
	require.NoError(t, err)
	total := 0
	for _, count := range counts {
		total += count
	}
	assert.Equal(t, len(queries)*1000, total)
	logLoadMemory(t, "after reads and fills")
	started = time.Now()
	retention, err := kline.NewRetention(state.klines, retentionScopes, 1000, func() time.Time { return now.Add(time.Hour) })
	require.NoError(t, err)
	require.NoError(t, retention.Clean(t.Context()))
	t.Logf("rolling_cleanup=%s", time.Since(started))
	started = time.Now()
	for _, query := range queries {
		require.NoError(t, state.klines.DeleteBefore(t.Context(), query.Scope, query.Interval, query.To))
	}
	late, err := loadRows(kline.Query{Series: queries[0].Series, From: queries[0].From, To: queries[0].From.Add(time.Minute)}, now)
	require.NoError(t, err)
	require.NoError(t, state.klines.UpsertMany(t.Context(), late))
	counts, err = state.inventory.CandleCounts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, counts)
	t.Logf("idle_cleanup_and_late_write=%s", time.Since(started))
	started = time.Now()
	cancel()
	service.Wait()
	t.Logf("fill_shutdown=%s", time.Since(started))
	peak := logLoadMemory(t, "after cleanup")
	if runtime.GOOS == "linux" && profile == "main" {
		require.Positive(t, peak, "Linux process RSS measurement is required")
		assert.LessOrEqual(t, peak, uint64(800_000_000), "agreed workload must leave 200 MB of process memory headroom")
	}
	runtime.KeepAlive(state)
}

func logLoadMemory(t *testing.T, stage string) uint64 {
	t.Helper()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	t.Logf("stage=%s heap_alloc=%d runtime_sys=%d", stage, stats.HeapAlloc, stats.Sys)
	var peak uint64
	if status, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "VmHWM:") || strings.HasPrefix(line, "VmRSS:") {
				t.Log(line)
				if strings.HasPrefix(line, "VmHWM:") {
					value, err := strconv.ParseUint(strings.Fields(line)[1], 10, 64)
					require.NoError(t, err)
					peak = value * 1024
				}
			}
		}
	}
	return peak
}

func publishLoadSnapshots(t *testing.T, state *localState, scope application.Scope, now time.Time) {
	t.Helper()
	value := decimal.RequireFromString("12345.1234567890123456789")
	funding := 8 * time.Hour
	next := now.Add(funding)
	count := int64(9007199254740993)
	var instruments []domain.Instrument
	var tickers []domain.Ticker
	var stats []domain.MarketStats
	for i := range 5000 {
		symbol := fmt.Sprintf("S%04dUSDT", i)
		instruments = append(instruments, domain.Instrument{Exchange: scope.Exchange, Market: scope.Market, Symbol: symbol, BaseAsset: symbol, QuoteAsset: "USDT", Status: domain.InstrumentStatusTrading, PriceTick: value, QtyStep: value, MinQty: &value, MaxQty: &value, MinNotional: &value, FundingInterval: &funding, DelistingTime: &next, UpdatedAt: now})
		tickers = append(tickers, domain.Ticker{Exchange: scope.Exchange, Market: scope.Market, Symbol: symbol, LastPrice: value, BidPrice: &value, AskPrice: &value, BidSize: &value, AskSize: &value, FundingRate: &value, NextFundingAt: &next, FetchedAt: now})
		stats = append(stats, domain.MarketStats{Exchange: scope.Exchange, Market: scope.Market, Symbol: symbol, Window: 24 * time.Hour, High: value, Low: value, Volume: value, Turnover: value, PriceChange: &value, TradeCount: &count, FetchedAt: now})
	}
	require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, instruments))
	require.NoError(t, state.tickers.ReplaceSnapshot(t.Context(), scope, tickers))
	require.NoError(t, state.marketStats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, stats))
}

type loadProvider struct {
	exchange domain.Exchange
	now      time.Time
	attempts *atomic.Int64
}

func (p *loadProvider) Exchange() domain.Exchange { return p.exchange }

func (p *loadProvider) SupportedTimeframes(market domain.Market) ([]domain.Timeframe, error) {
	if p.exchange == domain.ExchangeBinance {
		return binance.NewKlineProvider(nil, nil).SupportedTimeframes(market)
	}
	return bybit.NewKlineProvider(nil).SupportedTimeframes(market)
}

func (p *loadProvider) GetKlines(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.attempts.Add(1)
	return loadRows(request.Query, p.now)
}

type loadOperations struct{}

func (loadOperations) Run(ctx context.Context, _ application.Scope, attempt func(), run func(context.Context) error) error {
	attempt()
	return run(ctx)
}

func loadRows(query kline.Query, now time.Time) ([]kline.Stored, error) {
	calendar, err := domain.NewCalendar(query.Exchange, query.Market, query.Interval)
	if err != nil {
		return nil, err
	}
	value := decimal.RequireFromString("12345.1234567890123456789")
	count := int64(9007199254740993)
	rows := make([]kline.Stored, 0, 1000)
	for open := query.From; open.Before(query.To); {
		closeTime, err := calendar.Next(open)
		if err != nil {
			return nil, err
		}
		rows = append(rows, kline.Stored{RequestStartedAt: now, Candle: domain.Kline{Exchange: query.Exchange, Market: query.Market, Symbol: query.Symbol, Interval: query.Interval, OpenTime: open, CloseTime: closeTime, Open: value, High: value, Low: value, Close: value, Volume: value, Turnover: value, TradesCount: &count, FetchedAt: now}})
		open = closeTime
	}
	return rows, nil
}
