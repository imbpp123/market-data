# market-data

A Go service for a common crypto market data API. It is designed to give trading bots, MCP servers, and other clients one data model across exchanges, with shared caching to reduce duplicate requests.

v1 targets Binance and Bybit spot and linear markets, with instruments, tickers, 24-hour market statistics, and candlesticks.

**Status:** Phases 01–06 are implemented. Instruments now work end to end for Binance and Bybit spot/linear: exact normalization, bounded background refresh, atomic memory snapshots, and `GET /api/v1/instruments`. Configuration, health/readiness, graceful shutdown, domain calendars, repositories, and shared upstream admission/retries are available. Ticker/statistics collection, candle loading, the other market-data APIs, and observability exporters remain future work.

## Documentation

- [Development guide](docs/development.md) — setup, running locally, configuration, and checks.
- [Configuration example](docs/examples/config-v1.yaml) — all settings with comments.
- [Technical specification](docs/technical-specification-v1.md) — scope, architecture, and API requirements.
- [Implementation decisions](docs/implementation-contract-v1.md) — configuration and behavior contracts.
- [Roadmap](docs/phases/README.md) — implementation phases and current progress.
