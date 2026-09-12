package binance

import (
	"context"
	"log/slog"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

var _ instrument.Provider = (*spotInstrumentProvider)(nil)

type spotInstrumentProvider struct {
	client Client
	logger *slog.Logger
}

func NewSpotInstrumentProvider(client Client, logger *slog.Logger) *spotInstrumentProvider {
	return &spotInstrumentProvider{client: client, logger: logger}
}

func (*spotInstrumentProvider) Scope() application.Scope {
	return application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
}

func (p *spotInstrumentProvider) GetInstruments(ctx context.Context) ([]domain.Instrument, error) {
	sources, err := fetchInstruments(ctx, p.client, "/api/v3/exchangeInfo")
	if err != nil {
		return nil, err
	}

	return normalizeCatalog(ctx, sources, normalizeSpotInstrument, p.logger)
}

func normalizeSpotInstrument(source instrumentRow) (domain.Instrument, error) {
	row := domain.Instrument{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot, Symbol: source.Symbol, BaseAsset: source.BaseAsset, QuoteAsset: source.QuoteAsset, Status: spotInstrumentStatus(source.Status)}
	filters, err := selectInstrumentFilters(source)
	if err != nil {
		return row, err
	}

	row, err = normalizeOrderLimits(row, filters)
	if err != nil {
		return row, err
	}

	row.MinNotional, err = normalization.Limit(filters["MIN_NOTIONAL"].MinNotional)
	if err != nil {
		return row, err
	}

	other, err := normalization.Limit(filters["NOTIONAL"].MinNotional)
	if err != nil {
		return row, err
	}

	if other != nil && (row.MinNotional == nil || other.GreaterThan(*row.MinNotional)) {
		row.MinNotional = other
	}

	return row, nil
}

func spotInstrumentStatus(status string) domain.InstrumentStatus {
	switch status {
	case "TRADING":
		return domain.InstrumentStatusTrading
	case "END_OF_DAY", "HALT", "BREAK":
		return domain.InstrumentStatusHalted
	case "CANCEL_ONLY":
		return domain.InstrumentStatusCancelOnly
	default:
		return domain.InstrumentStatusUnknown
	}
}
