package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	grpctransport "market-data/internal/transport/grpc"
)

func serveGRPC() {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	instruments, tickers, stats, candles := fixtures()
	server, err := grpctransport.NewServer(root, grpctransport.Readers{Instruments: instruments, Tickers: tickers, MarketStats: stats, Klines: candles}, grpctransport.Settings{SnapshotTimeout: 5 * time.Second, KlineTimeout: 30 * time.Second, WriteGrace: 5 * time.Second, MaxSnapshots: 64, MaxKlines: 64, MaxRequestBytes: 8192, MaxResponseBytes: 16 << 20, MaxHeaderBytes: 32768, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}, nil)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	counts := &counters{}
	done := make(chan error, 1)
	go func() { done <- server.Serve(countedListener{Listener: listener, counts: counts}) }()
	emit(map[string]string{"address": listener.Addr().String()})
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "gc" {
			runtime.GC()
		}
		emit(measure(counts))
	}
	_ = server.Close()
	<-done
}

func grpcRequest(name string) (string, proto.Message, proto.Message, error) {
	kind, countText, ok := strings.Cut(name, "-")
	if strings.HasPrefix(name, "market-stats-") {
		kind, countText, ok = "market-stats", strings.TrimPrefix(name, "market-stats-"), true
	}
	count, err := strconv.Atoi(countText)
	if !ok || err != nil {
		return "", nil, nil, fmt.Errorf("invalid fixture: %s", name)
	}
	if kind == "klines" && count != 1 && count != 100 && count != 1000 {
		return "", nil, nil, fmt.Errorf("invalid candle count: %d", count)
	}
	if kind != "klines" && count != 1 && count != 20000 {
		return "", nil, nil, fmt.Errorf("invalid snapshot count: %d", count)
	}
	var symbol *string
	if count == 20000 {
		symbol = proto.String("full")
	}
	switch kind {
	case "instruments":
		return pb.MarketDataService_ListInstruments_FullMethodName, &pb.ListInstrumentsRequest{Symbol: symbol}, &pb.ListInstrumentsResponse{}, nil
	case "tickers":
		return pb.MarketDataService_ListTickers_FullMethodName, &pb.ListTickersRequest{Symbol: symbol}, &pb.ListTickersResponse{}, nil
	case "market-stats":
		return pb.MarketDataService_ListMarketStats_FullMethodName, &pb.ListMarketStatsRequest{Symbol: symbol}, &pb.ListMarketStatsResponse{}, nil
	case "klines":
		return pb.MarketDataService_GetKlines_FullMethodName, &pb.GetKlinesRequest{Exchange: proto.String("binance"), Market: proto.String("spot"), Symbol: proto.String("S0000USDT"), Interval: proto.String("1m"), From: timestamppb.New(time.Unix(1789154400, 0)), To: timestamppb.New(time.Unix(1789154400+int64(count)*60, 0))}, &pb.GetKlinesResponse{}, nil
	default:
		return "", nil, nil, fmt.Errorf("unknown fixture: %s", name)
	}
}

func benchmarkGRPC(address, name string, workers, samples int, expected string) {
	method, request, prototype, err := grpcRequest(name)
	if err != nil {
		log.Fatal(err)
	}
	counts := &counters{}
	before := measure(counts)
	timings := make([][]int64, workers)
	sizes := make([]int, workers)
	failures := make(chan error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20)))
			if err != nil {
				failures <- err
				return
			}
			defer func() { _ = connection.Close() }()
			for sample := -3; sample < samples; sample++ {
				response := prototype.ProtoReflect().New().Interface()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				started := time.Now()
				err := connection.Invoke(ctx, method, request, response)
				cancel()
				if err != nil {
					failures <- err
					return
				}
				body, err := proto.MarshalOptions{Deterministic: true}.Marshal(response)
				if err != nil {
					failures <- err
					return
				}
				digest := sha256.Sum256(body)
				if hex.EncodeToString(digest[:]) != expected {
					failures <- fmt.Errorf("response hash mismatch: %s", name)
					return
				}
				if sample >= 0 {
					timings[worker] = append(timings[worker], time.Since(started).Nanoseconds())
				}
				sizes[worker] = len(body)
			}
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		log.Fatal(err)
	}
	emit(map[string]any{"latency_ns": slices.Concat(timings...), "before": before, "after": measure(counts), "response_message_bytes": sizes[0], "request_message_bytes": proto.Size(request), "response_sha256": expected})
}

func encodeGRPC(name, path string, samples int) {
	_, _, message, err := grpcRequest(name)
	if err != nil {
		log.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := proto.Unmarshal(body, message); err != nil {
		log.Fatal(err)
	}
	counts := &counters{}
	before := measure(counts)
	timings := make([]int64, 0, samples)
	for range samples {
		started := time.Now()
		body, err = proto.Marshal(message)
		if err != nil {
			log.Fatal(err)
		}
		timings = append(timings, time.Since(started).Nanoseconds())
	}
	emit(map[string]any{"latency_ns": timings, "before": before, "after": measure(counts), "response_message_bytes": len(body)})
}
