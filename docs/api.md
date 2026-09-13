# API and clients

Use this guide to request market data and handle errors. To start the service, follow the [quick start](quickstart.md). Field meanings are in the [data model](data-model.md).

## Connect

The service is `marketdata.v1.MarketDataService`. Its default address is `localhost:9090`. Each method takes one request and returns one response. There is no streaming, pagination, or HTTP data API.

Use the generated [Go client example](../api/go/examples/client/main.go), [Python client guide](../api/python/README.md), or [async Python example](../api/examples/client_async.py). The [Protobuf schema](../api/proto/marketdata/v1/market_data.proto) defines all fields. The [API package guide](../api/README.md) covers client versions and generation.

Reuse a channel across calls. Set a deadline on every call and a receive limit of 16 MiB (`16777216` bytes). There is no native TLS or authentication; see [network access](operations.md#network-access) before using a remote address.

For command-line requests, install `grpcurl` and run from the repository root. Reflection is disabled, so pass the checked-in `api/descriptor.binpb` file.

## Read snapshots

A snapshot is the last successfully collected list for an exchange and market. These methods read memory and never call an exchange:

| Method | Data | Optional filters |
| --- | --- | --- |
| `ListInstruments` | Instrument catalog and trading rules | `exchange`, `market`, `symbol`, `status` |
| `ListTickers` | Current prices, quotes, and funding | `exchange`, `market`, `symbol` |
| `ListMarketStats` | Rolling 24-hour statistics | `exchange`, `market`, `symbol`, `window` |

For example, list trading instruments on Bybit linear:

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 5 \
  -d '{"exchange":"bybit","market":"linear","status":"trading"}' \
  localhost:9090 marketdata.v1.MarketDataService/ListInstruments
```

Get one ticker:

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 5 \
  -d '{"exchange":"binance","market":"spot","symbol":"BTCUSDT"}' \
  localhost:9090 marketdata.v1.MarketDataService/ListTickers
```

Get statistics for the same instrument:

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 5 \
  -d '{"exchange":"binance","market":"spot","symbol":"BTCUSDT","window":"24h"}' \
  localhost:9090 marketdata.v1.MarketDataService/ListMarketStats
```

Filter rules:

- Omitted exchange and market filters select all matching enabled pairs. Unknown or disabled pairs fail with `invalid_filter`.
- Values are case-sensitive. Use `binance` or `bybit`, and `spot` or `linear`. See the [instrument statuses](data-model.md#instruments) for `status` values.
- Use the exact exchange symbol. Symbols contain 1–128 UTF-8 bytes, with no whitespace or control characters. The service does not trim or uppercase them.
- An omitted filter and an explicitly empty string are different. Empty strings fail validation. Omitted `window` means `24h`; every other value, including empty, gives `unsupported_window`.
- Every selected pair must have a first successful snapshot for the requested data type. Otherwise, the whole request fails with `data_not_ready`, even if a symbol or status filter would hide that pair's rows.

Rows are sorted by exchange, market, then symbol. A ready snapshot with no matches returns an empty list. A failed refresh keeps the previous data and timestamps. Check `updated_at` or `fetched_at` for freshness; snapshots do not expire automatically.

## Read candles

`GetKlines` requires `exchange`, `market`, `symbol`, `interval`, `from`, and `to`. It returns one series, with candles sorted by `open_time`.

The example below requests ten one-minute candles. Replace both timestamps with a recent aligned range inside your configured history window:

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 30 \
  -d '{"exchange":"binance","market":"spot","symbol":"BTCUSDT","interval":"1m","from":"2026-09-13T11:50:00Z","to":"2026-09-13T12:00:00Z"}' \
  localhost:9090 marketdata.v1.MarketDataService/GetKlines
```

`from` and `to` are Protobuf timestamps; `grpcurl` accepts the JSON timestamp form shown above. Both must be valid, at or after the Unix epoch, and exactly on [interval boundaries](data-model.md#intervals-and-calendars). The range is half-open: it includes a candle at `from`, but excludes one at `to`.

For closed candles, end the range at the start of the current slot. At 12:03:20 UTC, a one-minute request ending at 12:03:00 includes only closed slots. Ending at 12:04:00 explicitly includes the current candle. A later end is invalid. There is no `include_open` flag.

The default limit is 1,000 requested slots. The oldest allowed start is 1,000 slots before the current slot boundary. These are separate checks: a short range can still be too old. The current candle counts toward request size. Missing candles do not move the history window backwards. See [history settings](configuration.md#candle-history).

Validation checks required fields and supported scope/interval first, then timestamps and range shape, slot count, history depth, catalog readiness, and symbol existence. Invalid ranges do not fetch exchange data. An empty range (`from == to`) still needs a valid range, a ready catalog, and a known symbol; it returns an empty candle list without fetching. An empty range at the next slot boundary is allowed.

Confirmed closed candles come from cache. Missing or unconfirmed candles trigger a bounded load that concurrent callers can share. An open candle is refreshed for each request, possibly through shared work. A candle loaded before its close needs a new fetch started at or after close before the cache treats it as final.

The API returns the complete requested range or an error. It does not invent candles, trim a range, or return partial success. Pre-listing gaps and empty exchange pages can produce `incomplete_data`. Valid pages already fetched remain cached if later work fails. A range that expires while waiting returns `range_out_of_retention`.

## Errors

Application errors contain a gRPC status and a generated `ErrorDetail` with a stable `reason`. Use these values in client logic; do not parse the human-readable message.

| gRPC status | Reasons |
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

Connection failures, unknown methods, malformed wire messages, client deadlines, and stream resets may have no `ErrorDetail`. Clients must handle missing and unknown reasons. A forced transport cutoff may report a native reset status instead of an application timeout.

Retry according to the reason and your total deadline. Fix invalid input first. Reduce concurrent work on overload. Do not repeatedly retry the same incomplete range without checking why data is missing. See [troubleshooting](troubleshooting.md) for specific actions.

## Request limits

By default, all snapshot methods share 64 active request slots and a 5-second service timeout. Candles have 64 caller slots and a 30-second service timeout. These limits apply across all connections. Excess callers are rejected immediately.

Capacity stays in use during validation, response creation, serialization, and sending. Transport completion has an extra 5-second grace measured from the same admission time. An earlier client deadline wins. Canceling one caller does not cancel a shared candle load owned by the service.

Default message limits are 8 KiB per request and 16 MiB per response, measured as uncompressed Protobuf bytes. Headers are limited to 32 KiB. A response that is too large fails without truncation. Narrow snapshot filters or split a candle range into smaller valid requests; there is no pagination token.

See [configuration](configuration.md) to change limits and [architecture](architecture.md#grpc-request-ownership) for how the server enforces them.

[Documentation index](../README.md#documentation)
