package bootstrap

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/config"
	"market-data/internal/infrastructure/storage/memory"
	grpctransport "market-data/internal/transport/grpc"
)

func startTestAPI(t *testing.T, readers grpctransport.Readers, settings grpctransport.Settings) pb.MarketDataServiceClient {
	t.Helper()
	scopes := enabledScopes(config.Defaults())
	if readers.Instruments == nil {
		readers.Instruments = instrument.NewReader(memory.NewInstrumentRepository(), scopes)
	}
	if readers.Tickers == nil {
		readers.Tickers = ticker.NewReader(memory.NewTickerRepository(), scopes, time.Now)
	}
	if readers.MarketStats == nil {
		readers.MarketStats = marketstats.NewReader(memory.NewMarketStatsRepository(), scopes)
	}
	server, err := grpctransport.NewServer(t.Context(), readers, settings, nil)
	require.NoError(t, err)
	listener := newPipeListener()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.dial(ctx, "tcp", "fixture") }), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(settings.MaxResponseBytes)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close(); _ = server.Close(); <-done })
	return pb.NewMarketDataServiceClient(connection)
}

func klineRequest(scope application.Scope, from, to time.Time) *pb.GetKlinesRequest {
	return &pb.GetKlinesRequest{Exchange: proto.String(string(scope.Exchange)), Market: proto.String(string(scope.Market)), Symbol: proto.String("BTCUSDT"), Interval: proto.String("1m"), From: timestamppb.New(from), To: timestamppb.New(to)}
}
