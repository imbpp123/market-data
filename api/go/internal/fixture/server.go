// Package fixture provides fixed contract data, not application behavior.
package fixture

import (
	"context"
	"fmt"
	"strings"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const Decimal = "12345.1234567890123456789"
const Count int64 = 9007199254740993
const Epoch int64 = 1789214400
const ReceiveLimit = 16 * 1024 * 1024

// Server uses symbol selectors only for fixed interoperability scenarios.
type Server struct {
	pb.UnimplementedMarketDataServiceServer
}

func Stamp() *timestamppb.Timestamp {
	return &timestamppb.Timestamp{Seconds: Epoch, Nanos: 123456789}
}

func identity(index int) (string, string, string) {
	exchanges := []string{"binance", "bybit"}
	markets := []string{"linear", "spot"}
	return exchanges[(index/10000)%2], markets[(index/5000)%2], fmt.Sprintf("S%04dUSDT", index%5000)
}

func Instruments(count int) *pb.ListInstrumentsResponse {
	result := &pb.ListInstrumentsResponse{}
	for i := range count {
		exchange, market, symbol := identity(i)
		result.Instruments = append(result.Instruments, &pb.Instrument{
			Exchange: exchange, Market: market, Symbol: symbol, BaseAsset: "S", QuoteAsset: "USDT", Status: "trading",
			PriceTick: Decimal, QtyStep: Decimal, MinQty: proto.String(Decimal), MaxQty: proto.String(Decimal), MinNotional: proto.String(Decimal),
			FundingIntervalSeconds: proto.Int64(28800), DelistingTime: Stamp(), UpdatedAt: Stamp(),
		})
	}
	return result
}

func Tickers(count int) *pb.ListTickersResponse {
	result := &pb.ListTickersResponse{}
	for i := range count {
		exchange, market, symbol := identity(i)
		result.Tickers = append(result.Tickers, &pb.Ticker{Exchange: exchange, Market: market, Symbol: symbol,
			LastPrice: Decimal, BidPrice: proto.String(Decimal), BidSize: proto.String(Decimal), AskPrice: proto.String(Decimal), AskSize: proto.String(Decimal),
			FundingRate: proto.String(Decimal), NextFundingInSeconds: proto.Int64(28800), FetchedAt: Stamp(),
		})
	}
	return result
}

func Stats(count int) *pb.ListMarketStatsResponse {
	result := &pb.ListMarketStatsResponse{}
	for i := range count {
		exchange, market, symbol := identity(i)
		result.MarketStats = append(result.MarketStats, &pb.MarketStats{Exchange: exchange, Market: market, Symbol: symbol, Window: "24h",
			High: Decimal, Low: Decimal, Volume: Decimal, Turnover: Decimal, PriceChange: proto.String(Decimal), TradeCount: proto.Int64(Count), FetchedAt: Stamp(),
		})
	}
	return result
}

func Klines(count int) *pb.GetKlinesResponse {
	result := &pb.GetKlinesResponse{Exchange: "binance", Market: "spot", Symbol: "S0000USDT", Interval: "1m"}
	for i := range count {
		open := Epoch - 60000 + int64(i)*60
		result.Klines = append(result.Klines, &pb.Kline{OpenTime: &timestamppb.Timestamp{Seconds: open}, CloseTime: &timestamppb.Timestamp{Seconds: open + 60},
			Open: Decimal, High: Decimal, Low: Decimal, Close: Decimal, Volume: Decimal, Turnover: Decimal, TradesCount: proto.Int64(Count), FetchedAt: Stamp(),
		})
	}
	return result
}

func scenarioError(ctx context.Context, symbol string) error {
	switch symbol {
	case "error":
		value, err := status.New(codes.InvalidArgument, "Invalid filter").WithDetails(&pb.ErrorDetail{Reason: "invalid_filter"})
		if err != nil {
			return err
		}
		return value.Err()
	case "unknown-detail":
		value := status.New(codes.Unavailable, "Fixture unavailable").Proto()
		value.Details = []*anypb.Any{{TypeUrl: "type.googleapis.com/future.Unknown", Value: []byte{8, 1}}}
		return status.FromProto(value).Err()
	case "native-error":
		return status.Error(codes.Unavailable, "Fixture unavailable")
	case "wait":
		<-ctx.Done()
		return status.FromContextError(ctx.Err()).Err()
	}
	return nil
}

func rows(symbol string) int {
	if symbol == "full" {
		return 20000
	}
	return 1
}

func (Server) ListInstruments(ctx context.Context, request *pb.ListInstrumentsRequest) (*pb.ListInstrumentsResponse, error) {
	if err := scenarioError(ctx, request.GetSymbol()); err != nil {
		return nil, err
	}
	result := Instruments(rows(request.GetSymbol()))
	if request.GetSymbol() == "presence" {
		result = Instruments(3)
		result.Instruments[0].MinQty, result.Instruments[0].FundingIntervalSeconds, result.Instruments[0].DelistingTime = nil, nil, nil
		result.Instruments[1].MinQty, result.Instruments[1].FundingIntervalSeconds = proto.String("0"), proto.Int64(0)
		result.Instruments[2].MinQty = proto.String(strings.Repeat("9", 1024))
	}
	// Echo scalar presence via an optional response field for serialization checks.
	if request.GetSymbol() == "echo" {
		result.Instruments[0].MinQty = request.Exchange
	}
	return result, nil
}

func (Server) ListTickers(ctx context.Context, request *pb.ListTickersRequest) (*pb.ListTickersResponse, error) {
	if err := scenarioError(ctx, request.GetSymbol()); err != nil {
		return nil, err
	}
	result := Tickers(rows(request.GetSymbol()))
	if request.GetSymbol() == "presence" {
		result = Tickers(2)
		result.Tickers[0].BidPrice, result.Tickers[0].NextFundingInSeconds = nil, nil
		result.Tickers[1].BidPrice, result.Tickers[1].NextFundingInSeconds = proto.String("0"), proto.Int64(0)
	}
	return result, nil
}

func (Server) ListMarketStats(ctx context.Context, request *pb.ListMarketStatsRequest) (*pb.ListMarketStatsResponse, error) {
	if err := scenarioError(ctx, request.GetSymbol()); err != nil {
		return nil, err
	}
	result := Stats(rows(request.GetSymbol()))
	if request.GetSymbol() == "presence" {
		result = Stats(2)
		result.MarketStats[0].PriceChange, result.MarketStats[0].TradeCount = nil, nil
		result.MarketStats[1].PriceChange, result.MarketStats[1].TradeCount = proto.String("0"), proto.Int64(0)
	}
	return result, nil
}

func (Server) GetKlines(ctx context.Context, request *pb.GetKlinesRequest) (*pb.GetKlinesResponse, error) {
	if err := scenarioError(ctx, request.GetSymbol()); err != nil {
		return nil, err
	}
	if request.From == nil || request.To == nil {
		return nil, status.Error(codes.InvalidArgument, "Fixture timestamps are required")
	}
	if err := request.From.CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid fixture timestamp")
	}
	if err := request.To.CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid fixture timestamp")
	}
	count := int((request.To.Seconds - request.From.Seconds) / 60)
	if count < 0 || count > 1000 {
		return nil, status.Error(codes.InvalidArgument, "Invalid fixture count")
	}
	result := Klines(count)
	if request.GetSymbol() == "presence" && count >= 2 {
		result.Klines[0].TradesCount = nil
		result.Klines[1].TradesCount = proto.Int64(0)
	}
	return result, nil
}
