# HTTP response fixtures

Nine fixed responses from the former HTTP API, captured on September 13, 2026. Gzip is storage compression; comparison traffic is uncompressed. The files cover 1 and 20,000 instruments, tickers, and statistics rows, plus 1, 100, and 1,000 candles.

The [HTTP fixture tests](../../scripts/api/httpfixture/main_test.go) compare response bytes against these files. The [transport comparison](../../scripts/api/compare_transports.py) also checks semantic equality with generated Protobuf fixtures before timing. These are test inputs, not benchmark results. Keep them independent of generated output.

`make api-http-baseline` writes new results and response copies to the ignored `bin/api-benchmarks/http-baseline/` directory. It must not overwrite these expected responses. The frozen handler source is documented in the [legacy transport note](../../scripts/api/httpfixture/legacyhttp/README.md).
