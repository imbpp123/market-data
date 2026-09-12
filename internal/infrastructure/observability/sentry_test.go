package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type eventSink struct {
	mu     sync.Mutex
	events []*sentry.Event
	wait   bool
	closed bool
}

func (s *eventSink) Configure(sentry.ClientOptions) {}

func (s *eventSink) SendEvent(event *sentry.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *eventSink) Flush(time.Duration) bool { return !s.wait }

func (s *eventSink) FlushWithContext(ctx context.Context) bool {
	if s.wait {
		<-ctx.Done()
		return false
	}
	return true
}

func (s *eventSink) Close() { s.closed = true }

func testSentry(t *testing.T, sink *eventSink) *Sentry {
	t.Helper()
	adapter, err := newSentry(config.Sentry{Enabled: true, DSN: "https://public@example.invalid/1", Environment: "test", TracesSampleRate: 1}, sink)
	require.NoError(t, err)
	t.Cleanup(adapter.Close)
	return adapter
}

func TestSentryReportsErrorsPanicAndTracesWithoutPayloads(t *testing.T) {
	sink := &eventSink{}
	adapter := testSentry(t, sink)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	routes := map[string]http.Handler{"/api/v1/klines": http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret panic payload") })}
	handler := adapter.HTTP(routes["/api/v1/klines"], routes, logger)
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/klines?exchange=bybit&market=linear&symbol=BTCUSDT&interval=1m&token=secret-query", nil)
	request.Header.Set("Authorization", "secret-header")

	handler.ServeHTTP(response, request)
	adapter.Report(errors.New("secret storage error"), map[string]string{"operation": "storage"})
	started := time.Now().Add(-time.Second)
	adapter.ObserveExchange(upstream.Event{Scope: upstream.Bybit, Market: domain.MarketLinear, Operation: upstream.Tickers, StartedAt: started, Duration: time.Second, Error: application.ErrUpstream})

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	require.True(t, adapter.Flush(t.Context()))
	encoded, err := json.Marshal(sink.events)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "secret")
	assert.NotContains(t, logs.String(), "secret")
	assert.NotContains(t, response.Body.String(), "secret")
	assert.Contains(t, string(encoded), "BTCUSDT")
	assert.Contains(t, string(encoded), "exchange.tickers")
	assert.Contains(t, string(encoded), "HTTP /api/v1/klines")
	assert.Contains(t, string(encoded), "Operation panicked")
	assert.Contains(t, string(encoded), "stacktrace")
	assert.Contains(t, string(encoded), "upstream_error")
}

func TestSentryFlushHonorsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &eventSink{wait: true}
		adapter := testSentry(t, sink)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		started := time.Now()

		assert.False(t, adapter.Flush(ctx))

		assert.Equal(t, time.Second, time.Since(started))
	})
}

func TestDisabledSentryDoesNotCreateClient(t *testing.T) {
	adapter, err := NewSentry(config.Sentry{DSN: "invalid and disabled"})
	require.NoError(t, err)
	require.Nil(t, adapter)

	adapter.Report(application.ErrInternal, nil)
	adapter.Panic(nil)
	adapter.Trace(t.Context(), "test", nil)()
	assert.True(t, adapter.Flush(t.Context()))
	adapter.Close()
}

func TestSentryZeroTraceRateKeepsErrors(t *testing.T) {
	sink := &eventSink{}
	adapter, err := newSentry(config.Sentry{Enabled: true, DSN: "https://public@example.invalid/1", Environment: "test"}, sink)
	require.NoError(t, err)
	defer adapter.Close()

	adapter.Trace(t.Context(), "test", nil)()
	adapter.Report(application.ErrInternal, nil)
	adapter.Report(context.Canceled, nil)

	require.Len(t, sink.events, 1)
	assert.Equal(t, "internal_error", sink.events[0].Message)
}
