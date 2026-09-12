package bybit

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

var _ instrument.Provider = (*linearInstrumentProvider)(nil)

type linearInstrumentProvider struct {
	client *Client
	logger *slog.Logger
}

func NewLinearInstrumentProvider(client *Client, logger *slog.Logger) *linearInstrumentProvider {
	return &linearInstrumentProvider{client: client, logger: logger}
}

func (*linearInstrumentProvider) Scope() application.Scope {
	return application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
}

func (p *linearInstrumentProvider) GetInstruments(ctx context.Context) ([]domain.Instrument, error) {
	if p.client == nil {
		return nil, application.ErrUnsupportedOperation
	}

	rows := make([]domain.Instrument, 0)
	seen := make(map[string]instrumentRow)
	for _, status := range []string{"", "PreLaunch"} {
		cursor := ""
		cursors := make(map[string]bool)
		collection := make(map[string]bool)
		for {
			parameters := url.Values{"category": {"linear"}, "limit": {"1000"}}
			if status != "" {
				parameters.Set("status", status)
			}

			if cursor != "" {
				parameters.Set("cursor", cursor)
			}

			page, err := fetchInstrumentPage(ctx, p.client, parameters)
			if err != nil {
				return nil, err
			}

			progress := 0
			for _, source := range *page.List {
				if err := ctx.Err(); err != nil {
					return nil, err
				}

				row, err := normalizeLinearInstrument(source)
				if err != nil {
					return nil, err
				}

				if !collection[row.Symbol] {
					progress++
					collection[row.Symbol] = true
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

			next := page.Cursor
			if (cursor != "" || next != "") && progress == 0 {
				return nil, normalization.Invalid("pagination progress")
			}

			if next == "" {
				break
			}

			if cursors[next] {
				return nil, normalization.Invalid("pagination cursor")
			}

			cursors[next] = true
			cursor = next
		}
	}

	return rows, nil
}

func normalizeLinearInstrument(source instrumentRow) (domain.Instrument, error) {
	row, err := normalizeInstrumentPrice(source, domain.MarketLinear)
	if err != nil {
		return row, err
	}

	if source.ContractType == "InversePerpetual" || source.ContractType == "InverseFutures" {
		return row, normalization.Invalid("contract category")
	}

	row.QtyStep, err = normalization.Step(source.LotSizeFilter.QtyStep)
	if err != nil {
		return row, err
	}

	row.MinQty, err = normalization.Limit(source.LotSizeFilter.MinOrderQty)
	if err != nil {
		return row, err
	}

	row.MaxQty, err = normalization.Limit(source.LotSizeFilter.MaxOrderQty)
	if err != nil {
		return row, err
	}

	row.MinNotional, err = normalization.Limit(source.LotSizeFilter.MinNotionalValue)
	if err != nil {
		return row, err
	}

	if source.ContractType == "LinearPerpetual" {
		row.FundingInterval, err = normalization.Duration(source.FundingInterval.String(), time.Minute)
		if err != nil {
			return row, err
		}

		row.DelistingTime, err = normalization.Delisting(source.DeliveryTime)
		if err != nil {
			return row, err
		}
	}

	switch source.ContractType {
	case "LinearPerpetual":
		row.ContractType = domain.ContractTypePerpetual
	case "LinearFutures":
		row.ContractType = domain.ContractTypeExpiry
	}

	return row, normalization.Validate(row)
}
