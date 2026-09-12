package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"market-data/internal/config"
)

func TestRunValidatesBeforeStartup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args, env []string
		wantError bool
	}{
		{"bad config", nil, []string{"MDS_SERVER_PORT=0"}, true},
		{"unknown env", nil, []string{"MDS_UNKNOWN=1"}, true},
		{"missing file", []string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}, nil, true},
		{"unknown flag", []string{"-missing"}, nil, true},
		{"positional argument", []string{"argument"}, nil, true},
		{"check only", []string{"-check-config"}, nil, false},
		{"help", []string{"-help"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := false
			err := run(context.Background(), tc.args, tc.env, io.Discard, slog.New(slog.NewJSONHandler(io.Discard, nil)), func(context.Context, config.Config, *slog.Logger) error {
				started = true

				return errors.New("startup must not be called")
			})
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.False(t, started)
		})
	}
}

func TestRunPassesFinalSettingsAndPreservesStartupError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server: {port: 8082}"), 0600))

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	expected := errors.New("listen failed")
	var received config.Config
	err := run(t.Context(), []string{"-config", path}, []string{"MDS_SERVER_PORT=8083"}, io.Discard, logger, func(_ context.Context, cfg config.Config, _ *slog.Logger) error {
		received = cfg

		return expected
	})
	assert.ErrorIs(t, err, expected)
	assert.Equal(t, 8083, received.Server.Port)

	assert.NotContains(t, logs.String(), "not implemented")
}
