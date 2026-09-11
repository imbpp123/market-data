package httptransport

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthAndReadiness(t *testing.T) {
	var ready atomic.Bool
	handler := NewHandler(ready.Load, 32)

	for _, tc := range []struct {
		path, method string
		initialized  bool
		status       int
		code         string
	}{
		{"/health", "GET", false, 200, ""},
		{"/ready", "GET", false, 503, "data_not_ready"},
		{"/ready", "GET", true, 200, ""},
		{"/health", "POST", true, 405, "method_not_allowed"},
		{"/ready", "HEAD", true, 405, "method_not_allowed"},
		{"/api/tickers", "GET", true, 404, "not_found"},
		{"/metrics", "GET", true, 404, "not_found"},
		{"/debug/stats", "GET", true, 404, "not_found"},
		{"/health?" + strings.Repeat("x", 33), "GET", true, 414, "request_too_large"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			ready.Store(tc.initialized)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			assert.Equal(t, tc.status, response.Code)

			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))

			if tc.code != "" {
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))

				assert.Equal(t, tc.code, body.Error.Code)
			}
		})
	}

	request := httptest.NewRequest("GET", "/health", strings.NewReader("body"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, 400, response.Code)
}
