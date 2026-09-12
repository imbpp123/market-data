package binance

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

var _ instrument.Provider = (*linearInstrumentProvider)(nil)

type linearInstrumentProvider struct {
	client Client
	logger *slog.Logger
}

func NewLinearInstrumentProvider(client Client, logger *slog.Logger) *linearInstrumentProvider {
	return &linearInstrumentProvider{client: client, logger: logger}
}

func (*linearInstrumentProvider) Scope() application.Scope {
	return application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}
}

func (p *linearInstrumentProvider) GetInstruments(ctx context.Context) ([]domain.Instrument, error) {
	sources, err := fetchInstruments(ctx, p.client, "/fapi/v1/exchangeInfo")
	if err != nil {
		return nil, err
	}

	response, err := p.client.Fetch(ctx, "/fapi/v1/fundingInfo", nil)
	if err != nil {
		return nil, err
	}

	funding, err := parseFunding(response.Body)
	if err != nil {
		return nil, err
	}

	rows, err := normalizeCatalog(ctx, sources, normalizeLinearInstrument, p.logger)
	if err != nil {
		return nil, err
	}

	for i, source := range sources {
		if source.ContractType == "PERPETUAL" {
			rows[i].FundingInterval = funding[source.Symbol]
		}
	}

	return rows, nil
}

func parseFunding(body []byte) (map[string]*time.Duration, error) {
	var source *[]struct {
		Symbol string `json:"symbol"`
		Hours  *int64 `json:"fundingIntervalHours"`
	}

	if json.Unmarshal(body, &source) != nil || source == nil {
		return nil, normalization.Invalid("funding metadata")
	}

	result := make(map[string]*time.Duration)
	for _, row := range *source {
		if row.Symbol == "" || row.Hours == nil || *row.Hours <= 0 || *row.Hours > int64((1<<63-1)/time.Hour) {
			return nil, normalization.Invalid("funding metadata")
		}

		if _, ok := result[row.Symbol]; ok {
			return nil, normalization.Invalid("duplicate funding symbol")
		}

		interval := time.Duration(*row.Hours) * time.Hour
		result[row.Symbol] = &interval
	}

	return result, nil
}

func normalizeLinearInstrument(source instrumentRow) (domain.Instrument, error) {
	row := domain.Instrument{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear, Symbol: source.Symbol, BaseAsset: source.BaseAsset, QuoteAsset: source.QuoteAsset, Status: linearInstrumentStatus(source.Status)}
	filters, err := selectInstrumentFilters(source)
	if err != nil {
		return row, err
	}

	row, err = normalizeOrderLimits(row, filters)
	if err != nil {
		return row, err
	}

	row.MinNotional, err = normalization.Limit(filters["MIN_NOTIONAL"].Notional)
	if err != nil {
		return row, err
	}

	return row, nil
}

func linearInstrumentStatus(status string) domain.InstrumentStatus {
	switch status {
	case "PENDING_TRADING":
		return domain.InstrumentStatusPreLaunch
	case "TRADING":
		return domain.InstrumentStatusTrading
	case "PRE_DELIVERING", "DELIVERING", "PRE_SETTLE", "SETTLING":
		return domain.InstrumentStatusSettling
	case "DELIVERED", "CLOSE":
		return domain.InstrumentStatusClosed
	case "TRADING_HALT":
		return domain.InstrumentStatusHalted
	case "TRADING_CANCEL_ONLY":
		return domain.InstrumentStatusCancelOnly
	default:
		return domain.InstrumentStatusUnknown
	}
}
