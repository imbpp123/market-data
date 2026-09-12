package binance

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type currentClient struct {
	bodies   map[string]string
	failures map[string]error
	at       map[string]time.Time
	paths    []string
	queries  []url.Values
}

func (c *currentClient) Fetch(ctx context.Context, path string, query url.Values) (upstream.Response, error) {
	if err := ctx.Err(); err != nil {
		return upstream.Response{}, err
	}

	c.paths = append(c.paths, path)
	c.queries = append(c.queries, query)
	return upstream.Response{Body: []byte(c.bodies[path]), FetchedAt: c.at[path]}, c.failures[path]
}

func TestTickerJoinUsesOnlyCurrentPriceSymbols(t *testing.T) {
	for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
		t.Run(string(market), func(t *testing.T) {
			pricePath, bookPath := "/api/v3/ticker/price", "/api/v3/ticker/bookTicker"
			if market == domain.MarketLinear {
				pricePath, bookPath = "/fapi/v2/ticker/price", "/fapi/v1/ticker/bookTicker"
			}

			at := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
			client := &currentClient{bodies: map[string]string{
				pricePath:               `[{"symbol":"B","price":"0"},{"symbol":"A","price":12345678901234567890.123456789},{"symbol":"C","price":"1"}]`,
				bookPath:                `[{"symbol":"EXTRA","bidPrice":"1","bidQty":"1"},{"symbol":"A","bidPrice":"20","bidQty":"0.000000000000000001","askPrice":"10","askQty":"2","lastQty":"999"}]`,
				"/fapi/v1/premiumIndex": `[{"symbol":"FUNDING_ONLY","lastFundingRate":"1","nextFundingTime":1800000000000},{"symbol":"A","lastFundingRate":"-0.0001","nextFundingTime":1800000000123}]`,
			}, at: map[string]time.Time{pricePath: at.Add(2 * time.Second), bookPath: at, "/fapi/v1/premiumIndex": at.Add(time.Second)}}
			provider := NewCurrentProvider(client, client, nil, nil)

			result, err := provider.GetTickers(t.Context(), market)

			require.NoError(t, err)
			require.NoError(t, result.TickerError)
			assert.False(t, result.HasMarketStats)
			require.Len(t, result.Tickers, 3)
			assert.Equal(t, "B", result.Tickers[0].Symbol)
			assert.Equal(t, "0", result.Tickers[0].LastPrice.String())
			assert.Nil(t, result.Tickers[0].BidPrice)
			assert.Nil(t, result.Tickers[0].FundingRate)
			row := result.Tickers[1]
			assert.Equal(t, "A", row.Symbol)
			assert.Equal(t, "12345678901234567890.123456789", row.LastPrice.String())
			require.NotNil(t, row.BidPrice)
			require.NotNil(t, row.BidSize)
			require.NotNil(t, row.AskPrice)
			require.NotNil(t, row.AskSize)
			assert.Equal(t, "20", row.BidPrice.String())
			assert.Equal(t, "0.000000000000000001", row.BidSize.String())
			assert.Equal(t, "10", row.AskPrice.String())
			assert.Equal(t, "2", row.AskSize.String())
			for _, row := range result.Tickers {
				assert.Equal(t, at.Add(2*time.Second), row.FetchedAt)
			}

			wantPaths := []string{pricePath, bookPath}
			if market == domain.MarketLinear {
				wantPaths = append(wantPaths, "/fapi/v1/premiumIndex")
				require.NotNil(t, row.FundingRate)
				assert.Equal(t, "-0.0001", row.FundingRate.String())
				require.NotNil(t, row.NextFundingAt)
				assert.Equal(t, time.UnixMilli(1800000000123).UTC(), *row.NextFundingAt)
			} else {
				assert.Nil(t, row.FundingRate)
				assert.Nil(t, row.NextFundingAt)
			}

			assert.Equal(t, wantPaths, client.paths)
			for _, query := range client.queries {
				assert.Empty(t, query)
			}
		})
	}
}

