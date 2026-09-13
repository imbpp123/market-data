package observability

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
