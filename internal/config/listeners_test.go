package config

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndependentListenerOverrides(t *testing.T) {
	cfg := loadText(t, `server:
  grpc: {host: localhost, port: 9091, max_request_bytes: 4096, max_response_bytes: 1048576, max_header_bytes: 16384}
  http: {host: 127.0.0.1, port: 8081, write_timeout: 3s, max_query_bytes: 2048}
`, "MDS_SERVER_GRPC_PORT=9092", "MDS_SERVER_HTTP_PORT=8082", "MDS_SERVER_GRPC_MAX_RESPONSE_BYTES=2097152", "MDS_SERVER_HTTP_MAX_QUERY_BYTES=1024")
	assert.Equal(t, GRPCServer{Host: "localhost", Port: 9092, MaxRequestBytes: 4096, MaxResponseBytes: 2097152, MaxHeaderBytes: 16384}, cfg.Server.GRPC)
	assert.Equal(t, 8082, cfg.Server.HTTP.Port)
	assert.Equal(t, "127.0.0.1", cfg.Server.HTTP.Host)
	assert.Equal(t, 3*time.Second, cfg.Server.HTTP.WriteTimeout)
	assert.Equal(t, 1024, cfg.Server.HTTP.MaxQueryBytes)
	assert.Equal(t, 64, cfg.Server.MaxSnapshotRequests)
}

func TestRemovedListenerSettingsAreRejected(t *testing.T) {
	for _, key := range []string{"host", "port", "read_header_timeout", "idle_timeout", "max_header_bytes", "max_query_bytes", "write_timeout"} {
		t.Run(key, func(t *testing.T) {
			_, err := Load(strings.NewReader("server: {"+key+": 1}"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "server."+key)
			_, err = Load(nil, []string{"MDS_SERVER_" + strings.ToUpper(key) + "=1"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown environment setting")
		})
	}
}

func TestListenerByteBounds(t *testing.T) {
	cases := []struct{ protocol, key string }{
		{"grpc", "max_request_bytes"}, {"grpc", "max_response_bytes"}, {"grpc", "max_header_bytes"},
		{"http", "max_header_bytes"}, {"http", "max_query_bytes"},
	}
	for _, tc := range cases {
		for _, raw := range []string{"0", "-1", "null", "true", "1.5", `"8192"`, "9223372036854775808"} {
			t.Run(tc.protocol+"/"+tc.key+"/"+raw, func(t *testing.T) {
				_, err := Load(strings.NewReader(fmt.Sprintf("server: {%s: {%s: %s}}", tc.protocol, tc.key, raw)), nil)
				require.Error(t, err)
			})
		}
		if tc.protocol == "grpc" {
			t.Run(tc.key+" runtime overflow", func(t *testing.T) {
				_, err := Load(nil, []string{"MDS_SERVER_GRPC_" + strings.ToUpper(tc.key) + "=2147483648"})
				require.Error(t, err)
			})
		}
	}
}

func TestListenerAddressValidation(t *testing.T) {
	cases := []struct {
		name, grpcHost, httpHost string
		grpcPort, httpPort       int
		valid                    bool
	}{
		{"defaults", "0.0.0.0", "0.0.0.0", 9090, 8080, true},
		{"same address", "127.0.0.1", "127.0.0.1", 8080, 8080, false},
		{"hostname case", "LOCALHOST", "localhost", 8080, 8080, false},
		{"wildcard", "0.0.0.0", "127.0.0.1", 8080, 8080, false},
		{"IPv6 wildcard", "::", "::1", 8080, 8080, false},
		{"mapped IPv4", "::ffff:127.0.0.1", "127.0.0.1", 8080, 8080, false},
		{"empty wildcard", "", "localhost", 8080, 8080, false},
		{"different IP", "127.0.0.1", "127.0.0.2", 8080, 8080, true},
		{"invalid gRPC host", "https://local", "127.0.0.1", 9090, 8080, false},
		{"invalid gRPC port", "localhost", "localhost", 65536, 8080, false},
		{"zero gRPC port", "localhost", "localhost", 0, 8080, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Server.GRPC.Host, cfg.Server.HTTP.Host = tc.grpcHost, tc.httpHost
			cfg.Server.GRPC.Port, cfg.Server.HTTP.Port = tc.grpcPort, tc.httpPort
			err := cfg.Validate()
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestReservedExporterPaths(t *testing.T) {
	for _, path := range []string{"/health", "/ready", "/debug/stats", "/api", "/api/v1/instruments", "/api/v1/other"} {
		t.Run(path, func(t *testing.T) {
			cfg := Defaults()
			cfg.Observability.Prometheus.Path = path
			assert.Error(t, cfg.Validate())
		})
	}
}
