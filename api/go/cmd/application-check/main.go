// Command application-check compares actual application responses across clients.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: application-check ADDRESS")
	}
	connection, err := grpc.NewClient(os.Args[1], grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20)))
	if err != nil {
		log.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := pb.NewMarketDataServiceClient(connection)
	result := make(map[string]json.RawMessage)
	save := func(name string, message proto.Message, err error) {
		if err != nil {
			log.Fatal(err)
		}
		body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(message)
		if err != nil {
			log.Fatal(err)
		}
		result[name] = body
	}
	instruments, err := client.ListInstruments(ctx, &pb.ListInstrumentsRequest{})
	save("instruments", instruments, err)
	tickers, err := client.ListTickers(ctx, &pb.ListTickersRequest{})
	save("tickers", tickers, err)
	statistics, err := client.ListMarketStats(ctx, &pb.ListMarketStatsRequest{})
	save("statistics", statistics, err)
	candles, err := client.GetKlines(ctx, &pb.GetKlinesRequest{Exchange: proto.String("binance"), Market: proto.String("spot"), Symbol: proto.String("A"), Interval: proto.String("1m"), From: timestamppb.New(time.Unix(candleTime("API_CANDLE_FROM", 1789214280), 0)), To: timestamppb.New(time.Unix(candleTime("API_CANDLE_TO", 1789214400), 0))})
	save("candles", candles, err)
	_, err = client.ListTickers(ctx, &pb.ListTickersRequest{Symbol: proto.String("")})
	if status.Code(err) != codes.InvalidArgument {
		log.Fatalf("Invalid status: %v", err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		log.Fatal("Missing application error detail")
	}
	detail, ok := details[0].(*pb.ErrorDetail)
	if !ok || detail.Reason != "invalid_parameter" {
		log.Fatal("Unexpected application reason")
	}
	err = connection.Invoke(ctx, "/unknown.Service/Method", &pb.ListTickersRequest{}, &pb.ListTickersResponse{})
	if status.Code(err) != codes.Unimplemented || len(status.Convert(err).Details()) != 0 {
		log.Fatal("Invalid native unknown-method status")
	}
	expired, stop := context.WithDeadline(ctx, time.Unix(0, 0))
	defer stop()
	_, err = client.ListTickers(expired, &pb.ListTickersRequest{})
	if status.Code(err) != codes.DeadlineExceeded || len(status.Convert(err).Details()) != 0 {
		log.Fatal("Invalid native deadline status")
	}
	canceled, stopCanceled := context.WithCancel(ctx)
	stopCanceled()
	_, err = client.ListTickers(canceled, &pb.ListTickersRequest{})
	if status.Code(err) != codes.Canceled || len(status.Convert(err).Details()) != 0 {
		log.Fatal("Invalid native cancellation status")
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Fatal(err)
	}
}

func candleTime(name string, fallback int64) int64 {
	if value := os.Getenv(name); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			log.Fatal(err)
		}
		return parsed
	}
	return fallback
}
