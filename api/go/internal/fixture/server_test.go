package fixture

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func client(t *testing.T, limit int) pb.MarketDataServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pb.RegisterMarketDataServiceServer(server, Server{})
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(limit)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return pb.NewMarketDataServiceClient(connection)
}

func TestContractValuesAndPresence(t *testing.T) {
	api := client(t, ReceiveLimit)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	instruments, err := api.ListInstruments(ctx, &pb.ListInstrumentsRequest{Symbol: proto.String("presence")})
	require.NoError(t, err)
	require.Len(t, instruments.Instruments, 3)
	assert.Nil(t, instruments.Instruments[0].MinQty)
	assert.Nil(t, instruments.Instruments[0].FundingIntervalSeconds)
	assert.Nil(t, instruments.Instruments[0].DelistingTime)
	assert.Equal(t, proto.String("0"), instruments.Instruments[1].MinQty)
	assert.Equal(t, proto.Int64(0), instruments.Instruments[1].FundingIntervalSeconds)
	assert.Len(t, *instruments.Instruments[2].MinQty, 1024)
	assert.Equal(t, int32(123456789), instruments.Instruments[2].UpdatedAt.Nanos)
	tickers, err := api.ListTickers(ctx, &pb.ListTickersRequest{Symbol: proto.String("presence")})
	require.NoError(t, err)
	assert.Nil(t, tickers.Tickers[0].BidPrice)
	assert.Nil(t, tickers.Tickers[0].NextFundingInSeconds)
	assert.Equal(t, proto.String("0"), tickers.Tickers[1].BidPrice)
	assert.Equal(t, proto.Int64(0), tickers.Tickers[1].NextFundingInSeconds)
	stats, err := api.ListMarketStats(ctx, &pb.ListMarketStatsRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(9007199254740993), *stats.MarketStats[0].TradeCount)
	assert.Equal(t, "12345.1234567890123456789", stats.MarketStats[0].High)
	absent, err := api.ListMarketStats(ctx, &pb.ListMarketStatsRequest{Symbol: proto.String("presence")})
	require.NoError(t, err)
	assert.Nil(t, absent.MarketStats[0].PriceChange)
	assert.Nil(t, absent.MarketStats[0].TradeCount)
	assert.Equal(t, proto.String("0"), absent.MarketStats[1].PriceChange)
	assert.Equal(t, proto.Int64(0), absent.MarketStats[1].TradeCount)
}

func TestRequestPresenceAndUnknownFields(t *testing.T) {
	cases := []struct {
		name     string
		exchange *string
	}{
		{name: "omitted"}, {name: "empty", exchange: proto.String("")}, {name: "canonical", exchange: proto.String("binance")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := &pb.ListInstrumentsRequest{Exchange: tc.exchange, Symbol: proto.String("echo")}
			encoded, err := proto.Marshal(request)
			require.NoError(t, err)
			encoded = protowire.AppendTag(encoded, 99, protowire.BytesType)
			encoded = protowire.AppendString(encoded, "future")
			var decoded pb.ListInstrumentsRequest
			require.NoError(t, proto.Unmarshal(encoded, &decoded))
			result, err := client(t, ReceiveLimit).ListInstruments(t.Context(), &decoded)
			require.NoError(t, err)
			assert.Equal(t, tc.exchange, result.Instruments[0].MinQty)
			assert.NotEmpty(t, decoded.ProtoReflect().GetUnknown())
		})
	}
}

func TestCandleRanges(t *testing.T) {
	cases := []struct {
		name     string
		count    int
		presence bool
	}{
		{name: "empty"}, {name: "multi row", count: 3}, {name: "count presence", count: 2, presence: true}, {name: "maximum", count: 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := &pb.GetKlinesRequest{From: &timestamppb.Timestamp{Seconds: Epoch - 60000}, To: &timestamppb.Timestamp{Seconds: Epoch - 60000 + int64(tc.count)*60}}
			if tc.presence {
				request.Symbol = proto.String("presence")
			}
			result, err := client(t, ReceiveLimit).GetKlines(t.Context(), request)
			require.NoError(t, err)
			assert.Equal(t, "binance", result.Exchange)
			assert.Equal(t, "spot", result.Market)
			assert.Equal(t, "S0000USDT", result.Symbol)
			assert.Equal(t, "1m", result.Interval)
			require.Len(t, result.Klines, tc.count)
			for i, row := range result.Klines {
				assert.Equal(t, Epoch-60000+int64(i)*60, row.OpenTime.Seconds)
				assert.Equal(t, Decimal, row.Close)
				assert.Equal(t, int32(123456789), row.FetchedAt.Nanos)
			}
			if tc.presence {
				assert.Nil(t, result.Klines[0].TradesCount)
				assert.Equal(t, proto.Int64(0), result.Klines[1].TradesCount)
			}
		})
	}
}

func TestInvalidFixtureRanges(t *testing.T) {
	cases := []struct {
		name     string
		from, to *timestamppb.Timestamp
	}{
		{name: "missing"},
		{name: "invalid nanos", from: &timestamppb.Timestamp{Nanos: 1000000000}, to: Stamp()},
		{name: "invalid seconds", from: &timestamppb.Timestamp{Seconds: 253402300800}, to: Stamp()},
		{name: "reversed", from: Stamp(), to: &timestamppb.Timestamp{Seconds: Epoch - 60}},
		{name: "too many", from: &timestamppb.Timestamp{}, to: &timestamppb.Timestamp{Seconds: 60060}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client(t, ReceiveLimit).GetKlines(t.Context(), &pb.GetKlinesRequest{From: tc.from, To: tc.to})
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestStatusDetails(t *testing.T) {
	cases := []struct {
		name   string
		code   codes.Code
		reason string
	}{
		{name: "error", code: codes.InvalidArgument, reason: "invalid_filter"}, {name: "unknown-detail", code: codes.Unavailable}, {name: "native-error", code: codes.Unavailable},
		{name: "response-too-large", code: codes.ResourceExhausted, reason: "response_too_large"},
		{name: "request-canceled", code: codes.Canceled, reason: "request_canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client(t, ReceiveLimit).ListTickers(t.Context(), &pb.ListTickersRequest{Symbol: proto.String(tc.name)})
			require.Equal(t, tc.code, status.Code(err))
			reason := ""
			for _, detail := range status.Convert(err).Details() {
				if value, ok := detail.(*pb.ErrorDetail); ok {
					reason = value.Reason
				}
			}
			assert.Equal(t, tc.reason, reason)
		})
	}
}

func TestClientReceiveCap(t *testing.T) {
	_, err := client(t, 1).ListTickers(t.Context(), &pb.ListTickersRequest{})
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}

func TestClientDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancel()
	_, err := client(t, ReceiveLimit).ListTickers(ctx, &pb.ListTickersRequest{Symbol: proto.String("wait")})
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := client(t, ReceiveLimit).ListTickers(ctx, &pb.ListTickersRequest{})
	assert.Equal(t, codes.Canceled, status.Code(err))
}

func TestFullProfileFitsResponseLimit(t *testing.T) {
	fixtures := []proto.Message{Instruments(20000), Tickers(20000), Stats(20000), Klines(1), Klines(100), Klines(1000)}
	for _, message := range fixtures {
		assert.Less(t, proto.Size(message), ReceiveLimit)
	}
}
