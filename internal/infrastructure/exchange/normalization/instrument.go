// Package normalization contains shared exact parsing for exchange adapters.
package normalization

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"market-data/internal/application"
	"market-data/internal/domain"
)

const maxDecimalCharacters = 1024

func Invalid(field string) error {
	return fmt.Errorf("normalize %s: %w", field, application.ErrInvalidUpstreamData)
}

func Limit(value string) (*decimal.Decimal, error) {
	if value == "" {
		return nil, nil
	}

	if len(value) > maxDecimalCharacters {
		return nil, Invalid("decimal size")
	}

	parsed, err := decimal.NewFromString(value)
	if err != nil || parsed.IsNegative() {
		return nil, Invalid("limit")
	}

	// Bound fixed-point expansion before String or comparisons can rescale it.
	// The input cap already bounds coefficient allocation; int64 avoids negating
	// MinInt32 when inspecting an upstream exponent.
	digits := int64(len(parsed.Coefficient().String()))
	exponent := int64(parsed.Exponent())
	characters := digits + exponent
	if exponent < 0 {
		characters = max(digits+1, 2-exponent)
	}
	if characters > maxDecimalCharacters {
		return nil, Invalid("decimal size")
	}

	return &parsed, nil
}

func Step(value string) (decimal.Decimal, error) {
	parsed, err := Limit(value)
	if err != nil || parsed == nil || !parsed.IsPositive() {
		return decimal.Decimal{}, Invalid("step")
	}

	return *parsed, nil
}

func Validate(row domain.Instrument) error {
	if row.Symbol == "" || row.BaseAsset == "" || row.QuoteAsset == "" {
		return Invalid("identifier")
	}

	if row.MinQty != nil && row.MaxQty != nil && row.MinQty.GreaterThan(*row.MaxQty) {
		return Invalid("quantity bounds")
	}

	return nil
}

func Duration(value string, unit time.Duration) (*time.Duration, error) {
	if value == "" {
		return nil, nil
	}

	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count <= 0 || count > math.MaxInt64/int64(unit) {
		return nil, Invalid("funding interval")
	}

	duration := time.Duration(count) * unit
	return &duration, nil
}

func Delisting(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}

	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || milliseconds < 0 {
		return nil, Invalid("delivery time")
	}

	if milliseconds == 0 {
		return nil, nil
	}

	timestamp := time.UnixMilli(milliseconds).UTC()
	if timestamp.Year() > 9999 {
		return nil, Invalid("delivery time")
	}

	return &timestamp, nil
}
