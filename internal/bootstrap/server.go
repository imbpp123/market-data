// Package bootstrap owns the HTTP server and the lifetime of process workers.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"market-data/internal/application/instrument"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/observability"
	httptransport "market-data/internal/transport/http"
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
			logger.Warn("Upstream attempt failed", "scope", event.Scope, "path", event.Path, "status", event.Status, "code", event.Code, "error", observability.ErrorCode(event.Error))
		}
	})
	if err != nil {
		return fmt.Errorf("initialize exchange clients: %w", err)
	}

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

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)))
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}

	return state.serve(ctx, cfg, logger, listener, workers...)
}

func (state *localState) serve(ctx context.Context, cfg config.Config, logger *slog.Logger, listener net.Listener, workers ...Worker) error {
	root, cancel := context.WithCancel(ctx)
	defer cancel()

	routes := httptransport.NewSnapshotHandlers(
		instrument.NewReader(state.instruments, enabledScopes(cfg)),
		ticker.NewReader(state.tickers, enabledScopes(cfg), time.Now),
		marketstats.NewReader(state.marketStats, enabledScopes(cfg)),
		cfg.Server.SnapshotTimeout, cfg.Server.MaxSnapshotRequests)
	service, err := state.klineService(root, cfg, time.Now)
	if err != nil {
		_ = listener.Close()
		return err
	}
	routes["/api/v1/klines"] = httptransport.NewKlinesHandler(service, cfg.Klines.RequestTimeout, cfg.Klines.MaxCallers)
	workers = append(workers, func(ctx context.Context) error {
		<-ctx.Done()
		service.Wait()
		return nil
	})

	operationWorkers, err := state.operationWorkers(cfg, logger, routes, time.Now)
	if err != nil {
		_ = listener.Close()
		return err
	}
	workers = append(workers, operationWorkers...)

	server := &http.Server{
		Handler:           state.telemetry.HTTP(httptransport.NewAPIHandler(state.ready.Load, cfg.Server.MaxQueryBytes, routes), routes, logger),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		WriteTimeout:      cfg.WriteTimeout(),
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return root },
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	var owned sync.WaitGroup
	failures := make(chan error, len(workers)+1)
	owned.Add(1)
	go func() {
		defer owned.Done()
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failures <- fmt.Errorf("serve HTTP: %w", err)
		}
	}()

	for _, worker := range workers {
		owned.Add(1)
		go func() {
			defer owned.Done()

			if err := state.runWorker(root, worker); err != nil && (root.Err() == nil || !errors.Is(err, root.Err())) {
				failures <- fmt.Errorf("worker failed: %w", err)
			}
		}()
	}

	// Local storage and routes are initialized; upstream snapshots may be unready.
	state.ready.Store(true)
	logger.Info("HTTP server started", "address", listener.Addr().String(), "phase", "bootstrap")
	logger.Warn("Admission state is memory-only; exchange usage and cooldowns can survive a restart")

	var result error

	select {
	case <-ctx.Done():
	case result = <-failures:
	}

	state.ready.Store(false)
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer stop()
	done := make(chan error, 1)
	go func() {
		err := server.Shutdown(shutdown)
		owned.Wait()
		done <- err
	}()

	select {
	case err := <-done:
		result = errors.Join(result, err)
		close(failures)
		for failure := range failures {
			result = errors.Join(result, failure)
		}
	case <-shutdown.Done():
		result = errors.Join(result, fmt.Errorf("shutdown deadline: %w", shutdown.Err()))
	}

	result = errors.Join(result, server.Close())
	if !state.telemetry.Flush(shutdown) {
		logger.Warn("Telemetry flush did not complete before shutdown deadline")
	}
	logger.Info("HTTP server stopped")

	return result
}
