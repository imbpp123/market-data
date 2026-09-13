package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/observability"
)

func (s *localState) operationWorkers(cfg config.Config, logger *slog.Logger, routes map[string]http.Handler, now func() time.Time) ([]Worker, error) {
	providers := map[domain.Exchange]kline.Provider{
		domain.ExchangeBinance: binance.NewKlineProvider(nil, nil),
		domain.ExchangeBybit:   bybit.NewKlineProvider(nil),
	}
	var scopes []kline.RetentionScope
	for _, scope := range enabledScopes(cfg) {
		intervals, err := providers[scope.Exchange].SupportedTimeframes(scope.Market)
		if err != nil {
			return nil, err
		}
		for _, interval := range intervals {
			scopes = append(scopes, kline.RetentionScope{Scope: scope, Interval: interval})
		}
	}
	retention, err := kline.NewRetention(s.klines, scopes, int64(cfg.Klines.MaxHistoryCandles), now)
	if err != nil {
		return nil, err
	}
	statistics := &observability.Statistics{Instruments: s.instrumentMetrics, Current: s.currentMetrics,
		Klines: s.klineMetrics, Exchanges: s.exchangeMetrics, Inventory: s.inventory, Scopes: enabledScopes(cfg)}
	if s.exchanges != nil {
		statistics.Admission = s.exchanges.admission
	}
	if cfg.Observability.Prometheus.Enabled {
		routes[cfg.Observability.Prometheus.Path] = statistics.PrometheusHandler()
	}
	if cfg.Observability.Stats.EndpointEnabled {
		routes["/debug/stats"] = statistics.DebugHandler()
	}
	workers := []Worker{func(ctx context.Context) error {
		return periodic(ctx, cfg.Storage.CleanupInterval, true, func(ctx context.Context) {
			started := now()
			finish := s.telemetry.Trace(ctx, "retention", nil)
			defer finish()
			if err := retention.Clean(ctx); err != nil {
				logger.Warn("Candle cleanup failed", "operation", "retention", "error", observability.ErrorCode(err))
				s.telemetry.Report(err, map[string]string{"operation": "retention"})
				return
			}
			logger.Info("Candle cleanup completed", "operation", "retention", "duration", now().Sub(started))
		})
	}}
	return workers, nil
}

// Wait after completion, so a slow pass cannot queue another pass.
func periodic(ctx context.Context, interval time.Duration, immediate bool, run func(context.Context)) error {
	if immediate && ctx.Err() == nil {
		run(ctx)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			run(ctx)
			timer.Reset(interval)
		}
	}
}

func (s *localState) runWorker(ctx context.Context, worker Worker) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.telemetry.Panic(map[string]string{"operation": "worker"})
			err = fmt.Errorf("worker panicked: %w", application.ErrInternal)
		}
	}()
	err = worker(ctx)
	s.telemetry.Report(err, map[string]string{"operation": "worker"})
	return err
}

// Exporters can request counters even when standalone collection is disabled.
func collectStatistics(cfg config.Config) bool {
	return cfg.Observability.Stats.Enabled || cfg.Observability.Stats.EndpointEnabled || cfg.Observability.Prometheus.Enabled
}
