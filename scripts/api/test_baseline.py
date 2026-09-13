"""Check semantic normalization and fixture matrix without network measurements."""
import unittest

from http_baseline import normalized_protobuf, paths


class BaselineTest(unittest.TestCase):
    def test_optional_values_counts_and_duration_names(self):
        source = {"tickers": [{"next_funding_in_seconds": "0", "bid_price": "0"}, {}]}
        self.assertEqual([{"next_funding_in": 0, "bid_price": "0"}, {}], normalized_protobuf(source, "tickers"))
        self.assertEqual([{"trade_count": 9007199254740993}], normalized_protobuf({"market_stats": [{"trade_count": "9007199254740993"}]}, "market-stats"))

    def test_candles_restore_only_response_series_fields(self):
        source = {"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m", "klines": [{"open": "1.23", "trades_count": "0"}]}
        self.assertEqual([{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m", "open": "1.23", "trades_count": 0}], normalized_protobuf(source, "klines"))
        self.assertEqual([], normalized_protobuf({"exchange": "binance"}, "klines"))

    def test_required_fixture_matrix(self):
        self.assertEqual({"instruments-1", "instruments-20000", "tickers-1", "tickers-20000", "market-stats-1", "market-stats-20000", "klines-1", "klines-100", "klines-1000"}, set(paths()))


if __name__ == "__main__":
    unittest.main()
