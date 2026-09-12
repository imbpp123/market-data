# Phase 12 evidence

Collected September 12–13, 2026. See the [release audit](../../release-verification-v1.md) for the full acceptance matrix, interpretation, and deployment assumptions.

## Artifacts

- [checks.log](checks.log): final `make check` output, including formatting, build, example configuration, pinned lint, tests, and race.
- [container.log](container.log): final isolated Compose lifecycle verification, including SIGTERM timing and normal exit.
- [docker-build.log](docker-build.log): final digest-pinned image build.
- [load-main.log](load-main.log): final main-profile run and automatic Linux peak-RSS gate (800,000,000 bytes).
- [load-broad.log](load-broad.log) and [containers.json](containers.json): expanded capacity run; Docker records OOM and exit 137. It does not pass the 1 GB capacity limit.
- [load-before-compaction.log](load-before-compaction.log): original main-profile measurement; functional checks passed but peak RSS exceeded the release target. This preceded the explicit memory assertion and compact storage.
- [live-compatibility.json](live-compatibility.json): four separate public current-data GETs from this computer, including Binance Spot FULL; HTTP 200 and required fields present in each response.
- [inner-imports.txt](inner-imports.txt): actual domain/application direct imports.
- [manifest.json](manifest.json): final image metadata, source/binary hashes, tool versions, and evidence hashes. A local image ID is recorded; no registry publication is claimed.

## Reproduction

Run normal checks and package verification:

```sh
make check
make docker-build
make docker-verify
```

For a native-host capacity run:

```sh
make release-load
```

For the measured Linux process envelope, compile a static test executable for the Docker engine architecture. These commands use arm64, as in this run; use amd64 on an amd64 engine:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o bin/release-load-linux ./internal/bootstrap
docker run --name market-data-load-verification \
  --network none --no-healthcheck --read-only --cap-drop ALL \
  --security-opt no-new-privileges \
  --memory 1000000000 --memory-swap 1000000000 \
  --mount "type=bind,src=$PWD/bin/release-load-linux,dst=/release-load,readonly" \
  -e MDS_RELEASE_LOAD=main -e GOMEMLIMIT=700MiB \
  --entrypoint /release-load market-data-service:v1 \
  -test.run '^TestReleaseLoad$' -test.v -test.timeout 15m
docker inspect market-data-load-verification
```

Use a new container name for each run. `MDS_RELEASE_LOAD=broad` selects the all-interval capacity scenario, which may be OOM-killed under this memory limit. Keep it separate from the main run so its peak does not contaminate the main measurement. Do not treat its exit 137 as a passing workload check. No production network or credentials are involved.

## Interpretation

The main run uses 600 series at full 1000-slot occupancy, four HTTP clients, and 20,000 rows in each snapshot type. It fills 600 missing slots and performs 2400 repeated range reads with zero new provider attempts. Final heap, process RSS, observed latency percentiles, retention timings, and shutdown timings are in the raw log. The synthetic provider excludes exchange network/admission pacing from those latency values.

The broad profile aims for 2850 series at 1000 slots each. It exceeds the 1 GB envelope while populating. No read/fill latency or final retained count is claimed for that failed run. Per-series retention does not cap total requested series.

Docker lifecycle verification uses the actual service executable. The capacity run uses the standalone compiled test executable with actual storage, application, and HTTP encoding. These are distinct checks. CPU timing, runtime memory, and RSS are environment-specific observations; they are not an SLA or a universal capacity guarantee.
