package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
)

type KlineReader interface {
	ValidateSeries(kline.Series) error
	Validate(kline.Query) error
	Get(context.Context, kline.Query) ([]domain.Kline, error)
}

type klineDTO struct {
	Exchange    domain.Exchange  `json:"exchange"`
	Market      domain.Market    `json:"market"`
	Symbol      string           `json:"symbol"`
	Interval    domain.Timeframe `json:"interval"`
	OpenTime    time.Time        `json:"open_time"`
	CloseTime   time.Time        `json:"close_time"`
	Open        string           `json:"open"`
	High        string           `json:"high"`
	Low         string           `json:"low"`
	Close       string           `json:"close"`
	Volume      string           `json:"volume"`
	Turnover    string           `json:"turnover"`
	TradesCount *int64           `json:"trades_count"`
	FetchedAt   time.Time        `json:"fetched_at"`
}

var timestampSyntax = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func NewKlinesHandler(reader KlineReader, timeout time.Duration, maximum int) http.Handler {
	slots := make(chan struct{}, maximum)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		query, err := parseKlineQuery(r.URL.RawQuery, reader)
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		if err := reader.Validate(query); err != nil {
			writeApplicationError(w, err)
			return
		}
		if err := ctx.Err(); err != nil {
			writeApplicationError(w, err)
			return
		}
		select {
		case slots <- struct{}{}:
			// Keep response buffers inside the caller limit until Write ends.
			defer func() { <-slots }()
		default:
			writeApplicationError(w, application.ErrServiceOverloaded)
			return
		}
		rows, err := reader.Get(ctx, query)
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		data := make([]klineDTO, 0, len(rows))
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				writeApplicationError(w, err)
				return
			}
			data = append(data, klineDTO{
				Exchange: row.Exchange, Market: row.Market, Symbol: row.Symbol, Interval: row.Interval,
				OpenTime: row.OpenTime.UTC(), CloseTime: row.CloseTime.UTC(), FetchedAt: row.FetchedAt.UTC(),
				Open: row.Open.String(), High: row.High.String(), Low: row.Low.String(), Close: row.Close.String(),
				Volume: row.Volume.String(), Turnover: row.Turnover.String(), TradesCount: row.TradesCount,
			})
		}
		body, err := json.Marshal(struct {
			Data []klineDTO `json:"data"`
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

func parseKlineQuery(raw string, reader KlineReader) (kline.Query, error) {
	var query kline.Query
	parameters, err := url.ParseQuery(raw)
	if err != nil {
		return query, application.ErrInvalidParameter
	}
	for key := range parameters {
		switch key {
		case "exchange", "market", "symbol", "interval", "from", "to":
		default:
			return query, application.ErrInvalidParameter
		}
	}
	for _, key := range []string{"exchange", "market", "symbol", "interval", "from", "to"} {
		values, exists := parameters[key]
		if !exists {
			return query, application.ErrInvalidParameter
		}
		if len(values) != 1 || values[0] == "" {
			if key == "interval" {
				return query, application.ErrInvalidInterval
			}
			return query, application.ErrInvalidParameter
		}
	}
	query.Series = kline.Series{
		Scope:  application.Scope{Exchange: domain.Exchange(parameters.Get("exchange")), Market: domain.Market(parameters.Get("market"))},
		Symbol: parameters.Get("symbol"), Interval: domain.Timeframe(parameters.Get("interval")),
	}
	if err := reader.ValidateSeries(query.Series); err != nil {
		return query, err
	}
	query.From, err = parseTimestamp(parameters.Get("from"))
	if err != nil {
		return query, err
	}
	query.To, err = parseTimestamp(parameters.Get("to"))
	return query, err
}

func parseTimestamp(value string) (time.Time, error) {
	if !timestampSyntax.MatchString(value) {
		return time.Time{}, application.ErrInvalidRange
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Unix() < 0 {
		return time.Time{}, application.ErrInvalidRange
	}
	return parsed.UTC(), nil
}
