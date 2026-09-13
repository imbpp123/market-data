package grpctransport

import (
	"context"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"
	"market-data/internal/testfixture/grpcapi"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func applicationFixture(t *testing.T, observe func(kline.Event)) *grpcapi.Fixture {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	fixture, err := grpcapi.New(ctx, observe)
	require.NoError(t, err)
	t.Cleanup(func() { cancel(); fixture.Service.Wait() })
	return fixture
}

func fixtureReaders(f *grpcapi.Fixture) Readers {
	return Readers{Instruments: f.InstrumentReader, Tickers: f.TickerReader, MarketStats: f.StatsReader, Klines: f.Service}
}

func candleRequest(from, to time.Time) *pb.GetKlinesRequest {
	return &pb.GetKlinesRequest{Exchange: proto.String("binance"), Market: proto.String("spot"), Symbol: proto.String("A"), Interval: proto.String("1m"), From: timestamppb.New(from), To: timestamppb.New(to)}
}

func TestApplicationSnapshotsAndAllRPCs(t *testing.T) {
	f := applicationFixture(t, nil)
	settings := testSettings()
	settings.MaxSnapshots = 64
	settings.MaxKlines = 64
	_, address := startServer(t, fixtureReaders(f), settings, nil)
	api := client(t, address)
	instruments, err := api.ListInstruments(t.Context(), &pb.ListInstrumentsRequest{})
	require.NoError(t, err)
	require.Len(t, instruments.Instruments, 3)
	assert.Equal(t, "A", instruments.Instruments[0].Symbol)
	assert.Nil(t, instruments.Instruments[0].MinQty)
	assert.Equal(t, "0", *instruments.Instruments[1].MinQty)
	assert.Len(t, *instruments.Instruments[2].MinQty, 1024)
	tickers, err := api.ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.NoError(t, err)
	require.Len(t, tickers.Tickers, 2)
	assert.Nil(t, tickers.Tickers[0].NextFundingInSeconds)
	require.NotNil(t, tickers.Tickers[1].NextFundingInSeconds)
	assert.Zero(t, *tickers.Tickers[1].NextFundingInSeconds)
	stats, err := api.ListMarketStats(t.Context(), &pb.ListMarketStatsRequest{})
	require.NoError(t, err)
	require.Len(t, stats.MarketStats, 2)
	assert.Equal(t, int64(9007199254740993), *stats.MarketStats[0].TradeCount)
	from := grpcapi.Now.Truncate(time.Minute).Add(-2 * time.Minute)
	candles, err := api.GetKlines(t.Context(), candleRequest(from, from.Add(2*time.Minute)))
	require.NoError(t, err)
	require.Len(t, candles.Klines, 2)
	assert.Equal(t, "A", candles.Symbol)
	assert.Equal(t, "12345.1234567890123456789", candles.Klines[0].Open)
	assert.Equal(t, int64(9007199254740993), *candles.Klines[0].TradesCount)
	warm, err := api.GetKlines(t.Context(), candleRequest(from, from.Add(2*time.Minute)))
	require.NoError(t, err)
	assert.True(t, proto.Equal(candles, warm))
	assert.Equal(t, int64(1), f.Provider.Attempts.Load())
}

func TestSnapshotReadinessIsNotHiddenByFilters(t *testing.T) {
	cases := []struct {
		name        string
		secondReady bool
		want        codes.Code
	}{{"unready scope", false, codes.Unavailable}, {"ready empty scope", true, codes.OK}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := applicationFixture(t, nil)
			other := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
			if tc.secondReady {
				require.NoError(t, f.Instruments.ReplaceSnapshot(t.Context(), other, nil))
			}
			readers := fixtureReaders(f)
			readers.Instruments = instrument.NewReader(f.Instruments, []application.Scope{grpcapi.Scope, other})
			_, address := startServer(t, readers, testSettings(), nil)
			response, err := client(t, address).ListInstruments(t.Context(), &pb.ListInstrumentsRequest{Symbol: proto.String("missing"), Status: proto.String("closed")})
			assert.Equal(t, tc.want, status.Code(err))
			if err == nil {
				assert.Empty(t, response.Instruments)
			}
			assert.Zero(t, f.Provider.Attempts.Load())
		})
	}
}

