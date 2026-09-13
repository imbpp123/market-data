package bootstrap

import (
	"context"
	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"io"
	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"net"
	"net/http"
	"os"
	"strconv"
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

	source, err := os.Open("/etc/market-data/config.yaml")
	require.NoError(t, err)
	// The probe flag belongs to the test runner, not the service configuration.
	environment := []string{}
	for _, entry := range os.Environ() {
		if entry != "MDS_RELEASE_CONTAINER_PROBE=1" {
			environment = append(environment, entry)
		}
	}
	cfg, err := config.Load(source, environment)
	require.NoError(t, err)
	require.NoError(t, source.Close())
	httpAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Server.HTTP.Port))
	connection, err := grpc.NewClient(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Server.GRPC.Port)), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	api := pb.NewMarketDataServiceClient(connection)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = api.ListInstruments(ctx, &pb.ListInstrumentsRequest{})
	assertUnreadyRPC(t, err)
	_, err = api.ListTickers(ctx, &pb.ListTickersRequest{})
	assertUnreadyRPC(t, err)
	_, err = api.ListMarketStats(ctx, &pb.ListMarketStatsRequest{})
	assertUnreadyRPC(t, err)
	end := time.Now().UTC().Truncate(time.Minute)
	_, err = api.GetKlines(ctx, klineRequest(application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}, end.Add(-time.Minute), end))
	assertUnreadyRPC(t, err)
	client := &http.Client{Timeout: 2 * time.Second}
	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/health", 200, `"status":"ok"`},
		{"/ready", 200, `"status":"ok"`},
		{"/api/v1/instruments", 404, "not_found"},
		{"/api/v1/tickers", 404, "not_found"},
		{"/api/v1/market-stats", 404, "not_found"},
		{"/api/v1/klines", 404, "not_found"},
		{"/api/v1/other", 404, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+httpAddress+tc.path, nil)
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
