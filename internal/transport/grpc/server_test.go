package grpctransport

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type fakeReaders struct {
	instruments []domain.Instrument
	tickers     []ticker.ReadModel
	stats       []domain.MarketStats
	klines      []domain.Kline
	list        func(context.Context) error
}

type fakeInstruments struct{ *fakeReaders }

func (fakeInstruments) Validate(instrument.Query) error { return nil }
func (f fakeInstruments) List(ctx context.Context, _ instrument.Query) ([]domain.Instrument, error) {
	if f.list != nil {
		if err := f.list(ctx); err != nil {
			return nil, err
		}
	}
	return f.instruments, nil
}

type fakeTickers struct{ *fakeReaders }

func (fakeTickers) Validate(application.SnapshotQuery) error { return nil }
func (f fakeTickers) List(ctx context.Context, _ application.SnapshotQuery) ([]ticker.ReadModel, error) {
	if f.list != nil {
		if err := f.list(ctx); err != nil {
			return nil, err
		}
	}
	return f.tickers, nil
}

type fakeStats struct{ *fakeReaders }

func (fakeStats) Validate(marketstats.Query) error { return nil }
func (f fakeStats) List(ctx context.Context, _ marketstats.Query) ([]domain.MarketStats, error) {
	if f.list != nil {
		if err := f.list(ctx); err != nil {
			return nil, err
		}
	}
	return f.stats, nil
}

type fakeKlines struct{ *fakeReaders }

func (fakeKlines) ValidateSeries(kline.Series) error { return nil }
func (fakeKlines) Validate(kline.Query) error        { return nil }
func (f fakeKlines) Get(ctx context.Context, _ kline.Query) ([]domain.Kline, error) {
	if f.list != nil {
		if err := f.list(ctx); err != nil {
			return nil, err
		}
	}
	return f.klines, nil
}

func (f *fakeReaders) readers() Readers {
	return Readers{fakeInstruments{f}, fakeTickers{f}, fakeStats{f}, fakeKlines{f}}
}

func testSettings() Settings {
	return Settings{SnapshotTimeout: 5 * time.Second, KlineTimeout: 5 * time.Second, WriteGrace: time.Second, MaxSnapshots: 1, MaxKlines: 1, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: time.Second, IdleTimeout: time.Minute}
}

func startServer(t *testing.T, readers Readers, settings Settings, observer Observer) (*Server, string) {
	t.Helper()
	server, err := NewServer(t.Context(), readers, settings, observer)
	require.NoError(t, err)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { require.NoError(t, server.Close()); <-done; server.owned.Wait() })
	return server, listener.Addr().String()
}

func client(t *testing.T, address string) pb.MarketDataServiceClient {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	return pb.NewMarketDataServiceClient(connection)
}

func rawRequest(t *testing.T, address string, window uint32, message []byte) (net.Conn, *http2.Framer) {
	t.Helper()
	connection, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	require.NoError(t, connection.SetDeadline(time.Now().Add(10*time.Second)))
	_, err = connection.Write([]byte(http2.ClientPreface))
	require.NoError(t, err)
	framer := http2.NewFramer(connection, connection)
	require.NoError(t, framer.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: window}))
	var headers bytes.Buffer
	encoder := hpack.NewEncoder(&headers)
	for _, field := range []hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "http"}, {Name: ":path", Value: pb.MarketDataService_ListTickers_FullMethodName}, {Name: ":authority", Value: address}, {Name: "content-type", Value: "application/grpc"}, {Name: "te", Value: "trailers"}} {
		require.NoError(t, encoder.WriteField(field))
	}
	require.NoError(t, framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: headers.Bytes(), EndHeaders: true}))
	body := make([]byte, 5+len(message))
	binary.BigEndian.PutUint32(body[1:5], uint32(len(message)))
	copy(body[5:], message)
	require.NoError(t, framer.WriteData(1, true, body))
	return connection, framer
}

func readResponseHeaders(t *testing.T, framer *http2.Framer) {
	t.Helper()
	for {
		frame, err := framer.ReadFrame()
		require.NoError(t, err)
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				require.NoError(t, framer.WriteSettingsAck())
			}
		case *http2.HeadersFrame:
			if frame.StreamID == 1 {
				return
			}
		}
	}
}

func largeReaders() *fakeReaders {
	rows := make([]ticker.ReadModel, 1000)
	for i := range rows {
		rows[i] = ticker.ReadModel{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot, Symbol: "BTCUSDT", LastPrice: decimal.RequireFromString(strings.Repeat("9", 1000)), FetchedAt: time.Unix(100, 123)}
	}
	return &fakeReaders{tickers: rows}
}

func TestSlowReaderKeepsCapacityUntilReset(t *testing.T) {
	completed := make(chan Event, 4)
	server, address := startServer(t, largeReaders().readers(), testSettings(), func(context.Context, string) func(Event) { return func(e Event) { completed <- e } })
	_, framer := rawRequest(t, address, 0, nil)
	readResponseHeaders(t, framer)

	_, err := client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	event := <-completed
	assert.Equal(t, "service_overloaded", event.Reason)
	assert.Len(t, server.snapshots, 1)
	require.NoError(t, framer.WriteRSTStream(1, http2.ErrCodeCancel))
	event = <-completed
	assert.True(t, event.TransportFailure)
	assert.Empty(t, server.snapshots)

	result, err := client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.NoError(t, err)
	assert.Len(t, result.Tickers, 1000)
}

func TestSlowReaderAbortsAtWriteDeadline(t *testing.T) {
	settings := testSettings()
	settings.SnapshotTimeout = time.Second
	settings.WriteGrace = time.Second
	completed := make(chan Event, 1)
	server, address := startServer(t, largeReaders().readers(), settings, func(context.Context, string) func(Event) { return func(e Event) { completed <- e } })
	_, framer := rawRequest(t, address, 0, nil)
	readResponseHeaders(t, framer)
	require.Len(t, server.snapshots, 1)

	event := <-completed
	assert.True(t, event.TransportFailure)
	assert.Equal(t, codes.DeadlineExceeded, event.Code)
	assert.Empty(t, server.snapshots)
	assert.GreaterOrEqual(t, event.Duration, 2*time.Second)
}

func TestRealRPCSuccessAndUnknownFields(t *testing.T) {
	_, address := startServer(t, (&fakeReaders{}).readers(), testSettings(), nil)
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	request := &pb.ListTickersRequest{}
	request.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	response := new(pb.ListTickersResponse)
	require.NoError(t, connection.Invoke(t.Context(), pb.MarketDataService_ListTickers_FullMethodName, request, response))
	assert.Empty(t, response.Tickers)
	_, err = client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{Symbol: proto.String("")})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestForcedCloseAbortsBlockedSend(t *testing.T) {
	completed := make(chan Event, 1)
	server, address := startServer(t, largeReaders().readers(), testSettings(), func(context.Context, string) func(Event) { return func(event Event) { completed <- event } })
	_, framer := rawRequest(t, address, 0, nil)
	readResponseHeaders(t, framer)
	require.Len(t, server.snapshots, 1)
	require.NoError(t, server.Close())
	event := <-completed
	assert.True(t, event.TransportFailure)
	assert.Equal(t, codes.Canceled, event.Code)
	assert.Empty(t, server.snapshots)
}

func TestOversizedResponseHasRichStatus(t *testing.T) {
	settings := testSettings()
	settings.MaxResponseBytes = 100
	_, address := startServer(t, largeReaders().readers(), settings, nil)
	_, err := client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{})
	assertReason(t, err, codes.ResourceExhausted, "response_too_large")
}
