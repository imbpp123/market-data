package bybit

import (
	"context"
	"log/slog"
	"net/url"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

var _ instrument.Provider = (*spotInstrumentProvider)(nil)

type spotInstrumentProvider struct {
	client *Client
	logger *slog.Logger
}

func NewSpotInstrumentProvider(client *Client, logger *slog.Logger) *spotInstrumentProvider {
	return &spotInstrumentProvider{client: client, logger: logger}
}

func (*spotInstrumentProvider) Scope() application.Scope {
	return application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
}

func (p *spotInstrumentProvider) GetInstruments(ctx context.Context) ([]domain.Instrument, error) {
	if p.client == nil {
		return nil, application.ErrUnsupportedOperation
	}

	page, err := fetchInstrumentPage(ctx, p.client, url.Values{"category": {"spot"}})
	if err != nil {
		return nil, err
	}

	if page.Cursor != "" {
		return nil, normalization.Invalid("pagination cursor")
	}

	rows := make([]domain.Instrument, 0, len(*page.List))
	seen := make(map[string]instrumentRow)
	for _, source := range *page.List {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		row, err := normalizeSpotInstrument(source)
		if err != nil {
			return nil, err
		}

		if previous, ok := seen[row.Symbol]; ok {
			if previous != source {
				return nil, normalization.Invalid("conflicting duplicate symbol")
			}

			continue
		}

		seen[row.Symbol] = source
		logUnknownStatus(p.logger, row, source.Status)
		rows = append(rows, row)
	}

	return rows, nil
}

func normalizeSpotInstrument(source instrumentRow) (domain.Instrument, error) {
	row, err := normalizeInstrumentPrice(source, domain.MarketSpot)
	if err != nil {
		return row, err
	}

	if source.ContractType != "" {
		return row, normalization.Invalid("contract category")
	}

	row.QtyStep, err = normalization.Step(source.LotSizeFilter.BasePrecision)
	if err != nil {
		return row, err
	}

	row.MaxQty, err = normalization.Limit(source.LotSizeFilter.MaxLimitOrderQty)
	if err != nil {
		return row, err
	}

	row.MinNotional, err = normalization.Limit(source.LotSizeFilter.MinOrderAmt)
	if err != nil {
		return row, err
	}

	return row, normalization.Validate(row)
}
