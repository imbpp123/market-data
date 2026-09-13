package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
)

type TickerReader interface {
	Validate(application.SnapshotQuery) error
	List(context.Context, application.SnapshotQuery) ([]ticker.ReadModel, error)
}

type MarketStatsReader interface {
	Validate(marketstats.Query) error
	List(context.Context, marketstats.Query) ([]domain.MarketStats, error)
}

// All snapshot routes share one caller limit; health routes bypass it.
func NewSnapshotHandlers(instruments InstrumentReader, tickers TickerReader, stats MarketStatsReader, timeout time.Duration, maximum int) map[string]http.Handler {
	slots := make(chan struct{}, maximum)
	return map[string]http.Handler{
		"/api/v1/instruments":  newInstrumentsHandler(instruments, timeout, slots),
		"/api/v1/tickers":      newTickersHandler(tickers, timeout, slots),
		"/api/v1/market-stats": newMarketStatsHandler(stats, timeout, slots),
	}
}

type tickerDTO struct {
	Exchange      domain.Exchange `json:"exchange"`
	Market        domain.Market   `json:"market"`
	Symbol        string          `json:"symbol"`
	LastPrice     string          `json:"last_price"`
	BidPrice      *string         `json:"bid_price"`
	BidSize       *string         `json:"bid_size"`
	AskPrice      *string         `json:"ask_price"`
	AskSize       *string         `json:"ask_size"`
	FundingRate   *string         `json:"funding_rate"`
	NextFundingIn *int64          `json:"next_funding_in"`
	FetchedAt     time.Time       `json:"fetched_at"`
}

type marketStatsDTO struct {
	Exchange    domain.Exchange `json:"exchange"`
	Market      domain.Market   `json:"market"`
	Symbol      string          `json:"symbol"`
	Window      string          `json:"window"`
	High        string          `json:"high"`
	Low         string          `json:"low"`
	Volume      string          `json:"volume"`
	Turnover    string          `json:"turnover"`
	PriceChange *string         `json:"price_change"`
	TradeCount  *int64          `json:"trade_count"`
	FetchedAt   time.Time       `json:"fetched_at"`
}

func decimalString(value *decimal.Decimal) *string {
	if value == nil {
		return nil
	}

	text := value.String()
	return &text
}

func newTickersHandler(reader TickerReader, timeout time.Duration, slots chan struct{}) http.Handler {
	return currentHandler(false, timeout, slots, func(q marketstats.Query) error { return reader.Validate(q.SnapshotQuery) }, func(ctx context.Context, q marketstats.Query) (any, error) {
		rows, err := reader.List(ctx, q.SnapshotQuery)
		if err != nil {
			return nil, err
		}

		data := make([]tickerDTO, 0, len(rows))
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			dto := tickerDTO{Exchange: row.Exchange, Market: row.Market, Symbol: row.Symbol, LastPrice: row.LastPrice.String(), BidPrice: decimalString(row.BidPrice), BidSize: decimalString(row.BidSize), AskPrice: decimalString(row.AskPrice), AskSize: decimalString(row.AskSize), FundingRate: decimalString(row.FundingRate), FetchedAt: row.FetchedAt.UTC()}
			if row.NextFundingIn != nil {
				value := int64(*row.NextFundingIn / time.Second)
				dto.NextFundingIn = &value
			}

			data = append(data, dto)
		}

		return data, nil
	})
}

func newMarketStatsHandler(reader MarketStatsReader, timeout time.Duration, slots chan struct{}) http.Handler {
	return currentHandler(true, timeout, slots, reader.Validate, func(ctx context.Context, q marketstats.Query) (any, error) {
		rows, err := reader.List(ctx, q)
		if err != nil {
			return nil, err
		}

		data := make([]marketStatsDTO, 0, len(rows))
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			data = append(data, marketStatsDTO{Exchange: row.Exchange, Market: row.Market, Symbol: row.Symbol, Window: "24h", High: row.High.String(), Low: row.Low.String(), Volume: row.Volume.String(), Turnover: row.Turnover.String(), PriceChange: decimalString(row.PriceChange), TradeCount: row.TradeCount, FetchedAt: row.FetchedAt.UTC()})
		}

		return data, nil
	})
}

func currentQuery(raw string, statistics bool) (marketstats.Query, error) {
	parameters, err := url.ParseQuery(raw)
	if err != nil {
		return marketstats.Query{}, application.ErrInvalidParameter
	}

	for key := range parameters {
		if key != "exchange" && key != "market" && key != "symbol" && (!statistics || key != "window") {
			return marketstats.Query{}, application.ErrInvalidParameter
		}
	}

	for _, key := range []string{"exchange", "market", "symbol", "window"} {
		values, exists := parameters[key]
		if exists && (len(values) != 1 || values[0] == "") {
			if key == "window" {
				return marketstats.Query{}, application.ErrUnsupportedWindow
			}

			return marketstats.Query{}, application.ErrInvalidParameter
		}
	}

	return marketstats.Query{SnapshotQuery: application.SnapshotQuery{Exchange: parameters.Get("exchange"), Market: parameters.Get("market"), Symbol: parameters.Get("symbol")}, Window: parameters.Get("window")}, nil
}

func currentHandler(statistics bool, timeout time.Duration, slots chan struct{}, validate func(marketstats.Query) error, list func(context.Context, marketstats.Query) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, err := currentQuery(r.URL.RawQuery, statistics)
		if err != nil {
			writeApplicationError(w, err)
			return
		}

		if err := validate(query); err != nil {
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
		data, err := list(ctx, query)
		if err != nil {
			writeApplicationError(w, err)
			return
		}

		body, err := json.Marshal(struct {
			Data any `json:"data"`
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
