// Command httpfixture measures the existing HTTP handlers with fixed reader data.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	httptransport "market-data/scripts/api/httpfixture/legacyhttp"
)

type counters struct{ read, written atomic.Int64 }

type countedConn struct {
	net.Conn
	counts *counters
}

func (connection countedConn) Read(buffer []byte) (int, error) {
	n, err := connection.Conn.Read(buffer)
	connection.counts.read.Add(int64(n))
	return n, err
}

func (connection countedConn) Write(buffer []byte) (int, error) {
	n, err := connection.Conn.Write(buffer)
	connection.counts.written.Add(int64(n))
	return n, err
}

type countedListener struct {
	net.Listener
	counts *counters
}

func (listener countedListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return countedConn{Conn: connection, counts: listener.counts}, nil
}

type measurements struct {
	CPUSeconds   float64 `json:"cpu_seconds"`
	TotalAlloc   uint64  `json:"total_alloc_bytes"`
	Mallocs      uint64  `json:"mallocs"`
	HeapAlloc    uint64  `json:"heap_alloc_bytes"`
	PeakRSS      int64   `json:"peak_rss_bytes"`
	ReadBytes    int64   `json:"connection_read_bytes"`
	WrittenBytes int64   `json:"connection_written_bytes"`
}

func measure(counts *counters) measurements {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		log.Fatal(err)
	}
	rss := usage.Maxrss
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	return measurements{CPUSeconds: float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6, TotalAlloc: memory.TotalAlloc, Mallocs: memory.Mallocs, HeapAlloc: memory.HeapAlloc, PeakRSS: rss, ReadBytes: counts.read.Load(), WrittenBytes: counts.written.Load()}
}

func handler() http.Handler {
	instruments, tickers, stats, candles := fixtures()
	routes := httptransport.NewSnapshotHandlers(instruments, tickers, stats, 5*time.Second, 64)
	routes["/api/v1/klines"] = httptransport.NewKlinesHandler(candles, 30*time.Second, 64)
	return httptransport.NewAPIHandler(func() bool { return true }, 16384, routes)
}

func emit(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		log.Fatal(err)
	}
}

func serve() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	counts := &counters{}
	server := &http.Server{Handler: handler(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(countedListener{Listener: listener, counts: counts}); err != nil && err != http.ErrServerClosed {
			log.Print(err)
		}
	}()
	emit(map[string]string{"address": listener.Addr().String()})
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "gc" {
			runtime.GC()
		}
		emit(measure(counts))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Print(err)
	}
	<-done
}

type result struct {
	LatencyNS  []int64      `json:"latency_ns"`
	Before     measurements `json:"before"`
	After      measurements `json:"after"`
	BodyBytes  int          `json:"response_message_bytes"`
	BodySHA256 string       `json:"response_sha256"`
}

func benchmark(url string, workers, samples int, expected string) {
	counts := &counters{}
	before := measure(counts)
	timings := make([][]int64, workers)
	sizes := make([]int, workers)
	hashes := make([]string, workers)
	failures := make(chan error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			transport := &http.Transport{DisableCompression: true}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
			for sample := -3; sample < samples; sample++ {
				started := time.Now()
				response, err := client.Get(url)
				if err != nil {
					failures <- err
					return
				}
				body, err := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				if err != nil {
					failures <- err
					return
				}
				if closeErr != nil {
					failures <- closeErr
					return
				}
				if response.StatusCode != http.StatusOK {
					failures <- fmt.Errorf("HTTP status %d", response.StatusCode)
					return
				}
				digest := sha256.Sum256(body)
				hash := hex.EncodeToString(digest[:])
				if hash != expected {
					failures <- fmt.Errorf("response hash mismatch: %s", hash)
					return
				}
				if sample >= 0 {
					timings[worker] = append(timings[worker], time.Since(started).Nanoseconds())
				}
				sizes[worker], hashes[worker] = len(body), hash
			}
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		log.Fatal(err)
	}
	emit(result{LatencyNS: slices.Concat(timings...), Before: before, After: measure(counts), BodyBytes: sizes[0], BodySHA256: hashes[0]})
}

func encode(path string, samples int) {
	target := handler()
	counts := &counters{}
	before := measure(counts)
	timings := make([]int64, 0, samples)
	var body []byte
	for range samples {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		started := time.Now()
		target.ServeHTTP(response, request)
		timings = append(timings, time.Since(started).Nanoseconds())
		if response.Code != http.StatusOK {
			log.Fatal(response.Code)
		}
		body = response.Body.Bytes()
	}
	digest := sha256.Sum256(body)
	emit(result{LatencyNS: timings, Before: before, After: measure(counts), BodyBytes: len(body), BodySHA256: hex.EncodeToString(digest[:])})
}

func main() {
	grpcMode := flag.Bool("grpc", false, "Use actual gRPC transport")
	fixtureName := flag.String("fixture", "", "Fixed response name")
	protoFile := flag.String("proto-file", "", "Protobuf input for encoding-only measurement")
	url := flag.String("url", "", "HTTP client URL")
	path := flag.String("encode", "", "Handler-only measurement path")
	workers := flag.Int("workers", 1, "Concurrent clients")
	samples := flag.Int("samples", 20, "Samples per client")
	expected := flag.String("sha256", "", "Expected fixed response hash")
	flag.Parse()
	if *workers < 1 || *workers > 4 || *samples < 1 {
		log.Fatal("Invalid measurement bounds")
	}
	if *grpcMode {
		if *protoFile != "" {
			encodeGRPC(*fixtureName, *protoFile, *samples)
		} else if *url != "" {
			benchmarkGRPC(*url, *fixtureName, *workers, *samples, *expected)
		} else {
			serveGRPC()
		}
		return
	}
	if *url != "" {
		benchmark(*url, *workers, *samples, *expected)
		return
	}
	if *path != "" {
		encode(*path, *samples)
		return
	}
	serve()
}
