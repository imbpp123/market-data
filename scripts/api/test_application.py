"""Installed Python client against actual transport, readers, storage and fills."""
import asyncio
import json
import os
import pathlib
import unittest
import time

import grpc
from google.protobuf.json_format import MessageToDict
from google.protobuf.timestamp_pb2 import Timestamp
from grpc_status import rpc_status
from marketdata.v1 import market_data_pb2 as pb
from marketdata.v1 import market_data_pb2_grpc as rpc

ADDRESS = os.environ["API_APPLICATION_ADDRESS"]
OPTIONS = [("grpc.max_receive_message_length", 16 * 1024 * 1024)]


def candle_request():
    return pb.GetKlinesRequest(exchange="binance", market="spot", symbol="A", interval="1m", **{"from": Timestamp(seconds=int(os.environ.get("API_CANDLE_FROM", 1789214280))), "to": Timestamp(seconds=int(os.environ.get("API_CANDLE_TO", 1789214400)))})


class ApplicationTest(unittest.TestCase):
    def setUp(self):
        self.channel = grpc.insecure_channel(ADDRESS, options=OPTIONS)
        self.addCleanup(self.channel.close)
        self.client = rpc.MarketDataServiceStub(self.channel)

    def test_all_rpc_responses_match_go(self):
        responses = {
            "instruments": self.client.ListInstruments(pb.ListInstrumentsRequest(), timeout=5),
            "tickers": self.client.ListTickers(pb.ListTickersRequest(), timeout=5),
            "statistics": self.client.ListMarketStats(pb.ListMarketStatsRequest(), timeout=5),
            "candles": self.client.GetKlines(candle_request(), timeout=5),
        }
        normalized = {key: MessageToDict(value, preserving_proto_field_name=True, always_print_fields_with_no_presence=True) for key, value in responses.items()}
        self.assertEqual(json.loads(pathlib.Path(os.environ.get("API_EXPECTED_FILE", "application-go.json")).read_text()), normalized)
        rows = responses["instruments"].instruments
        self.assertFalse(rows[0].HasField("min_qty"))
        self.assertTrue(rows[1].HasField("min_qty"))
        self.assertEqual("0", rows[1].min_qty)
        self.assertEqual("9" * 1024, rows[2].min_qty)
        self.assertEqual(123456789, rows[2].updated_at.nanos)
        self.assertEqual(9007199254740993, responses["statistics"].market_stats[0].trade_count)
        self.assertEqual(9007199254740993, responses["candles"].klines[0].trades_count)
        self.assertEqual(0, responses["tickers"].tickers[1].next_funding_in_seconds)
        self.assertEqual(not bool(os.environ.get("API_CANDLE_FROM")), responses["tickers"].tickers[1].HasField("next_funding_in_seconds"))

    def test_validation_and_error_details(self):
        invalid_timestamp = candle_request()
        getattr(invalid_timestamp, "from").nanos = 1000000000
        cases = [
            (self.client.ListTickers, pb.ListTickersRequest(symbol=""), grpc.StatusCode.INVALID_ARGUMENT, "invalid_parameter"),
            (self.client.ListInstruments, pb.ListInstrumentsRequest(exchange="BINANCE"), grpc.StatusCode.INVALID_ARGUMENT, "invalid_filter"),
            (self.client.ListMarketStats, pb.ListMarketStatsRequest(window=""), grpc.StatusCode.INVALID_ARGUMENT, "unsupported_window"),
            (self.client.GetKlines, invalid_timestamp, grpc.StatusCode.INVALID_ARGUMENT, "invalid_range"),
        ]
        for call, request, code, reason in cases:
            with self.subTest(reason=reason), self.assertRaises(grpc.RpcError) as failure:
                call(request, timeout=5)
            self.assertEqual(code, failure.exception.code())
            rich = rpc_status.from_call(failure.exception)
            self.assertIsNotNone(rich)
            detail = pb.ErrorDetail()
            self.assertTrue(rich.details[0].Unpack(detail))
            self.assertEqual(reason, detail.reason)

    def test_empty_missing_symbol_and_unknown_fields(self):
        self.assertEqual([], list(self.client.ListTickers(pb.ListTickersRequest(symbol="missing"), timeout=5).tickers))
        request = pb.ListTickersRequest.FromString(b"\xa0\x06\x01")
        self.assertEqual(2, len(self.client.ListTickers(request, timeout=5).tickers))
        empty = candle_request()
        getattr(empty, "from").CopyFrom(empty.to)
        result = self.client.GetKlines(empty, timeout=5)
        self.assertEqual("A", result.symbol)
        self.assertEqual([], list(result.klines))

    def test_native_errors_without_details(self):
        unknown = self.channel.unary_unary("/unknown.Service/Method")
        with self.assertRaises(grpc.RpcError) as failure:
            unknown(b"", timeout=5)
        self.assertEqual(grpc.StatusCode.UNIMPLEMENTED, failure.exception.code())
        self.assertIsNone(rpc_status.from_call(failure.exception))

    def test_native_connection_failure_without_details(self):
        with grpc.insecure_channel("127.0.0.1:1") as channel:
            with self.assertRaises(grpc.RpcError) as failure:
                rpc.MarketDataServiceStub(channel).ListTickers(pb.ListTickersRequest(), timeout=0.1)
            self.assertIn(failure.exception.code(), (grpc.StatusCode.UNAVAILABLE, grpc.StatusCode.DEADLINE_EXCEEDED))
            self.assertIsNone(rpc_status.from_call(failure.exception))

    @unittest.skipUnless(os.environ.get("API_CANDLE_FROM"), "Final composition partial-cache fixture")
    def test_partial_cache_four_async_clients_then_warm(self):
        async def check():
            async def read():
                async with grpc.aio.insecure_channel(ADDRESS, options=OPTIONS) as channel:
                    request = candle_request()
                    request.symbol = "B"
                    started = time.perf_counter_ns()
                    response = await rpc.MarketDataServiceStub(channel).GetKlines(request, timeout=5)
                    return response, time.perf_counter_ns() - started
            first = await asyncio.gather(*(read() for _ in range(4)))
            warm = await asyncio.gather(*(read() for _ in range(4)))
            for response, _ in first + warm:
                self.assertEqual(first[0][0], response)
                self.assertEqual(2, len(response.klines))
                self.assertEqual("12345.1234567890123456789", response.klines[0].open)
                self.assertEqual("1.1234567890123456789", response.klines[1].open)
                self.assertEqual(9007199254740993, response.klines[1].trades_count)
            print(json.dumps({"profile": "final_composition_partial_then_warm", "first_ns": [value for _, value in first], "warm_ns": [value for _, value in warm], "clients": 4, "rows": 2}), flush=True)
        asyncio.run(check())

    def test_async_client(self):
        async def check():
            async with grpc.aio.insecure_channel(ADDRESS, options=OPTIONS) as channel:
                client = rpc.MarketDataServiceStub(channel)
                responses = {
                    "instruments": await client.ListInstruments(pb.ListInstrumentsRequest(), timeout=5),
                    "tickers": await client.ListTickers(pb.ListTickersRequest(), timeout=5),
                    "statistics": await client.ListMarketStats(pb.ListMarketStatsRequest(), timeout=5),
                    "candles": await client.GetKlines(candle_request(), timeout=5),
                }
                normalized = {key: MessageToDict(value, preserving_proto_field_name=True, always_print_fields_with_no_presence=True) for key, value in responses.items()}
                self.assertEqual(json.loads(pathlib.Path(os.environ.get("API_EXPECTED_FILE", "application-go.json")).read_text()), normalized)
                with self.assertRaises(grpc.aio.AioRpcError) as failure:
                    await client.ListTickers(pb.ListTickersRequest(symbol=""), timeout=5)
                self.assertEqual(grpc.StatusCode.INVALID_ARGUMENT, failure.exception.code())
                detail = pb.ErrorDetail()
                self.assertTrue(rpc_status.from_call(failure.exception).details[0].Unpack(detail))
                self.assertEqual("invalid_parameter", detail.reason)
                with self.assertRaises(grpc.aio.AioRpcError) as failure:
                    await channel.unary_unary("/unknown.Service/Method")(b"", timeout=5)
                self.assertEqual(grpc.StatusCode.UNIMPLEMENTED, failure.exception.code())
                self.assertIsNone(rpc_status.from_call(failure.exception))
        asyncio.run(check())


if __name__ == "__main__":
    unittest.main()