func TestTickerFailureKeepsCompletePreviousSnapshot(t *testing.T) {
	cases := []struct {
		name, path, body string
		failure          error
	}{
		{"price HTTP", "/fapi/v2/ticker/price", "", application.ErrUpstream},
		{"book HTTP", "/fapi/v1/ticker/bookTicker", "", application.ErrUpstream},
		{"funding HTTP", "/fapi/v1/premiumIndex", "", application.ErrUpstream},
		{"price duplicate", "/fapi/v2/ticker/price", `[{"symbol":"A","price":"1"},{"symbol":"A","price":"2"}]`, nil},
		{"book duplicate", "/fapi/v1/ticker/bookTicker", `[{"symbol":"EXTRA"},{"symbol":"EXTRA"}]`, nil},
		{"funding duplicate", "/fapi/v1/premiumIndex", `[{"symbol":"EXTRA"},{"symbol":"EXTRA"}]`, nil},
		{"required last missing", "/fapi/v2/ticker/price", `[{"symbol":"A"}]`, nil},
		{"negative last", "/fapi/v2/ticker/price", `[{"symbol":"A","price":"-1"}]`, nil},
		{"bad quote", "/fapi/v1/ticker/bookTicker", `[{"symbol":"A","bidQty":"1"}]`, nil},
		{"bad funding", "/fapi/v1/premiumIndex", `[{"symbol":"A","nextFundingTime":-1}]`, nil},
		{"null list", "/fapi/v1/ticker/bookTicker", `null`, nil},
		{"object instead of bulk", "/fapi/v1/ticker/bookTicker", `{}`, nil},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}
			at := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
			repo := memory.NewTickerRepository()
			old := []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", LastPrice: decimal.NewFromInt(7), FetchedAt: at}}
			require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, old))
			client := &currentClient{bodies: map[string]string{"/fapi/v2/ticker/price": `[{"symbol":"A","price":"1"}]`, "/fapi/v1/ticker/bookTicker": `[]`, "/fapi/v1/premiumIndex": `[]`}, failures: map[string]error{tt.path: tt.failure}}
			client.bodies[tt.path] = tt.body
			refresher := ticker.NewRefresher(NewCurrentProvider(nil, client, nil, nil), scope, repo, nil, nil)

			err := refresher.Refresh(t.Context())

			want := tt.failure
			if want == nil {
				want = application.ErrInvalidUpstreamData
			}

			assert.ErrorIs(t, err, want)
			rows, err := repo.List(t.Context(), application.SnapshotFilter{Scopes: []application.Scope{scope}})
			require.NoError(t, err)
			assert.Equal(t, old, rows)
		})
	}
}

func TestBinanceStatisticsUseSeparateFullBulkEndpoint(t *testing.T) {
	for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
		t.Run(string(market), func(t *testing.T) {
			path := "/api/v3/ticker/24hr"
			if market == domain.MarketLinear {
				path = "/fapi/v1/ticker/24hr"
			}

			at := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
			client := &currentClient{bodies: map[string]string{path: `[{"symbol":"A","highPrice":"210.000000000000001","lowPrice":"190","volume":"5","quoteVolume":"1001","priceChange":"5","priceChangePercent":"2.5","count":9007199254740993}]`}, at: map[string]time.Time{path: at}}
			provider := NewCurrentProvider(client, client, nil, nil)

			rows, err := provider.GetMarketStats(t.Context(), market, 24*time.Hour)

			require.NoError(t, err)
			require.Len(t, rows, 1)
			row := rows[0]
			assert.Equal(t, domain.ExchangeBinance, row.Exchange)
			assert.Equal(t, market, row.Market)
			assert.Equal(t, "A", row.Symbol)
			assert.Equal(t, 24*time.Hour, row.Window)
			assert.Equal(t, "210.000000000000001", row.High.String())
			assert.Equal(t, "190", row.Low.String())
			assert.Equal(t, "5", row.Volume.String())
			assert.Equal(t, "1001", row.Turnover.String())
			require.NotNil(t, row.PriceChange)
			assert.Equal(t, "5", row.PriceChange.String())
			require.NotNil(t, row.TradeCount)
			assert.Equal(t, int64(9007199254740993), *row.TradeCount)
			assert.Equal(t, at, row.FetchedAt)
			assert.Equal(t, []string{path}, client.paths)
			if market == domain.MarketSpot {
				assert.Equal(t, url.Values{"type": {"FULL"}}, client.queries[0])
			} else {
				assert.Empty(t, client.queries[0])
			}
		})
	}
}

func TestBinanceRejectsUnsupportedCollectionBeforeIO(t *testing.T) {
	client := &currentClient{}
	provider := NewCurrentProvider(client, nil, nil, nil)

	_, err := provider.GetMarketStats(t.Context(), domain.MarketSpot, 2*time.Hour)
	assert.ErrorIs(t, err, application.ErrUnsupportedWindow)
	_, err = provider.GetTickers(t.Context(), domain.MarketLinear)
	assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
	_, err = provider.GetMarketStats(t.Context(), domain.Market("inverse"), 24*time.Hour)
	assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
	assert.Empty(t, client.paths)
}

func TestLinearInstrumentKeepsFundingContractType(t *testing.T) {
	cases := []struct {
		source string
		want   domain.ContractType
	}{{"PERPETUAL", domain.ContractTypePerpetual}, {"CURRENT_QUARTER", domain.ContractTypeExpiry}, {"NEXT_QUARTER", domain.ContractTypeExpiry}, {"new", domain.ContractTypeUnknown}}
	for _, tt := range cases {
		t.Run(tt.source, func(t *testing.T) {
			var source instrumentRow
			require.NoError(t, json.Unmarshal([]byte(spotInstrument), &source))
			source.ContractType = tt.source

			row, err := normalizeLinearInstrument(source)

			require.NoError(t, err)
			assert.Equal(t, tt.want, row.ContractType)
		})
	}
}
