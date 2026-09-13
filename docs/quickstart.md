# Quick start

Choose one of the two options below. Both start gRPC on `localhost:9090` and operational HTTP on `localhost:8080`, with both markets enabled on both exchanges. Downloads and live data collection require network access. No exchange credentials are needed.

## Local

Install Git, Make, and Go **1.27.1**, then clone and run:

```sh
git clone https://github.com/imbpp123/market-data.git
cd market-data
make build
./bin/market-data-service -config config/config-v1.yaml -check-config
MDS_SERVER_GRPC_HOST=127.0.0.1 MDS_SERVER_HTTP_HOST=127.0.0.1 \
  ./bin/market-data-service -config config/config-v1.yaml
```

The host overrides keep the service on loopback. Stop it with Ctrl+C.

## Docker

Install Docker and `curl`. This option requires a published image in [GHCR](https://github.com/imbpp123/market-data/pkgs/container/market-data). Replace `RELEASE_TAG` below with its matching Git tag, including the `v` prefix if present. There is no `latest` image tag.

Download the configuration and API descriptor from that version:

```sh
RELEASE_TAG=REPLACE_WITH_PUBLISHED_GIT_TAG
SOURCE_URL="https://raw.githubusercontent.com/imbpp123/market-data/${RELEASE_TAG}"
mkdir -p market-data-docker/api
cd market-data-docker
curl -fL "${SOURCE_URL}/config/config-v1.yaml" -o config.yaml
curl -fL "${SOURCE_URL}/api/descriptor.binpb" -o api/descriptor.binpb
chmod a+r config.yaml
```

Start the image in the same terminal:

```sh
docker run --rm --name market-data --platform linux/amd64 \
  -p 127.0.0.1:9090:9090 -p 127.0.0.1:8080:8080 \
  --mount "type=bind,source=$(pwd)/config.yaml,target=/etc/market-data/config.yaml,readonly" \
  --memory 1000000000 --memory-swap 1000000000 \
  -e GOMEMLIMIT=700MiB --stop-timeout 40 \
  "ghcr.io/imbpp123/market-data:${RELEASE_TAG#v}"
```

The published image targets Linux/amd64; other host architectures need emulation. Stop it from another terminal with `docker stop market-data`. For building your own image or using Compose, see the [Docker and Compose guide](operations.md#docker-and-compose).

## Send a request

Install `grpcurl`. In a second terminal, open the repository root for a local run, or the `market-data-docker` directory for a Docker run:

```sh
grpcurl -plaintext -protoset api/descriptor.binpb -max-msg-sz 16777216 -max-time 5 \
  -d '{"exchange":"binance","market":"spot","symbol":"BTCUSDT"}' \
  localhost:9090 marketdata.v1.MarketDataService/ListTickers
```

A successful response contains a `tickers` array. Example values below are illustrative; prices, timestamps, and optional fields depend on the exchange response:

```json
{
  "tickers": [
    {
      "exchange": "binance",
      "market": "spot",
      "symbol": "BTCUSDT",
      "lastPrice": "60000",
      "bidPrice": "59999.99",
      "bidSize": "0.5",
      "askPrice": "60000.01",
      "askSize": "0.8",
      "fetchedAt": "2026-09-13T12:00:00Z"
    }
  ]
}
```

Until the first successful snapshot, the request returns `UNAVAILABLE` with `ErrorDetail.reason=data_not_ready`. Retry after collection succeeds. If it persists, inspect the service logs using the [diagnostic guide](operations.md#diagnose-and-operate). `/health` and `/ready` report process and local initialization status, not data availability or freshness.

Clients should reuse channels, set call deadlines, and allow responses up to 16 MiB. Candle ranges must be aligned and half-open: `[from, to)`. See the [API guide](development.md#instruments) for filters, candle ranges, and error behavior, and the [operations guide](operations.md) for resource and rate-limit details.

For settings and environment overrides, see the [configuration guide](development.md#configuration) and [complete example](../config/config-v1.yaml). For more guides, return to the [documentation index](../README.md#documentation).
