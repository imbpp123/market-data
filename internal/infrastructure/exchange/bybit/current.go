package bybit

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

var _ ticker.Provider = (*currentProvider)(nil)
var _ marketstats.Provider = (*currentProvider)(nil)

type currentProvider struct {
	client      *Client
	instruments instrument.Repository
}

func NewCurrentProvider(client *Client, instruments instrument.Repository) *currentProvider {
	return &currentProvider{client: client, instruments: instruments}
}

func (*currentProvider) Exchange() domain.Exchange { return domain.ExchangeBybit }

func (*currentProvider) Capabilities() application.ExchangeCapabilities {
	return application.ExchangeCapabilities{MarketStatsWithTicker: true, MarketStatsWindows: []time.Duration{24 * time.Hour}}
}

func (*currentProvider) GetMarketStats(_ context.Context, _ domain.Market, window time.Duration) ([]domain.MarketStats, error) {
	if window != 24*time.Hour {
		return nil, application.ErrUnsupportedWindow
	}

	return nil, application.ErrUnsupportedOperation
}

func (p *currentProvider) GetTickers(ctx context.Context, market domain.Market) (ticker.Collection, error) {
	if !market.Valid() || p.client == nil {
		return ticker.Collection{}, application.ErrUnsupportedOperation
	}

	response, err := p.client.Fetch(ctx, "/v5/market/tickers", url.Values{"category": {string(market)}})
	if err != nil {
		return ticker.Collection{}, err
	}

	var envelope struct {
		Code   *int `json:"retCode"`
		Result struct {
			Category string               `json:"category"`
			List     *[]normalization.Row `json:"list"`
		} `json:"result"`
	}

	if json.Unmarshal(response.Body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || envelope.Result.Category != string(market) || envelope.Result.List == nil {
		return ticker.Collection{}, normalization.Invalid("ticker envelope")
	}

	scope := application.Scope{Exchange: domain.ExchangeBybit, Market: market}
	result := ticker.Collection{HasMarketStats: true, FetchedAt: response.FetchedAt.UTC()}
	contracts, err := normalization.Contracts(ctx, p.instruments, scope)
	result.TickerError = err
	if err == nil {
		result.Tickers, result.TickerError = normalizeTickers(ctx, *envelope.Result.List, market, contracts, result.FetchedAt)
	}

	result.MarketStats, result.MarketStatsError = normalization.Statistics(ctx, *envelope.Result.List, scope, result.FetchedAt)
	return result, nil
}

func normalizeTickers(ctx context.Context, sources []normalization.Row, market domain.Market, contracts map[string]domain.ContractType, fetchedAt time.Time) ([]domain.Ticker, error) {
	if _, err := normalization.Index(ctx, sources); err != nil {
		return nil, err
	}

	rows := make([]domain.Ticker, 0, len(sources))
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		symbol, err := source.Symbol()
		if err != nil {
			return nil, err
		}

		row := domain.Ticker{Exchange: domain.ExchangeBybit, Market: market, Symbol: symbol, FetchedAt: fetchedAt.UTC()}
		last, err := source.Decimal("lastPrice", true, false)
		if err != nil {
			return nil, err
		}

		row.LastPrice = *last
		row.BidPrice, row.BidSize, err = source.Quote("bid1Price", "bid1Size")
		if err != nil {
			return nil, err
		}

		row.AskPrice, row.AskSize, err = source.Quote("ask1Price", "ask1Size")
		if err != nil {
			return nil, err
		}

		row.FundingRate, row.NextFundingAt, err = source.Funding(market, contracts[symbol], "fundingRate")
		if err != nil {
			return nil, err
		}

		rows = append(rows, row)
	}

	return rows, nil
}
