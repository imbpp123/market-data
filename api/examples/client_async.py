"""Async use of the same generated stub, without a second SDK."""
import asyncio
import sys

import grpc
from marketdata.v1 import market_data_pb2 as pb
from marketdata.v1 import market_data_pb2_grpc as rpc
from client import OPTIONS, candle_request, reason


async def main(address):
    async with grpc.aio.insecure_channel(address, options=OPTIONS) as channel:
        client = rpc.MarketDataServiceStub(channel)
        try:
            result = await client.ListInstruments(pb.ListInstrumentsRequest(), timeout=5)
            for row in result.instruments:
                print("min_qty", row.min_qty if row.HasField("min_qty") else None)
            await client.ListTickers(pb.ListTickersRequest(), timeout=5)
            await client.ListMarketStats(pb.ListMarketStatsRequest(), timeout=5)
            await client.GetKlines(candle_request(), timeout=30)
        except grpc.aio.AioRpcError as error:
            print("status", error.code().name, "reason", reason(error), file=sys.stderr)
            raise SystemExit(1) from error


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1] if len(sys.argv) > 1 else "localhost:9090"))
