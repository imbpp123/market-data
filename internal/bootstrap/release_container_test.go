package bootstrap

import (
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The packaging check runs this probe inside the isolated service container.
func TestReleaseContainerProbe(t *testing.T) {
	if os.Getenv("MDS_RELEASE_CONTAINER_PROBE") != "1" {
		t.Skip("run with make docker-verify")
	}
	assert.Equal(t, 65532, os.Getuid())
	certificates, err := os.ReadFile("/etc/ssl/certs/ca-certificates.crt")
	require.NoError(t, err)
	assert.Contains(t, string(certificates), "BEGIN CERTIFICATE")
	file, err := os.OpenFile("/etc/market-data/config.yaml", os.O_WRONLY, 0)
	if file != nil {
		_ = file.Close()
	}
	require.Error(t, err, "mounted configuration must reject writes")

	client := &http.Client{Timeout: 2 * time.Second}
	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/health", 200, `"status":"ok"`},
		{"/ready", 200, `"status":"ok"`},
		{"/api/v1/instruments", 503, "data_not_ready"},
		{"/api/v1/tickers", 503, "data_not_ready"},
		{"/api/v1/market-stats", 503, "data_not_ready"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), "GET", "http://127.0.0.1:8080"+tc.path, nil)
			require.NoError(t, err)
			response, err := client.Do(request)
			require.NoError(t, err)
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.status, response.StatusCode)
			assert.Contains(t, string(body), tc.body)
		})
	}
}
