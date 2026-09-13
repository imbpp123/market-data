# Market Data Go client

Typed Protobuf messages and a gRPC client for Market Data: instruments, tickers, rolling 24-hour statistics, and candles. The package connects to a running service. It has no exchange SDK dependency.

## Install

Use Go **1.27.1**. From your application's Go module, replace `COMMIT_SHA` with a full existing commit containing this client:

```sh
go get github.com/imbpp123/market-data/api/go@COMMIT_SHA
```

Import `github.com/imbpp123/market-data/api/go/marketdata/v1`. Create a reused channel with `grpc.NewClient`, then a client with `NewMarketDataServiceClient`. See the [complete example](examples/client/main.go) for calls and error handling. Its candle timestamps are fixed test values; use recent aligned times for a running service.

## Usage and documentation

- Set a deadline for each call and `grpc.MaxCallRecvMsgSize(16777216)` on the channel.
- Use `insecure.NewCredentials()` only for a trusted plaintext endpoint. The server has no native TLS.
- Keep decimals exact and check optional pointers for `nil` before reading them.
- Read the bundled [client guide](CLIENT_GUIDE.md) for methods, fields, candle ranges, and errors. It is written for people and coding assistants.

From your application, inspect the installed package:

```sh
go doc github.com/imbpp123/market-data/api/go/marketdata/v1
go doc github.com/imbpp123/market-data/api/go/marketdata/v1.GetKlinesRequest
```

Package documentation includes a connection example. The guide stays at the module root after download; find that path with `go list -m -f '{{.Dir}}' github.com/imbpp123/market-data/api/go`.
