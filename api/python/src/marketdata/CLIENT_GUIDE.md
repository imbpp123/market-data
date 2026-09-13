# Market Data client guide

This guide is for application developers and coding assistants using the Go or Python client. The client provides generated messages and a gRPC stub for a running Market Data service. Installing it does not start the service, collect data, or connect directly to an exchange.

## Connect and call

The service name is `marketdata.v1.MarketDataService`. The default gRPC address is `localhost:9090`; operational HTTP uses port 8080. Reuse one channel, set a deadline for each call, and allow responses up to 16 MiB (`16777216` bytes).

The server has no native TLS or authentication. Use plaintext only on a trusted connection. A protected remote connection needs a separately configured endpoint that supports gRPC HTTP/2.

Go imports `github.com/imbpp123/market-data/api/go/marketdata/v1`, creates a channel with `grpc.NewClient`, then calls `NewMarketDataServiceClient`. Use `context.WithTimeout` for call deadlines and `grpc.MaxCallRecvMsgSize(16777216)` for the response limit. Package help is available with `go doc github.com/imbpp123/market-data/api/go/marketdata/v1`.

Python imports `market_data_pb2` and `market_data_pb2_grpc` from `marketdata.v1`. Create `MarketDataServiceStub(channel)` with a `grpc` or `grpc.aio` channel. Pass `timeout=` on calls and set the channel option `("grpc.max_receive_message_length", 16777216)`. Async calls use `await` with the same messages and stub.

## Methods

| Method | Returns | Request fields |
| --- | --- | --- |
| `ListInstruments` | Cached catalog and ordinary limit-order rules | Optional `exchange`, `market`, `symbol`, `status` |
| `ListTickers` | Cached trade prices, bid/ask quotes, and funding | Optional `exchange`, `market`, `symbol` |
| `ListMarketStats` | Cached rolling 24-hour statistics | Optional `exchange`, `market`, `symbol`, `window` |
| `GetKlines` | One complete candle range | Required `exchange`, `market`, `symbol`, `interval`, `from`, `to` |

Each call returns one response. There is no pagination, streaming, multi-series candle request, or HTTP market-data API. Message constructors do not perform the server's business validation.

## Filters and readiness

Use exact names: exchanges `binance` and `bybit`; markets `spot` and `linear`. Binance linear means USD-M. Symbols are exact exchange identifiers, scoped by exchange and market. They contain 1–128 UTF-8 bytes without whitespace or control characters. Do not trim or uppercase them.

Omitted snapshot filters select all matching enabled scopes. An explicitly empty string is invalid. `window` defaults to `24h`; every other value is unsupported. Instrument statuses are `unknown`, `pre_launch`, `trading`, `halted`, `cancel_only`, `settling`, and `closed`.

Snapshot methods make no exchange calls. Every selected exchange/market pair must have a first successful snapshot for that data type, or the whole request fails with `data_not_ready`. Ready scopes with no matching rows return an empty list. Rows are sorted by exchange, market, then symbol.

Failed refreshes keep previous data and timestamps. Check `updated_at` or `fetched_at` for freshness. `/ready` only reports local service initialization. It does not guarantee that exchange data is ready or fresh.

## Values and presence

Decimals are exact strings. Use decimal arithmetic, not binary floats. Prices use quote asset per base asset unit; quantities and volume use base asset units; turnover uses quote asset units. The service bounds numeric text and fixed-point expansion to 1,024 characters/bytes as applicable, without rounding values to fit.

An absent optional value is different from zero. In Go, check an optional scalar pointer for `nil` before using it; getters alone lose presence information. In Python, use `HasField("field_name")`. Optional timestamps also have presence. Counts are signed 64-bit integers; do not pass them through a float.

Instrument steps and limits describe ordinary limit orders, not all exchange order checks. `funding_interval_seconds` describes a known interval, not a next-event time. `delisting_time` is a known scheduled time; absence is not a promise that delisting cannot happen.

Ticker `last_price` is a last trade price, not a mark or index price. Bid and ask each have both a price and size, or neither. `funding_rate` is a signed fraction (`0.0001` means `0.01%`). `next_funding_in_seconds` is recalculated on read; it becomes absent when the known event time arrives without a new schedule.

Statistics use the exchange's rolling `24h` window, not a UTC calendar day. `price_change` is an absolute price difference, not a percentage. Statistics and ticker freshness are independent. Bybit does not provide statistics or candle trade counts; their fields stay absent.

