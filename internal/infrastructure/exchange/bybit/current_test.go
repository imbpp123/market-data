package bybit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const currentRow = `{"symbol":"A","lastPrice":"105.250000000000000001","bid1Price":"100","bid1Size":"0.000000000000000001","ask1Price":"106","ask1Size":"2","fundingRate":"-0.0001","nextFundingTime":"1800000000123","highPrice24h":"110","lowPrice24h":"90","volume24h":"3","turnover24h":"305.1","prevPrice24h":"100","price24hPcnt":"999"}`

func TestBybitSharedNormalizationIsIndependent(t *testing.T) {
	cases := []struct {
		name, replace, with        string
		tickerBad, statsBad, outer bool
	}{
		{name: "both valid"},
		{name: "ticker quote invalid", replace: `"bid1Size":"0.000000000000000001"`, with: `"bid1Size":{}`, tickerBad: true},
		{name: "stats high invalid", replace: `"highPrice24h":"110"`, with: `"highPrice24h":{}`, statsBad: true},
		{name: "last invalid reference absent", replace: `"lastPrice":"105.250000000000000001"`, with: `"lastPrice":"bad"`, tickerBad: true},
		{name: "duplicate symbols", outer: false, tickerBad: true, statsBad: true},
		{name: "wrong category", outer: true},
		{name: "missing list", outer: true},
		{name: "envelope code", outer: true},
	}

	for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
		t.Run(string(market), func(t *testing.T) {
			for _, tt := range cases {
				t.Run(tt.name, func(t *testing.T) {
					row := strings.Replace(currentRow, tt.replace, tt.with, 1)
					if tt.name == "last invalid reference absent" {
						row = strings.Replace(row, `"prevPrice24h":"100"`, `"prevPrice24h":""`, 1)
					}

					rows := row
					if tt.name == "duplicate symbols" {
						rows += "," + row
					}

					body := `{"retCode":0,"result":{"category":"` + string(market) + `","list":[` + rows + `]}}`
					if tt.name == "wrong category" {
						body = strings.Replace(body, string(market), "inverse", 1)
					}

					if tt.name == "missing list" {
						body = `{"retCode":0,"result":{"category":"` + string(market) + `"}}`
					}

					if tt.name == "envelope code" {
						body = strings.Replace(body, `"retCode":0`, `"retCode":10001`, 1)
					}

					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						assert.Equal(t, "/v5/market/tickers", r.URL.Path)
						assert.Equal(t, "category="+string(market), r.URL.RawQuery)
						_, _ = io.WriteString(w, body)
					}))
					defer server.Close()
					cfg := config.Defaults()
					controller, err := upstream.New(cfg, upstream.SystemClock{})
					require.NoError(t, err)
					transport, err := upstream.NewTransport(controller, upstream.Bybit, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
					require.NoError(t, err)
					client, err := NewClient(server.URL, transport)
					require.NoError(t, err)
					provider := NewCurrentProvider(client, memory.NewInstrumentRepository())
					ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Tickers)
					require.NoError(t, err)
					defer cancel()

					result, err := provider.GetTickers(ctx, market)

					assert.Equal(t, 1, controller.Attempts(ctx), "shared response must use one HTTP attempt")
					if tt.outer {
						require.Error(t, err)
						assert.Empty(t, result.Tickers)
						assert.Empty(t, result.MarketStats)
						return
					}

					require.NoError(t, err)
					assert.True(t, result.HasMarketStats)
					assert.Equal(t, tt.tickerBad, result.TickerError != nil)
					assert.Equal(t, tt.statsBad, result.MarketStatsError != nil)
					if !tt.tickerBad {
						require.Len(t, result.Tickers, 1)
						ticker := result.Tickers[0]
						assert.Equal(t, "105.250000000000000001", ticker.LastPrice.String())
						require.NotNil(t, ticker.BidPrice)
						require.NotNil(t, ticker.BidSize)
						require.NotNil(t, ticker.AskPrice)
						require.NotNil(t, ticker.AskSize)
						assert.Equal(t, "100", ticker.BidPrice.String())
						assert.Equal(t, "0.000000000000000001", ticker.BidSize.String())
						assert.Equal(t, "106", ticker.AskPrice.String())
						assert.Equal(t, "2", ticker.AskSize.String())
						assert.Equal(t, result.FetchedAt, ticker.FetchedAt)
						if market == domain.MarketLinear {
							require.NotNil(t, ticker.FundingRate)
							assert.Equal(t, "-0.0001", ticker.FundingRate.String())
							require.NotNil(t, ticker.NextFundingAt)
							assert.Equal(t, time.UnixMilli(1800000000123).UTC(), *ticker.NextFundingAt)
						} else {
							assert.Nil(t, ticker.FundingRate)
							assert.Nil(t, ticker.NextFundingAt)
						}
					}

					if !tt.statsBad {
						require.Len(t, result.MarketStats, 1)
						stats := result.MarketStats[0]
						assert.Equal(t, "110", stats.High.String())
						assert.Equal(t, "90", stats.Low.String())
						assert.Equal(t, "3", stats.Volume.String())
						assert.Equal(t, "305.1", stats.Turnover.String())
						assert.Nil(t, stats.TradeCount)
						if tt.name == "last invalid reference absent" {
							assert.Nil(t, stats.PriceChange)
						} else {
							require.NotNil(t, stats.PriceChange)
							assert.Equal(t, "5.250000000000000001", stats.PriceChange.String())
						}

						assert.Equal(t, result.FetchedAt, stats.FetchedAt)
					}
				})
			}
		})
	}
}

func TestBybitHasNoStatisticsFallback(t *testing.T) {
	provider := NewCurrentProvider(nil, nil)

	_, err := provider.GetMarketStats(t.Context(), domain.MarketSpot, 24*time.Hour)
	assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
	_, err = provider.GetMarketStats(t.Context(), domain.MarketSpot, 2*time.Hour)
	assert.ErrorIs(t, err, application.ErrUnsupportedWindow)
	_, err = provider.GetTickers(t.Context(), domain.Market("inverse"))
	assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
}

func TestLinearInstrumentKeepsFundingContractType(t *testing.T) {
	cases := []struct {
		source string
		want   domain.ContractType
	}{{"LinearPerpetual", domain.ContractTypePerpetual}, {"LinearFutures", domain.ContractTypeExpiry}, {"new", domain.ContractTypeUnknown}}
	for _, tt := range cases {
		t.Run(tt.source, func(t *testing.T) {
			var source instrumentRow
			require.NoError(t, json.Unmarshal([]byte(linearInstrument), &source))
			source.ContractType = tt.source

			row, err := normalizeLinearInstrument(source)

			require.NoError(t, err)
			assert.Equal(t, tt.want, row.ContractType)
		})
	}
}
