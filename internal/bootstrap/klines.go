package bootstrap

import (
	"context"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/exchange/upstream"
)

type klineOperations struct {
	admission *upstream.Controller
	telemetry telemetry
}

func (o klineOperations) Run(ctx context.Context, scope application.Scope, observe func(), run func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			o.telemetry.Panic(map[string]string{"operation": "kline_fill", "exchange": string(scope.Exchange), "market": string(scope.Market)})
			err = application.ErrInternal
		}
	}()
	finish := o.telemetry.Trace(ctx, "kline_fill", map[string]string{"exchange": string(scope.Exchange), "market": string(scope.Market)})
	defer finish()
	transportScope := upstream.Bybit
	if scope.Exchange == domain.ExchangeBinance {
		transportScope = upstream.BinanceSpot
		if scope.Market == domain.MarketLinear {
			transportScope = upstream.BinanceLinear
		}
	}
	ctx, cancel, err := o.admission.Begin(ctx, transportScope, upstream.Klines)
	if err != nil {
		return err
	}
	defer cancel()
	return run(upstream.WithAttemptObserver(ctx, observe))
}

func (s *localState) klineService(ctx context.Context, cfg config.Config, now func() time.Time) (*kline.Service, error) {
	providers := map[domain.Exchange]kline.Provider{
		domain.ExchangeBinance: binance.NewKlineProvider(s.exchanges.binance[upstream.BinanceSpot], s.exchanges.binance[upstream.BinanceLinear]),
		domain.ExchangeBybit:   bybit.NewKlineProvider(s.exchanges.bybit),
	}
	var scopes []kline.ScopeSettings
	for _, scope := range enabledScopes(cfg) {
		limits := cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest
		if scope.Exchange == domain.ExchangeBinance {
			limits = cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest
		}
		limit := limits.Spot
		if scope.Market == domain.MarketLinear {
			limit = limits.Linear
		}
		scopes = append(scopes, kline.ScopeSettings{Scope: scope, Provider: providers[scope.Exchange], PageLimit: limit})
	}
	return kline.NewService(ctx, s.klines, s.instruments, klineOperations{admission: s.exchanges.admission, telemetry: s.telemetry}, scopes, kline.Settings{
		HistoryCandles: int64(cfg.Klines.MaxHistoryCandles), MaxCallers: cfg.Klines.MaxCallers,
		MaxActiveFills: cfg.Klines.MaxActiveFills, MaxActiveFillsPerExchange: cfg.Klines.MaxActiveFillsPerExchange,
		MaxAttempts: cfg.Upstream.LanesPerExchange.Klines.MaxAttempts, FillTimeout: cfg.Klines.FillTimeout,
	}, now, func(event kline.Event) {
		if collectStatistics(cfg) {
			s.klineMetrics.Observe(event)
		}
		if event.Completed {
			s.telemetry.Report(event.Error, map[string]string{"operation": "kline_fill", "exchange": string(event.Scope.Exchange), "market": string(event.Scope.Market), "symbol": event.Symbol, "interval": string(event.Interval)})
		}
	})
}
