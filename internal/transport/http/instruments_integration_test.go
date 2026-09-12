package httptransport

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const binanceCatalogRow = `{"symbol":"ABCUSDT","baseAsset":"ABC","quoteAsset":"USDT","status":"TRADING","contractType":"PERPETUAL","deliveryDate":4133404800000,"filters":[{"filterType":"LOT_SIZE","stepSize":"0.000001"},{"filterType":"PRICE_FILTER","tickSize":"0.000000000000000123"}]}`
const bybitCatalogRow = `{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","status":"Trading","contractType":"LinearPerpetual","fundingInterval":480,"deliveryTime":"0","lotSizeFilter":{"qtyStep":"0.000001"},"priceFilter":{"tickSize":"0.000000000000000123"}}`
const bybitSpotCatalogRow = `{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","status":"Trading","lotSizeFilter":{"basePrecision":"0.000001"},"priceFilter":{"tickSize":"0.000000000000000123"}}`

func TestInstrumentsEndToEnd(t *testing.T) {
	cases := []struct {
		name     string
		exchange domain.Exchange
		market   domain.Market
		failure  string
		want     error
	}{
		{"Binance spot decimal expansion", domain.ExchangeBinance, domain.MarketSpot, "decimal expansion", application.ErrInvalidUpstreamData},
		{"Binance linear decimal expansion", domain.ExchangeBinance, domain.MarketLinear, "decimal expansion", application.ErrInvalidUpstreamData},
		{"Bybit spot decimal expansion", domain.ExchangeBybit, domain.MarketSpot, "decimal expansion", application.ErrInvalidUpstreamData},
		{"Bybit linear decimal expansion", domain.ExchangeBybit, domain.MarketLinear, "decimal expansion", application.ErrInvalidUpstreamData},
		{"Binance spot invalid row", domain.ExchangeBinance, domain.MarketSpot, "normalization", application.ErrInvalidUpstreamData},
		{"Binance linear funding source failure", domain.ExchangeBinance, domain.MarketLinear, "funding source", application.ErrUpstream},
		{"Binance linear malformed funding", domain.ExchangeBinance, domain.MarketLinear, "funding parse", application.ErrInvalidUpstreamData},
		{"Bybit spot invalid row", domain.ExchangeBybit, domain.MarketSpot, "normalization", application.ErrInvalidUpstreamData},
		{"Bybit linear page failure", domain.ExchangeBybit, domain.MarketLinear, "page", application.ErrUpstream},
		{"Bybit linear PreLaunch failure", domain.ExchangeBybit, domain.MarketLinear, "prelaunch", application.ErrUpstream},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var mode atomic.Int32
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				failed := mode.Load() == 1
				empty := mode.Load() == 2
				body := ""
				if tt.exchange == domain.ExchangeBinance {
					if r.URL.Path == "/fapi/v1/fundingInfo" {
						if failed && tt.failure == "funding source" {
							w.WriteHeader(400)
							_, _ = io.WriteString(w, `{"code":-1,"msg":"private upstream error"}`)
							return
						}
						body = `[{"symbol":"ABCUSDT","fundingIntervalHours":4}]`
						if failed && tt.failure == "funding parse" {
							body = `[{"symbol":"ABCUSDT","fundingIntervalHours":0}]`
						}
					} else {
						row := binanceCatalogRow
						if failed && tt.failure == "normalization" {
							row += `,{"symbol":"BAD"}`
						}
						if empty {
							row = ""
						}
						body = `{"rateLimits":[],"symbols":[` + row + `]}`
					}
				} else {
					if failed && ((tt.failure == "page" && r.URL.Query().Get("cursor") != "") || (tt.failure == "prelaunch" && r.URL.Query().Get("status") == "PreLaunch")) {
						w.WriteHeader(400)
						_, _ = io.WriteString(w, `{"retCode":10001,"retMsg":"private upstream error"}`)
						return
					}
					row, cursor := bybitCatalogRow, ""
					if tt.market == domain.MarketSpot {
						row = bybitSpotCatalogRow
					}
					if failed && tt.failure == "normalization" {
						row += `,{"symbol":"BAD"}`
					}
					if failed && tt.failure == "page" {
						cursor = "next"
					}
					if empty || r.URL.Query().Get("status") == "PreLaunch" {
						row = ""
					}
					body = `{"retCode":0,"result":{"category":"` + string(tt.market) + `","list":[` + row + `],"nextPageCursor":"` + cursor + `"}}`
				}
				if failed && tt.failure == "decimal expansion" {
					body = strings.ReplaceAll(body, "0.000000000000000123", "1e-2147483648")
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			cfg := config.Defaults()
			controller, err := upstream.New(cfg, upstream.SystemClock{})
			require.NoError(t, err)
			transportScope := upstream.Bybit
			if tt.exchange == domain.ExchangeBinance {
				transportScope = upstream.BinanceSpot
				if tt.market == domain.MarketLinear {
					transportScope = upstream.BinanceLinear
				}
			}
			transport, err := upstream.NewTransport(controller, transportScope, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
			require.NoError(t, err)
			var provider instrument.Provider
			if tt.exchange == domain.ExchangeBinance {
				client, err := binance.NewClient(transportScope, server.URL, transport)
				require.NoError(t, err)
				if tt.market == domain.MarketSpot {
					provider = binance.NewSpotInstrumentProvider(client, nil)
				} else {
					provider = binance.NewLinearInstrumentProvider(client, nil)
				}
			} else {
				client, err := bybit.NewClient(server.URL, transport)
				require.NoError(t, err)
				if tt.market == domain.MarketSpot {
					provider = bybit.NewSpotInstrumentProvider(client, nil)
				} else {
					provider = bybit.NewLinearInstrumentProvider(client, nil)
				}
			}
			repo := memory.NewInstrumentRepository()
			scope := application.Scope{Exchange: tt.exchange, Market: tt.market}
			now := time.Date(2026, 9, 12, 12, 0, 0, 123, time.FixedZone("offset", 7200))
			refresh := instrument.NewRefresher(provider, repo, func() time.Time { return now }, nil)
			handler := instrumentHandler(instrument.NewReader(repo, []application.Scope{scope}), time.Second, 1)
			unready := httptest.NewRecorder()
			handler.ServeHTTP(unready, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))
			require.Equal(t, 503, unready.Code)
			require.Equal(t, int64(0), calls.Load())
			ctx, cancel, err := controller.Begin(t.Context(), transportScope, upstream.Instruments)
			require.NoError(t, err)
			defer cancel()

			require.NoError(t, refresh.Refresh(ctx))

			count := calls.Load()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments?symbol=ABCUSDT&status=trading", nil))
			require.Equal(t, 200, response.Code)
			var payload struct {
				Data []map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
			require.Len(t, payload.Data, 1)
			row := payload.Data[0]
			assert.Equal(t, "0.000000000000000123", row["price_tick"])
			assert.Equal(t, "0.000001", row["qty_step"])
			assert.Equal(t, "2026-09-12T10:00:00.000000123Z", row["updated_at"])
			for _, key := range []string{"min_qty", "max_qty", "min_notional", "delisting_time"} {
				assert.Contains(t, row, key)
				assert.Nil(t, row[key])
			}
			assert.Contains(t, row, "funding_interval")
			if tt.market == domain.MarketSpot {
				assert.Nil(t, row["funding_interval"])
			} else if tt.exchange == domain.ExchangeBinance {
				assert.Equal(t, float64(14400), row["funding_interval"])
			} else {
				assert.Equal(t, float64(28800), row["funding_interval"])
			}
			assert.Equal(t, count, calls.Load(), "reads must not fetch upstream")

			mode.Store(1)
			now = now.Add(time.Hour)
			failedCtx, stop, err := controller.Begin(t.Context(), transportScope, upstream.Instruments)
			require.NoError(t, err)
			defer stop()
			assert.ErrorIs(t, refresh.Refresh(failedCtx), tt.want)
			count = calls.Load()
			stale := httptest.NewRecorder()
			handler.ServeHTTP(stale, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))
			assert.Equal(t, 200, stale.Code)
			assert.JSONEq(t, response.Body.String(), stale.Body.String())
			assert.Equal(t, count, calls.Load())

			mode.Store(2)
			emptyCtx, stopEmpty, err := controller.Begin(t.Context(), transportScope, upstream.Instruments)
			require.NoError(t, err)
			defer stopEmpty()
			require.NoError(t, refresh.Refresh(emptyCtx))
			empty := httptest.NewRecorder()
			handler.ServeHTTP(empty, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))
			assert.Equal(t, 200, empty.Code)
			assert.JSONEq(t, `{"data":[]}`, empty.Body.String())
		})
	}
}
