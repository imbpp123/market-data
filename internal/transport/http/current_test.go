package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentHTTPFiltersAndReadiness(t *testing.T) {
	cases := []struct {
		name, path, query, code string
		ready                   bool
		status                  int
	}{
		{"ticker unready", "tickers", "", "data_not_ready", false, 503},
		{"stats unready", "market-stats", "", "data_not_ready", false, 503},
		{"ticker empty ready", "tickers", "", "", true, 200},
		{"stats default window", "market-stats", "", "", true, 200},
		{"stats canonical window", "market-stats", "window=24h", "", true, 200},
		{"stats missing symbol", "market-stats", "symbol=ABSENT", "", true, 200},
		{"window empty", "market-stats", "window=", "unsupported_window", false, 400},
		{"window repeated", "market-stats", "window=24h&window=24h", "unsupported_window", false, 400},
		{"window alias", "market-stats", "window=24h0m0s", "unsupported_window", false, 400},
		{"window number", "market-stats", "window=86400", "unsupported_window", false, 400},
		{"window case", "market-stats", "window=24H", "unsupported_window", false, 400},
		{"window duration", "market-stats", "window=2h", "unsupported_window", false, 400},
		{"ticker window", "tickers", "window=24h", "invalid_parameter", false, 400},
		{"unknown key", "market-stats", "extra=1", "invalid_parameter", false, 400},
		{"malformed query", "market-stats", "symbol=%zz", "invalid_parameter", false, 400},
		{"repeated scalar", "tickers", "market=spot&market=spot", "invalid_parameter", false, 400},
		{"empty scalar", "tickers", "symbol=", "invalid_parameter", false, 400},
		{"unknown exchange", "market-stats", "exchange=other", "invalid_filter", false, 400},
		{"disabled exchange", "tickers", "exchange=bybit", "invalid_filter", false, 400},
		{"disabled market", "market-stats", "market=linear", "invalid_filter", false, 400},
		{"filter before window", "market-stats", "exchange=other&window=2h", "invalid_filter", false, 400},
		{"invalid symbol", "tickers", "symbol=A+B", "invalid_filter", false, 400},
		{"invalid UTF8", "market-stats", "symbol=%FF", "invalid_filter", false, 400},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
			tickers, stats := memory.NewTickerRepository(), memory.NewMarketStatsRepository()
			if tt.ready {
				require.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, nil))
				require.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, nil))
			}

			scopes := []application.Scope{scope}
			handler := NewAPIHandler(func() bool { return true }, 8192, NewSnapshotHandlers(nil, ticker.NewReader(tickers, scopes, time.Now), marketstats.NewReader(stats, scopes), time.Second, 2))
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/"+tt.path+"?"+tt.query, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.Equal(t, tt.status, response.Code)
			if tt.code != "" {
				assert.Contains(t, response.Body.String(), `"code":"`+tt.code+`"`)
			} else {
				assert.JSONEq(t, `{"data":[]}`, response.Body.String())
			}
		})
	}
}

func TestCurrentHTTPUsesOneClockAndExactSeparateDTOs(t *testing.T) {
	at := time.Date(2026, 9, 12, 12, 0, 0, 123, time.UTC)
	now := at
	next := at.Add(90 * time.Second)
	tickers, stats := memory.NewTickerRepository(), memory.NewMarketStatsRepository()
	scopes := []application.Scope{{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}, {Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}}
	for _, scope := range scopes {
		require.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, []domain.Ticker{
			{Exchange: scope.Exchange, Market: scope.Market, Symbol: "B", LastPrice: decimal.RequireFromString("12345678901234567890.123456789"), NextFundingAt: &next, FetchedAt: at},
			{Exchange: scope.Exchange, Market: scope.Market, Symbol: "A", LastPrice: decimal.NewFromInt(1), FundingRate: new(decimal.RequireFromString("-0.0001")), NextFundingAt: &next, FetchedAt: at},
		}))
		require.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, []domain.MarketStats{
			{Exchange: scope.Exchange, Market: scope.Market, Symbol: "A", Window: 24 * time.Hour, High: decimal.NewFromInt(210), Low: decimal.NewFromInt(190), Volume: decimal.NewFromInt(3), Turnover: decimal.RequireFromString("601.1234567890123456789"), PriceChange: new(decimal.NewFromInt(5)), TradeCount: new(int64(0)), FetchedAt: at.Add(-time.Minute)},
		}))
	}

	calls := 0
	reader := ticker.NewReader(tickers, scopes, func() time.Time { calls++; return now })
	handler := NewAPIHandler(func() bool { return true }, 8192, NewSnapshotHandlers(nil, reader, marketstats.NewReader(stats, scopes), time.Second, 2))
	cases := []struct {
		name      string
		elapsed   time.Duration
		remaining any
	}{{"90 seconds", 0, float64(90)}, {"30 seconds", time.Minute, float64(30)}, {"subsecond", 90*time.Second - time.Millisecond, float64(0)}, {"arrived", 90 * time.Second, nil}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			now = at.Add(tt.elapsed)
			before := calls
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/tickers", nil))

			require.Equal(t, 200, response.Code)
			assert.Equal(t, before+1, calls)
			var body struct {
				Data []map[string]any `json:"data"`
			}

			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Len(t, body.Data, 4)
			assert.Equal(t, "binance", body.Data[0]["exchange"])
			assert.Equal(t, "A", body.Data[0]["symbol"])
			assert.Equal(t, "B", body.Data[1]["symbol"])
			assert.Equal(t, "12345678901234567890.123456789", body.Data[1]["last_price"])
			for _, row := range body.Data {
				assert.Equal(t, tt.remaining, row["next_funding_in"])
				assert.Equal(t, at.Format(time.RFC3339Nano), row["fetched_at"])
				assert.Contains(t, row, "bid_price")
				assert.Nil(t, row["bid_price"])
				assert.NotContains(t, row, "next_funding_at")
				assert.NotContains(t, row, "next_funding_time")
				assert.NotContains(t, row, "volume")
				assert.NotContains(t, row, "window")
			}
		})
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/market-stats?exchange=bybit&market=linear&symbol=A", nil))
	require.Equal(t, 200, response.Code)
	assert.JSONEq(t, `{"data":[{"exchange":"bybit","market":"linear","symbol":"A","window":"24h","high":"210","low":"190","volume":"3","turnover":"601.1234567890123456789","price_change":"5","trade_count":0,"fetched_at":"`+at.Add(-time.Minute).Format(time.RFC3339Nano)+`"}]}`, response.Body.String())
}

