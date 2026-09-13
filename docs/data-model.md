# Data model

This guide explains what the returned data means. Use the [API guide](api.md) for requests and the [Protobuf schema](../api/proto/marketdata/v1/market_data.proto) for the complete field list.

## Identity and values

An instrument is identified by `exchange + market + symbol`. `BTCUSDT` on Binance spot and `BTCUSDT` on Bybit linear are different instruments. Use the exchange's exact symbol and asset names, including any multiplier prefixes.

Prices are in quote asset units per base asset unit. For `BTCUSDT`, the base asset is BTC and the quote asset is USDT. Volume is in base asset units; turnover is in quote asset units. Do not calculate turnover as volume times the latest price. Do not assume a contract's quote asset is always its settlement asset.

Decimals travel as exact strings, such as `"0.000001"`. Use a decimal type in calculations; converting them to binary floating point can lose precision. The service accepts at most 1,024 bytes of source numeric text and 1,024 characters of fixed-point expansion before trimming fractional zeros. Values outside these bounds fail normalization instead of being rounded or truncated.

An absent optional value means unavailable or not applicable. It is different from an explicit zero. Counts use signed 64-bit integers. Times use Protobuf timestamps with nanosecond precision and are described in UTC throughout these guides.

## Instruments

An instrument describes the current catalog entry: assets, trading status, price and quantity steps, and available order limits. These limits describe an ordinary limit order with a price and quantity. They do not cover every exchange order rule or guarantee order acceptance.

For example, a price step of `0.01` means prices move in increments of `0.01` quote asset units. Quantity steps and limits use base asset units. Minimum notional is a minimum order value (`price × quantity`). A missing minimum quantity is not zero and should not be inferred from minimum notional without an order price.

The service maps exchange statuses to these values:

| Status | Meaning |
| --- | --- |
| `unknown` | Missing or unrecognized status |
| `pre_launch` | Before normal trading; some auction activity may exist |
| `trading` | Normal trading status |
| `halted` | Trading is stopped or paused |
| `cancel_only` | Only order cancellation is allowed |
| `settling` | Delivery or settlement is being prepared or performed |
| `closed` | Trading or the contract has ended |

An unknown status never becomes `trading`. When a symbol disappears from a successful catalog refresh, it is removed from the current catalog. The service does not create a synthetic `closed` entry or maintain a historical catalog.

`updated_at` is the local time when a complete instrument refresh finishes successfully. It is the same for all rows in that snapshot. It is not the time when the exchange changed an instrument. Failed refreshes preserve the old snapshot and timestamp.

### Funding and delisting

`funding_interval_seconds` is the known interval between regular funding settlements for an applicable perpetual contract. It is not a countdown. Spot and expiry contracts have no funding interval. Bybit intervals are converted from minutes; Binance intervals come from explicit `fundingInfo` entries. Missing Binance entries stay absent; the service does not assume eight hours.

`delisting_time` is a confirmed scheduled delisting time when the adapter supports it. Bybit perpetual delivery metadata can provide it. Expiry dates are not treated as delisting dates. Binance currently leaves this field absent. An absent value does not prove that an instrument will remain listed.

## Tickers

A ticker describes the latest collected trade price, best available bid and ask, and applicable funding data. The last price is not a mark price, index price, or bid/ask average.

Each quote side contains both price and size, or neither. Missing quotes are not replaced with the last price. Binance tickers join several REST responses from one collection cycle, so their fields are not a synchronized view of the exchange matching engine.

`funding_rate` is a signed fraction: `0.0001` means `0.01%`. It is the latest published regular funding rate, not an annual rate or a guaranteed future payment.

`next_funding_in_seconds` is calculated from the stored next event time when a response is built. The same current time is used for all rows in that response. Fractional seconds are dropped, so zero can mean an event less than one second away. Once the known event time arrives, the value becomes absent until the service receives a new schedule. Reads do not advance the schedule or change `fetched_at`.

## Market statistics

Market statistics describe a rolling `24h` window: high and low prices, volume, turnover, and optional price change and trade count. A rolling window is not a UTC calendar day. The exchange defines its boundaries, which may differ between exchanges.

`price_change` is an absolute price difference, not a percentage. Binance provides it directly. Bybit uses exact subtraction of the previous price from the current price; when the previous price is missing or zero, the change is absent. Bybit does not provide a trade count for this model.

Statistics have their own snapshot and freshness. The service does not build them from candles or derive other window sizes from `24h` values.

## Candles

A candle, also called a kline, contains open, high, low, and close trade prices, volume, turnover, and an optional trade count for one slot. These are ordinary trade candles, not mark-price or index-price candles. Binance supplies trade counts; Bybit leaves them absent.

Each candle covers `[open_time, close_time)`: the start is included and the end is excluded. A five-minute candle starting at 10:00 ends at 10:05. The adapter converts Binance's inclusive 10:04:59.999 end to 10:05:00. Bybit's end is calculated from the interval calendar.

An open candle contains changing values. Passing its close time does not make a cached copy final. The service confirms it with a successful request started at or after close. A response received after close can still contain intermediate data if its request started earlier.

The cache assumes that confirmed candles will not be revised later by the exchange. It does not reconcile later corrections. Missing slots remain missing; the service never creates zero candles to fill gaps.

## Intervals and calendars

All four supported exchange/market pairs accept `1m`, `3m`, `5m`, `15m`, `30m`, `1h`, `2h`, `4h`, `6h`, `12h`, `1d`, `1w`, and `1M`.

Additional intervals:

| Interval | Binance spot | Binance linear | Bybit spot / linear |
| --- | --- | --- | --- |
| `1s` | Supported | Not supported | Not supported |
| `8h` | Supported | Supported | Not supported |
| `3d` | Supported | Supported | Not supported |

Names are exact and case-sensitive: `1m` is a minute, `1M` is a calendar month. Aliases such as `60m` are not accepted. The service does not build unsupported intervals from shorter candles.

Daily candles start at 00:00 UTC. Weekly candles start on Monday at 00:00 UTC. Monthly candles run from the first day of one month to the first day of the next; a month is not 30 days. Binance three-day slots use the anchor `1970-01-02T00:00:00Z`, stepping forward in three-day intervals.

These calendars are implemented in [timeframe.go](../internal/domain/timeframe.go). Exchange interval tables live in the [Binance](../internal/infrastructure/exchange/binance/klines.go) and [Bybit](../internal/infrastructure/exchange/bybit/klines.go) adapters. Saved [exchange fixtures](../testdata/exchange/README.md) check weekly and three-day boundaries.

## Freshness

Ticker, statistics, and candle `fetched_at` values record local receipt time of successfully normalized source responses. A Binance ticker uses the latest receipt time among the responses needed for its collection cycle. Candles from different pages may have different receipt times.

Cache reads and failed refreshes do not update timestamps. A timestamp says when this service received data; it does not guarantee when the exchange produced it. Check age according to your application's needs. Keep the host clock synchronized because candle boundaries and funding countdowns depend on it.

[Documentation index](../README.md#documentation)
