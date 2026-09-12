package normalization

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
)

// Row keeps field parsing branch-local and preserves original numeric text.
type Row map[string]json.RawMessage

func (r Row) Text(key string) (string, error) {
	raw := r[key]
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	if raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return "", Invalid(key)
		}

		return value, nil
	}

	var value json.Number
	if json.Unmarshal(raw, &value) != nil {
		return "", Invalid(key)
	}

	return value.String(), nil
}

func (r Row) Symbol() (string, error) {
	var value string
	if json.Unmarshal(r["symbol"], &value) != nil || value == "" {
		return "", Invalid("symbol")
	}

	return value, nil
}

func signedDecimal(value string) (*decimal.Decimal, error) {
	if len(value) > maxDecimalCharacters {
		return nil, Invalid("decimal size")
	}

	if value == "" {
		return nil, nil
	}

	// Reuse the bounded parser without losing the sign.
	magnitude := strings.TrimPrefix(value, "-")
	if strings.HasPrefix(value, "-") && (magnitude == "" || strings.HasPrefix(magnitude, "+") || strings.HasPrefix(magnitude, "-")) {
		return nil, Invalid("decimal")
	}
	parsed, err := Limit(magnitude)
	if err != nil {
		return nil, err
	}

	if parsed != nil && strings.HasPrefix(value, "-") {
		if decimalCharacters(*parsed)+1 > maxDecimalCharacters {
			return nil, Invalid("decimal size")
		}
		v := parsed.Neg()
		parsed = &v
	}

	return parsed, nil
}

func (r Row) Decimal(key string, required, signed bool) (*decimal.Decimal, error) {
	text, err := r.Text(key)
	if err != nil {
		return nil, err
	}

	var value *decimal.Decimal
	if signed {
		value, err = signedDecimal(text)
	} else {
		value, err = Limit(text)
	}

	if err != nil || (required && value == nil) {
		return nil, Invalid(key)
	}

	return value, nil
}

func (r Row) Quote(priceKey, sizeKey string) (*decimal.Decimal, *decimal.Decimal, error) {
	price, err := r.Decimal(priceKey, false, false)
	if err != nil {
		return nil, nil, err
	}

	size, err := r.Decimal(sizeKey, false, false)
	if err != nil {
		return nil, nil, err
	}

	if price == nil && size == nil {
		return nil, nil, nil
	}

	if price != nil && size != nil {
		if size.IsZero() {
			return nil, nil, nil
		}

		if price.IsPositive() {
			return price, size, nil
		}
	}

	return nil, nil, Invalid("quote pair")
}

func Contracts(ctx context.Context, repository instrument.Repository, scope application.Scope) (map[string]domain.ContractType, error) {
	result := make(map[string]domain.ContractType)
	if repository == nil || scope.Market == domain.MarketSpot {
		return result, nil
	}

	rows, err := repository.List(ctx, instrument.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{scope}}})
	if errors.Is(err, application.ErrDataNotReady) {
		return result, nil
	}

	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		result[row.Symbol] = row.ContractType
	}

	return result, nil
}

func (r Row) Funding(market domain.Market, kind domain.ContractType, rateKey string) (*decimal.Decimal, *time.Time, error) {
	if market == domain.MarketSpot || kind == domain.ContractTypeExpiry {
		return nil, nil, nil
	}

	value, err := r.Text("nextFundingTime")
	if err != nil {
		return nil, nil, err
	}

	next, err := Delisting(value)
	if err != nil {
		return nil, nil, err
	}

	if kind != domain.ContractTypePerpetual && next == nil {
		return nil, nil, nil
	}

	rate, err := r.Decimal(rateKey, false, true)
	return rate, next, err
}

func (r Row) Count() (*int64, error) {
	raw, exists := r["count"]
	if !exists {
		return nil, nil
	}

	value, err := r.Text("count")
	if err != nil || string(raw) == "null" {
		return nil, Invalid("count")
	}

	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count < 0 {
		return nil, Invalid("count")
	}

	return &count, nil
}

func Index(ctx context.Context, rows []Row) (map[string]Row, error) {
	result := make(map[string]Row, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		symbol, err := row.Symbol()
		if err != nil {
			return nil, err
		}

		if _, exists := result[symbol]; exists {
			return nil, Invalid("duplicate symbol")
		}

		result[symbol] = row
	}

	return result, nil
}

func Statistics(ctx context.Context, sources []Row, scope application.Scope, fetchedAt time.Time) ([]domain.MarketStats, error) {
	if _, err := Index(ctx, sources); err != nil {
		return nil, err
	}

	rows := make([]domain.MarketStats, 0, len(sources))
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		symbol, err := source.Symbol()
		if err != nil {
			return nil, err
		}

		row := domain.MarketStats{Exchange: scope.Exchange, Market: scope.Market, Symbol: symbol, Window: 24 * time.Hour, FetchedAt: fetchedAt.UTC()}
		fields := []string{"highPrice", "lowPrice", "volume", "quoteVolume"}
		if scope.Exchange == domain.ExchangeBybit {
			fields = []string{"highPrice24h", "lowPrice24h", "volume24h", "turnover24h"}
		}

		targets := []*decimal.Decimal{&row.High, &row.Low, &row.Volume, &row.Turnover}
		for i, field := range fields {
			value, err := source.Decimal(field, true, false)
			if err != nil {
				return nil, err
			}

			*targets[i] = *value
		}

		if scope.Exchange == domain.ExchangeBybit {
			previous, err := source.Decimal("prevPrice24h", false, false)
			if err != nil {
				return nil, err
			}

			if previous != nil && previous.IsPositive() {
				last, err := source.Decimal("lastPrice", true, false)
				if err != nil {
					return nil, err
				}

				change := last.Sub(*previous)
				row.PriceChange = &change
			}
		} else {
			row.PriceChange, err = source.Decimal("priceChange", false, true)
			if err != nil {
				return nil, err
			}

			row.TradeCount, err = source.Count()
			if err != nil {
				return nil, err
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}
