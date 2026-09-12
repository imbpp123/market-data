package httptransport

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentAdaptersPublishSeparateCacheOnlyAPIs(t *testing.T) {
	cases := []struct {
		exchange domain.Exchange
		market   domain.Market
		scope    upstream.Scope
		attempts int
		failing  string
	}{
		{domain.ExchangeBinance, domain.MarketSpot, upstream.BinanceSpot, 3, "ticker"},
		{domain.ExchangeBinance, domain.MarketSpot, upstream.BinanceSpot, 3, "statistics"},
		{domain.ExchangeBinance, domain.MarketLinear, upstream.BinanceLinear, 4, "ticker"},
		{domain.ExchangeBinance, domain.MarketLinear, upstream.BinanceLinear, 4, "statistics"},
		{domain.ExchangeBybit, domain.MarketSpot, upstream.Bybit, 1, "ticker"},
		{domain.ExchangeBybit, domain.MarketSpot, upstream.Bybit, 1, "statistics"},
		{domain.ExchangeBybit, domain.MarketLinear, upstream.Bybit, 1, "ticker"},
		{domain.ExchangeBybit, domain.MarketLinear, upstream.Bybit, 1, "statistics"},
	}

	for _, tt := range cases {
		t.Run(string(tt.exchange)+"/"+string(tt.market)+"/"+tt.failing, func(t *testing.T) {
			var requests atomic.Int64
			var failed atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				assert.Empty(t, r.URL.Query().Get("symbol"))
				assert.Empty(t, r.URL.Query().Get("symbols"))
				price, volume, quote := "105.25", "3", ""
				if failed.Load() {
					price, volume = "106.25", "5"
					if tt.failing == "statistics" {
						volume = "bad"
					} else {
						quote = `,"bid1Price":"bad","bid1Size":"1"`
					}
				}
				switch {
				case r.URL.Path == "/v5/market/tickers":
					_, _ = fmt.Fprintf(w, `{"retCode":0,"result":{"category":"%s","list":[{"symbol":"A","lastPrice":"%s","highPrice24h":"110","lowPrice24h":"90","volume24h":"%s","turnover24h":"305.1234567890123456789","prevPrice24h":"100"%s}]}}`, tt.market, price, volume, quote)
				case strings.HasSuffix(r.URL.Path, "/price"):
					_, _ = fmt.Fprintf(w, `[{"symbol":"A","price":"%s"}]`, price)
				case strings.HasSuffix(r.URL.Path, "/bookTicker"), strings.HasSuffix(r.URL.Path, "/premiumIndex"):
					if failed.Load() && tt.failing == "ticker" && strings.HasSuffix(r.URL.Path, "/bookTicker") {
						_, _ = io.WriteString(w, `[{"symbol":"A","bidPrice":"bad","bidQty":"1"}]`)
					} else {
						_, _ = io.WriteString(w, `[]`)
					}
				case strings.HasSuffix(r.URL.Path, "/24hr"):
					if tt.market == domain.MarketSpot {
						assert.Equal(t, "FULL", r.URL.Query().Get("type"))
					}

					_, _ = fmt.Fprintf(w, `[{"symbol":"A","highPrice":"110","lowPrice":"90","volume":"%s","quoteVolume":"305.1234567890123456789","priceChange":"5.25"}]`, volume)
				default:
					t.Errorf("unexpected upstream path: %s", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			cfg := config.Defaults()
			controller, err := upstream.New(cfg, upstream.SystemClock{})
			require.NoError(t, err)
			transport, err := upstream.NewTransport(controller, tt.scope, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
			require.NoError(t, err)
			var provider interface {
				ticker.Provider
				marketstats.Provider
			}

			if tt.exchange == domain.ExchangeBinance {
				client, err := binance.NewClient(tt.scope, server.URL, transport)
				require.NoError(t, err)
				provider = binance.NewCurrentProvider(client, client, nil, nil)
			} else {
				client, err := bybit.NewClient(server.URL, transport)
				require.NoError(t, err)
				provider = bybit.NewCurrentProvider(client, nil)
			}

			tickers, stats := memory.NewTickerRepository(), memory.NewMarketStatsRepository()
			scope := application.Scope{Exchange: tt.exchange, Market: tt.market}
			refresh := ticker.NewRefresher(provider, scope, tickers, stats, nil)
			ctx, cancel, err := controller.Begin(t.Context(), tt.scope, upstream.Tickers)
			require.NoError(t, err)
			defer cancel()

			require.NoError(t, refresh.Refresh(ctx))
			if !provider.Capabilities().MarketStatsWithTicker {
				statsContext, stop, err := controller.Begin(t.Context(), tt.scope, upstream.MarketStats)
				require.NoError(t, err)
				defer stop()
				require.NoError(t, marketstats.NewRefresher(provider, scope, stats, time.Now, nil).Refresh(statsContext))
				assert.Equal(t, 1, controller.Attempts(statsContext))
				assert.Equal(t, tt.attempts-1, controller.Attempts(ctx))
			} else {
				assert.Equal(t, 1, controller.Attempts(ctx))
			}

			handler := NewAPIHandler(func() bool { return true }, 8192, NewSnapshotHandlers(nil, ticker.NewReader(tickers, []application.Scope{scope}, time.Now), marketstats.NewReader(stats, []application.Scope{scope}), time.Second, 2))
			for range 2 {
				tickerResponse := httptest.NewRecorder()
				handler.ServeHTTP(tickerResponse, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/tickers", nil))
				require.Equal(t, 200, tickerResponse.Code)
				assert.Contains(t, tickerResponse.Body.String(), `"last_price":"105.25"`)
				assert.NotContains(t, tickerResponse.Body.String(), `"volume"`)
				statsResponse := httptest.NewRecorder()
				handler.ServeHTTP(statsResponse, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/market-stats", nil))
				require.Equal(t, 200, statsResponse.Code)
				assert.Contains(t, statsResponse.Body.String(), `"turnover":"305.1234567890123456789"`)
				assert.Contains(t, statsResponse.Body.String(), `"price_change":"5.25"`)
				assert.Contains(t, statsResponse.Body.String(), `"trade_count":null`)
				assert.NotContains(t, statsResponse.Body.String(), `"last_price"`)
			}

			assert.Equal(t, int64(tt.attempts), requests.Load(), "cache reads must not fetch upstream")
			read := func(path string) string {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
				require.Equal(t, 200, response.Code)
				return response.Body.String()
			}
			oldTicker, oldStats := read("/api/v1/tickers"), read("/api/v1/market-stats")
			failed.Store(true)
			next, stop, err := controller.Begin(t.Context(), tt.scope, upstream.Tickers)
			require.NoError(t, err)
			defer stop()
			tickerError := refresh.Refresh(next)
			if provider.Capabilities().MarketStatsWithTicker || tt.failing == "ticker" {
				require.ErrorIs(t, tickerError, application.ErrInvalidUpstreamData)
			} else {
				require.NoError(t, tickerError)
			}
			if !provider.Capabilities().MarketStatsWithTicker {
				nextStats, stopStats, err := controller.Begin(t.Context(), tt.scope, upstream.MarketStats)
				require.NoError(t, err)
				defer stopStats()
				statsError := marketstats.NewRefresher(provider, scope, stats, time.Now, nil).Refresh(nextStats)
				if tt.failing == "statistics" {
					require.ErrorIs(t, statsError, application.ErrInvalidUpstreamData)
				} else {
					require.NoError(t, statsError)
				}
			}
			count := requests.Load()
			newTicker, newStats := read("/api/v1/tickers"), read("/api/v1/market-stats")
			if tt.failing == "ticker" {
				assert.JSONEq(t, oldTicker, newTicker, "failed refresh must preserve values and fetched_at")
				assert.Contains(t, newStats, `"volume":"5"`)
			} else {
				assert.JSONEq(t, oldStats, newStats, "failed refresh must preserve values and fetched_at")
				assert.Contains(t, newTicker, `"last_price":"106.25"`)
			}
			assert.Equal(t, count, requests.Load(), "reads after a failed refresh stay cache-only")

		})
	}
}
