"""Run from a clean installed package against the local Go fixture."""
import asyncio
import importlib.metadata
import importlib.util
import os
import pathlib
import shutil
import sys
import unittest

import grpc
from google.protobuf.timestamp_pb2 import Timestamp
from marketdata.v1 import market_data_pb2 as pb
from marketdata.v1 import market_data_pb2_grpc as rpc
from client import OPTIONS, candle_request, reason

ADDRESS = os.environ["API_FIXTURE_ADDRESS"]
DECIMAL = "12345.1234567890123456789"


class ContractTest(unittest.TestCase):
    def setUp(self):
        self.channel = grpc.insecure_channel(ADDRESS, options=OPTIONS)
        self.addCleanup(self.channel.close)
        self.client = rpc.MarketDataServiceStub(self.channel)

    def test_installation_is_isolated_and_typed(self):
        self.assertEqual("0.1.0", importlib.metadata.version("market-data-api"))
        self.assertTrue(pathlib.Path(pb.__file__).is_relative_to(pathlib.Path(sys.prefix)))
        package = pathlib.Path(pb.__file__).parent
        self.assertTrue((package / "market_data_pb2.pyi").is_file())
        self.assertTrue((package.parent / "py.typed").is_file())
        self.assertIsNone(importlib.util.find_spec("grpc_tools"))
        for tool in ("go", "protoc", "protoc-gen-go"):
            self.assertIsNone(shutil.which(tool))

    def test_values_presence_and_long_decimal(self):
        result = self.client.ListInstruments(pb.ListInstrumentsRequest(symbol="presence"), timeout=5)
        absent, zero, long = result.instruments
        self.assertFalse(absent.HasField("min_qty"))
        self.assertFalse(absent.HasField("funding_interval_seconds"))
        self.assertFalse(absent.HasField("delisting_time"))
        self.assertTrue(zero.HasField("min_qty"))
        self.assertEqual("0", zero.min_qty)
        self.assertTrue(zero.HasField("funding_interval_seconds"))
        self.assertEqual(0, zero.funding_interval_seconds)
        self.assertEqual("9" * 1024, long.min_qty)
        self.assertEqual((1789214400, 123456789), (long.updated_at.seconds, long.updated_at.nanos))
        tickers = self.client.ListTickers(pb.ListTickersRequest(symbol="presence"), timeout=5).tickers
        self.assertFalse(tickers[0].HasField("bid_price"))
        self.assertFalse(tickers[0].HasField("next_funding_in_seconds"))
        self.assertTrue(tickers[1].HasField("bid_price"))
        self.assertEqual("0", tickers[1].bid_price)
        self.assertTrue(tickers[1].HasField("next_funding_in_seconds"))
        self.assertEqual(0, tickers[1].next_funding_in_seconds)
        stats = self.client.ListMarketStats(pb.ListMarketStatsRequest(), timeout=5).market_stats[0]
        self.assertEqual(9007199254740993, stats.trade_count)
        self.assertEqual(DECIMAL, stats.high)
        self.assertEqual((1789214400, 123456789), (stats.fetched_at.seconds, stats.fetched_at.nanos))
        optional = self.client.ListMarketStats(pb.ListMarketStatsRequest(symbol="presence"), timeout=5).market_stats
        for field in ("price_change", "trade_count"):
            self.assertFalse(optional[0].HasField(field))
            self.assertTrue(optional[1].HasField(field))
        self.assertEqual("0", optional[1].price_change)
        self.assertEqual(0, optional[1].trade_count)

    def test_omitted_empty_canonical_and_unknown_fields(self):
        for value in (None, "", "binance"):
            with self.subTest(value=value):
                request = pb.ListInstrumentsRequest(symbol="echo")
                if value is not None:
                    request.exchange = value
                # New field 99 with an unknown string. Older readers must accept it.
                request = pb.ListInstrumentsRequest.FromString(request.SerializeToString() + b"\x9a\x06\x06future")
                result = self.client.ListInstruments(request, timeout=5).instruments[0]
                self.assertEqual(value is not None, result.HasField("min_qty"))
                if value is not None:
                    self.assertEqual(value, result.min_qty)

    def test_empty_multi_maximum_and_count_presence(self):
        for count in (0, 3, 1000):
            with self.subTest(count=count):
                request = candle_request()
                request.to.seconds = getattr(request, "from").seconds + 60 * count
                result = self.client.GetKlines(request, timeout=5)
                self.assertEqual(("binance", "spot", "S0000USDT", "1m"),
                                 (result.exchange, result.market, result.symbol, result.interval))
                self.assertEqual(count, len(result.klines))
                for index, row in enumerate(result.klines):
                    self.assertEqual(1789154400 + index * 60, row.open_time.seconds)
                    self.assertEqual(DECIMAL, row.close)
                    self.assertEqual(9007199254740993, row.trades_count)
        request = candle_request()
        request.symbol = "presence"
        request.to.seconds += 60
        result = self.client.GetKlines(request, timeout=5)
        self.assertFalse(result.klines[0].HasField("trades_count"))
        self.assertTrue(result.klines[1].HasField("trades_count"))
        self.assertEqual(0, result.klines[1].trades_count)

    def test_invalid_timestamp(self):
        request = candle_request()
        getattr(request, "from").nanos = 1000000000
        with self.assertRaises(grpc.RpcError) as failure:
            self.client.GetKlines(request, timeout=5)
        self.assertEqual(grpc.StatusCode.INVALID_ARGUMENT, failure.exception.code())

    def test_known_unknown_and_absent_details(self):
        for symbol, expected_code, expected_reason in (
            ("error", grpc.StatusCode.INVALID_ARGUMENT, "invalid_filter"),
            ("unknown-detail", grpc.StatusCode.UNAVAILABLE, None),
            ("native-error", grpc.StatusCode.UNAVAILABLE, None),
        ):
            with self.subTest(symbol=symbol), self.assertRaises(grpc.RpcError) as failure:
                self.client.ListTickers(pb.ListTickersRequest(symbol=symbol), timeout=5)
            self.assertEqual(expected_code, failure.exception.code())
            self.assertEqual(expected_reason, reason(failure.exception))

    def test_receive_cap(self):
        with grpc.insecure_channel(ADDRESS, options=[("grpc.max_receive_message_length", 1)]) as channel:
            with self.assertRaises(grpc.RpcError) as failure:
                rpc.MarketDataServiceStub(channel).ListTickers(pb.ListTickersRequest(), timeout=5)
            self.assertEqual(grpc.StatusCode.RESOURCE_EXHAUSTED, failure.exception.code())

    def test_deadline(self):
        grpc.channel_ready_future(self.channel).result(timeout=5)
        with self.assertRaises(grpc.RpcError) as failure:
            self.client.ListTickers(pb.ListTickersRequest(symbol="wait"), timeout=0.1)
        self.assertEqual(grpc.StatusCode.DEADLINE_EXCEEDED, failure.exception.code())

    def test_full_snapshots(self):
        for method, request, field in (
            (self.client.ListInstruments, pb.ListInstrumentsRequest(symbol="full"), "instruments"),
            (self.client.ListTickers, pb.ListTickersRequest(symbol="full"), "tickers"),
            (self.client.ListMarketStats, pb.ListMarketStatsRequest(symbol="full"), "market_stats"),
        ):
            result = method(request, timeout=5)
            self.assertEqual(20000, len(getattr(result, field)))
            self.assertLess(result.ByteSize(), 16 * 1024 * 1024)


