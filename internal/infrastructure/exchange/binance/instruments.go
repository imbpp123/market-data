package binance

import (
	"context"
	"encoding/json"
	"log/slog"

	"market-data/internal/application"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

type instrumentRow struct {
	Symbol       string             `json:"symbol"`
	BaseAsset    string             `json:"baseAsset"`
	QuoteAsset   string             `json:"quoteAsset"`
	Status       string             `json:"status"`
	ContractType string             `json:"contractType"`
	Filters      []instrumentFilter `json:"filters"`
}

type instrumentFilter struct {
	Type        string `json:"filterType"`
	TickSize    string `json:"tickSize"`
	StepSize    string `json:"stepSize"`
	MinQty      string `json:"minQty"`
	MaxQty      string `json:"maxQty"`
	MinNotional string `json:"minNotional"`
	Notional    string `json:"notional"`
}

func fetchInstruments(ctx context.Context, client Client, path string) ([]instrumentRow, error) {
	if client == nil {
		return nil, application.ErrUnsupportedOperation
	}

	response, err := client.Fetch(ctx, path, nil)
	if err != nil {
		return nil, err
	}

	var catalog struct {
		Symbols *[]instrumentRow `json:"symbols"`
	}

	if json.Unmarshal(response.Body, &catalog) != nil || catalog.Symbols == nil {
		return nil, normalization.Invalid("symbols")
	}

	return *catalog.Symbols, nil
}

func normalizeCatalog(ctx context.Context, sources []instrumentRow, normalize func(instrumentRow) (domain.Instrument, error), logger *slog.Logger) ([]domain.Instrument, error) {
	rows := make([]domain.Instrument, 0, len(sources))
	seen := make(map[string]bool)
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		row, err := normalize(source)
		if err != nil {
			return nil, err
		}

		if seen[row.Symbol] {
			return nil, normalization.Invalid("duplicate symbol")
		}

		seen[row.Symbol] = true
		if row.Status == domain.InstrumentStatusUnknown && logger != nil {
			logger.Warn("Unknown instrument status", "exchange", row.Exchange, "market", row.Market, "symbol", row.Symbol, "status", source.Status)
		}

		rows = append(rows, row)
	}

	return rows, nil
}

func selectInstrumentFilters(source instrumentRow) (map[string]instrumentFilter, error) {
	filters := make(map[string]instrumentFilter)
	for _, filter := range source.Filters {
		switch filter.Type {
		case "PRICE_FILTER", "LOT_SIZE", "MIN_NOTIONAL", "NOTIONAL":
			if _, ok := filters[filter.Type]; ok {
				return nil, normalization.Invalid("duplicate filter")
			}

			filters[filter.Type] = filter
		}
	}

	return filters, nil
}

func normalizeOrderLimits(row domain.Instrument, filters map[string]instrumentFilter) (domain.Instrument, error) {
	var err error
	row.PriceTick, err = normalization.Step(filters["PRICE_FILTER"].TickSize)
	if err != nil {
		return row, err
	}

	row.QtyStep, err = normalization.Step(filters["LOT_SIZE"].StepSize)
	if err != nil {
		return row, err
	}

	row.MinQty, err = normalization.Limit(filters["LOT_SIZE"].MinQty)
	if err != nil {
		return row, err
	}

	row.MaxQty, err = normalization.Limit(filters["LOT_SIZE"].MaxQty)
	if err != nil {
		return row, err
	}

	return row, normalization.Validate(row)
}