func TestFailedRefreshPreservesSnapshot(t *testing.T) {
	f := applicationFixture(t, nil)
	require.Error(t, f.Tickers.ReplaceSnapshot(t.Context(), grpcapi.Scope, []domain.Ticker{{Symbol: "bad", Exchange: domain.ExchangeBybit}}))
	_, address := startServer(t, fixtureReaders(f), testSettings(), nil)
	response, err := client(t, address).ListTickers(t.Context(), &pb.ListTickersRequest{Symbol: proto.String("A")})
	require.NoError(t, err)
	require.Len(t, response.Tickers, 1)
	assert.Equal(t, grpcapi.Now, response.Tickers[0].FetchedAt.AsTime())
	assert.Equal(t, "12345.1234567890123456789", response.Tickers[0].LastPrice)
}

func TestSnapshotValidationOrder(t *testing.T) {
	cases := []struct {
		name    string
		request *pb.ListInstrumentsRequest
		reason  string
	}{
		{"present empty", &pb.ListInstrumentsRequest{Exchange: proto.String("")}, "invalid_parameter"},
		{"canonical exchange", &pb.ListInstrumentsRequest{Exchange: proto.String("BINANCE")}, "invalid_filter"},
		{"disabled scope", &pb.ListInstrumentsRequest{Market: proto.String("linear")}, "invalid_filter"},
		{"invalid symbol", &pb.ListInstrumentsRequest{Symbol: proto.String("A B")}, "invalid_filter"},
		{"empty status before values", &pb.ListInstrumentsRequest{Exchange: proto.String("bad"), Status: proto.String("")}, "invalid_status"},
		{"exchange before status", &pb.ListInstrumentsRequest{Exchange: proto.String("bad"), Status: proto.String("bad")}, "invalid_filter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := applicationFixture(t, nil)
			_, address := startServer(t, fixtureReaders(f), testSettings(), nil)
			_, err := client(t, address).ListInstruments(t.Context(), tc.request)
			assertReason(t, err, codes.InvalidArgument, tc.reason)
			assert.Zero(t, f.Provider.Attempts.Load())
		})
	}
}

func assertReason(t *testing.T, err error, code codes.Code, reason string) {
	t.Helper()
	require.Equal(t, code, status.Code(err))
	details := status.Convert(err).Details()
	require.Len(t, details, 1)
	detail, ok := details[0].(*pb.ErrorDetail)
	require.True(t, ok)
	assert.Equal(t, reason, detail.Reason)
}

