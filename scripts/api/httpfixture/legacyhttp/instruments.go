package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
)

type InstrumentReader interface {
	Validate(instrument.Query) error
	List(context.Context, instrument.Query) ([]domain.Instrument, error)
}

type instrumentDTO struct {
	Exchange        domain.Exchange         `json:"exchange"`
	Market          domain.Market           `json:"market"`
	Symbol          string                  `json:"symbol"`
	BaseAsset       string                  `json:"base_asset"`
	QuoteAsset      string                  `json:"quote_asset"`
	Status          domain.InstrumentStatus `json:"status"`
	PriceTick       string                  `json:"price_tick"`
	QtyStep         string                  `json:"qty_step"`
	MinQty          *string                 `json:"min_qty"`
	MaxQty          *string                 `json:"max_qty"`
	MinNotional     *string                 `json:"min_notional"`
	FundingInterval *int64                  `json:"funding_interval"`
	DelistingTime   *time.Time              `json:"delisting_time"`
	UpdatedAt       time.Time               `json:"updated_at"`
}

func NewInstrumentsHandler(reader InstrumentReader, timeout time.Duration, maximum int) http.Handler {
	return newInstrumentsHandler(reader, timeout, make(chan struct{}, maximum))
}

func newInstrumentsHandler(reader InstrumentReader, timeout time.Duration, slots chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parameters, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeApplicationError(w, application.ErrInvalidParameter)
			return
		}

		for key := range parameters {
			if key != "exchange" && key != "market" && key != "symbol" && key != "status" {
				writeApplicationError(w, application.ErrInvalidParameter)
				return
			}
		}

		for _, key := range []string{"exchange", "market", "symbol", "status"} {
			values, exists := parameters[key]
			if exists && (len(values) != 1 || values[0] == "") {
				failure := application.ErrInvalidParameter
				if key == "status" {
					failure = application.ErrInvalidStatus
				}

				writeApplicationError(w, failure)
				return
			}
		}

		query := instrument.Query{
			Exchange: parameters.Get("exchange"),
			Market:   parameters.Get("market"),
			Symbol:   parameters.Get("symbol"),
			Status:   parameters.Get("status"),
		}
		if err := reader.Validate(query); err != nil {
			writeApplicationError(w, err)
			return
		}

		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			writeApplicationError(w, application.ErrServiceOverloaded)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		rows, err := reader.List(ctx, query)
		if err != nil {
			writeApplicationError(w, err)
			return
		}

		data := make([]instrumentDTO, 0, len(rows))
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				writeApplicationError(w, err)
				return
			}

			dto := instrumentDTO{Exchange: row.Exchange, Market: row.Market, Symbol: row.Symbol, BaseAsset: row.BaseAsset, QuoteAsset: row.QuoteAsset, Status: row.Status, PriceTick: row.PriceTick.String(), QtyStep: row.QtyStep.String(), UpdatedAt: row.UpdatedAt.UTC()}
			if row.MinQty != nil {
				value := row.MinQty.String()
				dto.MinQty = &value
			}

			if row.MaxQty != nil {
				value := row.MaxQty.String()
				dto.MaxQty = &value
			}

			if row.MinNotional != nil {
				value := row.MinNotional.String()
				dto.MinNotional = &value
			}

			if row.FundingInterval != nil {
				value := int64(*row.FundingInterval / time.Second)
				dto.FundingInterval = &value
			}

			if row.DelistingTime != nil {
				value := row.DelistingTime.UTC()
				dto.DelistingTime = &value
			}

			data = append(data, dto)
		}

		body, err := json.Marshal(struct {
			Data []instrumentDTO `json:"data"`
		}{Data: data})
		if err != nil {
			writeApplicationError(w, err)
			return
		}

		if err := ctx.Err(); err != nil {
			writeApplicationError(w, err)
			return
		}

		_, _ = w.Write(append(body, '\n'))
	})
}