class AsyncContractTest(unittest.IsolatedAsyncioTestCase):
    async def test_four_calls_and_error_details(self):
        async with grpc.aio.insecure_channel(ADDRESS, options=OPTIONS) as channel:
            client = rpc.MarketDataServiceStub(channel)
            self.assertEqual(DECIMAL, (await client.ListInstruments(pb.ListInstrumentsRequest(), timeout=5)).instruments[0].price_tick)
            self.assertEqual(DECIMAL, (await client.ListTickers(pb.ListTickersRequest(), timeout=5)).tickers[0].last_price)
            self.assertEqual(9007199254740993, (await client.ListMarketStats(pb.ListMarketStatsRequest(), timeout=5)).market_stats[0].trade_count)
            self.assertEqual(DECIMAL, (await client.GetKlines(candle_request(), timeout=5)).klines[0].close)
            with self.assertRaises(grpc.aio.AioRpcError) as failure:
                await client.ListTickers(pb.ListTickersRequest(symbol="error"), timeout=5)
            self.assertEqual("invalid_filter", reason(failure.exception))

    async def test_explicit_cancellation(self):
        async with grpc.aio.insecure_channel(ADDRESS, options=OPTIONS) as channel:
            call = rpc.MarketDataServiceStub(channel).ListTickers(pb.ListTickersRequest(symbol="wait"), timeout=5)
            call.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await call


if __name__ == "__main__":
    unittest.main()
