# Market Data

Market Data provides a single entry point for market data from multiple exchanges through one API. It handles data collection, caching, and exchange request limits for client applications.

## Features

- Instrument catalogs, tickers, 24-hour statistics, and candles.
- Shared caching to reduce exchange requests.
- Coordinated candle loading for concurrent clients.
- Exchange request-limit tracking and cooldown handling.
- Background updates and automatic retries for temporary failures.
- A common data format with exact decimal values.
- Configurable candle history and automatic cleanup.
- Request timeouts and overload protection.
- gRPC API with Go and Python clients.
- Health checks, logs, and optional Prometheus and Sentry integration.
- YAML and environment configuration, with Docker Compose support.

## Supported exchanges and markets

| Exchange | Spot | Linear futures | Inverse futures | Options |
| --- | --- | --- | --- | --- |
| Binance | Supported | Supported: USDⓈ-M | Not supported: COIN-M | Not supported |
| Bybit | Supported | Supported: linear contracts | Not supported | Not supported |

## Documentation

| Guide | Contents |
| --- | --- |
| [Quick start](docs/quickstart.md) | Local and Docker setup, with a first API request. |
| [API and clients](docs/api.md) | Requests, filters, candle ranges, clients, and errors. |
| [Data model](docs/data-model.md) | Entities, units, timestamps, and candle intervals. |
| [Configuration](docs/configuration.md) | YAML, environment overrides, defaults, and validation. |
| [Architecture](docs/architecture.md) | Components, data flow, caching, and exchange request limits. |
| [Operations](docs/operations.md) | Deployment, monitoring, memory, updates, and shutdown. |
| [Troubleshooting](docs/troubleshooting.md) | Common failures, checks, and fixes. |
| [Development](docs/development.md) | Source layout, tests, API generation, and releases. |

## Limits

- All data and request-limit state live in memory and are lost on restart. The default candle history is 1,000 calendar slots per series.
- Failed refreshes preserve previous snapshots. v1 has no snapshot expiry; clients must check `updated_at` or `fetched_at` for freshness.
- v1 assumes one instance with no other exchange clients sharing its outgoing IP. Restarting does not reset exchange-side usage or bans.
- There is no native TLS or authentication. Use a trusted network or a separately protected connection for remote access.
- Trading and account APIs, order books, raw trades, WebSocket ingestion, persistent storage, ticker/statistics history, and non-24h statistics are outside v1. There is no statistics aggregation from candles, strategy calculation, or MCP server.
- There is no HTTP market-data API, gateway, or gRPC reflection. Use generated clients or the checked-in descriptor.

## Contributing

Read the [development rules](AGENTS.md), keep changes focused, and add tests for non-trivial logic. Include check results in pull requests; see the [development checks](docs/development.md#checks).

For bug reports, include the version, reproduction steps, expected and actual behavior, and relevant logs with secrets removed.

## License

Licensed under the [MIT License](LICENSE).
