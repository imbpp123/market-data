import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ListInstrumentsRequest(_message.Message):
    __slots__ = ("exchange", "market", "symbol", "status")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    status: str
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ..., status: _Optional[str] = ...) -> None: ...

class ListTickersRequest(_message.Message):
    __slots__ = ("exchange", "market", "symbol")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ...) -> None: ...

class ListMarketStatsRequest(_message.Message):
    __slots__ = ("exchange", "market", "symbol", "window")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    WINDOW_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    window: str
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ..., window: _Optional[str] = ...) -> None: ...

class GetKlinesRequest(_message.Message):
    __slots__ = ("exchange", "market", "symbol", "interval", "to")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    INTERVAL_FIELD_NUMBER: _ClassVar[int]
    FROM_FIELD_NUMBER: _ClassVar[int]
    TO_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    interval: str
    to: _timestamp_pb2.Timestamp
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ..., interval: _Optional[str] = ..., to: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., **kwargs) -> None: ...

class ListInstrumentsResponse(_message.Message):
    __slots__ = ("instruments",)
    INSTRUMENTS_FIELD_NUMBER: _ClassVar[int]
    instruments: _containers.RepeatedCompositeFieldContainer[Instrument]
    def __init__(self, instruments: _Optional[_Iterable[_Union[Instrument, _Mapping]]] = ...) -> None: ...

class ListTickersResponse(_message.Message):
    __slots__ = ("tickers",)
    TICKERS_FIELD_NUMBER: _ClassVar[int]
    tickers: _containers.RepeatedCompositeFieldContainer[Ticker]
    def __init__(self, tickers: _Optional[_Iterable[_Union[Ticker, _Mapping]]] = ...) -> None: ...

class ListMarketStatsResponse(_message.Message):
    __slots__ = ("market_stats",)
    MARKET_STATS_FIELD_NUMBER: _ClassVar[int]
    market_stats: _containers.RepeatedCompositeFieldContainer[MarketStats]
    def __init__(self, market_stats: _Optional[_Iterable[_Union[MarketStats, _Mapping]]] = ...) -> None: ...

class GetKlinesResponse(_message.Message):
    __slots__ = ("exchange", "market", "symbol", "interval", "klines")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    INTERVAL_FIELD_NUMBER: _ClassVar[int]
    KLINES_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    interval: str
    klines: _containers.RepeatedCompositeFieldContainer[Kline]
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ..., interval: _Optional[str] = ..., klines: _Optional[_Iterable[_Union[Kline, _Mapping]]] = ...) -> None: ...