func TestCurrentReadersCannotFilterAwayUnreadyScopes(t *testing.T) {
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	scopes := []application.Scope{scope, {Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}}
	tickers, stats := memory.NewTickerRepository(), memory.NewMarketStatsRepository()
	require.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, nil))
	require.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, nil))
	query := application.SnapshotQuery{Symbol: "ABSENT"}

	_, err := ticker.NewReader(tickers, scopes, time.Now).List(t.Context(), query)
	assert.ErrorIs(t, err, application.ErrDataNotReady)
	_, err = marketstats.NewReader(stats, scopes).List(t.Context(), marketstats.Query{SnapshotQuery: query})
	assert.ErrorIs(t, err, application.ErrDataNotReady)
}

type blockedTickerReader struct{ started chan struct{} }

func (blockedTickerReader) Validate(application.SnapshotQuery) error { return nil }

func (r blockedTickerReader) List(ctx context.Context, _ application.SnapshotQuery) ([]ticker.ReadModel, error) {
	close(r.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSnapshotCallerLimitIsSharedAndDeadlineReleasesSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
		scopes := []application.Scope{scope}
		stats := memory.NewMarketStatsRepository()
		require.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, nil))
		instruments := memory.NewInstrumentRepository()
		require.NoError(t, instruments.ReplaceSnapshot(t.Context(), scope, nil))
		blocked := blockedTickerReader{started: make(chan struct{})}
		handler := NewAPIHandler(func() bool { return true }, 8192, NewSnapshotHandlers(instrument.NewReader(instruments, scopes), blocked, marketstats.NewReader(stats, scopes), time.Second, 1))
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/tickers", nil))
		}()
		<-blocked.started
		for _, path := range []string{"/api/v1/instruments", "/api/v1/market-stats"} {
			rejected := httptest.NewRecorder()
			handler.ServeHTTP(rejected, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
			assert.Equal(t, 503, rejected.Code)
			assert.Contains(t, rejected.Body.String(), "service_overloaded")
		}

		invalid := httptest.NewRecorder()
		handler.ServeHTTP(invalid, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/market-stats?window=2h", nil))
		assert.Equal(t, 400, invalid.Code)
		assert.Contains(t, invalid.Body.String(), "unsupported_window")
		health := httptest.NewRecorder()
		handler.ServeHTTP(health, httptest.NewRequestWithContext(t.Context(), "GET", "/health", nil))
		assert.Equal(t, 200, health.Code)

		time.Sleep(time.Second)
		<-done

		assert.Equal(t, 504, response.Code)
		accepted := httptest.NewRecorder()
		handler.ServeHTTP(accepted, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/market-stats", nil))
		assert.Equal(t, 200, accepted.Code)
	})
}

func TestConcurrentCurrentHTTPReadsSeeWholeSnapshots(t *testing.T) {
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	tickers, stats := memory.NewTickerRepository(), memory.NewMarketStatsRepository()
	scopes := []application.Scope{scope}
	first := []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "A"}, {Exchange: scope.Exchange, Market: scope.Market, Symbol: "B"}}
	second := []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "C"}}
	statsFirst := []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "A", Window: 24 * time.Hour}, {Exchange: scope.Exchange, Market: scope.Market, Symbol: "B", Window: 24 * time.Hour}}
	statsSecond := []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "C", Window: 24 * time.Hour}}
	require.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, first))
	require.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, statsFirst))
	handler := NewAPIHandler(func() bool { return true }, 8192, NewSnapshotHandlers(nil, ticker.NewReader(tickers, scopes, time.Now), marketstats.NewReader(stats, scopes), time.Second, 100))
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
			for _, path := range []string{"tickers", "market-stats"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/"+path, nil))
				assert.Equal(t, 200, response.Code)
				var body struct{ Data []struct{ Symbol string } }
				if !assert.NoError(t, json.Unmarshal(response.Body.Bytes(), &body)) {
					return
				}

				var symbols []string
				for _, row := range body.Data {
					symbols = append(symbols, row.Symbol)
				}

				assert.Contains(t, []string{"A,B", "C"}, strings.Join(symbols, ","))
			}
		})
	}

	workers.Wait()
}
