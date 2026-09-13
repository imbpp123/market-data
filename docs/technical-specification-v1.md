# Market Data Service — Technical Specification v1

> **Status:** gRPC serves all market data, with a separate operational HTTP listener. Migration phases 1–3 are independently reviewed. Phase 4 is complete after independent review and correction of one finding. The full migration is complete. The [gRPC verification](grpc-migration-verification.md) records current traffic, installed clients and bounded Linux capacity. The [HTTP audit](release-verification-v1.md) remains historical evidence.
>
> This document keeps the content of the original 60 sections. Timeframe, MarketStats, and Market statistics API now have separate sections, with 65 sections in total. It includes Go 1.27.1 and request admission requirements, with matching changes in related sections.
>
> **Decision contract:** [Implementation decisions](implementation-contract-v1.md), [complete configuration](examples/config-v1.yaml), and [evidence](evidence/phase-01/README.md) close the phase 01 implementation choices. The [decision register](specification-decisions-v1.md) distinguishes user requirements, engineering defaults, and future verification gates.

Current decision status, evidence, and implementation gates are recorded in the [v1 decision register](specification-decisions-v1.md). Historical SDK checks reported below are not a reproducible test suite in this repository.

## 1. Service goals

Market Data Service is a separate Go service that fetches, normalizes, stores, and serves market data from crypto exchanges.

v1 supports:

- Binance
- Bybit

Main goals:

1. Provide one common API for different exchange APIs.
2. Reduce exchange requests by reusing cached and stored data.
3. Hide exchange SDK and API details from service clients.
4. Allow new exchanges and storage implementations without changes to business logic.
5. Provide a reliable market data source for a trading bot, MCP server, and other consumers.

Default enabled markets: `spot` and `linear` on both Binance and Bybit, confirmed on September 11, 2026. Binance `linear` means USDⓈ-M. Exchange and market settings may disable individual scopes.

