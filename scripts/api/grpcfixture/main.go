// Command grpcfixture serves actual handlers and application services for installed clients.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"market-data/internal/testfixture/grpcapi"
	grpctransport "market-data/internal/transport/grpc"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fixture, err := grpcapi.New(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	server, err := grpctransport.NewServer(ctx, grpctransport.Readers{Instruments: fixture.InstrumentReader, Tickers: fixture.TickerReader, MarketStats: fixture.StatsReader, Klines: fixture.Service}, grpctransport.Settings{SnapshotTimeout: 5 * time.Second, KlineTimeout: 30 * time.Second, WriteGrace: 5 * time.Second, MaxSnapshots: 64, MaxKlines: 64, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}, nil)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(listener.Addr())
	go func() { <-ctx.Done(); _ = server.Close() }()
	_ = server.Serve(listener)
	cancel()
	fixture.Service.Wait()
}
