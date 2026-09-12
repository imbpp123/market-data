package ticker

import (
	"time"

	"market-data/internal/domain"

	"github.com/shopspring/decimal"
)

// ReadModel keeps the funding timestamp out of the public read contract.
// Transport converts NextFundingIn to integer seconds, preserving explicit nil.
type ReadModel struct {
	Exchange domain.Exchange
	Market   domain.Market
	Symbol   string

	LastPrice decimal.Decimal
	BidPrice  *decimal.Decimal
	BidSize   *decimal.Decimal
	AskPrice  *decimal.Decimal
	AskSize   *decimal.Decimal

	FundingRate   *decimal.Decimal
	NextFundingIn *time.Duration
	FetchedAt     time.Time
}

type ReadModelBuilder struct {
	now func() time.Time
}

// NewReadModelBuilder requires an injected, non-nil clock.
func NewReadModelBuilder(now func() time.Time) *ReadModelBuilder {
	return &ReadModelBuilder{now: now}
}

func (b *ReadModelBuilder) Build(rows []domain.Ticker) []ReadModel {
	now := b.now()
	result := make([]ReadModel, len(rows))
	for i, row := range rows {
		var remaining *time.Duration
		if row.NextFundingAt != nil && row.NextFundingAt.After(now) {
			duration := row.NextFundingAt.Sub(now)
			remaining = &duration
		}

		result[i] = ReadModel{
			Exchange: row.Exchange, Market: row.Market, Symbol: row.Symbol,
			LastPrice: row.LastPrice,
			BidPrice:  copyDecimal(row.BidPrice), BidSize: copyDecimal(row.BidSize),
			AskPrice: copyDecimal(row.AskPrice), AskSize: copyDecimal(row.AskSize),
			FundingRate: copyDecimal(row.FundingRate), NextFundingIn: remaining,
			FetchedAt: row.FetchedAt,
		}
	}

	return result
}

func copyDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil {
		return nil
	}

	copy := value.Copy()
	return &copy
}
