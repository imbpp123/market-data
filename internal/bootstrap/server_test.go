package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeBecomesReadyAfterLocalInitialization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cfg := config.Defaults()
		state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		result := make(chan error, 1)
		go func() {
			result <- state.serve(ctx, cfg, testLogger(), listener)
		}()
		defer func() {
			cancel()
			require.NoError(t, <-result)
		}()
		synctest.Wait()
		transport := &http.Transport{DialContext: listener.dial}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://local/ready", nil)
		require.NoError(t, err)

		response, err := client.Do(request)

		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		assert.Equal(t, http.StatusOK, response.StatusCode)
	})
}

func TestServeCancelsAndWaitsForWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cfg := config.Defaults()
		state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		started := make(chan struct{})
		stopped := make(chan struct{})
		worker := func(ctx context.Context) error {
			defer close(stopped)
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		result := make(chan error, 1)
		go func() {
			result <- state.serve(ctx, cfg, testLogger(), listener, worker)
		}()
		<-started

		cancel()

		require.NoError(t, <-result)
		assert.True(t, channelClosed(stopped), "server returned before its worker stopped")
		assert.True(t, channelClosed(listener.closed), "listener remains open")
	})
}

func TestWorkerFailureStopsServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		expected := errors.New("worker unavailable")
		worker := func(context.Context) error {
			return expected
		}

		err = state.serve(t.Context(), cfg, testLogger(), listener, worker)

		assert.ErrorIs(t, err, expected)
		assert.True(t, channelClosed(listener.closed), "listener remains open")
	})
}

func TestShutdownStopsWaitingAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cfg := config.Defaults()
		state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		started := make(chan struct{})
		releaseWorker := make(chan struct{})
		worker := func(context.Context) error {
			close(started)
			<-releaseWorker
			return nil
		}
		result := make(chan error, 1)
		go func() {
			result <- state.serve(ctx, cfg, testLogger(), listener, worker)
		}()
		<-started
		shutdownStarted := time.Now()

		cancel()
		err = <-result
		close(releaseWorker)

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, cfg.Server.ShutdownTimeout, time.Since(shutdownStarted))
		synctest.Wait()
	})
}

func TestRunRejectsInvalidConfigBeforeStartingWorkers(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Port = 0
	var started atomic.Bool
	worker := func(context.Context) error {
		started.Store(true)
		return nil
	}

	err := Run(t.Context(), cfg, testLogger(), worker)

	assert.Error(t, err)
	assert.False(t, started.Load())
}

func TestRunRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := Run(ctx, config.Defaults(), testLogger())

	assert.ErrorIs(t, err, context.Canceled)
}

func TestServeReturnsListenerFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		require.NoError(t, listener.Close())

		err = state.serve(t.Context(), cfg, testLogger(), listener)

		assert.ErrorIs(t, err, net.ErrClosed)
	})
}

func TestWorkerShutdownFailureIsPreserved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cfg := config.Defaults()
		state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		expected := errors.New("worker cleanup failed")
		started := make(chan struct{})
		worker := func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return expected
		}
		result := make(chan error, 1)
		go func() {
			result <- state.serve(ctx, cfg, testLogger(), listener, worker)
		}()
		<-started

		cancel()

		assert.ErrorIs(t, <-result, expected)
	})
}

func TestShutdownClosesAnIdleConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cfg := config.Defaults()
		state, err := newLocalState(int64(cfg.Klines.MaxHistoryCandles), time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		result := make(chan error, 1)
		go func() {
			result <- state.serve(ctx, cfg, testLogger(), listener)
		}()
		client, err := listener.dial(ctx, "tcp", "local")
		require.NoError(t, err)
		defer func() { _ = client.Close() }()
		synctest.Wait()

		cancel()
		require.NoError(t, <-result)

		_, err = client.Write([]byte("GET /health HTTP/1.1\r\nHost: local\r\n\r\n"))
		assert.Error(t, err, "connection remains usable after shutdown")
	})
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// Pipe connections exercise the real HTTP server without external sockets or sleeps.
type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		connections: make(chan net.Conn),
		closed:      make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-l.connections:
		return connection, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })

	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()

	select {
	case l.connections <- server:
		return client, nil
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}
