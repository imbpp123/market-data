// Command contract-fixture serves synthetic values for client contract tests.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/imbpp123/market-data/api/go/internal/fixture"
	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc"
)

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize(64*1024), grpc.MaxSendMsgSize(fixture.ReceiveLimit))
	pb.RegisterMarketDataServiceServer(server, fixture.Server{})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	done := make(chan struct{})
	go func() { <-ctx.Done(); server.Stop(); close(done) }()
	fmt.Println(listener.Addr().String())
	if err := server.Serve(listener); err != nil {
		log.Print(err)
	}
	cancel()
	<-done
}
