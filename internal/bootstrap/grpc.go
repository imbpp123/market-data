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
	"time"

	"google.golang.org/grpc/codes"
	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/config"
	"market-data/internal/infrastructure/observability"
	grpctransport "market-data/internal/transport/grpc"
	httptransport "market-data/internal/transport/http"
)

type grpcSettings struct {
	Address     string
	HTTPAddress string
	Transport   grpctransport.Settings
}

type listenFunc func(context.Context, string, string) (net.Listener, error)

func (state *localState) serveGRPC(ctx context.Context, cfg config.Config, logger *slog.Logger, settings grpcSettings, listen listenFunc, workers ...Worker) error {
	root, cancel := context.WithCancel(ctx)
	defer cancel()
	service, err := state.klineService(root, cfg, time.Now)
	if err != nil {
		return err
	}
	workers = append(workers, func(ctx context.Context) error { <-ctx.Done(); service.Wait(); return nil })
	state.rpcMetrics = observability.NewRPC()
	observe := state.rpcObserver(cfg, logger)
	data, err := grpctransport.NewServer(root, grpctransport.Readers{Instruments: instrument.NewReader(state.instruments, enabledScopes(cfg)), Tickers: ticker.NewReader(state.tickers, enabledScopes(cfg), time.Now), MarketStats: marketstats.NewReader(state.marketStats, enabledScopes(cfg)), Klines: service}, settings.Transport, observe)
	if err != nil {
		return err
	}
	routes := make(map[string]http.Handler)
	operationWorkers, err := state.operationWorkers(cfg, logger, routes, time.Now)
	if err != nil {
		_ = data.Close()
		return err
	}
	workers = append(workers, operationWorkers...)
	operations := &http.Server{Handler: state.telemetry.HTTP(httptransport.NewAPIHandler(state.ready.Load, cfg.Server.HTTP.MaxQueryBytes, routes), routes, logger), ReadHeaderTimeout: cfg.Server.HTTP.ReadHeaderTimeout, IdleTimeout: cfg.Server.HTTP.IdleTimeout, WriteTimeout: cfg.Server.HTTP.WriteTimeout, MaxHeaderBytes: cfg.Server.HTTP.MaxHeaderBytes, BaseContext: func(net.Listener) context.Context { return root }}
	return state.runDual(root, cancel, cfg.Server.ShutdownTimeout, settings.Address, settings.HTTPAddress, data, operations, listen, workers...)
}

func (state *localState) runDual(ctx context.Context, cancel context.CancelFunc, timeout time.Duration, dataAddress, httpAddress string, data *grpctransport.Server, operations *http.Server, listen listenFunc, workers ...Worker) error {
	defer state.ready.Store(false)
	defer cancel()
	defer func() { _ = data.Close() }()
	defer func() { _ = operations.Close() }()
	if err := ctx.Err(); err != nil {
		return err
	}
	dataListener, err := listen(ctx, "tcp", dataAddress)
	if err != nil {
		return fmt.Errorf("listen gRPC: %w", err)
	}
	defer func() { _ = dataListener.Close() }()
	httpListener, err := listen(ctx, "tcp", httpAddress)
	if err != nil {
		return fmt.Errorf("listen operational HTTP: %w", err)
	}
	defer func() { _ = httpListener.Close() }()
	if err := ctx.Err(); err != nil {
		return err
	}
	var owned sync.WaitGroup
	failures := make(chan error, len(workers)+2)
	start := func(run func() error) {
		owned.Add(1)
		go func() {
			defer owned.Done()
			if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
				failures <- err
			}
		}()
	}
	state.ready.Store(true)
	start(func() error { return data.Serve(dataListener) })
	start(func() error { return operations.Serve(httpListener) })
	for _, worker := range workers {
		start(func() error { return state.runWorker(ctx, worker) })
	}
	var result error
	select {
	case <-ctx.Done():
	case result = <-failures:
	}
	state.ready.Store(false)
	data.StopAdmission()
	cancel()
	shutdown, stop := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer stop()
	// Both drains and worker waits consume this same deadline.
	finished := make(chan error, 1)
	go func() {
		results := make(chan error, 2)
		go func() { results <- data.Shutdown(shutdown) }()
		go func() { results <- operations.Shutdown(shutdown) }()
		err := errors.Join(<-results, <-results)
		owned.Wait()
		finished <- err
	}()
	select {
	case err := <-finished:
		result = errors.Join(result, err)
		close(failures)
		for err := range failures {
			result = errors.Join(result, err)
		}
	case <-shutdown.Done():
		result = errors.Join(result, fmt.Errorf("shutdown deadline: %w", shutdown.Err()))
		_ = data.Close()
		_ = operations.Close()
	}
	state.telemetry.Flush(shutdown)
	return result
}

func (state *localState) rpcObserver(cfg config.Config, logger *slog.Logger) grpctransport.Observer {
	return func(ctx context.Context, method string) func(grpctransport.Event) {
		finishTrace := state.telemetry.Trace(ctx, "RPC "+method, map[string]string{"method": method})
		finishMetrics := func(grpctransport.Event) {}
		if collectStatistics(cfg) {
			finishMetrics = state.rpcMetrics.Start(ctx, method)
		}
		return func(event grpctransport.Event) {
			defer finishTrace()
			finishMetrics(event)
			tags := map[string]string{"method": method, "status": event.Code.String(), "reason": event.Reason}
			if event.Panic {
				state.telemetry.Panic(tags)
			} else if reportRPCError(event) {
				state.telemetry.Report(event.Error, tags)
			}
			logger.Debug("RPC completed", "method", method, "status", event.Code.String(), "reason", event.Reason, "duration", event.Duration, "request_bytes", event.RequestBytes, "response_bytes", event.ResponseBytes)
		}
	}
}

func reportRPCError(event grpctransport.Event) bool {
	if event.Error == nil || errors.Is(event.Error, context.Canceled) || errors.Is(event.Error, application.ErrDataNotReady) {
		return false
	}
	switch event.Code {
	case codes.OK, codes.Canceled, codes.InvalidArgument, codes.NotFound, codes.Unimplemented:
		return false
	default:
		return true
	}
}

func configuredGRPC(cfg config.Config) grpcSettings {
	return grpcSettings{
		Address:     net.JoinHostPort(cfg.Server.GRPC.Host, strconv.Itoa(cfg.Server.GRPC.Port)),
		HTTPAddress: net.JoinHostPort(cfg.Server.HTTP.Host, strconv.Itoa(cfg.Server.HTTP.Port)),
		Transport: grpctransport.Settings{
			SnapshotTimeout: cfg.Server.SnapshotTimeout, KlineTimeout: cfg.Klines.RequestTimeout,
			WriteGrace: config.WriteGrace, MaxSnapshots: cfg.Server.MaxSnapshotRequests, MaxKlines: cfg.Klines.MaxCallers,
			MaxRequestBytes: cfg.Server.GRPC.MaxRequestBytes, MaxResponseBytes: cfg.Server.GRPC.MaxResponseBytes, MaxHeaderBytes: cfg.Server.GRPC.MaxHeaderBytes,
			ReadHeaderTimeout: cfg.Server.HTTP.ReadHeaderTimeout, IdleTimeout: cfg.Server.HTTP.IdleTimeout,
		},
	}
}
