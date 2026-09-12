package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthProbe(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wildcard  bool
		canceled  bool
		wantError bool
	}{
		{name: "healthy", status: 200},
		{name: "wildcard listener", status: 200, wildcard: true},
		{name: "unhealthy", status: 503, wantError: true},
		{name: "redirect rejected", status: 302, wantError: true},
		{name: "canceled", status: 200, canceled: true, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/health", r.URL.Path)
				assert.Equal(t, "GET", r.Method)
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
			require.NoError(t, err)
			port, err := strconv.Atoi(portText)
			require.NoError(t, err)
			if tc.wildcard {
				host = "0.0.0.0"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}

			err = checkHealth(ctx, host, port)

			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHealthProbeDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now())
		defer cancel()

		err := checkHealth(ctx, "127.0.0.1", 1)

		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestRunHealthcheckDoesNotStartService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)

	err = run(t.Context(), []string{"-healthcheck"}, []string{"MDS_SERVER_HOST=" + host, "MDS_SERVER_PORT=" + port}, io.Discard, slog.New(slog.NewJSONHandler(io.Discard, nil)), func(context.Context, config.Config, *slog.Logger) error {
		t.Error("healthcheck started the service")
		return nil
	})

	assert.NoError(t, err)
}
