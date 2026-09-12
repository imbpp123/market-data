package binance

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
	"market-data/internal/infrastructure/exchange/upstream"
)

var _ ticker.Provider = (*currentProvider)(nil)
var _ marketstats.Provider = (*currentProvider)(nil)

type currentProvider struct {
	clients     map[domain.Market]Client
	instruments instrument.Repository
	logger      *slog.Logger
}

func NewCurrentProvider(spot, linear Client, instruments instrument.Repository, logger *slog.Logger) *currentProvider {
	return &currentProvider{clients: map[domain.Market]Client{domain.MarketSpot: spot, domain.MarketLinear: linear}, instruments: instruments, logger: logger}
}

func (*currentProvider) Exchange() domain.Exchange { return domain.ExchangeBinance }

func (*currentProvider) Capabilities() application.ExchangeCapabilities {
	return application.ExchangeCapabilities{MarketStatsWindows: []time.Duration{24 * time.Hour}}
}

func fetchCurrent(ctx context.Context, client Client, path string, parameters url.Values) ([]normalization.Row, upstream.Response, error) {
	response, err := client.Fetch(ctx, path, parameters)
	if err != nil {
		return nil, response, err
	}

	var rows *[]normalization.Row
	if json.Unmarshal(response.Body, &rows) != nil || rows == nil {
		return nil, response, normalization.Invalid("current data envelope")
	}

	return *rows, response, nil
}

func (p *currentProvider) GetMarketStats(ctx context.Context, market domain.Market, window time.Duration) ([]domain.MarketStats, error) {
	if window != 24*time.Hour {
		return nil, application.ErrUnsupportedWindow
	}

	client := p.clients[market]
	if client == nil {
		return nil, application.ErrUnsupportedOperation
	}

	path := "/api/v3/ticker/24hr"
	parameters := url.Values{"type": {"FULL"}}
	if market == domain.MarketLinear {
		path, parameters = "/fapi/v1/ticker/24hr", nil
	}

	rows, response, err := fetchCurrent(ctx, client, path, parameters)
	if err != nil {
		return nil, err
	}

	return normalization.Statistics(ctx, rows, application.Scope{Exchange: domain.ExchangeBinance, Market: market}, response.FetchedAt)
}

func (p *currentProvider) GetTickers(ctx context.Context, market domain.Market) (ticker.Collection, error) {
	client := p.clients[market]
	if client == nil {
		return ticker.Collection{}, application.ErrUnsupportedOperation
	}

	paths := []string{"/api/v3/ticker/price", "/api/v3/ticker/bookTicker"}
	if market == domain.MarketLinear {
		paths = []string{"/fapi/v2/ticker/price", "/fapi/v1/ticker/bookTicker", "/fapi/v1/premiumIndex"}
	}

	sources := make([][]normalization.Row, 3)
	indexes := make([]map[string]normalization.Row, 3)
	var fetchedAt time.Time
	for i, path := range paths {
		rows, response, err := fetchCurrent(ctx, client, path, nil)
		if err != nil {
			return ticker.Collection{}, err
		}

		index, err := normalization.Index(ctx, rows)
		if err != nil {
			return ticker.Collection{}, err
		}

		sources[i], indexes[i] = rows, index
		if response.FetchedAt.After(fetchedAt) {
			fetchedAt = response.FetchedAt.UTC()
		}
	}

	if p.logger != nil {
		for i := 1; i < len(paths); i++ {
			missing, extra := 0, 0
			for symbol := range indexes[0] {
				if _, ok := indexes[i][symbol]; !ok {
					missing++
				}
			}

			for symbol := range indexes[i] {
				if _, ok := indexes[0][symbol]; !ok {
					extra++
				}
			}

			if missing+extra > 0 {
				p.logger.Warn("Ticker source symbol sets differ", "market", market, "path", paths[i], "missing", missing, "extra", extra)
			}
		}
	}

	contracts, err := normalization.Contracts(ctx, p.instruments, application.Scope{Exchange: domain.ExchangeBinance, Market: market})
	if err != nil {
		return ticker.Collection{}, err
	}

	rows, err := normalizeTickers(ctx, sources[0], indexes[1], indexes[2], market, contracts, fetchedAt)
	return ticker.Collection{Tickers: rows, TickerError: err, FetchedAt: fetchedAt}, nil
}

func normalizeTickers(ctx context.Context, prices []normalization.Row, books, funding map[string]normalization.Row, market domain.Market, contracts map[string]domain.ContractType, fetchedAt time.Time) ([]domain.Ticker, error) {
	rows := make([]domain.Ticker, 0, len(prices))
	for _, price := range prices {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		symbol, err := price.Symbol()
		if err != nil {
			return nil, err
		}

		row := domain.Ticker{Exchange: domain.ExchangeBinance, Market: market, Symbol: symbol, FetchedAt: fetchedAt.UTC()}
		last, err := price.Decimal("price", true, false)
		if err != nil {
			return nil, err
		}

		row.LastPrice = *last
		row.BidPrice, row.BidSize, err = books[symbol].Quote("bidPrice", "bidQty")
		if err != nil {
			return nil, err
		}

		row.AskPrice, row.AskSize, err = books[symbol].Quote("askPrice", "askQty")
		if err != nil {
			return nil, err
		}

		row.FundingRate, row.NextFundingAt, err = funding[symbol].Funding(market, contracts[symbol], "lastFundingRate")
		if err != nil {
			return nil, err
		}

		rows = append(rows, row)
	}

	return rows, nil
}
