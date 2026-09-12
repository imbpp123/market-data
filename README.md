# market-data

A Go service for a common crypto market data API. It is designed to give trading bots, MCP servers, and other clients one data model across exchanges, with shared caching to reduce duplicate requests.

v1 targets Binance and Bybit spot and linear markets, with instruments, tickers, 24-hour market statistics, and candlesticks.

**Status:** Early development. Configuration loading, health endpoints, graceful shutdown, domain models, UTC candle calendars, funding read models, application contracts, and in-memory repositories are implemented. Exchange integrations and market-data endpoints are not available yet.

## Documentation

- [Development guide](docs/development.md) — setup, running locally, configuration, and checks.
- [Configuration example](docs/examples/config-v1.yaml) — all settings with comments.
- [Technical specification](docs/technical-specification-v1.md) — scope, architecture, and API requirements.
- [Implementation decisions](docs/implementation-contract-v1.md) — configuration and behavior contracts.
- [Roadmap](docs/phases/README.md) — implementation phases and current progress.