Expected v1 workload, confirmed on September 11, 2026: 3–4 clients, up to 50 symbols, and three requested history windows: 1h candles for up to 20 days, 5m candles for 2 days, and 1m candles for 8 hours. These clients need only closed candles; the current open candle is not needed for this workload. This describes client demand, not a restriction on supported intervals, an instrument filter, or a retention policy. The [decision register](specification-decisions-v1.md#workload-calculation) records sizing assumptions and remaining load questions.

Clients may request after a new candle closes or repeat reads at other times. No fixed polling schedule is required. If the requested closed-candle range is complete and confirmed in cache, return it without upstream requests. Fetch missing or unconfirmed data through the existing planner and cache-fill coordination. Cache reads still use local resources and remain subject to applicable request bounds.

The service RAM limit is 1 GB, confirmed on September 11, 2026. Size conservatively against 1,000,000,000 bytes for the whole process. Verify memory suitability with sustained cache occupancy and concurrent operations before release. Sections 38 and 41 define the shared history depth of 1,000 candle slots per series; this limit alone does not prove that all requested series fit in memory.

Main data in v1:

- instruments;
- tickers;
- market statistics with a 24h window in v1;
- klines.

---

## 2. Technology

Language:

```text
Go 1.27.1
```

The Go version is fixed as of September 11, 2026, based on the [official release history](https://go.dev/doc/devel/release). The build, Dockerfile, and CI use the same fixed version, not a moving `latest`.

For all prices, volumes, quantities, rates, turnover, and other decimal market values, use:

```go
github.com/shopspring/decimal
```

and:

```go
decimal.Decimal
```

Do not use `float32` / `float64` for market values in the domain or application layer.

For example:

```go
type Ticker struct {
    LastPrice decimal.Decimal
    BidPrice  decimal.Decimal
    AskPrice  decimal.Decimal
}
```

Integer values stay integers:

```go
TradesCount int64
```

Timestamps:

```go
time.Time
```

Durations:

```go
time.Duration
```

---

## 3. Decimal precision

Exchange APIs often return market values as strings:

```json
{
  "price": "112345.12345678"
}
```

The adapter must convert the value directly:

```go
decimal.NewFromString(value)
```

Avoid this:

```go
float64
↓
decimal
```

because precision may already be lost.

If an official exchange SDK converts a market value to `float64` for an endpoint, we can skip the SDK for that endpoint. Use a custom HTTP adapter based on the official documentation.

The exchange SDK must never define our domain model.

---

## 4. Unified domain model

Convert all exchange-specific responses to our own structs.

For example:

```go
type Exchange string

const (
    ExchangeBinance Exchange = "binance"
    ExchangeBybit   Exchange = "bybit"
)
```

We should include the market type from the start:

```go
type Market string
```

For example:

```go
const (
    MarketSpot   Market = "spot"
    MarketLinear Market = "linear"
)
```

This is useful even if we only use some markets at first.

It keeps these instruments separate:

```text
BTCUSDT / Binance Spot
BTCUSDT / Binance Futures
BTCUSDT / Bybit Linear
```

They are different market instruments.

---

## 5. Instrument

Minimum v1 model:

```go
type Instrument struct {
    Exchange Exchange
    Market   Market

    Symbol     string
    BaseAsset  string
    QuoteAsset string

    Status InstrumentStatus

    PriceTick decimal.Decimal
    QtyStep   decimal.Decimal

    MinQty      *decimal.Decimal
    MaxQty      *decimal.Decimal
    MinNotional *decimal.Decimal

    FundingInterval *time.Duration
    DelistingTime   *time.Time

    UpdatedAt time.Time
}
```

The model does not need to cover all Binance and Bybit fields in advance.

We build the domain model from scratch and extend it when needed.

Exchange-specific structs must not appear in the public API.

### Limit order semantics

`QtyStep`, `MinQty`, `MaxQty`, and `MinNotional` describe a normal limit order (`LIMIT`, `GTC`). The order has an explicit price and quantity in `BaseAsset` units. These fields do not cover special post-only, RPI, iceberg, conditional, or reduce-only modes.

| Field | Meaning |
| --- | --- |
| `PriceTick` | Price step in `QuoteAsset` per unit of `BaseAsset`. |
| `QtyStep` | Quantity step for a normal limit order, in `BaseAsset` units. |
| `MinQty` | The applicable fixed minimum quantity; `null` if no separate limit is provided or applies. |
| `MaxQty` | The applicable fixed maximum quantity; `null` if no separate limit is provided or applies. |
| `MinNotional` | Minimum value of a normal limit order in `QuoteAsset`; the value is `price × quantity`. Use `null` if no separate limit is provided or applies. |

`MinQty`, `MaxQty`, and `MinNotional` are nullable. A missing limit must not become zero. Bybit spot does not provide a current separate minimum quantity, so `MinQty = nil`. Do not calculate a fixed `MinQty` from `MinNotional`: the result depends on the order price.

The gRPC API writes decimal fields as strings. Optional fields have explicit Protobuf presence, separate from zero. `PriceTick` and `QtyStep` are required for a supported instrument. Do not replace an empty, invalid, zero, or negative step with a default. The adapter returns a normalization error and does not publish a partial snapshot.

These fields provide reference limits for a normal limit order. They do not cover all exchange filters or guarantee that the exchange will accept an order. Dynamic price limits, account restrictions, and other checks are outside the v1 model.

### Field mapping

This check covers Bybit `spot` / `linear` and Binance Spot / USDⓈ-M. The tables define our adapter rules. They do not add markets to the enabled list in the config.

#### Identity and timestamps

| Our field | Bybit | Binance | Conversion |
| --- | --- | --- | --- |
| `Exchange` | Bybit adapter | Binance adapter | Constant `bybit` or `binance`. |
| `Market` | `category` | Selected API family | `spot` → `spot`; Bybit `linear` and Binance USDⓈ-M → `linear`. Check that the record matches the requested market. |
| `Symbol` | `symbol` | `symbol` | Keep the exchange identifier. Do not build it from base and quote. |
| `BaseAsset` | `baseCoin` | `baseAsset` | Keep the asset identifier, including multiplier prefixes. |
| `QuoteAsset` | `quoteCoin` | `quoteAsset` | Keep the quote asset identifier. |
| `Status` | `status` | `status` | Convert using the tables below. |
| `UpdatedAt` | No matching instrument timestamp | No matching instrument timestamp | UTC time when our service finishes fetching and normalizing the full snapshot successfully. Use the same time for all snapshot records. |

`UpdatedAt` is not the time when the exchange changed the parameters. Do not use `launchTime`, `onboardDate`, the exchange response time, or Binance `serverTime`. A failed refresh keeps the previous snapshot and `UpdatedAt`.

Empty required identifiers are a normalization error. Do not infer the market from a symbol suffix. Do not assume that `QuoteAsset` is the settlement currency for every contract type.

Sources: [Bybit Instruments Info](https://bybit-exchange.github.io/docs/v5/market/instrument), [Binance Spot Exchange Information](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/general#exchange-information), [Binance USDⓈ-M Exchange Information](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#exchange-information).

#### Price and quantity filters

In the Bybit table, quantity fields are inside `lotSizeFilter`, and the price field is inside `priceFilter`. Select Binance filters by `filterType`, regardless of their order in the array.

| Our field | Bybit linear | Bybit spot | Binance USDⓈ-M | Binance spot |
| --- | --- | --- | --- | --- |
| `PriceTick` | `tickSize` | `tickSize` | `PRICE_FILTER.tickSize` | `PRICE_FILTER.tickSize` |
| `QtyStep` | `qtyStep` | `basePrecision` | `LOT_SIZE.stepSize` | `LOT_SIZE.stepSize` |
| `MinQty` | `minOrderQty` | `null` | `LOT_SIZE.minQty` | `LOT_SIZE.minQty` |
| `MaxQty` | `maxOrderQty` | `maxLimitOrderQty` | `LOT_SIZE.maxQty` | `LOT_SIZE.maxQty` |
| `MinNotional` | `minNotionalValue` | `minOrderAmt` | `MIN_NOTIONAL.notional` | `MIN_NOTIONAL.minNotional` and/or `NOTIONAL.minNotional`, using the rule below |

Normalization rules:

1. Parse decimal strings directly into `decimal.Decimal`, without a float step. Bybit spot `basePrecision` is a decimal step, for example `"0.000001"`, not a number of decimal places.
2. Binance `pricePrecision`, `quantityPrecision`, `baseAssetPrecision`, and Bybit `priceScale` do not replace the step fields in the table.
3. Bybit spot: do not use the deprecated `minOrderQty`, `maxOrderQty`, or `maxOrderAmt`. For a normal limit order, do not use market/post-only maximums or the post-only multiplier.
4. Binance: do not use `MARKET_LOT_SIZE` instead of `LOT_SIZE`. Bybit linear: do not use `maxMktOrderQty` instead of `maxOrderQty`.
5. Binance spot: if both minimum-notional filters are present, use the larger applicable lower limit. If only one is present, use it. If neither is present, use `MinNotional = nil`. `applyToMarket` and `applyMinToMarket` do not disable the limit for a normal limit order.
6. Convert a missing optional limit to `nil`. Treat zero as no limit only when the rules of that exchange filter explicitly allow it. An invalid string is not a missing value; it causes a normalization error.
7. Negative limits are a normalization error. So is `MinQty > MaxQty` when both are set. Do not use `null` to hide an upstream request or parsing error.

Sources: [Bybit Instruments Info](https://bybit-exchange.github.io/docs/v5/market/instrument), [Binance Futures Filters](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/common-definition), [Binance Spot Filters](https://developers.binance.com/en/docs/products/spot/filters).

### Instrument status

Use our own enum:

```go
type InstrumentStatus string

const (
    InstrumentStatusUnknown    InstrumentStatus = "unknown"
    InstrumentStatusPreLaunch  InstrumentStatus = "pre_launch"
    InstrumentStatusTrading    InstrumentStatus = "trading"
    InstrumentStatusHalted     InstrumentStatus = "halted"
    InstrumentStatusCancelOnly InstrumentStatus = "cancel_only"
    InstrumentStatusSettling   InstrumentStatus = "settling"
    InstrumentStatusClosed     InstrumentStatus = "closed"
)
```

| Our status | Meaning |
| --- | --- |
| `unknown` | The status is missing or not recognized. |
| `pre_launch` | Preparation for normal trading or pre-market. Some auction operations may be available. |
| `trading` | The exchange reports normal trading status. This does not guarantee that a specific account can place an order. |
| `halted` | Trading is stopped or paused. This does not mean a final delisting. |
| `cancel_only` | Only order cancellation is allowed. |
| `settling` | Delivery or settlement is being prepared or is in progress. |
| `closed` | The exchange reports that trading or the contract has ended. Historical data may still be available. |

This is a simple model of the instrument lifecycle. It does not describe which trading actions are allowed.

#### Bybit status mapping

| Exchange status | Our `InstrumentStatus` |
| --- | --- |
| `PreLaunch` | `pre_launch` |
| `PendingOpen` | `pre_launch` |
| `Trading` | `trading` |
| `Delivering` | `settling` |
| `Closed` | `closed` |
| Missing, empty, or any unknown value | `unknown` |

Use this for supported categories. A row in the table does not mean that the endpoint returns that status for every category.

Source: [Bybit Instrument Status](https://bybit-exchange.github.io/docs/v5/enum#status).

#### Binance USDⓈ-M status mapping

| Exchange status | Our `InstrumentStatus` |
| --- | --- |
| `PENDING_TRADING` | `pre_launch` |
| `TRADING` | `trading` |
| `PRE_DELIVERING` | `settling` |
| `DELIVERING` | `settling` |
| `PRE_SETTLE` | `settling` |
| `SETTLING` | `settling` |
| `DELIVERED` | `closed` |
| `CLOSE` | `closed` |
| `TRADING_HALT` | `halted` |
| `TRADING_CANCEL_ONLY` | `cancel_only` |
| Missing, empty, or any unknown value | `unknown` |

Source: [Binance USDⓈ-M Contract Status](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/common-definition).

#### Binance spot status mapping

| Exchange status | Our `InstrumentStatus` |
| --- | --- |
| `TRADING` | `trading` |
| `END_OF_DAY` | `halted` |
| `HALT` | `halted` |
| `BREAK` | `halted` |
| `CANCEL_ONLY` | `cancel_only` |
| Missing, empty, or any unknown value | `unknown` |

Do not map `END_OF_DAY` or `BREAK` to a final instrument closure. Source: [Binance Spot Symbol Status](https://developers.binance.com/en/docs/products/spot/enums#symbol-status-status).

#### Status handling rules

Define the mapping explicitly for each exchange and market. Do not guess the status from part of a string, the delisting time, or an available ticker. An unknown status must not become `trading`. Store the record as `unknown` and write the original value to a structured log for checks. Do not add an exchange-specific field to the public model.

The Instruments RPC `status` filter accepts only our enum values. An unknown filter value returns `INVALID_ARGUMENT / invalid_status`.

The mapping table does not guarantee a full upstream catalog for all statuses. For refresh, explicitly define the upstream status filters and fetch all their pages. Bybit returns a limited set of statuses by default. If a symbol is absent from a successfully loaded full snapshot of the selected set, remove it from the current repository as described in section 20. Do not create a made-up record with status `closed`. A failed page must not cause replacement with a partial snapshot.

### Funding interval and delisting time

Checked against the official documentation on September 11, 2026. The derivatives below are Bybit linear and Binance USDⓈ-M. Do not assume these fields are supported on other markets.

| Domain field | Protobuf field | Meaning |
| --- | --- | --- |
| `FundingInterval *time.Duration` | optional `funding_interval_seconds`: integer seconds | The current known interval between regular funding settlements. It is not the next funding time or a history of interval changes. |
| `DelistingTime *time.Time` | optional `delisting_time`: Protobuf Timestamp | The known scheduled delisting time of this instrument on this market. |

Both fields have explicit Protobuf presence. The transport converts `time.Duration` to integer seconds, not nanoseconds. The adapter checks that the funding interval is positive.

`null` means that no applicable value was provided. Funding interval does not apply to spot and is `null`. `delisting_time: null` does not guarantee that there is no future delisting. We may have no confirmed time.

#### Bybit

The public linear endpoint `GET /v5/market/instruments-info` provides:

- `fundingInterval` in minutes: convert it to a domain duration. The gRPC API returns integer seconds, for example `480` minutes → `28800` seconds;
- `deliveryTime` in milliseconds: the documentation explicitly defines it as the delisting time for perpetual contracts. Convert `"0"` to `null`.

For expiry futures, `deliveryTime` is the expiry or delivery date. Do not automatically put it in `delisting_time`. The adapter must check `contractType`; `market=linear` alone is not enough. Funding interval applies only to perpetual contracts.

Source: [Bybit Instruments Info](https://bybit-exchange.github.io/docs/v5/market/instrument).

#### Binance USDⓈ-M

`GET /fapi/v1/fundingInfo` provides `fundingIntervalHours` for symbols with changed funding settings. Convert hours to a domain duration, then to integer seconds in the gRPC API. This response is not a full instrument catalog. Join it with `exchangeInfo` by symbol.

Phase 01 decision: use a valid explicit interval from the successful full `fundingInfo` response. A perpetual symbol absent from that response gets `null`; do not infer an eight-hour interval from the general FAQ. The FAQ base interval is not a current per-symbol guarantee, especially for inactive instruments. A failed or malformed response fails the refresh and preserves the previous snapshot instead of publishing new nulls or defaults. Unknown contract types and expiry futures have no inferred fallback. The interval can change and is refreshed with instruments. Captured explicit values and synthetic failure cases are in the [phase 01 evidence](evidence/phase-01/README.md).

Sources: [Binance Funding Rate Info](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#get-funding-rate-info), [Binance Funding Rates — default and interval changes](https://www.binance.com/en/support/faq/detail/360033525031).

`GET /fapi/v1/exchangeInfo` has a `deliveryDate` field. The checked documentation calls it Delivery Date and shows `4133404800000` for a perpetual contract. A nonzero timestamp alone does not confirm a scheduled delisting. Keep Binance `delisting_time` as `null` until the rules for this field are confirmed. Do not assume that any future date means delisting. The separate `Delist-Schedule` page did not provide a current API description during the check. No authoritative source was confirmed in phase 01. Keeping this field null is the v1 decision; a later non-null mapping requires new source evidence.

Source: [Binance Exchange Information](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#exchange-information).

#### Refresh and verification

Extra requests run during background instrument refresh and use the `instruments` budget, including pages and retries. The Instruments RPC still reads only the repository. If a required refresh source fails, keep the previous snapshot and its `UpdatedAt`. Do not silently clear known values or use a default. An unsupported field is different from a failed request to a supported source.

For identity, filters, and statuses, add table-driven unit tests without network access and adapter integration tests with `httptest.Server`:

- all explicit rows of each status mapping table, plus unknown, empty, and missing values;
- filter selection by `filterType` when the array order changes;
- different limit/market/post-only maximums: Instrument gets only the limit value;
- no deprecated Bybit spot fields; `MinQty = nil`, `QtyStep` comes from `basePrecision`;
- Binance precision metadata does not change steps;
- Binance spot: no minimum-notional filter, one filter, and both filters; with both, use the larger lower limit;
- exact decimal string conversion, missing optional limits, invalid strings, negative values, zero steps, and conflicting limits;
- Protobuf decimal strings and optional presence; one `UpdatedAt` for a successful snapshot;
- a page or parsing error keeps the previous snapshot and its `UpdatedAt`;
- an unknown RPC status filter returns `INVALID_ARGUMENT / invalid_status`.

Unit and integration tests must cover minutes and hours converted to seconds, `null` for fields that do not apply, `deliveryTime="0"`, perpetual versus expiry futures, and explicit optional presence on the wire. Check that a fundingInfo error does not trigger the default. Cover a symbol missing from a successful fundingInfo response (null), a successful empty response, and the special perpetual placeholder date (delisting_time remains null).

---

## 6. Ticker

Stored v1 domain model for Bybit spot/linear and Binance spot/USDⓈ-M. Internal `NextFundingAt` stores the absolute time. The ticker read model returns the remaining duration as `NextFundingIn`:

```go
type Ticker struct {
    Exchange Exchange
    Market   Market

    Symbol string

    LastPrice decimal.Decimal

    BidPrice *decimal.Decimal
    BidSize  *decimal.Decimal

    AskPrice *decimal.Decimal
    AskSize  *decimal.Decimal

    FundingRate   *decimal.Decimal
    NextFundingAt *time.Time

    FetchedAt time.Time
}
```

The tables below define adapter normalization rules. They do not automatically apply to inverse contracts or options.

### Field semantics

| Domain field | Protobuf field | Meaning |
| --- | --- | --- |
| `Exchange` | `exchange` | Exchange, set as an adapter constant. |
| `Market` | `market` | Market, based on the upstream API category or family. |
| `Symbol` | `symbol` | The exchange instrument identifier. Do not build it from base and quote. |
| `LastPrice` | `last_price` | Last trade price provided by the ticker endpoint. It is not the mark price, index price, or bid/ask midpoint. |
| `BidPrice` | `bid_price` | Best available buy price in the public source we use. |
| `BidSize` | `bid_size` | Quantity at the best buy price, in `BaseAsset` units. |
| `AskPrice` | `ask_price` | Best available sell price in the public source we use. |
| `AskSize` | `ask_size` | Quantity at the best sell price, in `BaseAsset` units. |
| `FundingRate` | `funding_rate` | Latest regular funding rate published by the exchange for the relevant interval, as a signed fraction of one. |
| `NextFundingAt` | Not published directly | Internal timestamp of the next funding event from upstream. Used to calculate the countdown on read. |
| `NextFundingIn` (read model, `*time.Duration`) | optional `next_funding_in_seconds`: integer seconds | Time left until the known next funding event when the response is built. |
| `FetchedAt` | `fetched_at` | Local UTC receipt time of the last successful response needed to build the current ticker. |

Prices are in `QuoteAsset` per unit of `BaseAsset`. Take asset identifiers and multiplier prefixes from Instrument without recalculating them. This unit rule covers only the supported spot and linear markets.

Ticker contains only current prices, quotes, and funding. Window statistics are stored separately in `MarketStats` from section 7.

### Upstream endpoints

| Exchange / market | Source | Assembly |
| --- | --- | --- |
| Bybit spot | `GET /v5/market/tickers?category=spot` | One response without `symbol` contains the ticker list. |
| Bybit linear | `GET /v5/market/tickers?category=linear` | One response without `symbol` contains the ticker list. |
| Binance spot | `GET /api/v3/ticker/price` + `GET /api/v3/ticker/bookTicker` | Join current price and best bid/ask by symbol. |
| Binance USDⓈ-M | `GET /fapi/v2/ticker/price` + `GET /fapi/v1/ticker/bookTicker` + `GET /fapi/v1/premiumIndex` | Join current price, best bid/ask, and funding by exact symbol. |

For Binance, make bulk requests without `symbol`/`symbols`. The checked SDKs support bulk calls without symbol/symbols (section 14). If upstream limits require batches, use bounded batches for the full selected set. Publish the snapshot only after all batches succeed. Do not make one request per symbol when a bulk endpoint is available.

All three Binance USDⓈ-M requests belong to one ticker collector cycle and the `tickers` budget. Count each HTTP retry separately. One successful provider call does not mean one HTTP request.

Sources checked on September 11, 2026: [Bybit Tickers](https://bybit-exchange.github.io/docs/v5/market/tickers), [Binance Spot Market Data](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/market), [Binance USDⓈ-M Market Data](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data), [Binance USDⓈ-M Book Ticker](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#symbol-order-book-ticker).

### Field mapping

The Bybit table uses fields from `result.list[]` for both supported categories. For Binance, `price`, `book`, and `funding` (`premiumIndex`, USDⓈ-M only) refer to responses from those endpoints. The Binance ticker collector does not call 24hr endpoints.

| Domain field | Bybit spot / linear | Binance spot | Binance USDⓈ-M |
| --- | --- | --- | --- |
| `Exchange` | `bybit` | `binance` | `binance` |
| `Market` | `result.category`: `spot` / `linear` | `spot` | `linear` |
| `Symbol` | `symbol` | `price.symbol` | `price.symbol` |
| `LastPrice` | `lastPrice` | `price.price` | `price.price` |
| `BidPrice` | `bid1Price` | `book.bidPrice` | `book.bidPrice` |
| `BidSize` | `bid1Size` | `book.bidQty` | `book.bidQty` |
| `AskPrice` | `ask1Price` | `book.askPrice` | `book.askPrice` |
| `AskSize` | `ask1Size` | `book.askQty` | `book.askQty` |
| `FundingRate` | Linear perpetual: `fundingRate`; spot: `null` | `null` | Perpetual: `funding.lastFundingRate` |
| `NextFundingAt` | Linear perpetual: `nextFundingTime`; spot: `null` | `null` | Perpetual: `funding.nextFundingTime` |
| `NextFundingIn` (read model) | `NextFundingAt - now`, using the rules below | `null` | `NextFundingAt - now`, using the rules below |
| `FetchedAt` | Time when our service receives the response | Later receipt time of the two responses | Latest local receipt time of the three responses in the current cycle |

Exchange timestamps in the table are expected in Unix milliseconds. Convert them to UTC `time.Time` with integer arithmetic. Do not request microsecond response mode from Binance. A change of units requires explicit adapter and test changes.

### Funding rate and countdown

Parse `FundingRate` directly from a decimal string. `"0.0001"` means `0.01%` for the relevant funding interval. Do not multiply it by 100 or convert it to a yearly rate. Negative rates are valid. Keep explicit zero; a missing or empty value gives `nil`; an invalid string causes a normalization error. This field describes the latest published value. It is not a payment history or a guaranteed final rate for a future settlement.

Bybit returns funding fields in the ticker response we already use. Binance needs a third bulk request, `premiumIndex`. Use its `lastFundingRate`, not `interestRate` or the historical funding endpoint. Sources: [Bybit Tickers](https://bybit-exchange.github.io/docs/v5/market/tickers), [Binance Mark Price and Funding Rate](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#mark-price).

Upstream `nextFundingTime` is an absolute Unix timestamp in milliseconds: a string from Bybit and an integer from Binance. The adapter converts it to UTC `NextFundingAt`. Empty, missing, or zero → `nil`. Negative, invalid, or overflowing values → normalization error. For spot and known expiry futures, both funding fields are `nil`, even if upstream uses numeric placeholders. For linear contracts of unknown type, a positive `nextFundingTime` is evidence of a provided funding schedule. If neither metadata nor a funding schedule confirms that funding applies, keep the fields `nil`.

On a ticker read, the application builds a read model with `NextFundingIn *time.Duration`. The transport writes it as optional `next_funding_in_seconds`: integer remaining seconds. Do not use `next_funding_time` for a duration. Keep timestamps and countdowns distinct.

Calculation rules:

1. Read `now` from replaceable clocks once for the whole response, including the list endpoint.
2. If `NextFundingAt == nil`, return `NextFundingIn = nil`.
3. If `NextFundingAt <= now`, return `nil`: the known time has arrived and there is no new schedule yet. Do not move it to the next interval automatically.
4. Otherwise, calculate `NextFundingAt.Sub(now)` as `time.Duration`. For the wire value, drop the fractional second with integer division by `time.Second`. `0` is possible only for a known event less than a second away.
5. Do not store the calculated countdown in the repository. Do not change the snapshot or `FetchedAt` on read. A new RPC to the cache recalculates the countdown without an exchange request.

Example: stored `NextFundingAt = 16:00:00 UTC` gives `next_funding_in_seconds = 90` at `15:58:30`, then `30` at `15:59:30`, then an absent value at `16:00:00` if no new ticker has arrived. This countdown assumes correctly synchronized system clocks. It is an estimate based on the known schedule, not a guarantee of the actual settlement time.

We can read `Instrument.FundingInterval` from an already loaded repository for the same `exchange + market + symbol`, including Bybit. This lookup must not start an upstream fetch or block ticker until instruments are loaded. The interval defines frequency, but not the schedule anchor. It alone cannot define `NextFundingAt`, round the current time to 4/8 hours, or replace missing `nextFundingTime`. The source of the next event is the Bybit ticker response or Binance premiumIndex. A missing instrument cache does not prevent use of an explicit funding schedule from these responses.

### Optional quotes

Normalize bid and ask independently. The price and quantity of one side are either both set or both `nil`.

Adapter rules for an absent side:

- if both upstream values are missing/empty or both are zero, store two `nil` values;
- if quantity is explicitly zero and price is positive, the side is also empty; both fields are `nil`;
- positive quantity with a missing/zero price, only one filled field without clear evidence of an empty side, or a negative or invalid value is a normalization error;
- keep two positive decimal values without rounding.

These rules define our contract for missing quotes. They do not allow an HTTP request error to become `null` values. Do not replace a missing quote with `LastPrice`, mark price, or the other side. Do not use `lastQty` as bid/ask size: it is a different value.

### Timestamp semantics

Measure `FetchedAt` locally when the response arrives and save it after successful normalization. For a snapshot from one response, all records share the same time. For several responses, use the mapping table rule. Repository reads and failed refreshes do not change `FetchedAt`. It shows receipt time and does not guarantee fresh matching engine data.

### Binance ticker snapshot assembly

The price response defines the main symbol set. After two successful responses for spot or three for USDⓈ-M, left join book/funding by exact symbol:

1. For each price record, set LastPrice and the available quotes/funding from the current cycle.
2. A missing book/funding record gives `nil` for the related optional fields.
3. A symbol found only in book/funding does not create a ticker without a price record.
4. Record order does not affect the result. A duplicate symbol in one response is a normalization error.

If a required HTTP request, batch, or normalization fails, keep the previous ticker snapshot and FetchedAt. Do not mix parts from different cycles. After successful assembly, perform one atomic ReplaceSnapshot. Warn when a price symbol is missing from book or funding, but do not treat this as an HTTP error. Ignore symbols found only in book or funding without a warning.

MarketStats is not part of this join. A failure in its collector does not cancel ticker publication or stop the ticker worker. Several REST responses are not a synchronized matching engine snapshot. FetchedAt does not mean that all parts were produced at the same time.

### Decimal parsing and JSON

Parse all market values from the original decimal strings without a `float32`/`float64` step. If an endpoint returns a JSON number, keep its original text with `json.Number` or an equivalent and parse it as decimal. An SDK that first converts this value to float is not suitable for this path.

Required `LastPrice` must not get a zero default for a missing value, an empty string, or a parsing error: the refresh fails. Keep explicitly provided nonnegative numbers, including zero, without guessing missing trades. Negative LastPrice is invalid; negative FundingRate is valid.

Do not validate the assembled ticker as an order book from one point in time.

In Protobuf, all decimal fields are strings and optional fields have explicit presence. `fetched_at` is a Timestamp. Do not serialize internal `NextFundingAt`. Return optional `next_funding_in_seconds` as integer seconds instead.

### Ticker verification

Table-driven unit tests and adapter integration tests with `httptest.Server`, without production network access:

- all ticker mapping fields for the four exchange/market pairs;
- Binance spot makes price + book requests; neither Binance ticker collector requests 24hr;
- bid/ask can be absent independently; zero sides; invalid price/size pair; `lastQty` does not affect quote size;
- Binance USDⓈ-M: three requests, join by symbol with different order, different sets, and duplicates;
- failure of the second/third request or a batch keeps the previous snapshot and timestamps;
- every HTTP request and retry uses the `tickers` budget;
- required empty/invalid numeric fields do not become zero;
- ticker messages keep decimal strings and optional presence and have no statistics fields;
- funding rate: positive/negative values, explicit zero, empty value, and parsing error;
- spot and expiry futures do not get funding from placeholders; unknown type without a confirmed schedule → `nil`;
- nextFundingTime: string/integer milliseconds, zero, missing, negative value, and overflow;
- countdown decreases on repeated reads of one snapshot; less than a second → `0`, time reached → `null`;
- the list endpoint uses one `now`; clock tests are deterministic;
- present/missing instrument cache does not cause an upstream fetch; FundingInterval alone does not create a schedule;
- internal NextFundingAt is not on the wire; NextFundingIn is written in seconds;
- a cache read does not change FetchedAt or make upstream requests.

---

## 7. Market statistics

`MarketStats` is a separate domain model for market statistics over a time window. Section 37 describes its gRPC API, which reads a separate repository. The shared Bybit upstream fetch does not merge the models or their API contracts.

### Domain model

```go
type MarketStats struct {
    Exchange Exchange
    Market   Market
    Symbol   string

    Window time.Duration

    High     decimal.Decimal
    Low      decimal.Decimal
    Volume   decimal.Decimal
    Turnover decimal.Decimal

    PriceChange *decimal.Decimal
    TradeCount  *int64

    FetchedAt time.Time
}
```

`Window` is the duration of a rolling statistics window, not the collector refresh interval. In v1, the only supported value is `24 * time.Hour` for every exchange. The gRPC API accepts window `24h`. Model and field names have no `24h` suffix, so we can add `2h`, `3h`, or `6h` later without changing the response structure.

Other windows in v1 return `INVALID_ARGUMENT / unsupported_window` before cache reads or any upstream action. Do not label 24h statistics as another window, scale volume by a ratio, or treat a 24h window as a calendar day. A future window needs explicit provider support or a separate calculation with defined boundaries, rules for incomplete data, and tests. v1 does not build new windows from klines.

`FetchedAt` is the local receipt time of the source response for these statistics. Do not copy it from a newer ticker or update it on a cache read. Each model has its own freshness. The exchange defines the actual rolling window boundaries and calculation. `Window=24h` does not promise matching boundaries across exchanges or a full 24h history for a new instrument.

### Statistics field mapping

Bybit uses the same `result.list[]` record received by the ticker collector. Binance uses a separate 24hr response. The table applies to spot/linear.

| Domain field | Protobuf field | Bybit | Binance spot / USDⓈ-M |
| --- | --- | --- | --- |
| `Exchange` | `exchange` | `bybit` | `binance` |
| `Market` | `market` | `result.category` | From the API family |
| `Symbol` | `symbol` | `symbol` | `symbol` |
| `Window` | `window` | Canonical string `24h` in the API; duration 24h internally | Same |
| `High` | `high` | `highPrice24h` | `highPrice` |
| `Low` | `low` | `lowPrice24h` | `lowPrice` |
| `Volume` | `volume` | `volume24h` | `volume` |
| `Turnover` | `turnover` | `turnover24h` | `quoteVolume` |
| `PriceChange` | `price_change` | `lastPrice - prevPrice24h` from the same source record | `priceChange` |
| `TradeCount` | `trade_count` | `null` | `count` |
| `FetchedAt` | `fetched_at` | Local receipt time of the shared response | Local receipt time of the 24hr response |

High/Low are prices in QuoteAsset per unit of BaseAsset. Volume is a BaseAsset quantity. Turnover is in QuoteAsset. PriceChange is a signed price difference, not a percentage. Do not calculate Turnover by multiplying Volume by the current LastPrice.

Required High/Low/Volume/Turnover values are parsed exactly. Do not replace missing, empty, or invalid values with zero; keep an explicitly provided nonnegative zero. An error in a required field cancels only that stats snapshot. PriceChange and TradeCount are nullable. On the wire, decimals are strings, TradeCount is int64, and optional values have presence. Write Window as the canonical string `24h` and FetchedAt as Timestamp.

### Price change

For Bybit, use exact decimal subtraction: `lastPrice - prevPrice24h`. If the previous price applies, lastPrice is required. A missing, empty, negative, or invalid value cancels the stats snapshot. If the previous price is missing or zero, PriceChange stays nil and the statistics branch does not need lastPrice. Example: `105.25 - 100 = 5.25`; a fall to `95` gives `-5`; an unchanged price gives `0`.

Do not use `price24hPcnt` instead of the difference. Do not recover the previous price from a rounded relative change. For Binance, use `priceChange`, not `priceChangePercent`. For example, a change of `5` from a starting price of `200` must stay `5`, not `2.5` or `0.025`.

`PriceChange` is optional. Missing/empty Bybit `prevPrice24h` gives `nil`; do not use the current price instead. In our model, a zero previous price means no reference price and also gives `nil`. A negative or invalid value is a normalization error. Missing/empty Binance `priceChange` gives `nil`; an explicitly provided valid `"0"` stays a zero change.

For Bybit, `TradeCount = nil`; do not make extra requests to count trades. For Binance, parse `count` as a nonnegative `int64`. An explicit `0` stays a pointer to zero. A missing field gives `nil`; a fractional value, negative value, empty string, or overflow is a normalization error.

### Statistics sources and collection

| Exchange / market | MarketStats source for Window=24h | Mode |
| --- | --- | --- |
| Bybit spot / linear | Already fetched `/v5/market/tickers` | Shared fetch with ticker; no separate stats polling. |
| Binance spot | `GET /api/v3/ticker/24hr?type=FULL` | Independent statistics background worker. |
| Binance USDⓈ-M | `GET /fapi/v1/ticker/24hr` | Independent statistics background worker. |

Binance stats requests omit symbol/symbols for bulk loading. If upstream requires an explicit symbol list, the adapter uses bounded batches with the same completeness rules as the ticker collector. The checked SDK itself does not require this change. Spot must use FULL because the shorter response does not guarantee all required fields.

Bybit: decode one HTTP response once, then normalize it independently into a full ticker snapshot and a full MarketStats snapshot. PriceChange uses lastPrice from that response, not TickerRepository. A statistics branch error keeps the old MarketStats and does not block a valid Ticker. A ticker branch error does not block valid statistics. A transport or shared envelope error prevents both updates. Publish each successful branch atomically and separately. No transaction across the two repositories is needed. Publishing one branch must not interrupt processing of the other.

For Bybit, /market-stats reads the repository and never calls /market/tickers again. If the ticker collector stops, statistics also stop updating. There is no separate fallback fetch.

Binance: the stats worker fetches immediately at startup, then waits for `market_stats.refresh_interval` after each cycle ends. The default is `30s`, configurable independently of ticker. Allow at most one active refresh per exchange/market/window. Batches are bounded and published only as a full snapshot. Ticker and stats do not update each other's repositories. A failure in one independent branch does not block the other.

Count the shared Bybit request once in the `tickers` budget, even when it updates two models. Independent Binance stats requests use the separate `market_stats` budget. All requests and retries also pass the common exchange limits. The new budget does not increase the total limit. It is a share of the same total budget, as described in section 32.

Sources: [Bybit Tickers](https://bybit-exchange.github.io/docs/v5/market/tickers), [Binance Spot 24hr](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/market#24hr-ticker-price-change-statistics), [Binance USDⓈ-M 24hr](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#24hr-ticker-price-change-statistics).

### Statistics verification

- Full field mapping for the four exchange/market combinations; exact decimal values and units.
- PriceChange: rise, fall, zero, and missing/zero previous price; a price change does not become a percentage.
- Bybit TradeCount=nil; Binance count: zero, missing, negative/fractional value, and overflow.
- One Bybit upstream request updates two models; an error in one projection does not cancel the other.
- Binance ticker does not call 24hr; stats does not call price/book/funding; refresh rates are independent.
- Different windows use separate repository keys; unsupported 2h/3h/6h return unsupported_window without upstream calls.
- Before the first successful snapshot, the API returns `UNAVAILABLE / data_not_ready`; a cache hit makes no requests.
- A failed refresh keeps the previous data and FetchedAt; a stats failure does not make ticker older.
- Each data RPC response contains only its own data type; responses use one window format.
- Stats requests and retries follow their separate budget and bounds; shutdown cancels active and waiting stats workers.

---

## 8. Kline

```go
type Kline struct {
    Exchange Exchange
    Market   Market

    Symbol   string
    Interval Timeframe

    OpenTime  time.Time
    CloseTime time.Time

    Open  decimal.Decimal
    High  decimal.Decimal
    Low   decimal.Decimal
    Close decimal.Decimal

    Volume   decimal.Decimal
    Turnover decimal.Decimal

    TradesCount *int64

    FetchedAt time.Time
}
```

The model describes normal trade candles for Bybit spot/linear and Binance spot/USDⓈ-M linear. This mapping does not cover inverse contracts, mark/index/premium price candles, or continuous contract candles. Even if an endpoint accepts more contract types, the adapter must stay within the selected market scope.

### Upstream endpoints

| Exchange / market | Endpoint | Request context |
| --- | --- | --- |
| Bybit spot | `GET /v5/market/kline` | Explicit `category=spot`, symbol, interval. |
| Bybit linear | `GET /v5/market/kline` | Explicit `category=linear`, symbol, interval. |
| Binance spot | `GET /api/v3/klines` | Symbol, interval; UTC timezone. Do not use uiKlines. |
| Binance USDⓈ-M linear | `GET /fapi/v1/klines` | Symbol, interval; normal trade klines. |

Each request is for one symbol and interval. Pagination and retries use the common exchange budget and the klines budget. One provider call may include several HTTP requests.

### Field mapping

Below, `row[n]` is a zero-based index inside one candle. Bybit returns rows in `result.list[]`; Binance returns a root response array. Identifiers taken from the request come from the validated context of that provider call.

| Domain field | Protobuf field | Bybit spot / linear | Binance spot / USDⓈ-M linear |
| --- | --- | --- | --- |
| `Exchange` | `exchange` | Adapter constant: bybit | Adapter constant: binance |
| `Market` | `market` | result.category; check that it matches the request | From the API family: spot / linear |
| `Symbol` | `symbol` | result.symbol; check that it matches the request | From the request; an individual row has no symbol |
| `Interval` | `interval` | From the request, after conversion to Timeframe | From the request, after conversion to Timeframe |
| `OpenTime` | `open_time` | row[0], startTime: string Unix milliseconds | row[0], open time: integer Unix milliseconds |
| `CloseTime` | `close_time` | Calculate the next boundary from OpenTime using Interval | row[6], close time: integer Unix milliseconds; add 1ms for the exclusive boundary |
| `Open` | `open` | row[1], openPrice | row[1], open |
| `High` | `high` | row[2], highPrice | row[2], high |
| `Low` | `low` | row[3], lowPrice | row[3], low |
| `Close` | `close` | row[4], closePrice | row[4], close |
| `Volume` | `volume` | row[5], volume | row[5], volume |
| `Turnover` | `turnover` | row[6], turnover | row[7], quote asset volume |
| `TradesCount` | `trades_count` | Not provided: nil | row[8], number of trades: nonnegative int64 |
| `FetchedAt` | `fetched_at` | Local HTTP response receipt time | Local HTTP response receipt time |

Do not mix positions: Bybit row[6] is turnover; Binance row[6] is a timestamp. Extra Binance taker-buy fields and the unused field are not used.

Section 9 defines Timeframe, its operations, and exchange mapping.

### Time boundaries and freshness

Domain and gRPC use the half-open candle range `[OpenTime, CloseTime)`. CloseTime is the end of the time slot. It is not the last trade time or proof that the data is final.

Conversion rules:

1. Parse timestamps with integer arithmetic and range checks, then convert to UTC time.Time. A missing, invalid, or negative timestamp, or overflow, is a normalization error. Do not use float.
2. Request Binance spot in UTC (`timeZone=0` or the default), without microsecond response mode. The adapter expects milliseconds. Do not guess units from the number of digits.
3. Convert Binance inclusive close time to exclusive CloseTime by adding exactly 1ms, with an overflow check. Check CloseTime > OpenTime and the interval duration. A mismatch is an error; do not silently correct upstream data.
4. Bybit does not return CloseTime. Calculate the next boundary from OpenTime with the Timeframe rules in section 9. For Binance, use the same operation to check the normalized CloseTime. Do not copy calendar rules into adapters.
5. Do not calculate CloseTime from the next returned row: a response may have gaps or only one candle. Take the candle start from upstream and do not round it freely. Planner request alignment must match the selected exchange calendar and interval.

Example: for a 5m candle with OpenTime=10:00:00, Binance returns close time 10:04:59.999. In the domain model, both exchanges use CloseTime=10:05:00. For the February 2028 monthly candle, the boundaries are `[2028-02-01T00:00:00Z, 2028-03-01T00:00:00Z)`.

Record FetchedAt when the full successful HTTP response arrives, before normalization. Save it only with successfully normalized data. All rows on one page share the timestamp; different pages may have different times. Cache reads and failed refreshes do not change FetchedAt. This is our service's receipt time, not an exchange event timestamp.

REST kline responses have no separate flag that marks a candle as final. An open candle contains intermediate OHLC and accumulated Volume/Turnover/TradesCount. Close is the latest available trade price in that slot. Do not mix it with a ticker value.

Clarification of the cache rules in sections 26–29: a candle loaded before CloseTime still needs a refresh after that boundary. Local time passing does not make intermediate values final. A successful fetch with a request started after CloseTime is required. Store request start time as internal cache-fill information, not a new domain/HTTP field. FetchedAt >= CloseTime alone is not enough: the request may have started before the boundary. v1 assumes synchronized clocks and stable closed candles after this fetch. Later exchange corrections are not tracked separately.

### Numeric values and missing data

For the supported spot/linear markets, OHLC are prices in QuoteAsset per unit of BaseAsset. Volume is a BaseAsset quantity; Turnover is in QuoteAsset. Do not recalculate symbol identifiers or multipliers yourself. Do not calculate Turnover as Volume × Close. Inverse contracts need a different unit mapping and are outside this model.

OHLC, Volume, and Turnover are required. Parse source decimal strings directly into decimal.Decimal without float32/float64. A missing/empty, invalid, or negative value is an error. Do not replace explicit numeric zero with nil. Check Low <= Open/Close <= High. Do not infer that there were no trades from zero price or volume alone.

Bybit TradesCount is always nil: the endpoint does not provide a trade count. Do not make extra requests or estimate it from volume. Binance count is required by the upstream contract. Keep zero as a pointer to 0. Missing, null, fractional/negative values, or overflow are errors. The shared model is nullable because Bybit has no value, not to hide a broken Binance response.

A symbol/category mismatch, incomplete row, or error in a used field fails normalization of the whole page. Do not publish a partial page. Earlier successfully saved pages can stay in cache, but do not return the requested range as complete if loading the rest fails.

Bybit returns rows from newest to oldest. The adapter returns them in ascending OpenTime order, regardless of upstream order. Reject duplicate OpenTime values within a page. Handle overlapping pages with upsert by exchange/market/symbol/interval/OpenTime. An older result must not overwrite a newer open candle. An empty successful response is not a zero candle. Do not create missing slots or mark them as loaded data.

In Protobuf, all decimal fields are strings. trades_count is optional int64. Interval is a canonical string from the section 9 table and appears once in the response series identity. All three times use Timestamp with nanosecond precision. Serialization must keep the exclusive meaning of CloseTime.

### Sources and verification

Mapping checked on September 11, 2026, against the official [Bybit Kline](https://bybit-exchange.github.io/docs/v5/market/kline), [Binance Spot Kline](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/market#klinecandlestick-data), and [Binance USDⓈ-M Kline](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#klinecandlestick-data) descriptions. The exclusive boundary, validation, and cache policy above are our normalization contract on top of these APIs.

Table-driven unit tests and adapter integration tests with httptest.Server, without production network access:

- All mapping fields for the four exchange/market combinations; request endpoint, category, symbol, and interval.
- Different turnover and close time positions; no dependency on unused taker-buy fields.
- Exact decimals, large values, and long fractional parts; zeros; missing/invalid values and OHLC rules.
- TradesCount: Bybit nil; Binance positive values and zero; missing, null, negative, fractional values, and overflow.
- String/integer milliseconds, UTC, exclusive boundary, wrong duration, and timestamp overflow.
- All interval mappings, 1m versus 1M, unsupported interval before cache/upstream; calendar months, leap-year February, and year change.
- One candle and gaps do not break CloseTime calculation; row order is normalized; duplicates and wrong symbol/category are rejected.
- Open candle refresh and fetch after close; a response to a request started before the boundary does not become final just because it arrives later.
- Pagination keeps the series context, is bounded, and uses budgets; a page error does not return an incomplete range as complete.
- Cache reads keep FetchedAt; a failed refresh keeps previous values; Protobuf follows decimal string, optional presence, and time boundary rules.


---

## 9. Timeframe

### Purpose and representation

Timeframe is a domain value type. It defines the candle time slot size and operations for moving between slots. Kline, repository keys, gap detection, and the fetch planner use it. File: internal/domain/timeframe.go. A separate interval service or repository is not needed.

```go
type Timeframe string
```

Define typed constants for all canonical values in the table below. The allowed values form a closed set. Casting any string to Timeframe does not replace validation. The zero value, an empty string, spaces, and unknown values are invalid. Case matters: 1m is a minute; 1M is a calendar month. Accept strings exactly as listed. Do not trim spaces or accept alternatives such as 60m instead of 1h.

Timeframe is not an alias for time.Duration. A month has no fixed duration. MarketStats.Window stays a separate statistics window duration and does not use this type. A 24h window and a 1d calendar candle have different boundary rules.

### Time semantics

We do not fix the Timeframe functions and methods now. Define them during implementation based on the needs of the code that uses this type.

All intervals except 1M have fixed durations. 1d, 3d, and 1w are 24, 72, and 168 hours in UTC. A monthly candle starts on the first day of the month at 00:00:00 UTC and ends at the start of the next calendar month. Do not replace a month with 30 days.

Boundary calculation follows the selected exchange calendar and interval. Phase 01 live fixtures confirm Monday 00:00 UTC for 1w on all four supported exchange/market pairs. Binance Spot and USDⓈ-M 3d starts follow the congruence anchored at 1970-01-02T00:00:00Z (not Unix epoch modulo 72 hours). This anchor is an inference from consecutive BTCUSDT/ETHUSDT rows across the 2025/2026 year boundary; use it with the saved [raw responses and expected UTC boundaries](evidence/phase-01/README.md). Do not round upstream OpenTime to hide a mismatch. Invalid input or time calculation overflow must not silently produce a corrected result. Adapter tests must replay these fixtures before those intervals ship.

### Exchange interval mapping

The table defines the canonical set and market support. Sources are the official Kline APIs listed in section 8.

| Timeframe / HTTP | Bybit interval | Binance spot interval | Binance USDⓈ-M interval |
| --- | --- | --- | --- |
| `1s` | Not supported | `1s` | Not supported |
| `1m` | `1` | `1m` | `1m` |
| `3m` | `3` | `3m` | `3m` |
| `5m` | `5` | `5m` | `5m` |
| `15m` | `15` | `15m` | `15m` |
| `30m` | `30` | `30m` | `30m` |
| `1h` | `60` | `1h` | `1h` |
| `2h` | `120` | `2h` | `2h` |
| `4h` | `240` | `4h` | `4h` |
| `6h` | `360` | `6h` | `6h` |
| `8h` | Not supported | `8h` | `8h` |
| `12h` | `720` | `12h` | `12h` |
| `1d` | `D` | `1d` | `1d` |
| `3d` | Not supported | `3d` | `3d` |
| `1w` | `W` | `1w` | `1w` |
| `1M` | `M` | `1M` | `1M` |

Domain stores canonical values and calendar arithmetic. It does not import exchange SDKs or contain Bybit parameters such as D/W/M. Adapters own exchange string conversions and support tables. Provider.SupportedTimeframes(market) gives the application a list of supported domain values. The list and conversions must use one adapter mapping table so they stay consistent.

The application checks support for the selected exchange/market before cache lookup and upstream fetch. Support is market-specific: for example, Binance spot accepts 1s, while Binance linear does not. An unsupported value returns `INVALID_ARGUMENT / invalid_interval`. Do not silently replace the interval or build candles from another interval.

### Use in planning and storage

Parse and validate interval at the application entry point. Adapters also reject unsupported values on direct calls. gRPC has no separate copy of the upstream mapping. Keep the model field `Kline.Interval Timeframe` and the Protobuf field `interval`. Renaming the type does not rename these fields. Use the canonical string in the cache key and response.

The planner uses shared Timeframe rules to calculate candle slot boundaries. Do not count monthly slots by dividing time.Duration. Request bounds limit all slot creation and counting. Stop planning before an upstream fetch if there is no progress, a boundary error, or too many slots. The next slot starting does not make cached OHLC final; section 8 defines those rules.

### Verification

Deterministic domain unit tests and table-driven adapter tests:

- All canonical values; empty and zero values, unknown strings, spaces, 1m versus 1M, and rejection of the 60m alias.
- Candle boundaries at day and year changes; months with 28/29/30/31 days; leap-year February.
- Invalid time boundaries and overflow; UTC conversion regardless of the input timezone.
- All exchange mappings and market-specific support; capabilities do not list an interval that the adapter cannot convert.
- Gap detection and monthly slot counting without a fixed duration; bounds stop an oversized range.
- Confirmed alignment fixtures for weekly and multi-day series; do not hide an anchor mismatch by rounding upstream OpenTime.

---

## 10. Architecture

Use Clean Architecture without adding unnecessary structure.

```text
                    gRPC API
                       │
                       ▼
                  Application
                 /           \
                /             \
               ▼               ▼
        Repository         Exchange Provider
        interfaces            interfaces
             │                    │
             ▼                    ▼
         InMemory           Exchange Adapters
                            /             \
                         Binance         Bybit
```

Main dependency rule:

```text
domain
   ↑
application
   ↑
infrastructure
```

Domain/application must not import:

```text
Binance SDK
Bybit SDK
HTTP framework
Redis
PostgreSQL
Sentry
Prometheus
```

---

## 11. Interfaces

Place interfaces next to the application code that depends on them.

For example:

```text
internal/
├── application/
│   ├── instrument/
│   │   ├── service.go
│   │   └── repository.go
│   │
│   ├── ticker/
│   │   ├── service.go
│   │   └── repository.go
│   │
│   ├── marketstats/
│   │   ├── service.go
│   │   └── repository.go
│   │
│   └── kline/
│       ├── service.go
│       ├── repository.go
│       └── provider.go
```

Rule:

> The consumer owns the interface, not the implementation.

For example:

```go
type KlineRepository interface {
    GetRange(
        ctx context.Context,
        query KlineQuery,
    ) ([]domain.Kline, error)

    UpsertMany(
        ctx context.Context,
        klines []domain.Kline,
    ) error

    DeleteBefore(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
        interval domain.Timeframe,
        before time.Time,
    ) error
}
```

Infrastructure implementation:

```go
type MemoryKlineRepository struct {
    // ...
}
```

It simply implements this interface.

---

## 12. Exchange facade

The application layer works only with consumer-owned interfaces. Instrument providers are bound to one exchange/market pair at construction:

```go
type InstrumentProvider interface {
    Scope() application.Scope
    GetInstruments(ctx context.Context) ([]domain.Instrument, error)
}
```

Binance spot, Binance linear, Bybit spot, and Bybit linear use separate implementations. Each owns its endpoint selection and market rules. Bootstrap chooses the implementation for each enabled scope. The application refresher obtains the scope from the provider; it does not pass a second market selector. Common parsing and transport admission remain shared. Binance's two SDK adapters implement a common raw client interface; Bybit's market providers share its unified SDK client.

The following broader facade is illustrative for the remaining features, not a requirement to combine their concrete market implementations:

```go
type MarketDataProvider interface {
    Exchange() domain.Exchange

    GetTickers(
        ctx context.Context,
        market domain.Market,
    ) (TickerCollection, error)

    GetMarketStats(
        ctx context.Context,
        market domain.Market,
        window time.Duration,
    ) ([]domain.MarketStats, error)

    GetKlines(
        ctx context.Context,
        request KlineRequest,
    ) ([]domain.Kline, error)

    Capabilities() ExchangeCapabilities

    SupportedTimeframes(market domain.Market) ([]domain.Timeframe, error)
}
```

SupportedTimeframes is a local read-only lookup without upstream requests. An unknown/unsupported market returns an error. The method returns a safe copy of the canonical interval list from section 9.

The application consumer owns the collection result and capabilities:

```go
type TickerCollection struct {
    Tickers     []domain.Ticker
    TickerError error

    HasMarketStats   bool
    MarketStats      []domain.MarketStats
    MarketStatsError error
}

type ExchangeCapabilities struct {
    MarketStatsWithTicker bool
    MarketStatsWindows   []time.Duration
}
```

`GetTickers` returns an outer error for a transport/envelope failure; do not publish the result. For a successful shared response, return normalization errors for the two full snapshots independently in TickerError and MarketStatsError. A nil error with an empty slice means a successfully fetched empty snapshot, not a failed fetch.

Bybit: MarketStatsWithTicker=true, HasMarketStats=true, and Window=24h in all stats records. The application saves each successful branch separately. Binance: both flags=false; GetTickers does not fetch statistics. The worker checks the capability, not the exchange name.

The worker calls separate GetMarketStats only when MarketStatsWithTicker=false. A direct call to this method on Bybit returns unsupported_operation without network access. Ticker and MarketStats RPC handlers read only repositories and do not call provider methods. In v1, MarketStatsWindows contains only 24h; validate window before any fetch. These capabilities are the same for each provider's supported markets in v1.

The application layer does not know:

```text
/v5/market/kline
/fapi/v1/klines
```

It also does not know Binance/Bybit DTO types.

---

## 13. Exchange implementations

Infrastructure:

```text
internal/infrastructure/exchange/
├── binance/
│   ├── provider.go
│   ├── instruments.go
│   ├── tickers.go
│   ├── market_stats.go
│   └── klines.go
│
└── bybit/
    ├── provider.go
    ├── instruments.go
    ├── tickers.go
    ├── market_stats.go
    └── klines.go
```

These packages are facades/adapters around the exchange SDKs.

For example:

```text
Bybit SDK response
       ↓
Bybit adapter
       ↓
domain.Ticker + domain.MarketStats
```

---

## 14. SDK selection

Checked on September 11, 2026: official documentation, SDK source at fixed commits, and local Go tests with httptest.Server. There is no need to research the full endpoint list again before implementation. Domain mapping tables stay in sections 5–9. This section records the client choice, SDK limits, and HTTP request costs.

### Selected SDKs and inspected revisions

| Market | Choice | Checked revision |
| --- | --- | --- |
| Binance spot | github.com/binance/binance-connector-go/clients/spot | Declared client version 1.14.1, commit 9803fd1c7ed74179cba56becec3bfa3b83e7549e. |
| Binance USDⓈ-M | github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures | Declared client version 1.20.2, same commit. |
| Binance shared transport | github.com/binance/binance-connector-go/common/v2 | Common source from the same commit; client go.mod files require v2.8.1. |
| Bybit spot / linear | github.com/bybit-exchange/bybit.go.api | Commit a58e14c6fd93d5484875d22c1141564b40c85f71. |

Sources: [Binance connectors](https://github.com/binance/binance-connector-go/tree/9803fd1c7ed74179cba56becec3bfa3b83e7549e), [Bybit SDK](https://github.com/bybit-exchange/bybit.go.api/tree/a58e14c6fd93d5484875d22c1141564b40c85f71). During implementation, pin dependencies to the checked revisions or matching module versions. An unchecked latest is not the same result. Repeat affected contract tests when updating an SDK.

Result: both official SDKs are suitable for the listed public REST endpoints, with the adapter requirements below. Using an SDK does not remove the need for normalization, validation, or service load control. A custom HTTP path is allowed for an endpoint if the checked SDK version loses required data. There is no need to fork the SDK or replace all endpoints in advance.

### What is already specified

| Question | Where the result is recorded |
| --- | --- |
| Instruments: fields, filters, statuses, funding interval, delisting | Section 5; open Binance issues are still clearly marked there. |
| Current prices, bid/ask, funding, ticker assembly | Section 6. |
| Statistics and reuse of the Bybit response | Section 7. |
| Kline positions, numeric types, time boundaries, missing Bybit trade count | Section 8. |
| Canonical timeframes and exchange values | Section 9. |

### SDK verification results

| Check | Binance | Bybit |
| --- | --- | --- |
| Required endpoints | Available for Spot: exchangeInfo, price, bookTicker, 24hr, klines. Available for USDⓈ-M: exchangeInfo, fundingInfo, price v2, bookTicker, premiumIndex, 24hr, klines. | GetInstrumentInfo, GetMarketTickers, and GetMarketKline are available; the ticker response is also used for MarketStats. |
| Context | Methods accept context.Context and pass it to the HTTP request. Cancellation before the call was tested locally. | Same; cancellation before the call was tested locally. |
| Test base URL and transport | ConfigurationRestAPI.BasePath and HTTPSAgent with http.RoundTripper. | WithBaseURL and Client.HTTPClient. |
| Bulk ticker/stats requests | Calls without symbol/symbols were checked: the SDK sends one request and parses an array. No extra batching is needed because of the SDK. | Symbol is optional; one shared ticker request serves both models. |
| Decimal values | Required typed fields are string/pointer to string. The kline tuple uses a string/int64 union. Exact strings and large int64 values survived decode/encode tests; arrays use SDK Items containers. | Documented decimal strings keep their precision. Generic result has an important JSON number limit; see below. |
| Pagination | The SDK makes one request per Execute. The application plans kline ranges/pages; exchangeInfo needs no cursor pagination. | Cursor is passed through params; nextPageCursor is returned in result. There is no automatic page loop. |
| Errors | An HTTP error becomes a Go error, but public Execute returns a nil response and loses access to error headers. | HTTP >=400 becomes a Go error. HTTP 200 with retCode !=0 returns ServerResponse without a Go error. |
| Retries | Default Retries=3; our service must use Retries=0. | The checked REST callAPI has no retry loop of its own. |
| Outgoing REST request limits | No built-in limiter: ParseRateLimitHeaders reads metadata after the response; SendRequest does not wait for a budget. | No built-in limiter: callAPI calls the HTTP client immediately. SetApiRateLimit/GetApiRateLimit call remote APIs and do not limit local requests. Section 32 gives details and service requirements. |

Source code: [Binance Spot request methods](https://github.com/binance/binance-connector-go/blob/9803fd1c7ed74179cba56becec3bfa3b83e7549e/clients/spot/src/restapi/api_market.go), [Binance Futures request methods](https://github.com/binance/binance-connector-go/blob/9803fd1c7ed74179cba56becec3bfa3b83e7549e/clients/derivativestradingusdsfutures/src/restapi/api_market_data.go), [Binance configuration](https://github.com/binance/binance-connector-go/blob/9803fd1c7ed74179cba56becec3bfa3b83e7549e/common/common/configuration.go), [Binance transport](https://github.com/binance/binance-connector-go/blob/9803fd1c7ed74179cba56becec3bfa3b83e7549e/common/common/utils.go), [Bybit market methods](https://github.com/bybit-exchange/bybit.go.api/blob/a58e14c6fd93d5484875d22c1141564b40c85f71/market_service.go), [Bybit client and decoder](https://github.com/bybit-exchange/bybit.go.api/blob/a58e14c6fd93d5484875d22c1141564b40c85f71/bybit_api_client.go).

### Mandatory adapter handling

1. Disable internal Binance retries. The service controls retries and pages. Each one passes the common and operation budgets again. Use the same service mechanism for Bybit.
2. Configure both clients with a transport that counts actual requests and reads HTTP status and rate-limit headers before SDK processing, including error responses. A successful Binance RestApiResponse has headers, but public Execute drops them on errors. Bybit ServerResponse has no headers at all. Keep the information in the context of the specific operation. Do not use a shared mutable lastResponse field for concurrent requests. Passing a custom RoundTripper to Binance and keeping Retry-After was tested locally.
3. The Bybit adapter checks both transport error and retCode. Treat retCode=10006 as a rate limit even with HTTP 200. Do not publish result from an error envelope as a successful snapshot.
4. Bybit GetServerResponse uses json.Unmarshal into Result interface{}, so JSON numbers become float64. The test integer 9007199254740993 became 9007199254740992. This shows a decoder limit; it does not mean that the exchange sends prices in this format. Documented string prices stay exact. Do not convert float64 back to decimal or marshal/unmarshal result to recover the original number. For required integer metadata, check the type, that it is an integer, the safe range, and the field's allowed range. If a market value is a JSON number, use the original body and an exact decoder, or a custom HTTP path for that endpoint as described in section 3.
5. SDK getters with zero defaults do not prove that a field exists. For Binance, check pointers/presence. For Bybit, check field presence and type in result. Schema errors must not become default prices, timestamps, or counts.
6. Normalize the shared Bybit response into two models as described in section 7. The SDK does not handle independent snapshot publication. No SDK type leaves infrastructure adapters. Generated array containers serialize as an object with items, so marshaling an SDK response does not replace mapping the normalized model to Protobuf.

### Request costs and rate-limit scopes

These are documented costs for selected requests, not recommended service budgets. Bulk ticker/stats requests omit symbol/symbols. Each retry consumes the cost again.

| API | Request | Cost |
| --- | --- | --- |
| Binance spot | exchangeInfo | REQUEST_WEIGHT 20 |
| Binance spot | ticker/price; ticker/bookTicker | 4 each; full ticker cycle: 8 |
| Binance spot | ticker/24hr, FULL | 80 |
| Binance spot | klines | 2; limit up to 1000 |
| Binance USDⓈ-M | exchangeInfo | REQUEST_WEIGHT 1 |
| Binance USDⓈ-M | ticker/price v2; ticker/bookTicker; premiumIndex | 2 + 5 + 10; full ticker cycle: 17 |
| Binance USDⓈ-M | ticker/24hr | 40 |
| Binance USDⓈ-M | klines | limit 1–99: 1; 100–499: 2; 500–1000: 5; 1001–1500: 10 |
| Binance USDⓈ-M | fundingInfo | Weight 0, but a separate limit shared with fundingRate: 500 requests / 5 minutes / IP |
| Bybit spot / linear | instruments-info, tickers, kline | Every HTTP request counts toward the common HTTP IP limit; shared ticker/stats counts once |

Cost sources: [Binance Spot general](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/general#exchange-information), [Binance Spot market](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/market), [Binance USDⓈ-M market](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data).

Binance: use the IP scope and applicable windows from rateLimits in the relevant exchangeInfo. Spot and USDⓈ-M limit catalogs do not replace each other. The common service limits in section 32 may be stricter. Use X-MBX-USED-WEIGHT-* for monitoring and stricter limits, not to reset local usage. For USDⓈ-M ticker/price v2 and ticker/bookTicker, the documentation marks X-MBX-USED-WEIGHT-1M as inaccurate. Ignore this header from those endpoints. For HTTP 429/418, follow cooldown and Retry-After when provided. Sources: [Spot limits](https://developers.binance.com/en/docs/products/spot/rest-api#limits), [USDⓈ-M limits](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/general-info#limits).

Bybit: the documented common default limit is 600 HTTP requests / 5 seconds / IP. For 403 access too frequent, the documentation requires stopping HTTP sessions and waiting at least 10 minutes. This is not a normal retry after 2s. Handle X-Bapi-Limit, X-Bapi-Limit-Status, and X-Bapi-Limit-Reset-Timestamp when present, within their scope. Do not apply UID limits from trading endpoint tables to public market-data requests. Missing headers do not mean there is no IP limit. Source: [Bybit rate limits](https://bybit-exchange.github.io/docs/v5/rate-limit).

### Evidence and implementation gates

Local SDK tests passed on Go 1.26.0 darwin/arm64 with source at the listed commits and a fake HTTP server. They made no production exchange requests. Checks covered:

- ten Binance response model cases: price/book/stats arrays, funding, spot instrument filters, and spot/futures klines; all values survived decode/encode with SDK Items containers handled, including long decimal strings and integers above 2^53;
- ten Binance HTTP paths, no symbol filters, and one request per call;
- HTTP 429/500 without internal retries, the public SDK losing the error response, and custom transport keeping Retry-After;
- a context canceled before the call for Binance and Bybit;
- Bybit cursor request without automatic pagination, exact decimal strings, the generic numeric decoder limit, and retCode=10006 with HTTP 200.

The SDK check does not test adapters that have not been written yet. It also does not confirm live API access from the future deployment. The project Go version stays as listed in section 2. Builds and integration tests on that version are part of implementation and CI. The checked go.mod files require Go 1.25.0 for Binance modules and Go 1.21 for Bybit. No incompatibility with local Go 1.26.0 was found.

Phase 01 dispositions are now recorded in the [decision register](specification-decisions-v1.md) and [implementation contract](implementation-contract-v1.md):

- The original phase 01 profile used captured exchangeInfo limits and a 20% margin. The approved September 13 Binance rework replaces that policy with a configurable stop line; section 32 and the implementation contract define current behavior. Shares, pacing, queues, attempts, deadlines, and in-memory restart behavior remain.
- One instance is confirmed; the deployment must verify no other clients and no outstanding ban on its egress IP.
- Binance funding uses explicit intervals only, with null for an absent symbol in a successful response. Request failure preserves the previous snapshot. Binance delisting stays null.
- Twelve captured calendar cases cover 3d/1w alignment, both symbols, and year boundaries, with raw responses and expected normalized rows.
- Binance Spot FULL 24hr was verified with a successful live array response without symbol filters. Its [capture context](evidence/phase-01/binance-spot-full-statistics.json) is evidence for this environment, not future deployment access.
- The official release catalog confirms Go 1.27.1 availability. Phase 02 must install/use it; local Go remains 1.26.0. Earlier SDK checks still need recreation as project adapter tests in phases 06–09.

---

## 15. Binance

Primary choice:

```text
github.com/binance/binance-connector-go/clients/spot
github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures
```

Section 14 records the choice and checked revisions. Disable internal retries for both connectors. Our transport reads limit headers before SDK processing.

The exchange adapter must fully hide generated SDK structs.

No Binance type may leave:

```text
internal/infrastructure/exchange/binance
```

---

## 16. Bybit

Primary choice:

```text
github.com/bybit-exchange/bybit.go.api
```

Section 14 records the choice and checked revision. The adapter checks retCode, gets HTTP headers through transport, and follows the generic numeric decoder limits. Convert Bybit response structs to our domain structs.

No Bybit type may leave:

```text
internal/infrastructure/exchange/bybit
```

---

## 17. Storage

v1 uses:

```text
in-memory storage
```

The application depends only on repository interfaces.

In the future, we must be able to use:

```text
PostgreSQL
Redis
```

without changing use cases.

---

## 18. In-memory storage

Storage must be thread-safe.

Use suitable synchronization primitives:

```go
sync.RWMutex
```

or other suitable structures.

Main keys must include at least:

```text
exchange
market
symbol
```

For klines:

```text
exchange
market
symbol
interval
open_time
```

---

## 19. Instruments refresh

A background worker refreshes instruments. Set the interval in the service config separately for each exchange: `exchanges.<exchange>.instruments.refresh_interval`.

```yaml
exchanges:
  bybit:
    instruments:
      refresh_interval: 10m
  binance:
    instruments:
      refresh_interval: 10m
```

`10m` is the default when the setting is missing. The worker uses the loaded config value. Do not hardcode the interval in the worker. The value must be a positive duration.

Flow:

```text
startup
   ↓
immediate fetch
   ↓
store snapshot
   ↓
wait configured instruments.refresh_interval
   ↓
fetch again
```

An interval-based worker is suitable for instruments.

Each exchange refreshes independently.

A Binance error must not block Bybit.

---

## 20. Instrument storage semantics

Exchange endpoints usually return the current instrument snapshot, so the repository must support atomic snapshot replacement.

For example:

```go
type InstrumentRepository interface {
    ReplaceSnapshot(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
        instruments []domain.Instrument,
    ) error

    List(
        ctx context.Context,
        filter InstrumentFilter,
    ) ([]domain.Instrument, error)

    HasSnapshot(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
    ) (bool, error)
}
```

This lets us remove delisted instruments from the current snapshot.

---

## 21. Ticker collector

Tickers update continuously within their assigned budget.

A separate fixed `ticker refresh interval` is not needed.

Flow:

```text
wait for request admission
   ↓
fetch
   ↓
store complete snapshot
   ↓
start next cycle
```

Allow at most one unfinished refresh cycle for each `exchange + market` pair. Cycles run one after another. Do not build up refresh jobs.

Every HTTP request in the cycle, including pages and retries, passes the limits in section 32.

The actual rate depends on network latency, common and operation budgets, allowed concurrency, and error backoff.

Context cancellation stops admission and backoff waits. No busy loop is allowed.

A failed ticker refresh keeps the last successful Ticker snapshot. A new cycle must not bypass backoff or the wait time required by the exchange.

Section 6 defines Ticker sources and assembly. The collector publishes data to TickerRepository. The Tickers RPC reads the repository and does not start a refresh.

For Bybit, also pass the received HTTP response for independent MarketStats normalization as described in section 22. This reuses the source; it does not merge models, repositories, or RPC methods. The Binance ticker collector does not fetch MarketStats.

---

## 22. Market statistics collector

Refresh MarketStats using section 7 rules and publish it to MarketStatsRepository. A statistics normalization or publication error keeps the last successful stats snapshot and its FetchedAt. It does not cancel a successful Ticker update. The Market statistics RPC reads only the repository and does not start a refresh.

### Binance

An independent background worker fetches immediately at startup, then waits for `exchanges.binance.market_stats.refresh_interval` after each cycle. The default is `30s`; take the value from the service config.

Allow at most one unfinished cycle per exchange/market/window. The worker has its own context/deadline and bounded admission. All HTTP requests, batches, and retries use the market_stats budget and common exchange limits from section 32. Context cancellation stops interval, admission, and backoff waits. Do not build up refresh jobs.

The statistics refresh rate does not depend on the ticker rate. An error or stop in one worker does not stop the other. A common exchange cooldown applies to both within its scope.

### Bybit

Do not create a separate polling worker: `/v5/market/tickers` already contains the required statistics. After the shared fetch, normalize ticker and MarketStats independently and publish them to their own repositories. Section 7 defines error isolation rules.

Count the shared HTTP request once in the tickers budget. There is no separate stats refresh interval. Statistics freshness depends on the shared fetch. If it stops, both updates stop. Reading the MarketStats API does not make an extra Bybit request.

---

## 23. Ticker storage

The gRPC API never calls an exchange to fetch a ticker.

Flow:

```text
Exchange
   ↓
continuous ticker collector
   ↓
TickerRepository
   ↓
gRPC API
```

Use snapshot semantics for current tickers:

```go
type TickerRepository interface {
    ReplaceSnapshot(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
        tickers []domain.Ticker,
    ) error

    Get(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
        symbol string,
    ) (domain.Ticker, bool, error)

    List(
        ctx context.Context,
        filter TickerFilter,
    ) ([]domain.Ticker, error)

    HasSnapshot(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
    ) (bool, error)
}
```

v1 does not store ticker history.

Store only the latest state.

---

## 24. Market statistics storage

Store MarketStats in MarketStatsRepository next to application/marketstats. The Market statistics RPC reads only this repository and does not start an upstream fetch.

Repository contract:

```go
type MarketStatsRepository interface {
    ReplaceSnapshot(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
        window time.Duration,
        stats []domain.MarketStats,
    ) error

    Get(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
        symbol string,
        window time.Duration,
    ) (domain.MarketStats, bool, error)

    List(
        ctx context.Context,
        filter MarketStatsFilter,
    ) ([]domain.MarketStats, error)

    HasSnapshot(
        ctx context.Context,
        exchange domain.Exchange,
        market domain.Market,
        window time.Duration,
    ) (bool, error)
}
```

MarketStatsFilter contains Window and optional Exchange, Market, and Symbol. Record key: exchange/market/symbol/window. ReplaceSnapshot replaces only the exchange/market/window scope and checks that all records match it. HasSnapshot distinguishes an uninitialized scope from a successfully loaded empty snapshot. Reject duplicate keys. Reads return safe copies.

Do not store MarketStats history. A successful replacement removes absent symbols only from that stats scope. An error keeps the previous snapshot and FetchedAt. TickerRepository and MarketStatsRepository operations are independent and need no shared transaction. One repository being ready does not mean the other is ready.

---

## 25. Klines

Klines use a cache-aside strategy.

Flow:

```text
GET /klines
      ↓
read storage
      ↓
determine missing data
      ↓
if nothing missing:
    return cache
      ↓
otherwise:
    create minimal upstream fetch plan
      ↓
fetch exchange
      ↓
store
      ↓
read complete result
      ↓
return
```

---

## 26. Closed vs open klines

A closed kline is reusable as immutable only after the post-close fetch required by section 8.

For example:

```text
10:00–10:05
```

At `10:05`, the time slot has ended. Cached values fetched before close still need a successful request started after close. Local time passing, or a pre-close request arriving after close, does not finalize those values.

The current candle is mutable.

For example, at `10:03`:

```text
10:00–10:05
```

it is still forming.

So a cached current candle cannot be treated as permanently valid.

Kline cache logic must distinguish:

```text
closed candle
current/open candle
```

Closed candles confirmed by that post-close fetch can be reused from cache without another exchange request. v1 does not track later exchange corrections.

Refresh the current/open candle when needed.

---

## 27. Kline gap detection

Find the missing candle slots in the requested range.

The main optimization goal is:

> Minimize upstream exchange requests, not necessarily the number of candles fetched again.

Example requested range:

```text
10:00 ───────────────────── 15:00
```

Storage:

```text
10:00──10:30

gap A

11:00────────13:00

gap B

13:30────────15:00
```

If the exchange can return the whole range:

```text
10:35 → 13:25
```

in one request within its limit, make:

```text
1 request
```

instead of:

```text
request gap A
request gap B
```

---

## 28. Kline fetch planner

The Kline fetch planner finds which candle ranges to fetch from the exchange to fill cache gaps and refresh candles that are not final. It must cover all required slots with the fewest upstream requests, within the candle limit per request.

The application service uses the planner after reading the cache. The planner builds a plan. The application service runs requests through the exchange provider, saves data, and checks completeness again. The planner itself does not access the network or repository and does not run retries.

### Input and result

Inputs:

- the requested range for one series: exchange/market/symbol/timeframe;
- stored candles and information about which ones need a refresh under section 8 rules;
- maximum candles per upstream request from the final config for the selected exchange/market, as described in section 30;
- the current time, passed by the caller for deterministic calculation.

The result is `[]KlineFetchRange`: a list of ranges to fetch. Each range fits within one request limit. If all required candles are already stored and need no refresh, the plan is empty and no exchange requests are needed.

Implement a separate component:

```go
type KlineFetchPlanner struct {
    // ...
}
```

### Example

The client requests five-minute candles for `[10:00, 15:00)`. Only candles with OpenTime `10:30`, `10:35`, `11:00`, and `11:05` are missing from cache. All others are final and need no refresh.

With a limit of at least 8 candles, one fetch range `[10:30, 11:10)` is enough. It contains eight five-minute slots and covers both gaps. The four stored candles between the gaps are fetched again. This is allowed: the goal is to reduce request count, not to avoid all repeated data loading.

If the range does not fit the limit, the planner splits it into several valid requests. A partial plan that leaves required slots without a fetch is not allowed.

### Planning rules

Use Timeframe from section 9 to move between slots and handle calendar months. Do not copy calendar arithmetic into the planner. The example uses half-open boundaries. The adapter converts them to parameters of the selected upstream API.

The planner handles both missing candles and stored intermediate data that needs a refresh. A candle fetched before close does not become final just because the next slot starts.

Plan building follows request size and slot count bounds. Section 29 describes range merging. Rate limits and retries apply when running the plan, not inside the planner.

---

## 29. Fetch range merge algorithm

Algorithm:

1. Find all missing candle slots.
2. Add the current candle as refresh-required if it is in the requested range.
3. Take the earliest missing candle.
4. Extend the fetch range as far right as possible while the candle slot count stays within the exchange limit.
5. The extended range may include cached candles.
6. One exchange request fetches the whole range.
7. Repeat from the first missing candle outside that range.

Example:

```text
missing:
10:10
10:15
13:00
13:05
```

If:

```text
10:10 → 13:05
```

fits within the limit:

```text
1 upstream request
```

If it does not fit:

```text
2+ requests
```

---

## 30. Exchange kline limits

Set the maximum candles per upstream request in the service config separately for each exchange/market:

```text
exchanges.<exchange>.klines.max_candles_per_request.<market>
```

The service default is the documented endpoint maximum. This is not the exchange's default when the limit parameter is missing. The adapter always sends limit explicitly.

| Exchange / market | Service default max_candles_per_request | Allowed range | Exchange default limit when the parameter is missing |
| --- | --- | --- | --- |
| Bybit spot | 1000 | 1–1000 | 200 |
| Bybit linear | 1000 | 1–1000 | 200 |
| Binance spot | 1000 | 1–1000 | 500 |
| Binance linear (USDⓈ-M) | 1500 | 1–1500 | 500 |

Values checked on September 11, 2026: [Bybit Kline](https://bybit-exchange.github.io/docs/v5/market/kline), [Binance Spot Kline](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/market#klinecandlestick-data), [Binance USDⓈ-M Kline](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data#klinecandlestick-data).

```yaml
exchanges:
  bybit:
    klines:
      max_candles_per_request:
        spot: 1000
        linear: 1000
  binance:
    klines:
      max_candles_per_request:
        spot: 1000
        linear: 1500
```

A missing market value uses the table default. An explicit value must be an integer in the allowed range. Zero, negative or fractional values, or values above the exchange maximum are config errors at startup. Do not silently cap the value. This setting does not enable a market. Enabled markets are still defined by exchanges.<exchange>.markets.

After loading YAML and ENV overrides, the application passes the final exchange/market limit to the planner. Do not get it from ExchangeCapabilities or hardcode it in the planner or application service. Keep defaults and upper bounds together in config and validation. Do not repeat these numbers in the SDK adapter.

Each fetch range contains at most the configured slot count. The adapter sends limit explicitly based on the planned request size, within this bound. Request weight uses the actual sent limit as described in section 14, not always the config maximum. This setting does not replace the total client request size limit or exchange budgets.

When an exchange maximum changes, update defaults, validation, and tests based on the new documentation. Test each pair's default, a lower override, boundary values, config errors, the planner using the final limit, and the adapter sending limit and counting weight.

---

## 31. Coordinating concurrent candle requests

### Problem this solves

Several clients may request identical or overlapping candle ranges at the same time. If each request checks an empty cache and starts its own fetch, the service downloads the same data several times and spends the exchange budget again.

The service must coordinate cache fills. At most one fill can run for a candle series at a time. Other requests that need data from this series wait for the active fill, then check their own range in the cache again.

This works with the planner from section 28. The planner decides which ranges to fetch and how to merge gaps. Coordination decides who performs the fetch now and prevents several callers from running duplicate plans at the same time.

A cache fill includes reading the repository again, building the plan, running the required upstream requests, and saving the result. One fill may include several planned HTTP requests and retries. One operation does not mean one HTTP request for every range size.

### Identical ranges

Assume an empty cache. Clients A and B request Binance linear BTCUSDT, timeframe 5m, range `[10:00, 12:00)` at the same time. In this example, all candles are closed and the range fits in one exchange request.

1. Both requests find that data is missing.
2. A starts the fill for this series first.
3. B joins the wait for the active fill and does not call the exchange provider.
4. A fetches the candles and saves them to the repository.
5. After the fill ends, both callers read their range from the repository and get a response.

Result: one exchange request instead of two. The same rule applies to 50 clients requesting this range at once. More clients do not create more identical fetches.

A request that can use stored final candles returns them immediately. It does not have to wait for another fill just because it belongs to the same series.

### Partly overlapping ranges

For the same series, A requests `[10:00, 12:00)` and B requests `[11:00, 13:00)`. The cache starts empty. All candles in this example are final.

1. A starts fetching its range. B waits for this operation to finish.
2. After A saves the data, `[11:00, 12:00)` is also available to B.
3. B checks its full request `[11:00, 13:00)` again and finds that only `[12:00, 13:00)` is missing.
4. B passes through coordination again. If nobody else is filling this series, B starts a new fill for the remaining range.
5. B returns the full `[11:00, 13:00)` result from the repository.

A's fill ending does not mean B's request is complete. Do not return A's downloaded array to B: their boundaries differ. Each caller builds a response for its own range.

In v1, do not extend an active fill when new callers arrive. Later fills handle their remaining gaps. The planner may include stored slots between gaps if this reduces request count under sections 28–29.

### Coordination scope

The series defines the key:

```text
exchange + market + symbol + timeframe
```

For example:

```text
bybit:linear:BTCUSDT:5m
```

Do not include from/to in the key. Otherwise, overlapping ranges get different keys and load in parallel despite sharing candles.

Coordinate different symbols, timeframes, markets, and exchanges independently. BTCUSDT does not block ETHUSDT; Bybit does not block Binance. Common load limits still apply.

In v1, coordination covers the whole series. Even non-overlapping cache misses for one series wait for each other. This is a chosen simplification; separate locks for overlapping ranges are not needed. The mechanism works within one process. v1 does not coordinate several service instances.

### Application service steps

1. Validate client request parameters and bounds, then read the repository. If data is complete and ready to return, return it without a fill.
2. If data must be fetched, join the current fill by series key or start a new one.
3. Inside a new fill, read the repository again before building a plan. Another fill may have saved data between the first read and this operation. Do not use an old plan without checking again.
4. Build a current plan and run it through the provider with limits from sections 30 and 32–33. If the plan is already empty, finish the fill without upstream requests.
5. Save successfully fetched data before notifying waiting callers that the fill is complete.
6. Each caller reads the repository again and checks its own range for completeness. If gaps remain, repeat the coordination steps within its deadline and total request processing budget.

When refreshing an open candle, track updates already completed for the current RPC, including a shared fill. After a successful refresh, do not immediately request another only because the candle is still open. This would create an infinite loop. If the close boundary passes during the wait and a final result is needed, apply section 8 rules. The next independent RPC checks refresh needs again.

### Errors, cancellation, and wait limits

A fill error must not cause every waiting caller to start an immediate separate fetch. After an error, the caller checks the cache. If its own full range is ready to return, it may return the data. Otherwise, it ends with an error. Successfully saved pages stay in cache, but an incomplete range must not be returned as complete. Retries inside a fill are bounded and follow the shared retry policy.

If a successful exchange response adds no required data, do not repeat the same fill forever. No progress with an unchanged plan ends the request with an incomplete upstream data error. Do not create missing candles.

Each caller can stop waiting through its own context/deadline. Canceling one RPC, including the caller that started the fill, must not cancel a shared fetch that others still await. The fill uses the service lifecycle context with its own bounded deadline, not the first client's context. If all callers leave, an already started bounded fill may finish and save its result. Do not start new fills without callers. Service shutdown cancels active fills and admission waits.

Limit waiting callers, active fills, and total wait time before creating unbounded goroutines. Waiting for a fill does not occupy an active upstream HTTP request slot. On overload, use service_overloaded from section 33. These limits are needed in addition to duplicate prevention.

### Role of singleflight

Use `golang.org/x/sync/singleflight` with the series key to combine concurrent calls. The library combines calls with one key and shares their completion result with waiting callers. It is not a cache and does not inspect candle ranges. Source: [Go singleflight documentation](https://pkg.go.dev/golang.org/x/sync/singleflight).

The application service still handles repository rechecks, range completeness, deadlines, canceled waits, budgets, and errors. Apply singleflight to the fill, not to building one identical RPC response for all callers. A completed fill must not leave a permanent entry in a separate coordination registry.

### Behavior checks

Tests control event order and do not depend on random delays:

- 50 identical concurrent requests to an empty cache get a full result after one fill; upstream request count matches one plan.
- Partly overlapping requests reuse the loaded overlap and fetch only the remainder.
- The second fill reads the cache again and does not run an outdated plan.
- A full cache hit does not wait for an active fetch of another part of the same series.
- Different series load independently; at most one fill is active for one series.
- A shared fetch error does not cause a burst of retries from waiting callers; a partial result is not returned as complete.
- Canceling one caller does not cancel the fill for others; deadlines and shutdown stop the relevant waits and work.
- An open candle does not cause endless refreshes within one RPC; no progress or exceeded bounds end processing.

---

## 32. Exchange rate limits and operation budgets

### Purpose

This mechanism has two jobs: apply the local exchange-admission rules and prevent the continuous ticker collector from spending the budget assigned to instruments, klines, and independent market_stats loading. The Binance rules below replace the earlier hard common-budget policy as agreed on September 13, 2026; Bybit is unchanged.

The exchange limits actual HTTP requests, not calls to our API or provider methods. One fetch plan may create several requests. One Binance ticker cycle also has several requests. Each request needs separate admission. Reading ready data from the repository does not use the exchange budget.

Rate limits control usage over time. Section 33 separately limits concurrent requests, queue sizes, and deadlines. Following one limit does not replace the other.

### What the SDKs already provide

The source revisions from section 14 were checked. Neither selected SDK has a built-in admission mechanism for outgoing REST requests based on common and operation budgets.

| SDK | What it provides | What it does not do |
| --- | --- | --- |
| Binance | ParseRateLimitHeaders parses used weight and other headers after a response arrives. SendRequest converts HTTP 429/418 to errors and has separate retry logic. | It does not calculate available budget before sending, queue requests, or split limits between workers. RateLimits in a response does not mean automatic throttling. |
| Bybit | callAPI makes an HTTP request. SetApiRateLimit and GetApiRateLimit call signed /v5/apilimit/set and /v5/apilimit/query. | These methods change or read exchange-side settings. They do not create a local limiter. callAPI does not wait for a budget or coordinate workers. |

Sources: [Binance REST transport and header parsing](https://github.com/binance/binance-connector-go/blob/9803fd1c7ed74179cba56becec3bfa3b83e7549e/common/common/utils.go), [Bybit HTTP client](https://github.com/bybit-exchange/bybit.go.api/blob/a58e14c6fd93d5484875d22c1141564b40c85f71/bybit_api_client.go), [Bybit API limit methods](https://github.com/bybit-exchange/bybit.go.api/blob/a58e14c6fd93d5484875d22c1141564b40c85f71/api_limit.go).

Implement the limiter in the service infrastructure. All exchange adapters use it, including custom HTTP paths used when the SDK has limits. Disable internal Binance retries. The transport reads HTTP status and headers before the SDK can lose them while handling an error, as described in section 14.

### Common and operation budgets

A limit has a scope, a unit, a time window, and an allowed usage amount. For example, requests per IP over 5 seconds and request weight per minute are different limits. Do not add them together or replace them with one requests-per-second number.

For each applicable scope, define a common service budget and assigned budgets for `tickers`, `instruments`, `klines`, and `market_stats`. The common budget may be stricter than the exchange limit. The sum of assigned budgets must not exceed the common budget in the same unit and window. In v1, unused budget from one operation type is not passed to another.

Confirmed by the user on September 11, 2026: fixed default shares of each common service budget, with no borrowing:

| Operation | Default share |
| --- | --- |
| tickers | 60% |
| klines | 30% |
| instruments | 5% |
| market_stats | 5% |

The shares sum to 100%. They divide the Binance stop line or the Bybit allowance after its safety margin, not necessarily the exchange's full limit. Binance shares are weight units where the common limit uses weight, not percentages of HTTP request counts. Bybit's common request-count budget uses request counts. Apply shares independently to the relevant Binance Spot, Binance USDⓈ-M, and shared Bybit scopes. Never sum different units or combine the two Binance catalogs.

Use `upstream.operation_share_percent` for configurable integer percentages, with the table as built-in defaults. Merge missing entries from defaults. For each common allocation window with integer service allowance B, the operation allowance is `floor(B * percent / 100)` using checked integer arithmetic. Fractional remainders stay unused; rounding must never increase the total budget. Shares are caps, not a promise of refresh latency or a reservation of HTTP concurrency slots. Extra smoothing, concurrency, and endpoint-family limits remain separate requirements.

For a provider with `MarketStatsWithTicker=true`, combine the configured tickers and market_stats percentages at composition, before deriving integer allowances. Thus Bybit uses a fixed 65% share for the shared ticker/statistics path, 30% for klines, and 5% for instruments. Charge the shared HTTP request once to tickers; do not create an independent market_stats worker or admission budget. This is a fixed allocation for a joint operation, not runtime borrowing or two charges for one request. Binance retains separate 60% ticker and 5% statistics shares. An operation disabled without a shared collection path leaves its share unused. This provider-specific combination is the engineering recommendation requested by the user after specifying the base percentages.

Funding metadata requests during instrument refresh belong to `instruments`. Extra pages and retries keep the original operation type. Endpoint-family limits, such as Binance fundingInfo's separate request-count cap, are additional checks; they are not a second common pool to split between unrelated operations. A fundingInfo request must still fit its instruments share in any applicable common request-count allocation.

Spending all of the tickers share stops further ticker requests in that allocation window even if the klines allowance is idle. A common exchange cooldown still stops all affected operations. No-borrowing is confirmed v1 behavior, not a remaining proposal.


All workers and enabled markets share budgets within the relevant scope. Creating a new SDK client does not create a new budget. Limit Binance and Bybit independently. Binance Spot and USDⓈ-M scopes follow their API rules. The same outgoing IP alone does not mean their limit catalogs can be merged or used in place of each other.

### Cost and time windows

Before sending, the adapter determines cost from the endpoint and actual parameters. Section 14 has the checked request table. Binance USDⓈ-M kline cost depends on the sent limit, not the number of returned candles. Weight 0 for fundingInfo does not remove the separate request count limit for that endpoint group.

Several limits may apply to one request: common weight, request count, endpoint group limit, and the operation's assigned share. The request needs admission under each limit. Order creation limits do not apply to our public market-data requests.

Keep local sliding history for each applicable window, plus Binance in-flight costs and trustworthy observations. For Binance, reject a positive-cost request when CURRENT accounted usage is already above the stop line. At equality or below, allow a crossing request if all other checks pass. Operation shares remain strict: local operation usage plus request cost must fit. Apply every window independently and skip zero-cost constraints. Bybit retains its strict common and operation allowances.

Pacing remains separate from window accounting. Binance crossing is permitted only while current accounted usage is at or below the stop line; it is not an extra burst pool. Bybit allows no extra usage above its window allowance. One token bucket with an average rate does not implement all window rules. The choice of a helper library does not change this contract.

### Admission and budget usage

1. Check the operation context, deadline, and bounds. Determine its type, endpoint, parameters, and all applicable costs. An unknown cost or scope is an error before sending, not permission for a free request.
2. Check budgets before queueing. Binance common/share rejection returns service_overloaded immediately without an HTTP attempt. Otherwise wait for pacing, cooldown, and an HTTP slot with cancellation support. Bybit still waits for budget. No wait holds an HTTP slot.
3. At actual admission, check and reserve the cost across all applicable windows together. Parallel Binance requests see the crossing reservation before replies arrive. Do not consume one budget and then wait for another.
4. Pass the request to HTTP transport. Do not issue admission early so callers can collect permissions and send a large batch later. If execution is delayed before sending, check admission conditions again.
5. Handle the response or transport error, update limit information, and release the HTTP slot. A retry passes the full admission process again and consumes budget.

Cancellation before passing the request to transport does not consume budget. Cancel any reservation already made. After passing it to transport, do not return the cost, even on timeout, network error, or response error. The service cannot always know whether the exchange received the request. A normalization error after a successful response also does not return budget.

Waiting must not create a busy loop or an unbounded queue. Within one operation type, use arrival order. A waiting ticker must not block another type with available budget. Section 33 defines queues, wait times, and overload behavior. Validation must detect an allowed request whose cost exceeds its entire budget. It must not become an endless wait.

### Headers, errors, and common cooldown

Binance catalog updates may raise or lower limits without clearing usage. A valid reduction always applies, even if some request costs no longer fit; reject those costs clearly. Invalid catalogs keep the last valid state, not a scope ban. New/longer windows and restart use available history without a full-window pause. A missing rule does not prove removal.

Local usage tracking works even without headers. A valid Binance counter is combined with local costs not proven included; only its own request is proven included. Retain observations for a full window after receipt using monotonic elapsed time. This conservative estimate can approach twice actual usage. Discrepancies do not create cooldowns. Exchange data may make admission stricter, but must not reset local usage or return spent units. Responses may arrive out of order. An older value must not increase the available amount or shorten an existing cooldown. Section 14 defines which headers can be trusted.

Cooldown is the time until which no new requests are sent within the affected scope. The shared limiter stores it, not an individual worker. A new signal extends the time when needed. One retry loop ending does not remove cooldown. If the exact error scope is unknown, v1 pauses all outgoing requests to that exchange. The other exchange keeps working.

| Signal | Required behavior |
| --- | --- |
| Binance HTTP 429 or 418 | Set a common cooldown in the affected scope. Follow Retry-After when present. max_backoff does not cap the blocked period. |
| Bybit HTTP 403 with reason access too frequent | Stop HTTP sessions in the affected IP scope and stop new requests for at least 10 minutes. Do not treat every 403 as this error. |
| Bybit retCode=10006, including HTTP 200 | Handle it as a rate-limit error. Use a valid reset time when present. No Go error alone does not mean success. |
| Missing or invalid wait time on a rate-limit error | Use an explicitly configured fallback cooldown for this signal. No immediate retry. Fallback defaults are fixed in the complete configuration example. |

Bybit X-Bapi-Limit-Reset-Timestamp in a successful response may mean the current time. The header being present does not allow a budget reset. Do not apply trading endpoint UID limits to public market-data APIs. Sources: [Bybit rate limits](https://bybit-exchange.github.io/docs/v5/rate-limit), [Binance Spot limits](https://developers.binance.com/en/docs/products/spot/rest-api#limits), [Binance USDⓈ-M limits](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/general-info#limits).

An operation whose deadline comes before the allowed send time ends with an error and no new attempt. Already admitted requests may have been sent before the limit signal arrived; their cost is not returned. After cooldown, each next request checks the remaining budget again. Retry policy is in section 45.

### Deployment assumption

v1 uses one service instance, confirmed by the user on September 11, 2026. The working interpretation is that no other exchange clients share its outgoing IP; verify that condition at deployment. Dedicated IP ownership itself is not confirmed. See decision D01 in the [decision register](specification-decisions-v1.md).

Several instances or other clients behind one outgoing IP need a coordinated common budget. A local limiter in one process does not track usage by other processes. Distributed limit coordination is outside the current v1 scope.

### Default profile and restart policy

The [implementation contract](implementation-contract-v1.md) and [configuration example](examples/config-v1.yaml) define the current profile. Binance uses the last valid exchange limit or a reviewed starting value, optionally reduced by an explicit user cap. Apply `upstream.binance.stop_threshold_percent` (default 90): starting stop lines are Spot 5,400 weight/minute and USD-M 2,160 weight/minute, plus raw-request and funding-family windows. Bybit keeps its 20% margin and 480 requests/5s. Derive strict operation shares from these values. Pacing is separate.

The contract also defines finite lanes, attempts, deadlines, cooldown fallbacks, and catalog updates. User decision: all market data and admission state remain in memory in v1. Restart loses local usage, discovered limits, and cooldowns, uses configured bootstrap ceilings, and adds no automatic quiet period. Exchange-side limits may still apply. Persistence is deferred; implementation and restart tests belong to phase 05.

### Test scenarios

- Spending the full tickers share stops the next ticker request and still allows other types with available budgets to run.
- Concurrent Binance requests allow one crossing but reject later positive-cost work while current usage is above any stop line. Shares stay strict; Bybit common limits stay strict.
- Each page and retry consumes budget separately; a shared Bybit ticker/stats request consumes it once.
- Changing Binance kline limit changes cost before sending; unknown cost and an unsuitable budget do not cause an endless wait.
- Canceling a wait does not consume budget; an error after passing the request to transport does not return it.
- Cooldown applies to all workers in its scope; backoff does not shorten Retry-After, and a late old response does not remove the block.
- Missing headers and known inaccurate headers do not disable limits.
- Waiting for budget does not hold an HTTP slot, respects the deadline, and does not block the other exchange.

Limiter unit tests use controlled clocks and fake transport. They do not need to wait real minutes or call production APIs.

---

## 33. Bounded requests and overload behavior

In addition to outgoing request rate, set finite limits for:

- concurrent upstream requests, in total and per operation type;
- waiting operations, in total and per operation type;
- admission wait time;
- total request processing time, including waiting, fetching, and retries;
- candles in one client request;
- upstream attempts per fetch operation, including pages and retries.

Check limits before creating an unbounded number of goroutines, tasks, or queue entries. Waiting singleflight callers also count toward incoming operation limits.

Binance budget rejection is immediate for candle work that needs an exchange call; complete cached ranges still return. Background workers wait outside the failed cycle for expiry or state change, without warning loops, HTTP attempts, or refresh errors. Waiting for Bybit budget, pacing, or cooldown must not hold an active HTTP slot. Cancellation releases held resources.

Concurrency and queue limits must leave room for each operation type. Ticker cannot take all resources needed by instruments, klines, and independent market_stats loading.

When the queue is full, the API returns `RESOURCE_EXHAUSTED` with reason `service_overloaded`.

A request above `klines.max_history_candles` slots returns `INVALID_ARGUMENT` with reason `request_too_large`. The default is `1000`. This setting also limits how far back a request may start, as described in section 38. Check size and lookback before cache access, building a fetch plan, or allocating memory for all candle slots. Count calendar slots, not the number of rows an exchange happens to return.

When the deadline or attempt budget is reached, the operation ends with an error. Do not return an incomplete result as complete. Successfully fetched candles may stay in cache.

The total kline caller timeout is configured by `klines.request_timeout`, with a confirmed default of `30s`. Start the deadline at RPC admission. It includes validation, cache reads, queue and shared-fill waits, awaited upstream work, retries, and the final completeness check. Return immediately when the data is ready. Pages, retries, and coordination loops do not reset the deadline. Earlier caller deadlines and cancellation take precedence. Preserve the independent shared-fill lifetime from section 31: a caller timeout must not cancel work still awaited by others. An expired service deadline returns `DEADLINE_EXCEEDED` with reason `request_timeout` if the connection is still writable, without a successful partial result. This setting is separate from the timeout for one upstream HTTP attempt.

The remaining numeric bounds and ownership scopes are fixed in the [implementation contract](implementation-contract-v1.md#resource-ownership-and-finite-bounds) and configuration example. These are configurable engineering defaults, not guarantees that all concurrent cold loads complete in 30 seconds.

---

## 34. gRPC API

The [gRPC implementation contract](implementation-contract-v1.md#grpc-contract) and [schema](../api/proto/marketdata/v1/market_data.proto) define the active wire, presence, validation, and statuses. `marketdata.v1.MarketDataService` has four unary methods. Generated Go and Python packages are installed from this repository. All old market-data HTTP routes return 404; there is no compatibility listener, gateway, or reflection.

## 35. Instruments API

`ListInstruments` accepts optional exchange, market, symbol and status filters. It reads only the repository and returns typed rows sorted by exchange, market, symbol. Every selected scope must be ready; otherwise return `UNAVAILABLE / data_not_ready`. A ready empty scope or missing symbol returns an empty list. Failed refreshes preserve data and timestamps.

## 36. Tickers API

`ListTickers` accepts optional exchange, market and symbol filters and follows the same readiness, sorting and preservation rules. It never starts an exchange fetch. The response contains ticker fields from section 6, without window statistics.

## 37. Market statistics API

`ListMarketStats` accepts optional exchange, market, symbol and window filters. Omitted window means `24h`; empty and other values return `INVALID_ARGUMENT / unsupported_window`. It reads only its own repository and follows the same selected-scope readiness, empty-list, sorting and failed-refresh rules. Filtering by symbol does not change the response type. Future windows require an explicit extension to collection, configuration and validation.

## 38. Klines API

`GetKlines` requires exchange, market, symbol, interval, from and to. From/to are Protobuf Timestamps. Use aligned half-open ranges and the [range contract](implementation-contract-v1.md#kline-range-contract).

The Kline application service may start an exchange fetch only for missing/stale ranges.

History depth is configured by `klines.max_history_candles`, default `1000`, for every supported exchange/market/interval. This replaces the earlier retention durations. It defines the most recent N closed candle slots relative to now, not N arbitrarily old candles and not N stored rows. It does not cause automatic preloading or restrict support to the three intervals in the workload example.

Use the selected exchange/market calendar from section 9:

1. Capture an injected UTC clock value. Let `C` be the latest slot boundary at or before now: the current slot starts at C and the latest closed slot ends at C.
2. Calculate `cutoff` by moving N slots backwards from C using calendar operations. For `1M`, move by calendar months; never multiply by 30 days. Weekly and multi-day anchors require the evidence in D10. Missing upstream candles do not shift cutoff backwards.
3. After validating query values, count requested slots with bounded arithmetic. More than N slots returns `INVALID_ARGUMENT / request_too_large`. Otherwise, `from < cutoff` returns `INVALID_ARGUMENT / range_out_of_retention`. Reject before reading candle storage, joining a fill, or calling the exchange. Do not trim the range or serve expired rows awaiting cleanup. `from == cutoff` passes the depth check.
4. `[cutoff, C)` contains exactly N closed slots. The current open slot is not included in these N historical slots and cannot move cutoff backwards. Its API inclusion follows the gRPC implementation contract; any returned open slot also counts toward the N-slot request size limit.

For example, at `2026-09-11T12:00:30Z`, the default minute-candle window is `[2026-09-10T19:20:00Z, 2026-09-11T12:00:00Z)`. All 1,000 closed slots fit. Even one older slot returns `INVALID_ARGUMENT / range_out_of_retention`.

Use checked calendar arithmetic, including when the mathematical cutoff precedes the earliest valid API timestamp. Existing timestamp validation still applies; this does not permit negative upstream timestamps or guarantee data before listing. A nonempty requested range with missing historical or pre-listing slots returns incomplete_data as defined in the gRPC implementation contract; the empty-range case is separate.

Recheck the window before another planning pass or the final repository read after waiting. A range that expires when a new candle closes returns the same depth error; it must not cause a repeated fetch/cleanup loop. A complete snapshot read while the range is valid may finish serialization even if the boundary advances afterwards.

---

## 39. Exact wire values

All decimal market values use exact Protobuf strings. Optional fields distinguish absence from zero. Counts use int64 without a float conversion; Timestamp preserves nanoseconds. Candle series identity is encoded once per response. Consumers choose their own decimal representation.

## 40. API errors

Application errors use gRPC statuses with the generated `ErrorDetail.reason`. The [status/reason table](grpc-migration-specification.md#errors) is authoritative. Unknown methods and native transport failures may have no detail. Public messages never expose exchange payloads or internal errors. A caller receives either the complete success result or an error.

## 41. Data retention

Use the same N-slot history window as the API in section 38. `klines.max_history_candles` defaults to `1000` and applies to every supported timeframe. There is one setting for API depth, request slot count, and candle retention; do not add an independent per-interval duration map. The user's latest decision replaces the earlier 21d/3d/12h defaults and the original 30d example.

```yaml
klines:
  max_history_candles: 1000

storage:
  cleanup_interval: 1h
```

For each cached exchange/market/interval, compute cutoff by stepping N slots backwards from the most recent boundary at or before the cleanup clock value. Delete when `OpenTime < cutoff` and keep equality. Do not compare `FetchedAt`, extend lifetime on reads, or keep older slots merely because recent slots are missing. The limit is on calendar positions, not a last-N-existing-rows algorithm. Capture one clock value per cleanup pass; calendar rules may differ by exchange/market, so scope deletion by exchange, market, and interval.

Prune expired rows when merging a fill into a series, using the same current history boundary, and run periodic cleanup for idle series. A series contains at most N distinct closed-slot records after a merge, plus at most one current open record if that behavior is used. Do not prefetch missing slots to fill this capacity. Periodic cleanup lag can leave expired records in idle series, but neither increases their row count nor extends API access to older data.

Reads return safe snapshots during concurrent cleanup. A concurrent or late fill must not reinsert rows older than a cutoff already applied to its scope; skip those expired rows while preserving valid rows. Applied cleanup cutoffs must not move backwards. If cleanup removes data needed by a waiting caller, the history recheck from section 38 terminates an expired request instead of refetching it.

Current ticker and MarketStats snapshots store only the latest state per scope, without history. This cleanup does not apply to these snapshots or instruments. Empty candle series and their per-series bookkeeping must be released after no read/fill owns them; retention does not authorize unlimited metadata growth for historical symbol requests.

---

## 42. Configuration

Main config is YAML, with explicit MDS_ environment overrides. The [complete v1 configuration example](examples/config-v1.yaml) defines the active field/default inventory. The [implementation contract](implementation-contract-v1.md) explains units, scopes, capability handling, limits, restart behavior, and validation. The loader validates separate server.grpc and server.http settings; removed flat listener settings fail startup.

The user-confirmed core defaults are:

```yaml
klines:
  request_timeout: 30s
  max_history_candles: 1000
upstream:
  operation_share_percent:
    tickers: 60
    klines: 30
    instruments: 5
    market_stats: 5
```

Default statistics windows remain [24h]. Binance statistics refresh defaults to 30s after each cycle; Bybit uses shared ticker responses. Instruments default to 10m refresh. HTTP attempt timeout remains separate from the total caller timeout. Percentage shares use the Binance stop line or the Bybit allowance after its safety margin. The old requests_per_second example is replaced by explicit window budgets and minimum dispatch spacing.

---

## 43. Environment overrides

Example:

```text
MDS_SERVER_HTTP_PORT=8081
MDS_SERVER_GRPC_PORT=9091
MDS_STORAGE_DRIVER=memory
MDS_KLINES_REQUEST_TIMEOUT=30s
MDS_KLINES_MAX_HISTORY_CANDLES=1000
MDS_UPSTREAM_BINANCE_STOP_THRESHOLD_PERCENT=90
MDS_UPSTREAM_BINANCE_CATALOG_REFRESH_INTERVAL=1h
MDS_UPSTREAM_OPERATION_SHARE_PERCENT_TICKERS=60
MDS_UPSTREAM_OPERATION_SHARE_PERCENT_KLINES=30
MDS_UPSTREAM_OPERATION_SHARE_PERCENT_INSTRUMENTS=5
MDS_UPSTREAM_OPERATION_SHARE_PERCENT_MARKET_STATS=5
MDS_UPSTREAM_LIMITS_BYBIT_MIN_REQUEST_SPACING=10ms
MDS_EXCHANGES_BINANCE_MARKET_STATS_REFRESH_INTERVAL=30s
MDS_EXCHANGES_BINANCE_KLINES_MAX_CANDLES_PER_REQUEST_LINEAR=1000

MDS_SENTRY_DSN=...
```

Environment values override YAML.

`MDS_KLINES_MAX_HISTORY_CANDLES` overrides the YAML integer `klines.max_history_candles`. The default is `1000` when neither source specifies it. No per-interval environment map is needed; the setting applies uniformly to all supported intervals.

Do not store secrets in committed YAML.

---

## 44. Config validation

Klines: validate max_candles_per_request per exchange/market using section 30, after defaults and ENV overrides. The value must be an integer from 1 to the documented maximum.

`klines.request_timeout` must be a positive, finite duration representable by `time.Duration`. Omission uses `30s`; explicit empty, zero, negative, malformed, and overflowing values are configuration errors. Apply YAML and then `MDS_KLINES_REQUEST_TIMEOUT` before validation. Do not replace invalid values with the default.

`klines.max_history_candles` must be a positive integer representable by the implementation's checked slot-count type. Omission uses `1000`. Reject explicit null, empty, zero, negative, fractional, boolean, malformed, and overflowing values; YAML must contain an integer, while the environment value is its base-10 integer text. Apply defaults, YAML, then `MDS_KLINES_MAX_HISTORY_CANDLES`. Reject the superseded `storage.retention.klines` setting instead of silently ignoring it. Changes to the count do not alter the upstream page limits in section 30. Calendar subtraction and request slot counting must remain bounded and detect overflow.

The application fails fast on invalid config. For each enabled exchange, instruments.refresh_interval must be a positive duration. A missing setting uses the default 10m.

MarketStats: windows must be exactly `[24h]` and supported by all enabled providers' capabilities. The independent stats worker's refresh_interval must be positive. Reject a separate stats refresh_interval for a provider with MarketStatsWithTicker=true, so the config does not promise a schedule that does not exist.

`upstream.operation_share_percent` contains exactly `tickers`, `klines`, `instruments`, and `market_stats` after applying defaults, YAML, and the corresponding `MDS_UPSTREAM_OPERATION_SHARE_PERCENT_<OPERATION>` overrides. Each value must be an integer from 1 through 100 and their sum must be exactly 100. Reject unknown or duplicate keys, nulls, empty values, booleans, fractions, malformed integers, and invalid sums. A missing map, empty map, or missing entry keeps the corresponding default; a partial override must still produce a valid final sum. Apply the provider capability rule in section 32 before deriving effective allowances: a shared ticker/statistics path combines those percentages, while other disabled paths leave their share idle. The configured base percentages must still total 100.

Validate `upstream.binance.stop_threshold_percent` as an integer from 1 to 99 and `catalog_refresh_interval` as a positive finite duration, default 1h. Explicit legacy Binance window limits are user caps, including default-valued overrides; omitted limits follow exchange changes. The old safety margin applies to Bybit only. Set the new Binance percentage to 80 explicitly to keep an older 80% policy.

Validate derived starting integer allowances for every applicable common allocation window against the most expensive permitted request for each independent operation. Reject a zero allowance or any allowance smaller than a single allowed request; do not round it up or let admission wait forever. Keep a short smoothing limit distinct from an allocation window: splitting a tiny requests-per-second allowance into 5% portions can make instrument loading impossible. Pages and retries must all consume budget, but the percentages do not guarantee that an entire multi-page/retry operation finishes within its deadline. Absolute windows, pacing, and resource bounds are fixed in the implementation contract and complete configuration example.

Also check positive finite queue, concurrency, time, and request size limits. Check that common and operation budgets fit together, including burst, and that at least one valid request can run for every enabled operation type.

Examples:

```text
min_request_spacing <= 0
max_history_candles <= 0
invalid duration
unknown exchange
unknown market
unsupported storage driver
invalid Sentry sample rate
```

---

## 45. Retry policy

Use retries only for temporary failures:

```text
network errors
timeouts
429
selected 5xx
```

Do not retry most:

```text
4xx
validation errors
invalid symbol
```

Use:

```text
exponential backoff
+
jitter
```

All retry attempts pass common and operation budgets and count toward the operation's total upstream attempt limit. Nested SDK retries must not bypass these limits.

All requests in the affected limit scope must follow the exchange's wait time (`Retry-After` or a documented equivalent). `max_backoff` does not shorten this time. If the wait exceeds the operation deadline, end the operation without another attempt.

Budget rejection does not start an HTTP retry. A background Binance worker waits for ordinary admission recovery without a special probe; repeated deferral does not extend expiry. Catalog refresh reuses successful instrument exchangeInfo and defaults to 1h. A failed catalog refresh keeps the last valid limits and waits its bounded schedule.

After retries are exhausted, the background worker still follows backoff. Starting a new cycle immediately must not reset the limit.

---

## 46. Observability

Observability must be optional. The existing logging and metrics report Binance limit source/age, exchange limit, user cap, percentage, stop line, local/observed/accounted/reserved usage, headroom, uncertainty, per-window operation reasons, and predicted recovery. Catalog failures and oversized bodies remain distinct from budget/share rejection and exchange cooldown. See the [diagnostic contract](development.md#binance-admission-diagnostics).

Support:

```text
Sentry
Prometheus
built-in statistics
```

---

## 47. Sentry

Config:

```yaml
observability:
  sentry:
    enabled: true
    dsn: "${SENTRY_DSN}"
    environment: "production"
    traces_sample_rate: 0.1
```

Use Sentry mainly for:

```text
errors
panic recovery
data RPC and operational HTTP traces
exchange request traces
slow operations
storage errors
```

Add context:

```text
exchange
operation
symbol
market
interval
```

Do not send huge candle arrays to Sentry.

---

## 48. Prometheus

Prometheus must be fully optional.

If:

```yaml
prometheus:
  enabled: false
```

the endpoint is absent.

If enabled:

```text
GET /metrics
```

---

## 49. Built-in statistics

Prometheus may be absent, so the service must collect basic operational statistics on its own.

Store counters in memory:

```text
exchange_requests_total
exchange_errors_total

ticker_refresh_total
ticker_refresh_errors_total

market_stats_refresh_total
market_stats_refresh_errors_total

instrument_refresh_total
instrument_refresh_errors_total

kline_cache_hits_total
kline_cache_misses_total

kline_upstream_requests_total
kline_candles_downloaded_total

singleflight_shared_total

last_successful_ticker_fetch
last_successful_market_stats_fetch
last_successful_instrument_fetch

ticker_snapshot_size
market_stats_snapshot_size
instrument_snapshot_size
kline_count
```

Admission gauges use only fixed scopes, reviewed window names, operation names, and reason values. Arbitrary discovered windows appear only in detailed JSON; metrics count those windows by unit and count blocking windows per operation/reason without adding unlike costs. Diagnostic reads do not reserve or spend attempts.

Group MarketStats metrics by exchange/market/window, without a symbol label. Success means a published snapshot. Count normalization or write errors separately for each branch. A shared Bybit HTTP request increases exchange_requests_total once. The operational statistics in this section are not the market data model MarketStats.

For durations, we can store:

```text
count
sum
max
```

without a full histogram implementation.

---

## 50. Statistics logging

Do not write periodic operational statistics snapshots to logs. Collect counters in memory and expose them on request through optional Prometheus or `/debug/stats` endpoints.

`observability.stats.enabled` controls standalone collection. Either endpoint also enables collection independently. The statistics logging worker and `observability.stats.log_interval` setting are removed; reject the obsolete YAML key and `MDS_OBSERVABILITY_STATS_LOG_INTERVAL` environment variable as unknown settings.

---

## 51. Optional statistics endpoint

We can add an optional endpoint:

```text
GET /debug/stats
```

Config:

```yaml
observability:
  stats:
    endpoint_enabled: false
```

Response:

```json
{
  "bybit": {
    "requests_total": 12039,
    "errors_total": 12,
    "ticker_last_success": "...",
    "ticker_snapshot_size": 641
  }
}
```

The endpoint is disabled by default.

The current default response is a sorted array of `{name, labels, value}` samples; the earlier object above is illustrative. `GET /debug/stats?view=admission` returns one coherent Binance controller snapshot with detailed windows. Per-operation checks state their assumed maximum request cost; zero recovery time means unknown or impossible. See the [operating guide](development.md#binance-admission-diagnostics) for field semantics.

---

## 52. Structured logging

All important operations must have structured fields.

For example:

```json
{
  "level": "info",
  "operation": "get_klines",
  "exchange": "bybit",
  "market": "linear",
  "symbol": "BTCUSDT",
  "interval": "5m",
  "duration_ms": 81,
  "candles": 412,
  "cache_hit": false
}
```

Do not log every candle. Log meaningful admission transitions and catalog failures. Changed catalog logs include new limits, sources, and stop lines. Do not repeat unchanged catalogs or normal worker deferrals as warnings or exchange errors.

---

## 53. Health endpoints

```text
GET /health
GET /ready
```

`/health`:

```text
process alive
```

`/ready`:

```text
both server owners + storage initialized
```

A temporary exchange failure does not make the whole service unready.

Exchange data freshness can be checked separately through statistics.

---

## 54. Startup

Load defaults/YAML/environment, validate config, initialize optional observability and local statistics/storage, construct providers and admission. Then bind gRPC and operational HTTP and start owned instrument/ticker/independent statistics/retention workers without waiting for exchange success.

First data cycles are scheduled immediately, subject to cooldown and admission. They run independently per enabled exchange/market; instrument readiness does not block ticker. Bybit has no independent statistics worker. The three snapshot APIs return data_not_ready for uninitialized selected scopes, and klines requires a ready instrument catalog for symbol validation. An exchange outage does not turn local readiness into a global failure. See the [startup contract](implementation-contract-v1.md#startup-and-metadata).

---

## 55. Graceful shutdown

Handle:

```text
SIGINT
SIGTERM
```

Clear readiness and close RPC admission, then cancel root-owned work. Drain gRPC and operational HTTP concurrently within one `server.shutdown_timeout` budget (35s by default). Wait for fills and collectors. At the deadline force-stop pending sends and close HTTP. Sentry flush uses only the remaining budget. No unbounded GracefulStop is allowed.

## 56. Project structure

```text
market-data-service/
├── cmd/
│   └── market-data-service/
│       └── main.go
│
├── internal/
│   ├── domain/
│   │   ├── exchange.go
│   │   ├── market.go
│   │   ├── instrument.go
│   │   ├── ticker.go
│   │   ├── market_stats.go
│   │   ├── kline.go
│   │   └── timeframe.go
│   │
│   ├── application/
│   │   ├── instrument/
│   │   │   ├── service.go
│   │   │   └── repository.go
│   │   │
│   │   ├── ticker/
│   │   │   ├── service.go
│   │   │   └── repository.go
│   │   │
│   │   ├── marketstats/
│   │   │   ├── service.go
│   │   │   ├── collector.go
│   │   │   └── repository.go
│   │   │
│   │   └── kline/
│   │       ├── service.go
│   │       ├── repository.go
│   │       ├── provider.go
│   │       └── fetch_planner.go
│   │
│   ├── infrastructure/
│   │   ├── exchange/
│   │   │   ├── binance/
│   │   │   └── bybit/
│   │   │
│   │   ├── storage/
│   │   │   └── memory/
│   │   │
│   │   ├── ratelimit/
│   │   └── observability/
│   │
│   ├── transport/
│   │   └── http/
│   │
│   └── config/
│
├── configs/
│   └── config.yaml
│
├── Dockerfile
├── compose.yaml
├── Makefile
├── go.mod
└── README.md
```

---

## 57. Unit tests

Test business logic without external network.

Focus on:

### Timeframe

Cover Timeframe rules and adapter mapping from section 9, including calendar months, boundary calculations, and exchange/market support.

### Kline planner

Check:

```text
empty cache
complete cache

gap beginning
gap middle
gap end

multiple gaps

multiple gaps merged into one request

multiple gaps too large for one request

exchange request limit boundary

duplicate candles

unordered candles

current open candle refresh
closed candle cache hit
```

### Tickers and market statistics

Test mapping and edge cases from sections 6–7, API contracts from sections 36–37, and independent snapshot rules from sections 23–24. For Binance, use controlled clocks to test immediate fetch, a 30s wait after the cycle ends, no overlapping refreshes, and canceled waits on shutdown. For Bybit, test publication of both branches, including a normalization or repository error in one branch.

### Request limits

Deterministic tests without production network access:

- continuous ticker does not use up the instruments, klines, or independent market_stats budgets;
- Binance rejects positive-cost work when current accounted usage is above any stop line, with equality/crossing allowed and strict shares; Bybit retains strict common/share limits;
- pages and retries count as separate HTTP attempts;
- queue, concurrency, time, and request size limits are respected;
- oversized requests are rejected before upstream fetch;
- canceling a waiting operation releases resources;
- waiting for budget does not hold an active HTTP request slot;
- rate-limit cooldown applies to all requests in the affected scope;
- a new ticker cycle does not bypass backoff;
- overload does not return a successful partial result;
- invalid catalog then valid recovery clears diagnostic errors without usage reset;
- parallel candle callers allow one crossing, keep complete cached data readable, and count only dispatched attempts;
- workers wait through budget expiry and a longer real 429/418 cooldown without probes;
- diagnostic labels remain bounded when catalogs add windows, and restart history gaps are visible.

Use controlled clocks or another deterministic mechanism for time checks. Tests must not depend on real network delays.

---

## 58. Concurrency tests

Required check:

```text
50 simultaneous identical kline requests
```

Expected:

```text
one effective cache-fill operation
```

Also test:

```text
overlapping ranges
```

Example:

```text
A: 10:00–14:00
B: 12:00–16:00
```

Check that already loaded data is not fetched again without a reason.

Also test 50 concurrent ticker and MarketStats reads during ReplaceSnapshot. They must see only full snapshots and make no upstream requests. Different window scopes are isolated in the repository. This isolation test does not mean the v1 API supports other windows.

Run:

```bash
go test -race ./...
```

---

## 59. Integration tests

v1 does not use Testcontainers.

Build integration tests with:

```go
httptest.Server
```

and the real in-memory repository.

For exchange adapters:

```text
official SDK
      ↓
custom base URL
      ↓
httptest.Server
```

Test real mapping/parsing paths without production exchange requests.

---

## 60. Important integration scenarios

### Instruments

```text
fake exchange response
→ exchange adapter
→ domain normalization
→ repository
→ generated gRPC client
```

### Tickers

```text
fake exchange response
→ ticker collector
→ repository
→ generated gRPC client
```

### Market statistics

Bybit: one fake upstream response → two independent normalizations → two repositories → separate gRPC responses. An error in either branch, including a repository write, does not cancel publication of the other.

Binance: separate price/book/funding and 24hr requests → independent collectors → separate snapshots. A 24hr error does not stop ticker; a ticker error does not stop statistics. All HTTP attempts count toward the correct budgets.

Test the default window, unsupported_window without cache/upstream, data_not_ready before the first snapshot, a successful empty snapshot, filters, old fetched_at kept after an error, and no statistics fields in ticker messages.

### Kline cold cache

```text
API
→ empty repository
→ fake exchange
→ repository
→ response
```

### Warm cache

```text
API
→ repository
→ response
```

Expected:

```text
0 upstream requests
```

### Partial cache

```text
API
→ gaps detected
→ minimal fetch ranges
→ exchange
→ cache updated
→ full response
```

### Concurrent cache miss

```text
N callers
→ one fill
→ N responses
```

---

## 61. Dockerfile

Production-oriented multi-stage Dockerfile.

```text
Go builder
   ↓
compile binary
   ↓
minimal runtime image
```

Requirements:

```text
non-root user
small runtime image
graceful SIGTERM
config mount
healthcheck
```

---

## 62. compose.yaml

v1:

```text
market-data-service
```

Storage:

```text
memory
```

Compose uses a non-root user, a read-only root filesystem, 1,000,000,000-byte memory and swap limits, and GOMEMLIMIT=700MiB. Both published ports bind only to 127.0.0.1. The healthcheck uses operational HTTP. Config is mounted read-only. Market data and limiter state are memory-only in v1; no persistent state mount or initialization command is required. Restart loses local counters and cooldowns without resetting exchange-side limits.

Future:

```text
market-data-service
postgres
redis
```

without changing application architecture.

---

## 63. Makefile

Minimum:

```text
make build
make run

make test
make test-race

make lint
make check
make vet
make check-api
make release-load

make docker-build
make docker-verify
make docker-up
make docker-down
```

---

## 64. Non-goals v1

Not included:

```text
trading execution
orders
positions
balances
account leverage API

strategy calculations
trend detection
level detection
breakout detection

MCP server

order book
raw trades

Redis implementation
PostgreSQL implementation

ticker historical storage
market statistics historical storage
market statistics windows other than 24h
market statistics aggregation from klines

WebSocket market-data ingestion
```

WebSocket ingestion may be a separate next stage after the service architecture is stable.

---

## 65. Definition of Done

v1 is ready when:

1. Binance and Bybit are available through one domain/application API.
2. Adapters fully hide exchange SDKs.
3. All decimal market values use `decimal.Decimal`.
4. Float market values do not enter the domain layer.
5. Instruments refresh automatically.
6. Tickers update continuously within operation and common budgets, with backoff on errors.
7. The Ticker API always reads the repository.
8. Klines use cache-aside.
9. The Kline fetch planner minimizes exchange request count.
10. Several gaps are merged into one request when the exchange limit allows it.
11. Closed klines are reused from cache.
12. The current candle refreshes correctly.
13. Concurrent kline misses do not cause duplicate upstream requests.
14. All upstream HTTP requests, pages, and retries are accounted. No new positive-cost Binance request is admitted when its CURRENT accounted usage is already above any applicable stop line. Crossing is allowed; operation shares are strict. This is not a hard 90% actual-IP guarantee. External traffic, restart history gaps, delayed observations, and unseen limits remain unknown; conservative overlap may approach twice actual usage. Bybit keeps its existing admission rules.
15. Storage is fully behind interfaces.
16. Thread-safe in-memory storage is implemented.
17. Historical klines are cleaned up automatically.
18. Config loads from YAML.
19. ENV variables override YAML.
20. Sentry optional.
21. Prometheus optional.
22. Built-in service statistics work independently of Prometheus.
23. Structured logging is available.
24. Health/readiness endpoints are available.
25. Graceful shutdown works.
26. Unit tests are present.
27. Integration tests run without Testcontainers.
28. Tests do not need production Binance/Bybit access.
29. Race tests are present.
30. A Dockerfile is present.
31. compose.yaml is present.
32. A Makefile is present.
33. Concurrency, queues, wait time, kline request size, and upstream attempt count are bounded.
34. Continuous ticker does not use up other operation types' budgets and resources.
35. Overload and oversized requests return the documented errors.
36. Budget values and their config schema are fixed based on section 14 results and deployment conditions.
37. Window values are removed from Ticker and provided through a separate MarketStats model through `ListMarketStats`.
38. v1 supports only window=24h; other windows are rejected before cache/upstream. Window is part of the storage key.
39. Bybit fills both models with one request and independent publication; Binance refreshes statistics with a separate worker and budget.
40. The MarketStats API reads only the repository, distinguishes unready and empty snapshots, and keeps fetched_at after a failed refresh.
41. Tests cover all fields in both model mappings, independent updates, and the API contract as described in sections 6–7 and 57–60.

Current acceptance also requires all four methods from installed Go/Python clients, independent operational HTTP under saturation, bounded native send ownership, and the complete Linux capacity profile. See the [gRPC verification](grpc-migration-verification.md); historical HTTP latency is not a network gRPC baseline.
