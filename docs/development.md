# Development

Use this guide to build, change, test, and release the project. Read [AGENTS.md](../AGENTS.md) for contribution rules and [architecture](architecture.md) for component boundaries. Run commands from the repository root unless a guide says otherwise.

## Tools and local setup

Install Git, Make, and Go **1.27.1**. The module, Makefile, Dockerfile, and CI pin that version. Normal service builds need neither Python nor Protobuf generators.

```sh
make build
make check-config
make run
```

`make run` uses `config/config-v1.yaml` and binds both default listeners to all interfaces. Use the [local quick start](quickstart.md#local) for loopback bindings. `-check-config` validates settings without opening listeners or starting workers.

API generation and client checks also require Python **3.13 and 3.14**. See the [API tool guide](../api/README.md#reproduce-and-check) for pinned generators, runtime versions, and interpreter overrides. Downloads require network access; tests use controlled fixtures without exchange credentials.

## Find the right code

| Task | Start here |
| --- | --- |
| Change data meaning or candle calendars | [domain](../internal/domain), [data model](data-model.md) |
| Change snapshot reads or refreshes | [application](../internal/application) |
| Change candle planning, sharing, or retention | [application/kline](../internal/application/kline) |
| Change exchange requests or normalization | [exchange adapters](../internal/infrastructure/exchange) |
| Change budgets, retries, or cooldowns | [upstream controller](../internal/infrastructure/exchange/upstream) |
| Change cache storage | [memory repositories](../internal/infrastructure/storage/memory) |
| Change API input, output, or errors | [gRPC transport](../internal/transport/grpc), [schema](../api/proto/marketdata/v1/market_data.proto) |
| Change startup or dependency wiring | [bootstrap](../internal/bootstrap) |
| Add or change a setting | [config](../internal/config), [configuration reference](../config/config-v1.yaml) |

Keep business rules out of transport and startup code. Application interfaces belong near their callers; adapters implement them. Inject settings, clocks, and external dependencies so behavior can be tested without a real exchange.

## Change an exchange adapter

Use the existing adapter for the relevant market. Keep SDK types and response formats inside infrastructure. Normalize from original decimal text; a float conversion before parsing loses information. Bybit candles use a direct HTTP path through the same admitted transport to avoid the SDK's generic float decoder.

A candle adapter handles one planned page in an existing fill operation. It does not reset operation budgets or add its own pagination loop. Convert the API's exclusive end to the exchange's inclusive millisecond end where needed. Preserve successful request-start and receipt times for cache finality.

Reject malformed used fields, duplicate candle openings, invalid price relationships, and symbol/category mismatches as a whole page or snapshot failure. Do not turn source failures into missing optional values or publish partial snapshots. Add fixture-based tests for field mappings, precision, supported intervals, and failure paths.

When adding support, update the adapter's interval table, configuration validation, bootstrap wiring, and public support documentation as needed. Avoid a generic framework for hypothetical exchanges.

## Change the API

Edit the [Protobuf source](../api/proto/marketdata/v1/market_data.proto), then regenerate:

```sh
make generate-api
make check-api
```

Commit the schema, generated Go/Python code, type stubs, and descriptor together. Never edit generated output manually. Optional fields preserve absence separately from zero. Existing field numbers and names must remain compatible; reserve removed ones rather than reuse them.

The committed [compatibility baseline](../api/compatibility/README.md) is independent of generation. Do not replace it to silence a breaking-change failure. Compatible additions stay in `marketdata.v1`; an incompatible contract needs a new package version.

Go clients live in the nested `api/go` module. Root Go tests do not cover it. Python packages support ordinary and async clients, with installation checks from wheels and an exact local Git revision. Update the Python package version when its contents change. The package version and the wire package name are separate concepts.

Client examples should reuse a channel, set deadlines and the receive cap, and handle missing or unknown error details. Example fixture timestamps need adjustment before use against live service data.

## Checks

For service changes:

```sh
make check
```

The target stops at the first failure and runs sequentially, including with `make -j`:

1. Check Go formatting without changing files.
2. Build the executable and validate the full configuration.
3. Validate linter settings and run the linter.
4. Run tests.
5. Run tests with the race detector.

`govet` is included in the standard linter set. `make vet` is available as a focused check. The Makefile installs golangci-lint **v2.13.2** into ignored `bin/` on first use. This needs network access, `curl`, `tar`, and a SHA-256 utility; later runs reuse the binary.

Also run `make check-api` for schema, transport, client, or runtime changes. It checks reproducible generation, compatibility, the nested Go module, Python packaging, and installed clients against local Go servers. Normal CI and release CI run both sets of checks.

Run `make docker-build` and `make docker-verify` for container or lifecycle changes. Run the [capacity profile](operations.md#container-and-capacity-checks) when changing storage, message sizes, concurrency, or memory use.

Runtime upgrades must preserve [gRPC request ownership](architecture.md#grpc-request-ownership). Slow-reader, cancellation, deadline, delayed-dispatch, and shutdown tests check behavior that a successful ordinary RPC does not cover.

For documentation-only changes, verify content, relative links, anchors, examples, and whitespace. Go tests are not required unless the change also affects executable behavior.

## Tests and fixtures

Keep unit tests beside the implementation in `*_test.go`. Test inputs, outputs, state changes, and errors. Use `t.Context()`, controlled clocks, and synchronization. Use `testify/require` for prerequisites and `testify/assert` for independent checks. Do not depend on real exchange access, credentials, arbitrary sleeps, or incidental internal call order.

Integration tests exercise adapters with local HTTP servers and the API with real local gRPC connections. Lifecycle tests also use in-memory connections and controlled time. A restricted environment must allow local listeners for these tests.

[Testdata exchange fixtures](../testdata/exchange/README.md) cover captured calendar boundaries and funding metadata. [HTTP baseline fixtures](../testdata/http-baseline/README.md) are fixed expected responses used by regression tests and transport comparisons. Their frozen HTTP handler is test tooling; it is not linked into the production service.

Keep new benchmark output under ignored `bin/`, not in `docs/`. A benchmark report is not a replacement for a behavior test.

## Releases

The [release workflow](../.github/workflows/release.yml) publishes a Linux/amd64 image to `ghcr.io/<owner>/<repository>`. A version-tag push or published GitHub Release triggers it, including pre-releases. Use a semantic version with an optional `v` prefix. For example, `v1.2.3` publishes image tag `1.2.3`, and `v1.2.3-rc.1` publishes `1.2.3-rc.1`. No `latest` tag is published.

Before registry login, the workflow runs `make check-api` and `make check` on the tagged commit with the pinned Go version and both supported Python versions. Any failure stops publication. Run relevant [container and capacity checks](operations.md#container-and-capacity-checks) before releasing changes that affect those boundaries.

After selecting and checking the commit to release, tag and push it. Replace this example version with the intended version:

```sh
git tag v1.2.3
git push origin v1.2.3
```

Alternatively, publish a GitHub Release for the version tag. Using both triggers builds twice and replaces the same image tag. Use one trigger when a second build is unnecessary.

The workflow uses the repository's `GITHUB_TOKEN` with package write access. An existing GHCR package must allow this repository to write to it. Image publication does not deploy the service; follow [operations](operations.md#restarts-and-updates) for updates and rollback.

[Documentation index](../README.md#documentation)