class Instrument(_message.Message):
    __slots__ = ("exchange", "market", "symbol", "base_asset", "quote_asset", "status", "price_tick", "qty_step", "min_qty", "max_qty", "min_notional", "funding_interval_seconds", "delisting_time", "updated_at")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    BASE_ASSET_FIELD_NUMBER: _ClassVar[int]
    QUOTE_ASSET_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    PRICE_TICK_FIELD_NUMBER: _ClassVar[int]
    QTY_STEP_FIELD_NUMBER: _ClassVar[int]
    MIN_QTY_FIELD_NUMBER: _ClassVar[int]
    MAX_QTY_FIELD_NUMBER: _ClassVar[int]
    MIN_NOTIONAL_FIELD_NUMBER: _ClassVar[int]
    FUNDING_INTERVAL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    DELISTING_TIME_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    base_asset: str
    quote_asset: str
    status: str
    price_tick: str
    qty_step: str
    min_qty: str
    max_qty: str
    min_notional: str
    funding_interval_seconds: int
    delisting_time: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ..., base_asset: _Optional[str] = ..., quote_asset: _Optional[str] = ..., status: _Optional[str] = ..., price_tick: _Optional[str] = ..., qty_step: _Optional[str] = ..., min_qty: _Optional[str] = ..., max_qty: _Optional[str] = ..., min_notional: _Optional[str] = ..., funding_interval_seconds: _Optional[int] = ..., delisting_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Ticker(_message.Message):
    __slots__ = ("exchange", "market", "symbol", "last_price", "bid_price", "bid_size", "ask_price", "ask_size", "funding_rate", "next_funding_in_seconds", "fetched_at")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    LAST_PRICE_FIELD_NUMBER: _ClassVar[int]
    BID_PRICE_FIELD_NUMBER: _ClassVar[int]
    BID_SIZE_FIELD_NUMBER: _ClassVar[int]
    ASK_PRICE_FIELD_NUMBER: _ClassVar[int]
    ASK_SIZE_FIELD_NUMBER: _ClassVar[int]
    FUNDING_RATE_FIELD_NUMBER: _ClassVar[int]
    NEXT_FUNDING_IN_SECONDS_FIELD_NUMBER: _ClassVar[int]
    FETCHED_AT_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    last_price: str
    bid_price: str
    bid_size: str
    ask_price: str
    ask_size: str
    funding_rate: str
    next_funding_in_seconds: int
    fetched_at: _timestamp_pb2.Timestamp
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ..., last_price: _Optional[str] = ..., bid_price: _Optional[str] = ..., bid_size: _Optional[str] = ..., ask_price: _Optional[str] = ..., ask_size: _Optional[str] = ..., funding_rate: _Optional[str] = ..., next_funding_in_seconds: _Optional[int] = ..., fetched_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class MarketStats(_message.Message):
    __slots__ = ("exchange", "market", "symbol", "window", "high", "low", "volume", "turnover", "price_change", "trade_count", "fetched_at")
    EXCHANGE_FIELD_NUMBER: _ClassVar[int]
    MARKET_FIELD_NUMBER: _ClassVar[int]
    SYMBOL_FIELD_NUMBER: _ClassVar[int]
    WINDOW_FIELD_NUMBER: _ClassVar[int]
    HIGH_FIELD_NUMBER: _ClassVar[int]
    LOW_FIELD_NUMBER: _ClassVar[int]
    VOLUME_FIELD_NUMBER: _ClassVar[int]
    TURNOVER_FIELD_NUMBER: _ClassVar[int]
    PRICE_CHANGE_FIELD_NUMBER: _ClassVar[int]
    TRADE_COUNT_FIELD_NUMBER: _ClassVar[int]
    FETCHED_AT_FIELD_NUMBER: _ClassVar[int]
    exchange: str
    market: str
    symbol: str
    window: str
    high: str
    low: str
    volume: str
    turnover: str
    price_change: str
    trade_count: int
    fetched_at: _timestamp_pb2.Timestamp
    def __init__(self, exchange: _Optional[str] = ..., market: _Optional[str] = ..., symbol: _Optional[str] = ..., window: _Optional[str] = ..., high: _Optional[str] = ..., low: _Optional[str] = ..., volume: _Optional[str] = ..., turnover: _Optional[str] = ..., price_change: _Optional[str] = ..., trade_count: _Optional[int] = ..., fetched_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Kline(_message.Message):
    __slots__ = ("open_time", "close_time", "open", "high", "low", "close", "volume", "turnover", "trades_count", "fetched_at")
    OPEN_TIME_FIELD_NUMBER: _ClassVar[int]
    CLOSE_TIME_FIELD_NUMBER: _ClassVar[int]
    OPEN_FIELD_NUMBER: _ClassVar[int]
    HIGH_FIELD_NUMBER: _ClassVar[int]
    LOW_FIELD_NUMBER: _ClassVar[int]
    CLOSE_FIELD_NUMBER: _ClassVar[int]
    VOLUME_FIELD_NUMBER: _ClassVar[int]
    TURNOVER_FIELD_NUMBER: _ClassVar[int]
    TRADES_COUNT_FIELD_NUMBER: _ClassVar[int]
    FETCHED_AT_FIELD_NUMBER: _ClassVar[int]
    open_time: _timestamp_pb2.Timestamp
    close_time: _timestamp_pb2.Timestamp
    open: str
    high: str
    low: str
    close: str
    volume: str
    turnover: str
    trades_count: int
    fetched_at: _timestamp_pb2.Timestamp
    def __init__(self, open_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., close_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., open: _Optional[str] = ..., high: _Optional[str] = ..., low: _Optional[str] = ..., close: _Optional[str] = ..., volume: _Optional[str] = ..., turnover: _Optional[str] = ..., trades_count: _Optional[int] = ..., fetched_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ErrorDetail(_message.Message):
    __slots__ = ("reason",)
    REASON_FIELD_NUMBER: _ClassVar[int]
    reason: str
    def __init__(self, reason: _Optional[str] = ...) -> None: ...
