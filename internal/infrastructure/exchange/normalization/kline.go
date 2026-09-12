package normalization

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
)

// KlineRequest checks page shape. Catalog and retention checks belong to the
// application; the shared transport also enforces configured page limits.
func KlineRequest(request kline.Request, exchange domain.Exchange) error {
	if request.Exchange != exchange || !request.Market.Valid() || request.Symbol == "" {
		return application.ErrInvalidFilter
	}

	_, err := application.SelectSnapshot([]application.Scope{request.Scope}, application.SnapshotQuery{
		Exchange: string(request.Exchange), Market: string(request.Market), Symbol: request.Symbol,
	})
	if err != nil {
		return err
	}

	if request.Limit <= 0 {
		return application.ErrInvalidParameter
	}

	calendar, err := domain.NewCalendar(exchange, request.Market, request.Interval)
	if err != nil {
		return application.ErrInvalidInterval
	}

	if request.From.Unix() < 0 || request.To.Unix() < 0 {
		return application.ErrInvalidRange
	}

	count, err := calendar.CountSlots(request.From, request.To, int64(request.Limit))
	if err != nil || count != int64(request.Limit) {
		return application.ErrInvalidRange
	}

	return nil
}

// Klines decodes original tuples without a floating-point intermediate. A used
// field failure discards the entire page, including previously decoded rows.
func Klines(ctx context.Context, request kline.Request, sources [][]json.RawMessage, response upstream.Response) ([]kline.Stored, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(sources) > request.Limit || response.StartedAt.IsZero() || response.FetchedAt.IsZero() || response.StartedAt.After(response.FetchedAt) {
		return nil, Invalid("candle page metadata")
	}

	calendar, err := domain.NewCalendar(request.Exchange, request.Market, request.Interval)
	if err != nil {
		return nil, Invalid("candle calendar")
	}

	rows := make([]kline.Stored, 0, len(sources))
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		row, err := candle(request, calendar, source)
		if err != nil {
			return nil, err
		}

		row.FetchedAt = response.FetchedAt.UTC()
		rows = append(rows, kline.Stored{Candle: row, RequestStartedAt: response.StartedAt.UTC()})
	}

	slices.SortFunc(rows, func(a, b kline.Stored) int { return a.Candle.OpenTime.Compare(b.Candle.OpenTime) })
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Candle.OpenTime.Equal(rows[i].Candle.OpenTime) {
			return nil, Invalid("duplicate candle time")
		}
	}

	return rows, nil
}

func candle(request kline.Request, calendar domain.Calendar, source []json.RawMessage) (domain.Kline, error) {
	row := domain.Kline{Exchange: request.Exchange, Market: request.Market, Symbol: request.Symbol, Interval: request.Interval}
	bybit := request.Exchange == domain.ExchangeBybit
	required, turnover := 9, 7
	if bybit {
		required, turnover = 7, 6
	}

	if len(source) < required {
		return row, Invalid("candle tuple")
	}

	open, err := candleInteger(source[0], bybit)
	if err != nil {
		return row, err
	}

	row.OpenTime = time.UnixMilli(open).UTC()
	if row.OpenTime.Before(request.From) || !row.OpenTime.Before(request.To) {
		return row, Invalid("candle outside requested range")
	}

	row.CloseTime, err = calendar.Next(row.OpenTime)
	if err != nil {
		return row, Invalid("candle boundary")
	}

	if !bybit {
		closeTime, err := candleInteger(source[6], false)
		if err != nil || closeTime == math.MaxInt64 || !time.UnixMilli(closeTime+1).Equal(row.CloseTime) {
			return row, Invalid("candle close time")
		}

		count, err := candleInteger(source[8], false)
		if err != nil {
			return row, err
		}

		row.TradesCount = &count
	}

	targets := []*decimal.Decimal{&row.Open, &row.High, &row.Low, &row.Close, &row.Volume, &row.Turnover}
	for i, position := range []int{1, 2, 3, 4, 5, turnover} {
		value, err := (Row{"candle decimal": source[position]}).Decimal("candle decimal", true, false)
		if err != nil {
			return row, err
		}

		*targets[i] = *value
	}

	if row.Low.GreaterThan(row.Open) || row.Low.GreaterThan(row.Close) || row.Open.GreaterThan(row.High) || row.Close.GreaterThan(row.High) {
		return row, Invalid("candle OHLC bounds")
	}

	return row, nil
}

func candleInteger(raw json.RawMessage, quoted bool) (int64, error) {
	var value int64
	var err error
	if quoted {
		var text string
		if json.Unmarshal(raw, &text) != nil || text == "" {
			return 0, Invalid("candle integer")
		}

		for _, digit := range text {
			if digit < '0' || digit > '9' {
				return 0, Invalid("candle integer")
			}
		}

		value, err = strconv.ParseInt(text, 10, 64)
	} else {
		if len(raw) == 0 || string(raw) == "null" {
			return 0, Invalid("candle integer")
		}

		err = json.Unmarshal(raw, &value)
	}

	if err != nil || value < 0 {
		return 0, Invalid("candle integer")
	}

	return value, nil
}
