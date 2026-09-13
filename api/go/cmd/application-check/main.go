// Command application-check compares actual application responses across clients.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	candles, err := client.GetKlines(ctx, &pb.GetKlinesRequest{Exchange: proto.String("binance"), Market: proto.String("spot"), Symbol: proto.String("A"), Interval: proto.String("1m"), From: timestamppb.New(time.Unix(1789214280, 0)), To: timestamppb.New(time.Unix(1789214400, 0))})
	save("candles", candles, err)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Fatal(err)
	}
}
