# Market Data clients

Generated Go and Python clients for the Market Data gRPC service. They provide typed requests and responses for instruments, tickers, 24-hour statistics, and candles. Installing a client does not start the service or require an exchange SDK.

| Resource | Purpose |
| --- | --- |
| [Go library](go/README.md) | Install, connect, and find Go package documentation |
| [Python library](python/README.md) | Install, connect, and use sync or async calls |
| [Client guide](CLIENT_GUIDE.md) | Shared usage rules for people and coding assistants; included in both packages |
| [Protobuf schema](proto/marketdata/v1/market_data.proto) | Commented methods, messages, fields, and units |
| [Service API guide](../docs/api.md) | Requests, errors, and command-line examples |
| [Development guide](../docs/development.md#api-tools-and-packaging) | Generation, packaging, compatibility, and checks |

The service is `marketdata.v1.MarketDataService`, normally on port 9090. Reuse channels, set call deadlines, and allow responses up to 16 MiB. Decimals are exact strings; optional values distinguish absence from zero.

Generated files are committed. Edit the schema or shared guide, then run `make generate-api` and `make check-api`. Normal service builds and client installations need no generators. The [compatibility baseline](compatibility/README.md) is maintained separately.
