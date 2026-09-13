# Phase 01 evidence

Public, read-only discovery on September 11, 2026. These captures support specification decisions. Calendar and funding fixtures now live in [testdata/exchange](../../../testdata/exchange) and are replayed by offline tests without credentials or exchange access. Requests and capture times are embedded in each file. Calendar captured_at records request start, not response receipt; inject a separate receipt clock when testing FetchedAt.

## Calendar fixtures

Twelve files in [testdata/exchange](../../../testdata/exchange), named exchange-market-symbol-interval.json, contain the parsed raw JSON response, request URL, HTTP status, observed opens, and expected normalized rows. JSON formatting is not the original wire representation. Decimal strings preserve the upstream values. Binance inclusive close milliseconds become the exclusive next boundary; Bybit descending rows become ascending rows with null trades_count.

- Binance spot and linear: BTCUSDT and ETHUSDT, each at 3d and 1w (eight files).
- Bybit spot and linear: BTCUSDT and ETHUSDT, each at 1w (four files).
- Requested range: December 15, 2025 through January 14, 2026, crossing a year boundary.
- All weekly examples open Monday at 00:00 UTC: December 15/22/29 and January 5/12.
- Binance 3d opens: December 15/18/21/24/27/30 and January 2/5/8/11/14. The chosen fixed anchor is January 2, 1970 at 00:00 UTC, plus multiples of 72 hours. January 1, 1970 is not a matching anchor.

These anchors are an inference from consistent examples across both symbols and affected markets. They do not establish every historical row or eliminate the need for calendar and adapter tests.

## Other captures

| File | Provenance and use |
| --- | --- |
| [exchange-limits.json](exchange-limits.json) | Live Binance Spot and USDⓈ-M exchangeInfo rateLimits excerpts, source URLs, capture times, and full-response checksums. ORDERS limits are not service budgets. |
| [binance-spot-full-statistics.json](binance-spot-full-statistics.json) | Successful public type=FULL bulk statistics request, 3,701-row count, required-field checks, checksum, and excerpt. The full response is not stored. |
| [binance-funding-info.json](../../../testdata/exchange/binance-funding-info.json) | Full 782-record fundingInfo response and selected exchangeInfo metadata; expected explicit 1/4/8-hour values converted to seconds and missing-symbol null behavior. |

Synthetic funding failure cases are covered by the [adapter tests](../../../internal/infrastructure/exchange/binance/instruments_test.go). Executed Go toolchain checks are recorded in the [release audit](../../release-verification-v1.md).

The [HTTP examples](../../examples/http-contract-v1.json) are synthetic contract cases, not live service responses.

## Primary references

- [Binance Spot REST API](https://github.com/binance/binance-spot-api-docs/blob/master/rest-api.md): bulk statistics, request weights, IP limits, and Retry-After.
- [Binance USDⓈ-M market data](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data): fundingInfo endpoint-family limit and kline costs.
- [Bybit rate limits](https://bybit-exchange.github.io/docs/v5/rate-limit): 600 requests per 5 seconds per IP and access-too-frequent cooldown.
- [Bybit instruments](https://bybit-exchange.github.io/docs/v5/market/instrument): categories, default current catalog, PreLaunch selection, and pagination.
- [Go releases](https://go.dev/dl/): official toolchain distribution.

Deployment access, pinned SDK behavior, memory use, and throughput are covered in the [release audit](../../release-verification-v1.md). Public discovery from this environment is not proof of access from the deployment IP. No account data or credentials are included.
