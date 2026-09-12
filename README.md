# market-data

A Go service for a common crypto market data API. It is designed to give trading bots, MCP servers, and other clients one data model across exchanges, with shared caching to reduce duplicate requests.

v1 targets Binance and Bybit spot and linear markets, with instruments, tickers, 24-hour market statistics, and candlesticks.

**Status:** Phases 01–10 are implemented. All four data APIs work for Binance and Bybit spot/linear. Candles use bounded cache-aside fills with shared series coordination, exact exchange pages, caller cancellation, history validation, and complete-range responses. Configuration, health/readiness, graceful shutdown, domain calendars, memory repositories, shared upstream admission/retries, and internal feature counters are available. Periodic retention cleanup, observability exporters, and release packaging remain future work.

## Documentation

- [Development guide](docs/development.md) — setup, running locally, configuration, and checks.
- [Configuration example](docs/examples/config-v1.yaml) — all settings with comments.
- [Technical specification](docs/technical-specification-v1.md) — scope, architecture, and API requirements.
- [Implementation decisions](docs/implementation-contract-v1.md) — configuration and behavior contracts.
- [Roadmap](docs/phases/README.md) — implementation phases and current progress.
