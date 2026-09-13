package main

import (
	"context"
	"fmt"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
)

const fixtureDecimal = "12345.1234567890123456789"
const fixtureCount int64 = 9007199254740993
const fixtureEpoch int64 = 1789214400

type instrumentFixture []domain.Instrument
type tickerFixture []ticker.ReadModel
type statsFixture []domain.MarketStats
type klineFixture []domain.Kline

func (instrumentFixture) Validate(instrument.Query) error { return nil }

func (rows instrumentFixture) List(_ context.Context, query instrument.Query) ([]domain.Instrument, error) {
	if query.Symbol == "full" {
		return rows, nil
	}
	return rows[:1], nil
}

func (tickerFixture) Validate(application.SnapshotQuery) error { return nil }

func (rows tickerFixture) List(_ context.Context, query application.SnapshotQuery) ([]ticker.ReadModel, error) {
	if query.Symbol == "full" {
		return rows, nil
	}
	return rows[:1], nil
}

func (statsFixture) Validate(marketstats.Query) error { return nil }

func (rows statsFixture) List(_ context.Context, query marketstats.Query) ([]domain.MarketStats, error) {
	if query.Symbol == "full" {
		return rows, nil
	}
	return rows[:1], nil
}

func (klineFixture) ValidateSeries(kline.Series) error { return nil }

func (klineFixture) Validate(query kline.Query) error {
	count := int(query.To.Sub(query.From) / time.Minute)
	if count < 0 || count > 1000 {
		return application.ErrInvalidRange
	}
	return nil
}

func (rows klineFixture) Get(_ context.Context, query kline.Query) ([]domain.Kline, error) {
	return rows[:int(query.To.Sub(query.From)/time.Minute)], nil
}

func fixtures() (instrumentFixture, tickerFixture, statsFixture, klineFixture) {
	value := decimal.RequireFromString(fixtureDecimal)
	stamp := time.Unix(fixtureEpoch, 123456789).UTC()
	funding := 8 * time.Hour
	count := fixtureCount
	instruments := make(instrumentFixture, 20000)
	tickers := make(tickerFixture, 20000)
	stats := make(statsFixture, 20000)
	for i := range 20000 {
		exchange := []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit}[(i/10000)%2]
		market := []domain.Market{domain.MarketLinear, domain.MarketSpot}[(i/5000)%2]
		symbol := fmt.Sprintf("S%04dUSDT", i%5000)
		instruments[i] = domain.Instrument{Exchange: exchange, Market: market, Symbol: symbol, BaseAsset: "S", QuoteAsset: "USDT", Status: domain.InstrumentStatusTrading,
			PriceTick: value, QtyStep: value, MinQty: &value, MaxQty: &value, MinNotional: &value, FundingInterval: &funding, DelistingTime: &stamp, UpdatedAt: stamp}
		tickers[i] = ticker.ReadModel{Exchange: exchange, Market: market, Symbol: symbol, LastPrice: value, BidPrice: &value, BidSize: &value, AskPrice: &value, AskSize: &value, FundingRate: &value, NextFundingIn: &funding, FetchedAt: stamp}
		stats[i] = domain.MarketStats{Exchange: exchange, Market: market, Symbol: symbol, Window: 24 * time.Hour, High: value, Low: value, Volume: value, Turnover: value, PriceChange: &value, TradeCount: &count, FetchedAt: stamp}
	}
	candles := make(klineFixture, 1000)
	for i := range 1000 {
		start := time.Unix(fixtureEpoch-60000+int64(i)*60, 0).UTC()
		candles[i] = domain.Kline{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot, Symbol: "S0000USDT", Interval: domain.Timeframe1m,
			OpenTime: start, CloseTime: start.Add(time.Minute), Open: value, High: value, Low: value, Close: value, Volume: value, Turnover: value, TradesCount: &count, FetchedAt: stamp}
	}
	return instruments, tickers, stats, candles
}
