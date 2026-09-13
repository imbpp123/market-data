// A reused local channel with explicit deadlines and safe rich-status decoding.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func reason(err error) string {
	for _, detail := range status.Convert(err).Details() {
		if value, ok := detail.(*pb.ErrorDetail); ok {
			return value.Reason
		}
	}
	return ""
}

func run(address string) error {
	channel, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16*1024*1024)))
	if err != nil {
		return err
	}
	defer channel.Close()
	client := pb.NewMarketDataServiceClient(channel)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	instruments, err := client.ListInstruments(ctx, &pb.ListInstrumentsRequest{})
	if err != nil {
		return err
	}
	if len(instruments.Instruments) > 0 && instruments.Instruments[0].MinQty != nil {
		fmt.Println("min_qty", *instruments.Instruments[0].MinQty)
	}
	if _, err := client.ListTickers(ctx, &pb.ListTickersRequest{}); err != nil {
		return err
	}
	if _, err := client.ListMarketStats(ctx, &pb.ListMarketStatsRequest{}); err != nil {
		return err
	}
	_, err = client.GetKlines(ctx, &pb.GetKlinesRequest{Exchange: proto.String("binance"), Market: proto.String("spot"), Symbol: proto.String("S0000USDT"), Interval: proto.String("1m"), From: timestamppb.New(time.Unix(1789154400, 0)), To: timestamppb.New(time.Unix(1789154460, 0))})
	return err
}

func main() {
	address := "localhost:9090"
	if len(os.Args) > 1 {
		address = os.Args[1]
	}
	if err := run(address); err != nil {
		log.Fatalf("status=%s reason=%s", status.Code(err), reason(err))
	}
}