Times are Protobuf timestamps. `updated_at` is local completion time of an instrument refresh; `fetched_at` is local source-response receipt time. Cache reads do not update them. These are not exchange event timestamps.

## Candle ranges

Use aligned UTC boundaries and `[from, to)`: include the candle at `from`, exclude the candle at `to`. Both timestamps must be valid and at or after the Unix epoch. In Python, `from` is a keyword: construct with `**{"from": start, "to": end}` and read with `getattr(request, "from")`.

All supported markets accept `1m`, `3m`, `5m`, `15m`, `30m`, `1h`, `2h`, `4h`, `6h`, `12h`, `1d`, `1w`, and `1M`. Binance also accepts `8h` and `3d`; Binance spot alone accepts `1s`. Case matters: `1m` is a minute and `1M` is a calendar month. No interval aliases or synthetic resampling are provided.

Daily slots start at 00:00 UTC, weekly slots on Monday, and monthly slots on the first day of each calendar month. Binance three-day slots step from `1970-01-02T00:00:00Z` in three-day increments. A month is not 30 days.

At 12:03:20 UTC, a one-minute request ending at 12:03:00 includes only closed slots. Ending at 12:04:00 explicitly includes the current open candle; a later end is invalid. The default maximum is 1,000 requested slots, including an open candle. The oldest allowed start is 1,000 slots before the current slot boundary. A short old range can still be outside retention.

The current instrument catalog must be ready and contain the symbol, even for an empty range. There is no historical symbol discovery. A valid `from == to` returns an empty candle list; responses always identify the requested series. Candle rows are sorted by `open_time`, and `close_time` is exclusive.

Confirmed closed candles are reused from cache. Missing or unconfirmed slots start a bounded shared load. Each request refreshes an included open candle, possibly through shared work. A candle becomes final only after a successful fetch started at or after its close. Canceling one caller does not cancel the service-owned fill. Later exchange corrections to confirmed candles are not reconciled.

The result is the whole range or an error. No partial success, range trimming, or synthetic gap candles. Missing pre-listing data can produce `incomplete_data`. Successfully saved pages remain cached if later work fails. A range can expire while waiting.

## Errors and limits

Read the gRPC status and optional `ErrorDetail.reason`; do not parse the human-readable message. Go uses `status.Convert(err).Details()`. Python uses `grpc_status.rpc_status.from_call(error)` and unpacks an `ErrorDetail` from matching details. Native failures can have no detail; handle unknown reasons too.

| Status | Reasons |
| --- | --- |
| `INVALID_ARGUMENT` | `invalid_parameter`, `invalid_filter`, `invalid_status`, `unsupported_window`, `invalid_interval`, `invalid_range`, `request_too_large`, `range_out_of_retention` |
| `NOT_FOUND` | `symbol_not_found` |
| `UNAVAILABLE` | `data_not_ready`, `upstream_unavailable`, `upstream_error` |
| `RESOURCE_EXHAUSTED` | `service_overloaded`, `upstream_attempt_limit`, `response_too_large` |
| `FAILED_PRECONDITION` | `incomplete_data` |
| `DATA_LOSS` | `invalid_upstream_data` |
| `DEADLINE_EXCEEDED` | `request_timeout` |
| `CANCELLED` | `request_canceled` |
| `UNIMPLEMENTED` | `unsupported_operation` |
| `INTERNAL` | `internal_error` |

Fix invalid input before retrying. Reduce concurrent work on overload and wait for a real cooldown to clear. Do not blindly retry an unchanged incomplete range. Forced transport cutoffs may return a native stream-reset status rather than an application timeout.

Defaults are 5 seconds for a snapshot call and 30 seconds for a candle caller, with an earlier client deadline taking precedence. There are 64 shared snapshot caller slots and 64 candle caller slots across all connections. Requests are limited to 8 KiB and responses to 16 MiB of uncompressed Protobuf. Oversized responses fail without truncation. Narrow filters or split valid candle ranges when needed.

## Finding this guide after installation

Go modules include `CLIENT_GUIDE.md` at the module root. Find that directory with `go list -m -f '{{.Dir}}' github.com/imbpp123/market-data/api/go`. Use `go doc` for package and generated field descriptions.

Python packages include this guide and the commented schema. Read them without a source checkout:

```python
from importlib.resources import files

print(files("marketdata").joinpath("CLIENT_GUIDE.md").read_text())
print(files("marketdata.v1").joinpath("market_data.proto").read_text())
```

These files describe usage for people and coding assistants. Installation does not automatically load them into an assistant's context; point the assistant to this guide when integrating the client.
