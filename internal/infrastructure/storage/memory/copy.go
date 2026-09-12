// Package memory implements application storage contracts with owned snapshots.
package memory

import (
	"market-data/internal/application"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
)

func validateScope(scope application.Scope) error {
	if !scope.Exchange.Valid() || !scope.Market.Valid() {
		return application.ErrInvalidFilter
	}
	return nil
}

func copyPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil {
		return nil
	}
	copy := value.Copy()
	return &copy
}

func copyInstrument(row domain.Instrument) domain.Instrument {
	row.PriceTick = row.PriceTick.Copy()
	row.QtyStep = row.QtyStep.Copy()
	row.MinQty = copyDecimal(row.MinQty)
	row.MaxQty = copyDecimal(row.MaxQty)
	row.MinNotional = copyDecimal(row.MinNotional)
	row.FundingInterval = copyPointer(row.FundingInterval)
	row.DelistingTime = copyPointer(row.DelistingTime)
	return row
}

func copyTicker(row domain.Ticker) domain.Ticker {
	row.LastPrice = row.LastPrice.Copy()
	row.BidPrice = copyDecimal(row.BidPrice)
	row.BidSize = copyDecimal(row.BidSize)
	row.AskPrice = copyDecimal(row.AskPrice)
	row.AskSize = copyDecimal(row.AskSize)
	row.FundingRate = copyDecimal(row.FundingRate)
	row.NextFundingAt = copyPointer(row.NextFundingAt)
	return row
}

func copyMarketStats(row domain.MarketStats) domain.MarketStats {
	row.High = row.High.Copy()
	row.Low = row.Low.Copy()
	row.Volume = row.Volume.Copy()
	row.Turnover = row.Turnover.Copy()
	row.PriceChange = copyDecimal(row.PriceChange)
	row.TradeCount = copyPointer(row.TradeCount)
	return row
}

func copyKline(row domain.Kline) domain.Kline {
	row.Open = row.Open.Copy()
	row.High = row.High.Copy()
	row.Low = row.Low.Copy()
	row.Close = row.Close.Copy()
	row.Volume = row.Volume.Copy()
	row.Turnover = row.Turnover.Copy()
	row.TradesCount = copyPointer(row.TradesCount)
	return row
}