func TestKlineValidationAndBoundaryResults(t *testing.T) {
	end := grpcapi.Now.Truncate(time.Minute)
	cases := []struct {
		name   string
		change func(*pb.GetKlinesRequest)
		reason string
		rows   int
	}{
		{"missing timestamp", func(r *pb.GetKlinesRequest) { r.From = nil }, "invalid_parameter", 0},
		{"missing interval", func(r *pb.GetKlinesRequest) { r.Interval = nil }, "invalid_parameter", 0},
		{"empty interval", func(r *pb.GetKlinesRequest) { r.Interval = proto.String("") }, "invalid_interval", 0},
		{"canonical scope", func(r *pb.GetKlinesRequest) { r.Exchange = proto.String("BINANCE") }, "invalid_filter", 0},
		{"invalid nanos", func(r *pb.GetKlinesRequest) { r.From.Nanos = 1000000000 }, "invalid_range", 0},
		{"pre epoch", func(r *pb.GetKlinesRequest) { r.From.Seconds = -1 }, "invalid_range", 0},
		{"unaligned", func(r *pb.GetKlinesRequest) { r.From.Nanos = 1 }, "invalid_range", 0},
		{"reverse", func(r *pb.GetKlinesRequest) { r.From = timestamppb.New(end.Add(time.Minute)) }, "invalid_range", 0},
		{"future", func(r *pb.GetKlinesRequest) { r.To = timestamppb.New(end.Add(2 * time.Minute)) }, "invalid_range", 0},
		{"too many", func(r *pb.GetKlinesRequest) { r.From = timestamppb.New(end.Add(-1001 * time.Minute)) }, "request_too_large", 0},
		{"too old", func(r *pb.GetKlinesRequest) {
			r.From = timestamppb.New(end.Add(-1001 * time.Minute))
			r.To = timestamppb.New(end.Add(-1000 * time.Minute))
		}, "range_out_of_retention", 0},
		{"empty", func(r *pb.GetKlinesRequest) { r.From = r.To }, "", 0},
		{"maximum", func(r *pb.GetKlinesRequest) { r.From = timestamppb.New(end.Add(-1000 * time.Minute)) }, "", 1000},
		{"open slot", func(r *pb.GetKlinesRequest) {
			r.From = timestamppb.New(end)
			r.To = timestamppb.New(end.Add(time.Minute))
		}, "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := applicationFixture(t, nil)
			_, address := startServer(t, fixtureReaders(f), testSettings(), nil)
			request := candleRequest(end.Add(-time.Minute), end)
			tc.change(request)
			response, err := client(t, address).GetKlines(t.Context(), request)
			if tc.reason != "" {
				assertReason(t, err, codes.InvalidArgument, tc.reason)
				assert.Zero(t, f.Provider.Attempts.Load())
			} else {
				require.NoError(t, err)
				assert.Len(t, response.Klines, tc.rows)
				assert.Equal(t, "A", response.Symbol)
				if tc.rows == 0 {
					assert.Zero(t, f.Provider.Attempts.Load())
				}
			}
		})
	}
}

