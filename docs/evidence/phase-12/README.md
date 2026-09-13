# Phase 12 evidence

Collected September 12–13, 2026. See the [release audit](../../release-verification-v1.md) for the full acceptance matrix, interpretation, and deployment assumptions.

## Artifacts

- [containers.json](containers.json): main and expanded capacity run outcomes; the expanded run records OOM and exit 137. It does not pass the 1 GB capacity limit.
- [live-compatibility.json](live-compatibility.json): four separate public current-data GETs from this computer, including Binance Spot FULL; HTTP 200 and required fields present in each response.
- [inner-imports.txt](inner-imports.txt): actual domain/application direct imports.
- [manifest.json](manifest.json): final image metadata, source/binary hashes, tool versions, and evidence hashes. A local image ID is recorded; no registry publication is claimed.

## Archived logs

Raw logs are retained in Git commit `51573ffedcdc8ce9cac9deb735581bf8bfd99c29`, not in the current tree. Read a log from the repository root:

```sh
git show 51573ffedcdc8ce9cac9deb735581bf8bfd99c29:docs/evidence/phase-12/checks.log
```

Replace `checks.log` with `container.log` for Compose lifecycle verification, `docker-build.log` for image build output, `load-main.log` for the final main capacity run, `load-broad.log` for the failed expanded run, or `load-before-compaction.log` for the earlier measurement above the memory target. The release audit retains the results and limitations.

The manifest is an unchanged historical record. Its evidence hashes describe the original files, including archived logs and the original README, not the current tree. A shallow clone may need older history to read the logs.

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
