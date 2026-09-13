"""Python client messages and gRPC stubs for the Market Data service.

Import market_data_pb2 and market_data_pb2_grpc from marketdata.v1. Create a
MarketDataServiceStub on a reused grpc or grpc.aio channel, set a deadline for
each call, and allow 16777216-byte responses. Installing this package does not
start the service or connect directly to exchanges.

Decimals are exact strings; HasField distinguishes absent values from zero.
Candles use aligned UTC [from, to) ranges. Snapshot timestamps can be old after
refresh failures. Application errors carry an optional ErrorDetail reason.

Read the bundled usage guide for people and coding assistants:
    from importlib.resources import files
    print(files("marketdata").joinpath("CLIENT_GUIDE.md").read_text())

The commented schema is a resource at marketdata.v1/market_data.proto. Generated
messages and stubs are in marketdata.v1; they support Python 3.13 and 3.14.
"""