func TestSharedFillSurvivesOneCanceledRPCCaller(t *testing.T) {
	joined := make(chan struct{}, 1)
	f := applicationFixture(t, func(event kline.Event) {
		if event.SharedWait {
			joined <- struct{}{}
		}
	})
	entered := make(chan struct{})
	release := make(chan struct{})
	f.Provider.Run = func(ctx context.Context, r kline.Request) ([]kline.Stored, error) {
		close(entered)
		select {
		case <-release:
			return grpcapi.Rows(r), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	settings := testSettings()
	settings.MaxKlines = 2
	_, address := startServer(t, fixtureReaders(f), settings, nil)
	first, second := client(t, address), client(t, address)
	end := grpcapi.Now.Truncate(time.Minute)
	request := candleRequest(end.Add(-time.Minute), end)
	ctx, cancel := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() { _, err := first.GetKlines(ctx, request); firstDone <- err }()
	<-entered
	secondDone := make(chan *pb.GetKlinesResponse, 1)
	secondError := make(chan error, 1)
	go func() { r, err := second.GetKlines(t.Context(), request); secondDone <- r; secondError <- err }()
	<-joined
	cancel()
	assert.Equal(t, codes.Canceled, status.Code(<-firstDone))
	close(release)
	result := <-secondDone
	require.NoError(t, <-secondError)
	require.Len(t, result.Klines, 1)
	assert.Equal(t, "12345.1234567890123456789", result.Klines[0].Open)
	assert.Equal(t, int64(1), f.Provider.Attempts.Load())
}

func TestMissingCandleProgressPreservesValidCache(t *testing.T) {
	f := applicationFixture(t, nil)
	f.Provider.Run = func(_ context.Context, r kline.Request) ([]kline.Stored, error) {
		rows := grpcapi.Rows(r)
		if len(rows) > 1 {
			return rows[:1], nil
		}
		return nil, nil
	}
	settings := testSettings()
	settings.MaxKlines = 64
	_, address := startServer(t, fixtureReaders(f), settings, nil)
	api := client(t, address)
	end := grpcapi.Now.Truncate(time.Minute)
	_, err := api.GetKlines(t.Context(), candleRequest(end.Add(-2*time.Minute), end))
	assertReason(t, err, codes.FailedPrecondition, "incomplete_data")
	attempts := f.Provider.Attempts.Load()
	response, err := api.GetKlines(t.Context(), candleRequest(end.Add(-2*time.Minute), end.Add(-time.Minute)))
	require.NoError(t, err)
	assert.Len(t, response.Klines, 1)
	assert.Equal(t, attempts, f.Provider.Attempts.Load())
}

func TestEmptyRangeStillRequiresCatalog(t *testing.T) {
	f := applicationFixture(t, nil)
	f.Instruments = memory.NewInstrumentRepository()
	// A fresh service is required because repositories are constructor-owned.
	service, err := kline.NewService(t.Context(), f.Candles, f.Instruments, testOperations{}, []kline.ScopeSettings{{Scope: grpcapi.Scope, Provider: f.Provider, PageLimit: 500}}, kline.Settings{HistoryCandles: 1000, MaxCallers: 1, MaxActiveFills: 1, MaxActiveFillsPerExchange: 1, MaxAttempts: 1, FillTimeout: time.Second}, func() time.Time { return grpcapi.Now }, nil)
	require.NoError(t, err)
	defer service.Wait()
	readers := fixtureReaders(f)
	readers.Klines = service
	_, address := startServer(t, readers, testSettings(), nil)
	end := grpcapi.Now.Truncate(time.Minute)
	_, err = client(t, address).GetKlines(t.Context(), candleRequest(end, end))
	assertReason(t, err, codes.Unavailable, "data_not_ready")
	assert.Zero(t, f.Provider.Attempts.Load())
}

type testOperations struct{}

func (testOperations) Run(ctx context.Context, _ application.Scope, _ func(), run func(context.Context) error) error {
	return run(ctx)
}

func TestCalendarBoundariesPassThroughRPC(t *testing.T) {
	cases := []struct {
		name, interval string
		at             time.Time
		valid          bool
	}{
		{"week Monday", "1w", time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), true},
		{"week Tuesday", "1w", time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), false},
		{"month first day", "1M", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), true},
		{"month second day", "1M", time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := applicationFixture(t, nil)
			_, address := startServer(t, fixtureReaders(f), testSettings(), nil)
			request := candleRequest(tc.at, tc.at)
			request.Interval = proto.String(tc.interval)
			response, err := client(t, address).GetKlines(t.Context(), request)
			if tc.valid {
				require.NoError(t, err)
				assert.Empty(t, response.Klines)
				assert.Equal(t, tc.interval, response.Interval)
			} else {
				assertReason(t, err, codes.InvalidArgument, "invalid_range")
			}
			assert.Zero(t, f.Provider.Attempts.Load())
		})
	}
}

func TestCandleLimitAcrossChannelsKeepsSnapshotsAvailable(t *testing.T) {
	f := applicationFixture(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	f.Provider.Run = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
		close(entered)
		select {
		case <-release:
			return grpcapi.Rows(request), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	_, address := startServer(t, fixtureReaders(f), testSettings(), nil)
	first, second := client(t, address), client(t, address)
	end := grpcapi.Now.Truncate(time.Minute)
	request := candleRequest(end.Add(-time.Minute), end)
	done := make(chan error, 1)
	go func() { _, err := first.GetKlines(t.Context(), request); done <- err }()
	<-entered
	_, err := second.GetKlines(t.Context(), request)
	assertReason(t, err, codes.ResourceExhausted, "service_overloaded")
	snapshot, err := second.ListTickers(t.Context(), &pb.ListTickersRequest{})
	require.NoError(t, err)
	assert.Len(t, snapshot.Tickers, 2)
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, int64(1), f.Provider.Attempts.Load())
}
