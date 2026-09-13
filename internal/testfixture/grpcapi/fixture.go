// Package grpcapi provides fixed real application services for local client tests.
package grpcapi

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
)

var Now = time.Unix(1789214400, 123456789).UTC()

var Scope = application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}

type Fixture struct {
	Instruments      instrument.Repository
	Tickers          ticker.Repository
	Stats            marketstats.Repository
	Candles          kline.Repository
	Service          *kline.Service
	Provider         *Provider
	InstrumentReader *instrument.Reader
	TickerReader     *ticker.Reader
	StatsReader      *marketstats.Reader
}

func New(ctx context.Context, observe func(kline.Event)) (*Fixture, error) {
	f := &Fixture{Instruments: memory.NewInstrumentRepository(), Tickers: memory.NewTickerRepository(), Stats: memory.NewMarketStatsRepository(), Provider: &Provider{}}
	var err error
	f.Candles, err = memory.NewKlineRepository(1000, func() time.Time { return Now })
	if err != nil {
		return nil, err
	}
	zero := decimal.Zero
	duration := time.Duration(0)
	count := int64(9007199254740993)
	soon := Now.Add(999 * time.Millisecond)
	exact := decimal.RequireFromString("12345.1234567890123456789")
	long := decimal.RequireFromString(strings.Repeat("9", 1024))
	instruments := []domain.Instrument{
		{Exchange: Scope.Exchange, Market: Scope.Market, Symbol: "A", BaseAsset: "A", QuoteAsset: "USDT", Status: domain.InstrumentStatusTrading, PriceTick: exact, QtyStep: decimal.NewFromInt(1), UpdatedAt: Now},
		{Exchange: Scope.Exchange, Market: Scope.Market, Symbol: "B", BaseAsset: "B", QuoteAsset: "USDT", Status: domain.InstrumentStatusTrading, PriceTick: exact, QtyStep: decimal.NewFromInt(1), MinQty: &zero, FundingInterval: &duration, DelistingTime: &Now, UpdatedAt: Now},
		{Exchange: Scope.Exchange, Market: Scope.Market, Symbol: "C", BaseAsset: "C", QuoteAsset: "USDT", Status: domain.InstrumentStatusTrading, PriceTick: exact, QtyStep: decimal.NewFromInt(1), MinQty: &long, UpdatedAt: Now},
	}
	if err = f.Instruments.ReplaceSnapshot(ctx, Scope, instruments); err != nil {
		return nil, err
	}
	if err = f.Tickers.ReplaceSnapshot(ctx, Scope, []domain.Ticker{{Exchange: Scope.Exchange, Market: Scope.Market, Symbol: "A", LastPrice: exact, FetchedAt: Now}, {Exchange: Scope.Exchange, Market: Scope.Market, Symbol: "B", LastPrice: exact, BidPrice: &zero, NextFundingAt: &soon, FetchedAt: Now}}); err != nil {
		return nil, err
	}
	if err = f.Stats.ReplaceSnapshot(ctx, Scope, 24*time.Hour, []domain.MarketStats{{Exchange: Scope.Exchange, Market: Scope.Market, Symbol: "A", Window: 24 * time.Hour, High: exact, Low: zero, Volume: exact, Turnover: exact, TradeCount: &count, FetchedAt: Now}, {Exchange: Scope.Exchange, Market: Scope.Market, Symbol: "B", Window: 24 * time.Hour, PriceChange: &zero, TradeCount: new(int64), FetchedAt: Now}}); err != nil {
		return nil, err
	}
	scopes := []application.Scope{Scope}
	f.InstrumentReader = instrument.NewReader(f.Instruments, scopes)
	f.TickerReader = ticker.NewReader(f.Tickers, scopes, func() time.Time { return Now })
	f.StatsReader = marketstats.NewReader(f.Stats, scopes)
	f.Service, err = kline.NewService(ctx, f.Candles, f.Instruments, operations{}, []kline.ScopeSettings{{Scope: Scope, Provider: f.Provider, PageLimit: 500}}, kline.Settings{HistoryCandles: 1000, MaxCallers: 64, MaxActiveFills: 8, MaxActiveFillsPerExchange: 8, MaxAttempts: 10, FillTimeout: time.Minute}, func() time.Time { return Now }, observe)
	return f, err
}

type Provider struct {
	Attempts atomic.Int64
	Run      func(context.Context, kline.Request) ([]kline.Stored, error)
}

func (*Provider) Exchange() domain.Exchange { return domain.ExchangeBinance }
func (*Provider) SupportedTimeframes(domain.Market) ([]domain.Timeframe, error) {
	return []domain.Timeframe{domain.Timeframe("1m"), domain.Timeframe("1w"), domain.Timeframe("1M")}, nil
}
func (p *Provider) GetKlines(ctx context.Context, r kline.Request) ([]kline.Stored, error) {
	p.Attempts.Add(1)
	if p.Run != nil {
		return p.Run(ctx, r)
	}
	return Rows(r), nil
}

func Rows(r kline.Request) []kline.Stored {
	var result []kline.Stored
	count := int64(9007199254740993)
	for at := r.From; at.Before(r.To); at = at.Add(time.Minute) {
		result = append(result, kline.Stored{Candle: domain.Kline{Exchange: r.Exchange, Market: r.Market, Symbol: r.Symbol, Interval: r.Interval, OpenTime: at, CloseTime: at.Add(time.Minute), Open: decimal.RequireFromString("12345.1234567890123456789"), High: decimal.NewFromInt(12346), Low: decimal.NewFromInt(12344), Close: decimal.NewFromInt(12345), Volume: decimal.NewFromInt(1), Turnover: decimal.NewFromInt(2), TradesCount: &count, FetchedAt: Now}, RequestStartedAt: Now})
	}
	return result
}

type operations struct{}

func (operations) Run(ctx context.Context, _ application.Scope, onAttempt func(), run func(context.Context) error) error {
	onAttempt()
	return run(ctx)
}
