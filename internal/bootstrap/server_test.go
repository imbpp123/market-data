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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"market-data/internal/config"
)

func TestServeCancelsAndWaitsForWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := newPipeListener()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan struct{})
		stopped := make(chan struct{})
		result := make(chan error, 1)
		worker := func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			defer close(stopped)

			return ctx.Err()
		}
		go func() { result <- serve(ctx, config.Defaults(), testLogger(), listener, worker) }()
		<-started
		synctest.Wait()

		transport := &http.Transport{DialContext: listener.dial}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		response, err := client.Get("http://local/ready")
		require.NoError(t, err)

		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)

		require.NoError(t, response.Body.Close())

		assert.Equal(t, 200, response.StatusCode)

		cancel()
		require.NoError(t, <-result)

		assert.True(t, channelClosed(stopped), "returned before worker stopped")

		assert.True(t, channelClosed(listener.closed), "listener remains open")
	})
}

func TestWorkerFailureStopsServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		expected := errors.New("worker unavailable")
		listener := newPipeListener()
		err := serve(context.Background(), config.Defaults(), testLogger(), listener, func(context.Context) error { return expected })
		assert.ErrorIs(t, err, expected)

		assert.True(t, channelClosed(listener.closed), "listener remains open")
	})
}

func TestShutdownDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		listener := newPipeListener()
		ctx, cancel := context.WithCancel(context.Background())
		started, release := make(chan struct{}), make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- serve(ctx, cfg, testLogger(), listener, func(context.Context) error {
				close(started)
				<-release

				return nil
			})
		}()
		<-started
		before := time.Now()
		cancel()
		err := <-result
		assert.ErrorIs(t, err, context.DeadlineExceeded)

		assert.Equal(t, cfg.Server.ShutdownTimeout, time.Since(before))

		close(release)
		synctest.Wait()
	})
}

func TestRunRejectsInvalidConfigBeforeStarting(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Port = 0
	var started atomic.Bool
	err := Run(context.Background(), cfg, testLogger(), func(context.Context) error {
		started.Store(true)
		return nil
	})
	assert.Error(t, err)
	assert.False(t, started.Load())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, Run(ctx, config.Defaults(), testLogger()), context.Canceled)
}

func TestListenerFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := newPipeListener()
		require.NoError(t, listener.Close())

		err := serve(context.Background(), config.Defaults(), testLogger(), listener)
		assert.ErrorIs(t, err, net.ErrClosed)
	})
}

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// Pipe connections exercise the real HTTP server without external sockets or sleeps.
type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
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

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080} }

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

func TestWorkerShutdownFailureIsPreserved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		expected := errors.New("worker cleanup failed")
		listener := newPipeListener()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		started := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- serve(ctx, config.Defaults(), testLogger(), listener, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return expected
			})
		}()

		<-started
		cancel()
		assert.ErrorIs(t, <-result, expected)
	})
}

func TestShutdownClosesAnIdleConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := newPipeListener()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		result := make(chan error, 1)
		go func() {
			result <- serve(ctx, config.Defaults(), testLogger(), listener)
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
