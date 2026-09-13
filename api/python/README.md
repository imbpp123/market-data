# market-data-api

Generated `marketdata.v1` messages and gRPC stubs. Python 3.13 and 3.14 are supported and tested. Python installation needs no Go, protoc, or grpcio-tools. Version 0.1.0 is an initial local artifact, not a published release.

After these files are committed, pin a full commit containing this package:

```text
market-data-api @ git+https://github.com/imbpp123/market-data.git@COMMIT_SHA#subdirectory=api/python
```

`COMMIT_SHA` is a placeholder. Git access is required. Do not put credentials in the URL. The package version identifies the artifact; `marketdata.v1` identifies the wire contract.

```python
import grpc
from marketdata.v1 import market_data_pb2 as messages
from marketdata.v1 import market_data_pb2_grpc as rpc

with grpc.insecure_channel("localhost:9090", options=[("grpc.max_receive_message_length", 16 * 1024 * 1024)]) as channel:
    client = rpc.MarketDataServiceStub(channel)
    result = client.ListTickers(messages.ListTickersRequest(exchange="binance"), timeout=5)
    for ticker in result.tickers:
        print(ticker.last_price, ticker.bid_price if ticker.HasField("bid_price") else None)
```

Use TLS credentials for remote protected connections. Plaintext examples target local fixtures. Reuse a channel and set a deadline for each call. The generated stub also works with `grpc.aio`. See `api/examples` for complete sync/async examples with safe error details. Production still serves HTTP until migration phase 3.
