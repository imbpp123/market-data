package grpctransport

import (
	"context"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/protobuf/proto"
)

type InstrumentReader interface {
	Validate(instrument.Query) error
	List(context.Context, instrument.Query) ([]domain.Instrument, error)
}

type TickerReader interface {
	Validate(application.SnapshotQuery) error
	List(context.Context, application.SnapshotQuery) ([]ticker.ReadModel, error)
}

type MarketStatsReader interface {
	Validate(marketstats.Query) error
	List(context.Context, marketstats.Query) ([]domain.MarketStats, error)
}

type KlineReader interface {
	ValidateSeries(kline.Series) error
	Validate(kline.Query) error
	Get(context.Context, kline.Query) ([]domain.Kline, error)
}

type Readers struct {
	Instruments InstrumentReader
	Tickers     TickerReader
	MarketStats MarketStatsReader
	Klines      KlineReader
}

type handlers struct {
	pb.UnimplementedMarketDataServiceServer
	readers Readers
	maximum int
}

func emptyFilters(values ...*string) error {
	for _, value := range values {
		if value != nil && *value == "" {
			return application.ErrInvalidParameter
		}
	}
	return nil
}

func (h *handlers) ListInstruments(ctx context.Context, r *pb.ListInstrumentsRequest) (*pb.ListInstrumentsResponse, error) {
	if err := emptyFilters(r.Exchange, r.Market, r.Symbol); err != nil {
		return nil, err
	}
	if r.Status != nil && *r.Status == "" {
		return nil, application.ErrInvalidStatus
	}
	query := instrument.Query{Exchange: r.GetExchange(), Market: r.GetMarket(), Symbol: r.GetSymbol(), Status: r.GetStatus()}
	if err := h.readers.Instruments.Validate(query); err != nil {
		return nil, err
	}
	rows, err := h.readers.Instruments.List(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &pb.ListInstrumentsResponse{}
	err = convertRows(ctx, rows, h.maximum, 0, instrumentMessage, func(row *pb.Instrument) { result.Instruments = append(result.Instruments, row) })
	return result, err
}

func (h *handlers) ListTickers(ctx context.Context, r *pb.ListTickersRequest) (*pb.ListTickersResponse, error) {
	if err := emptyFilters(r.Exchange, r.Market, r.Symbol); err != nil {
		return nil, err
	}
	query := application.SnapshotQuery{Exchange: r.GetExchange(), Market: r.GetMarket(), Symbol: r.GetSymbol()}
	if err := h.readers.Tickers.Validate(query); err != nil {
		return nil, err
	}
	rows, err := h.readers.Tickers.List(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &pb.ListTickersResponse{}
	err = convertRows(ctx, rows, h.maximum, 0, tickerMessage, func(row *pb.Ticker) { result.Tickers = append(result.Tickers, row) })
	return result, err
}

func (h *handlers) ListMarketStats(ctx context.Context, r *pb.ListMarketStatsRequest) (*pb.ListMarketStatsResponse, error) {
	if err := emptyFilters(r.Exchange, r.Market, r.Symbol); err != nil {
		return nil, err
	}
	if r.Window != nil && *r.Window == "" {
		return nil, application.ErrUnsupportedWindow
	}
	query := marketstats.Query{SnapshotQuery: application.SnapshotQuery{Exchange: r.GetExchange(), Market: r.GetMarket(), Symbol: r.GetSymbol()}, Window: r.GetWindow()}
	if err := h.readers.MarketStats.Validate(query); err != nil {
		return nil, err
	}
	rows, err := h.readers.MarketStats.List(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &pb.ListMarketStatsResponse{}
	err = convertRows(ctx, rows, h.maximum, 0, statsMessage, func(row *pb.MarketStats) { result.MarketStats = append(result.MarketStats, row) })
	return result, err
}

func (h *handlers) GetKlines(ctx context.Context, r *pb.GetKlinesRequest) (*pb.GetKlinesResponse, error) {
	query, err := klineQuery(r, h.readers.Klines)
	if err != nil {
		return nil, err
	}
	if err := h.readers.Klines.Validate(query); err != nil {
		return nil, err
	}
	rows, err := h.readers.Klines.Get(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &pb.GetKlinesResponse{Exchange: r.GetExchange(), Market: r.GetMarket(), Symbol: r.GetSymbol(), Interval: r.GetInterval()}
	err = convertRows(ctx, rows, h.maximum, proto.Size(result), klineMessage, func(row *pb.Kline) { result.Klines = append(result.Klines, row) })
	return result, err
}

func klineQuery(r *pb.GetKlinesRequest, reader KlineReader) (kline.Query, error) {
	var query kline.Query
	for _, field := range []*string{r.Exchange, r.Market, r.Symbol} {
		if field == nil || *field == "" {
			return query, application.ErrInvalidParameter
		}
	}
	if r.Interval == nil {
		return query, application.ErrInvalidParameter
	}
	if *r.Interval == "" {
		return query, application.ErrInvalidInterval
	}
	if r.From == nil || r.To == nil {
		return query, application.ErrInvalidParameter
	}
	query.Series = kline.Series{Scope: application.Scope{Exchange: domain.Exchange(r.GetExchange()), Market: domain.Market(r.GetMarket())}, Symbol: r.GetSymbol(), Interval: domain.Timeframe(r.GetInterval())}
	if err := reader.ValidateSeries(query.Series); err != nil {
		return query, err
	}
	for _, timestamp := range []interface {
		CheckValid() error
		GetSeconds() int64
	}{r.From, r.To} {
		if timestamp.CheckValid() != nil || timestamp.GetSeconds() < 0 {
			return query, application.ErrInvalidRange
		}
	}
	query.From, query.To = r.From.AsTime(), r.To.AsTime()
	return query, nil
}
