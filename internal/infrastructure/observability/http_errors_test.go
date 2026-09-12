package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"
	httptransport "market-data/internal/transport/http"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedStatsReader struct{ err error }

func (r failedStatsReader) Validate(marketstats.Query) error { return nil }

func (r failedStatsReader) List(context.Context, marketstats.Query) ([]domain.MarketStats, error) {
	return nil, r.err
}

func TestSentryHTTPPreservesApplicationFailureCodes(t *testing.T) {
	cases := []struct {
		name                    string
		err                     error
		canceled                bool
		status                  int
		responseCode, eventCode string
	}{
		{name: "unready snapshot", err: application.ErrDataNotReady, status: 503, responseCode: "data_not_ready"},
		{name: "overload", err: application.ErrServiceOverloaded, status: 503, responseCode: "service_overloaded", eventCode: "service_overloaded"},
		{name: "cooldown", err: application.ErrUpstreamUnavailable, status: 503, responseCode: "upstream_unavailable", eventCode: "upstream_unavailable"},
		{name: "upstream", err: fmt.Errorf("secret source: %w", application.ErrUpstream), status: 502, responseCode: "upstream_error", eventCode: "upstream_error"},
		{name: "normalization", err: application.ErrInvalidUpstreamData, status: 502, responseCode: "invalid_upstream_data", eventCode: "invalid_upstream_data"},
		{name: "attempt limit", err: application.ErrUpstreamAttemptLimit, status: 502, responseCode: "upstream_attempt_limit", eventCode: "upstream_attempt_limit"},
		{name: "incomplete", err: application.ErrIncompleteData, status: 502, responseCode: "incomplete_data", eventCode: "incomplete_data"},
		{name: "deadline", err: fmt.Errorf("secret deadline: %w", context.DeadlineExceeded), status: 504, responseCode: "request_timeout", eventCode: "request_timeout"},
		{name: "application timeout", err: application.ErrRequestTimeout, status: 504, responseCode: "request_timeout", eventCode: "request_timeout"},
		{name: "wrapped cancellation", err: fmt.Errorf("secret canceled operation: %w", context.Canceled), status: 504, responseCode: "request_timeout"},
		{name: "joined cancellation", err: errors.Join(application.ErrUpstream, context.Canceled), status: 504, responseCode: "request_timeout"},
		{name: "canceled HTTP request", canceled: true, status: 504, responseCode: "request_timeout"},
		{name: "storage", err: errors.New("secret storage failure"), status: 500, responseCode: "internal_error", eventCode: "internal_error"},
		{name: "invalid filter", err: application.ErrInvalidFilter, status: 400, responseCode: "invalid_filter"},
		{name: "success", status: 200},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sink := &eventSink{}
			adapter := testSentry(t, sink)
			var reader httptransport.MarketStatsReader = failedStatsReader{err: tt.err}
			if tt.err == application.ErrDataNotReady {
				scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
				reader = marketstats.NewReader(memory.NewMarketStatsRepository(), []application.Scope{scope})
			}
			routes := httptransport.NewSnapshotHandlers(nil, nil, reader, time.Second, 1)
			handler := adapter.HTTP(httptransport.NewAPIHandler(func() bool { return true }, 8192, routes), routes, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			request := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/market-stats?exchange=bybit&market=spot", nil)
			request.Header.Set("Authorization", "secret-header")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.Equal(t, tt.status, response.Code)
			if tt.responseCode != "" {
				assert.Contains(t, response.Body.String(), `"code":"`+tt.responseCode+`"`)
			}
			var events []*sentry.Event
			for _, event := range sink.events {
				if event.Type != "transaction" {
					events = append(events, event)
				}
			}
			if tt.eventCode == "" {
				assert.Empty(t, events)
			} else {
				require.Len(t, events, 1)
				assert.Equal(t, tt.eventCode, events[0].Message)
				assert.Equal(t, "/api/v1/market-stats", events[0].Tags["route"])
				assert.Equal(t, fmt.Sprint(tt.status), events[0].Tags["status"])
				assert.Equal(t, "bybit", events[0].Tags["exchange"])
				assert.Equal(t, "spot", events[0].Tags["market"])
			}
			encoded, err := json.Marshal(sink.events)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "secret")
			assert.NotContains(t, response.Body.String(), "secret")
		})
	}
}

func TestSentryHTTPUnclassifiedFailureRetainsStatus(t *testing.T) {
	sink := &eventSink{}
	adapter := testSentry(t, sink)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	routes := map[string]http.Handler{"/metrics": next}
	handler := adapter.HTTP(next, routes, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))

	assert.Equal(t, 503, response.Code)
	var failures []*sentry.Event
	for _, event := range sink.events {
		if event.Type != "transaction" {
			failures = append(failures, event)
		}
	}
	require.Len(t, failures, 1)
	assert.Equal(t, "http_error", failures[0].Message)
	assert.Equal(t, "503", failures[0].Tags["status"])
	assert.Equal(t, "/metrics", failures[0].Tags["route"])
}
