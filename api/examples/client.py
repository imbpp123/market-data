"""Synchronous client using one owned channel and finite call deadlines."""
import sys

import grpc
from google.protobuf.timestamp_pb2 import Timestamp
from grpc_status import rpc_status
from marketdata.v1 import market_data_pb2 as pb
from marketdata.v1 import market_data_pb2_grpc as rpc

OPTIONS = [("grpc.max_receive_message_length", 16 * 1024 * 1024)]


def reason(error):
    status = rpc_status.from_call(error)
    if status is not None:
        for detail in status.details:
            if detail.Is(pb.ErrorDetail.DESCRIPTOR):
                value = pb.ErrorDetail()
                detail.Unpack(value)
                return value.reason
    return None


def candle_request():
    return pb.GetKlinesRequest(
        exchange="binance", market="spot", symbol="S0000USDT", interval="1m",
        **{"from": Timestamp(seconds=1789154400), "to": Timestamp(seconds=1789154460)},
    )


def main(address):
    with grpc.insecure_channel(address, options=OPTIONS) as channel:
        client = rpc.MarketDataServiceStub(channel)
        try:
            instruments = client.ListInstruments(pb.ListInstrumentsRequest(), timeout=5)
            for row in instruments.instruments:
                print("min_qty", row.min_qty if row.HasField("min_qty") else None)
            client.ListTickers(pb.ListTickersRequest(), timeout=5)
            client.ListMarketStats(pb.ListMarketStatsRequest(), timeout=5)
            client.GetKlines(candle_request(), timeout=30)
        except grpc.RpcError as error:
            print("status", error.code().name, "reason", reason(error), file=sys.stderr)
            raise SystemExit(1) from error


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "localhost:9090")
