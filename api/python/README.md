# Market Data Python client

Typed Protobuf messages and a gRPC stub for Market Data: instruments, tickers, rolling 24-hour statistics, and candles. The package connects to a running service and supports both synchronous and asynchronous calls.

## Install

Use Python **3.13 or 3.14**. Put this dependency in your requirements file, replacing `COMMIT_SHA` with a full existing commit containing the package:

```text
market-data-api @ git+https://github.com/imbpp123/market-data.git@COMMIT_SHA#subdirectory=api/python
```

Installation needs Git access but no Go, protoc, or exchange SDK. Do not put credentials in the dependency URL.

## Connect

```python
import grpc
from marketdata.v1 import market_data_pb2 as messages
from marketdata.v1 import market_data_pb2_grpc as rpc

options = [("grpc.max_receive_message_length", 16777216)]
with grpc.insecure_channel("localhost:9090", options=options) as channel:
    client = rpc.MarketDataServiceStub(channel)
    result = client.ListTickers(
        messages.ListTickersRequest(exchange="binance", market="spot"), timeout=5
    )
    for ticker in result.tickers:
        print(ticker.last_price, ticker.bid_price if ticker.HasField("bid_price") else None)
```

Reuse the channel and set a deadline per call. For async use, create a `grpc.aio` channel and `await` the same stub methods. Plaintext is for trusted connections; the server has no native TLS.

## Usage and documentation

The installed package includes a client guide for people and coding assistants, plus the commented schema. No repository checkout is needed to read them:

```python
from importlib.resources import files

print(files("marketdata").joinpath("CLIENT_GUIDE.md").read_text())
print(files("marketdata.v1").joinpath("market_data.proto").read_text())
```

The guide covers exact decimals, optional values, methods, candle ranges, freshness, and errors. `help("marketdata")` gives a short package description. Type hints are included. Package versions identify client artifacts; `marketdata.v1` identifies the wire API.
