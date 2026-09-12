package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/exchange/upstream"
)

func enabledScopes(cfg config.Config) []application.Scope {
	var scopes []application.Scope
	if cfg.Exchanges.Binance.Enabled {
		for _, market := range cfg.Exchanges.Binance.Markets {
			scopes = append(scopes, application.Scope{Exchange: domain.ExchangeBinance, Market: domain.Market(market)})
		}
	}

	if cfg.Exchanges.Bybit.Enabled {
		for _, market := range cfg.Exchanges.Bybit.Markets {
			scopes = append(scopes, application.Scope{Exchange: domain.ExchangeBybit, Market: domain.Market(market)})
		}
	}

	return scopes
}

func (s *localState) instrumentWorkers(cfg config.Config, logger *slog.Logger, clock upstream.Clock, jitter upstream.Jitter) ([]Worker, error) {
	workers := make([]Worker, 0)
	for _, scope := range enabledScopes(cfg) {
		provider, err := s.instrumentProvider(scope, logger)
		if err != nil {
			return nil, err
		}

		transportScope, interval := upstream.Bybit, cfg.Exchanges.Bybit.Instruments.RefreshInterval
		if scope.Exchange == domain.ExchangeBinance {
			interval = cfg.Exchanges.Binance.Instruments.RefreshInterval
			transportScope = upstream.BinanceSpot
			if scope.Market == domain.MarketLinear {
				transportScope = upstream.BinanceLinear
			}
		}

		gate, err := upstream.NewCycleGate(clock, jitter, cfg.HTTPClient.Retry, interval)
		if err != nil {
			return nil, fmt.Errorf("initialize instrument cycle: %w", err)
		}

		cycles := upstream.NewBoundedCycle(gate, s.exchanges.admission, transportScope, upstream.Instruments)
		refresher := instrument.NewRefresher(provider, s.instruments, clock.Now, func(event instrument.RefreshEvent) {
			s.instrumentMetrics.Observe(event)
			if event.Error != nil {
				logger.Warn("Instrument refresh failed", "exchange", event.Scope.Exchange, "market", event.Scope.Market, "error", event.Error)
			} else {
				logger.Info("Instrument snapshot published", "exchange", event.Scope.Exchange, "market", event.Scope.Market, "size", event.Size, "updated_at", event.UpdatedAt)
			}
		})
		workers = append(workers, func(ctx context.Context) error { return refresher.Run(ctx, cycles) })
	}

	return workers, nil
}

func (s *localState) instrumentProvider(scope application.Scope, logger *slog.Logger) (instrument.Provider, error) {
	switch scope {
	case application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}:
		return binance.NewSpotInstrumentProvider(s.exchanges.binance[upstream.BinanceSpot], logger), nil
	case application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}:
		return binance.NewLinearInstrumentProvider(s.exchanges.binance[upstream.BinanceLinear], logger), nil
	case application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}:
		return bybit.NewSpotInstrumentProvider(s.exchanges.bybit, logger), nil
	case application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}:
		return bybit.NewLinearInstrumentProvider(s.exchanges.bybit, logger), nil
	default:
		return nil, application.ErrUnsupportedOperation
	}
}
