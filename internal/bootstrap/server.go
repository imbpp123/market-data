// Package bootstrap owns the HTTP server and the lifetime of process workers.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"market-data/internal/config"
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

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)))
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}

	return serve(ctx, cfg, logger, listener, workers...)
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, listener net.Listener, workers ...Worker) error {
	root, cancel := context.WithCancel(ctx)
	defer cancel()

	var ready atomic.Bool
	server := &http.Server{
		Handler:           httptransport.NewHandler(ready.Load, cfg.Server.MaxQueryBytes),
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

			if err := worker(root); err != nil && (root.Err() == nil || !errors.Is(err, root.Err())) {
				failures <- fmt.Errorf("worker failed: %w", err)
			}
		}()
	}

	// Local routes are initialized before readiness; data repositories arrive in phase 04.
	ready.Store(true)
	logger.Info("HTTP server started", "address", listener.Addr().String(), "phase", "bootstrap")
	logger.Warn("Admission state is memory-only; exchange usage and cooldowns can survive a restart")

	var result error

	select {
	case <-ctx.Done():
	case result = <-failures:
	}

	ready.Store(false)
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
	logger.Info("HTTP server stopped")

	return result
}
