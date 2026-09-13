// Package bootstrap owns both servers and the lifetime of process workers.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/observability"
)

// Worker must stop when its context is canceled. Its owner waits during shutdown.
type Worker func(context.Context) error

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, workers ...Worker) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
	if err != nil {
		return err
	}

	state.telemetry, err = observability.NewSentry(cfg.Observability.Sentry)
	if err != nil {
		return err
	}
	defer state.telemetry.Close()

	state.exchanges, err = newExchangeClients(cfg, http.DefaultTransport, upstream.SystemClock{}, func(ceiling time.Duration) time.Duration {
		return time.Duration(rand.Int64N(int64(ceiling)))
	}, func(event upstream.Event) {
		if collectStatistics(cfg) {
			state.exchangeMetrics.Observe(event)
		}
		state.telemetry.ObserveExchange(event)
		if event.Error != nil {
			logger.Warn("Upstream attempt failed", "scope", event.Scope, "path", event.Path, "status", event.Status, "code", event.Code, "error", observability.ErrorCode(event.Error), "reason", event.FailureReason)
		}
	})
	if err != nil {
		return fmt.Errorf("initialize exchange clients: %w", err)
	}

	state.exchanges.admission.SetDiagnosticObserver(func(event upstream.DiagnosticEvent) {
		fields := []any{"scope", event.Scope, "reason", event.Reason}
		if event.Operation != "" {
			fields = append(fields, "operation", event.Operation, "next_eligible", event.NextEligible, "request_weight", event.RequestWeight, "funding_request", event.FundingRequest)
		}
		if event.Reason == "exchange_cooldown" {
			fields = append(fields, "next_eligible", event.NextEligible)
		}
		if len(event.Limits) > 0 {
			fields = append(fields, "limits", event.Limits, "threshold_percent", event.ThresholdPercent)
		}
		logger.Info("Admission state changed", fields...)
	})

	instrumentWorkers, err := state.instrumentWorkers(cfg, logger, upstream.SystemClock{}, func(ceiling time.Duration) time.Duration {
		return time.Duration(rand.Int64N(int64(ceiling)))
	})
	if err != nil {
		return err
	}
	workers = append(workers, instrumentWorkers...)
	currentWorkers, err := state.currentWorkers(cfg, logger, upstream.SystemClock{}, func(ceiling time.Duration) time.Duration {
		return time.Duration(rand.Int64N(int64(ceiling)))
	})
	if err != nil {
		return err
	}
	workers = append(workers, currentWorkers...)

	logger.Warn("Admission state is memory-only; exchange usage and cooldowns can survive a restart")

	var listenConfig net.ListenConfig
	return state.serveGRPC(ctx, cfg, logger, configuredGRPC(cfg), listenConfig.Listen, workers...)
}
