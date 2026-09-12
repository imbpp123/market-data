package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type Exchange string

const (
	ExchangeBinance Exchange = "binance"
	ExchangeBybit   Exchange = "bybit"
)

func (e Exchange) Valid() bool {
	return e == ExchangeBinance || e == ExchangeBybit
}

type Market string

const (
	MarketSpot   Market = "spot"
	MarketLinear Market = "linear"
)

func (m Market) Valid() bool {
	return m == MarketSpot || m == MarketLinear
}

type InstrumentStatus string

const (
	InstrumentStatusUnknown    InstrumentStatus = "unknown"
	InstrumentStatusPreLaunch  InstrumentStatus = "pre_launch"
	InstrumentStatusTrading    InstrumentStatus = "trading"
	InstrumentStatusHalted     InstrumentStatus = "halted"
	InstrumentStatusCancelOnly InstrumentStatus = "cancel_only"
	InstrumentStatusSettling   InstrumentStatus = "settling"
	InstrumentStatusClosed     InstrumentStatus = "closed"
)

func (s InstrumentStatus) Valid() bool {
	switch s {
	case InstrumentStatusUnknown, InstrumentStatusPreLaunch, InstrumentStatusTrading,
		InstrumentStatusHalted, InstrumentStatusCancelOnly, InstrumentStatusSettling, InstrumentStatusClosed:
		return true
	default:
		return false
	}
}

type ContractType string

const (
	ContractTypeUnknown   ContractType = ""
	ContractTypePerpetual ContractType = "perpetual"
	ContractTypeExpiry    ContractType = "expiry"
)

type Instrument struct {
	// ContractType is internal funding metadata and is not part of the HTTP DTO.
	ContractType ContractType

	Exchange Exchange
	Market   Market

	Symbol     string
	BaseAsset  string
	QuoteAsset string
	Status     InstrumentStatus

	PriceTick decimal.Decimal
	QtyStep   decimal.Decimal

	MinQty      *decimal.Decimal
	MaxQty      *decimal.Decimal
	MinNotional *decimal.Decimal

	FundingInterval *time.Duration
	DelistingTime   *time.Time
	UpdatedAt       time.Time
}

type Ticker struct {
	Exchange Exchange
	Market   Market
	Symbol   string

	LastPrice decimal.Decimal
	BidPrice  *decimal.Decimal
	BidSize   *decimal.Decimal
	AskPrice  *decimal.Decimal
	AskSize   *decimal.Decimal

	FundingRate   *decimal.Decimal
	NextFundingAt *time.Time
	FetchedAt     time.Time
}

type MarketStats struct {
	Exchange Exchange
	Market   Market
	Symbol   string
	Window   time.Duration

	High     decimal.Decimal
	Low      decimal.Decimal
	Volume   decimal.Decimal
	Turnover decimal.Decimal

	PriceChange *decimal.Decimal
	TradeCount  *int64
	FetchedAt   time.Time
}

type Kline struct {
	Exchange Exchange
	Market   Market
	Symbol   string
	Interval Timeframe

	OpenTime  time.Time
	CloseTime time.Time

	Open     decimal.Decimal
	High     decimal.Decimal
	Low      decimal.Decimal
	Close    decimal.Decimal
	Volume   decimal.Decimal
	Turnover decimal.Decimal

	TradesCount *int64
	FetchedAt   time.Time
}
