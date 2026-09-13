# Captured exchange fixtures

Shared offline fixtures for domain calendar tests and exchange adapter tests. The captures are unchanged from September 11, 2026. Each file contains its request context and response; tests need no credentials or live exchange access.

## Calendar fixtures

Twelve files in this directory, named exchange-market-symbol-interval.json, contain the parsed raw JSON response, request URL, HTTP status, observed opens, and expected normalized rows. JSON formatting is not the original wire representation. Decimal strings preserve the upstream values. Binance inclusive close milliseconds become the exclusive next boundary; Bybit descending rows become ascending rows with null trades_count.

- Binance spot and linear: BTCUSDT and ETHUSDT, each at 3d and 1w (eight files).
- Bybit spot and linear: BTCUSDT and ETHUSDT, each at 1w (four files).
- Requested range: December 15, 2025 through January 14, 2026, crossing a year boundary.
- All weekly examples open Monday at 00:00 UTC: December 15/22/29 and January 5/12.
- Binance 3d opens: December 15/18/21/24/27/30 and January 2/5/8/11/14. The chosen fixed anchor is January 2, 1970 at 00:00 UTC, plus multiples of 72 hours. January 1, 1970 is not a matching anchor.

These anchors are an inference from consistent examples across both symbols and affected markets. They do not establish every historical row or eliminate the need for calendar and adapter tests.

`captured_at` records request start, not response receipt. Inject a separate receipt clock when testing `FetchedAt`. JSON formatting is not the original wire representation.

## Funding fixture

`binance-funding-info.json` contains the full 782-record fundingInfo response and selected exchangeInfo metadata. Tests check explicit 1/4/8-hour values converted to seconds and absent-symbol behavior. Synthetic failure cases are in the [adapter tests](../../internal/infrastructure/exchange/binance/instruments_test.go).
