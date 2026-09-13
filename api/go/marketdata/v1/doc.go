// Package marketdatav1 provides typed messages and a gRPC client for Market Data.
// It reads instruments, tickers, rolling 24-hour statistics, and candles from a
// running service. It does not start a server or call an exchange directly.
//
// # Connect
//
// Reuse a channel and client. For a trusted local plaintext endpoint:
//
//	conn, err := grpc.NewClient("localhost:9090",
//		grpc.WithTransportCredentials(insecure.NewCredentials()),
//		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16777216)))
//	if err != nil {
//		return err
//	}
//	defer conn.Close()
//	client := marketdatav1.NewMarketDataServiceClient(conn)
//	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
//	defer cancel()
//	result, err := client.ListTickers(ctx, &marketdatav1.ListTickersRequest{
//		Exchange: proto.String("binance"),
//		Market:   proto.String("spot"),
//		Symbol:   proto.String("BTCUSDT"),
//	})
//
// This example uses context, time, google.golang.org/grpc,
// google.golang.org/grpc/credentials/insecure, and google.golang.org/protobuf/proto.
// Pass your operation's parent context. Set a separate deadline for each call.
// The service has no native TLS; remote encryption needs a configured endpoint.
//
// # Values and requests
//
// Decimals are exact strings. Optional scalar pointers distinguish absence from
// zero; a getter alone cannot preserve that distinction. Times are Protobuf
// timestamps in UTC. Snapshot reads use cache and can return old data after a
// refresh failure. Check UpdatedAt or FetchedAt for freshness.
//
// Candle ranges are aligned and half-open: [From, To). The default maximum is
// 1,000 slots within the rolling history window. A successful response contains
// the whole range; missing data never becomes synthetic candles. The server may
// load missing candles and share that work across callers.
//
// Application errors carry a gRPC status and optional ErrorDetail. Inspect
// status.Convert(err).Details(); handle native failures without a detail too.
//
// CLIENT_GUIDE.md at the module root provides complete usage rules for people and
// coding assistants. Generated type and field comments describe the wire values.
package marketdatav1
