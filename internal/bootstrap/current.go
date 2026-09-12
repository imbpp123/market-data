package bootstrap

import (
	"context"
	"log/slog"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/observability"
)

type currentProvider interface {
	ticker.Provider
	marketstats.Provider
}

func (s *localState) currentWorkers(cfg config.Config, logger *slog.Logger, clock upstream.Clock, jitter upstream.Jitter) ([]Worker, error) {
	providers := map[domain.Exchange]currentProvider{
		domain.ExchangeBinance: binance.NewCurrentProvider(s.exchanges.binance[upstream.BinanceSpot], s.exchanges.binance[upstream.BinanceLinear], s.instruments, logger),
		domain.ExchangeBybit:   bybit.NewCurrentProvider(s.exchanges.bybit, s.instruments),
	}

	observe := func(event application.RefreshEvent) {
		if collectStatistics(cfg) {
			s.currentMetrics.Observe(event)
		}
		operation := "tickers"
		if event.Window != 0 {
			operation = "market_stats"
		}
		s.telemetry.Report(event.Error, map[string]string{"operation": operation, "exchange": string(event.Scope.Exchange), "market": string(event.Scope.Market), "window": event.Window.String()})
		if event.Error != nil {
			logger.Warn("Current data refresh failed", "exchange", event.Scope.Exchange, "market", event.Scope.Market, "window", event.Window, "error", observability.ErrorCode(event.Error))
		}
	}

	var workers []Worker
	for _, scope := range enabledScopes(cfg) {
		provider := providers[scope.Exchange]
		transportScope := upstream.Bybit
		if scope.Exchange == domain.ExchangeBinance {
			transportScope = upstream.BinanceSpot
			if scope.Market == domain.MarketLinear {
				transportScope = upstream.BinanceLinear
			}
		}

		gate, err := upstream.NewCycleGate(clock, jitter, cfg.HTTPClient.Retry, 0)
		if err != nil {
			return nil, err
		}

		cycles := upstream.NewBoundedCycle(gate, s.exchanges.admission, transportScope, upstream.Tickers)
		refresher := ticker.NewRefresher(provider, scope, s.tickers, s.marketStats, observe)
		workers = append(workers, func(ctx context.Context) error { return refresher.Run(ctx, cycles) })
		if provider.Capabilities().MarketStatsWithTicker {
			continue
		}

		interval := cfg.Exchanges.Binance.MarketStats.RefreshInterval
		statsGate, err := upstream.NewCycleGate(clock, jitter, cfg.HTTPClient.Retry, interval)
		if err != nil {
			return nil, err
		}

		statsCycles := upstream.NewBoundedCycle(statsGate, s.exchanges.admission, transportScope, upstream.MarketStats)
		stats := marketstats.NewRefresher(provider, scope, s.marketStats, clock.Now, observe)
		workers = append(workers, func(ctx context.Context) error { return stats.Run(ctx, statsCycles) })
	}

	return workers, nil
}
