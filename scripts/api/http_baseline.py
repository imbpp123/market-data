"""Real TCP HTTP baseline with fixed values; no external exchange access."""
import argparse
import concurrent.futures
import datetime
import gzip
import hashlib
import http.client
import json
import os
import pathlib
import platform
import resource
import subprocess
import sys
import tempfile
import time
import urllib.parse

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "bin/api-benchmarks/http-baseline"


def run_json(command, **kwargs):
    return json.loads(subprocess.check_output([str(part) for part in command], text=True, **kwargs))


def usage():
    value = resource.getrusage(resource.RUSAGE_SELF)
    return {"cpu_seconds": value.ru_utime + value.ru_stime,
            "peak_rss_bytes": value.ru_maxrss * (1 if sys.platform == "darwin" else 1024)}


def python_client(address, path, expected, workers, samples):
    def worker():
        connection = http.client.HTTPConnection(address, timeout=30)
        timings = []
        try:
            for sample in range(-3, samples):
                started = time.perf_counter_ns()
                connection.request("GET", path, headers={"Accept-Encoding": "identity"})
                response = connection.getresponse()
                body = response.read()
                if response.status != 200 or hashlib.sha256(body).hexdigest() != expected:
                    raise RuntimeError("HTTP response differs from fixed fixture")
                if sample >= 0:
                    timings.append(time.perf_counter_ns() - started)
            return timings
        finally:
            connection.close()
    before = usage()
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        times = [value for batch in pool.map(lambda _: worker(), range(workers)) for value in batch]
    return {"latency_ns": times, "before": before, "after": usage(), "allocations": None,
            "allocation_limit": "Python allocation counts are not measured; tracemalloc would change the timed workload."}


def normalized_protobuf(value, kind):
    field = {"instruments": "instruments", "tickers": "tickers", "market-stats": "market_stats", "klines": "klines"}[kind]
    result = []
    for source in value.get(field, []):
        row = dict(source)
        for old, new in (("funding_interval_seconds", "funding_interval"), ("next_funding_in_seconds", "next_funding_in")):
            if old in row:
                row[new] = row.pop(old)
        for key in ("funding_interval", "next_funding_in", "trade_count", "trades_count"):
            if key in row:
                row[key] = int(row[key])
        if kind == "klines":
            row.update({key: value[key] for key in ("exchange", "market", "symbol", "interval")})
        result.append(row)
    return result


def paths():
    result = {}
    for kind in ("instruments", "tickers", "market-stats"):
        result[f"{kind}-1"] = f"/api/v1/{kind}"
        result[f"{kind}-20000"] = f"/api/v1/{kind}?symbol=full"
    start = 1789154400
    for count in (1, 100, 1000):
        iso = lambda seconds: datetime.datetime.fromtimestamp(seconds, datetime.timezone.utc).isoformat().replace("+00:00", "Z")
        query = urllib.parse.urlencode({"exchange": "binance", "market": "spot", "symbol": "S0000USDT", "interval": "1m", "from": iso(start), "to": iso(start + count * 60)})
        result[f"klines-{count}"] = "/api/v1/klines?" + query
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--client", action="store_true")
    parser.add_argument("--address")
    parser.add_argument("--path")
    parser.add_argument("--sha256")
    parser.add_argument("--workers", type=int, default=1)
    parser.add_argument("--samples", type=int, default=20)
    args = parser.parse_args()
    if args.client:
        print(json.dumps(python_client(args.address, args.path, args.sha256, args.workers, args.samples)))
        return
    OUTPUT.mkdir(parents=True, exist_ok=True)
    subprocess.run(["go", "build", "-o", ROOT / "bin/api-http-fixture", "./scripts/api/httpfixture"], cwd=ROOT, check=True)
    with tempfile.TemporaryDirectory(prefix="market-data-baseline-") as directory:
        work = pathlib.Path(directory)
        sizes = run_json(["go", "run", "./cmd/contract-sizes", work], cwd=ROOT / "api/go")
        server = subprocess.Popen([ROOT / "bin/api-http-fixture"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        try:
            address = json.loads(server.stdout.readline())["address"]
            def server_usage():
                server.stdin.write("stats\n")
                server.stdin.flush()
                return json.loads(server.stdout.readline())
            results = []
            fixture_sizes = {}
            for name, path in paths().items():
                connection = http.client.HTTPConnection(address, timeout=30)
                connection.request("GET", path)
                response = connection.getresponse()
                body = response.read()
                connection.close()
                if response.status != 200:
                    raise RuntimeError(f"{name}: HTTP {response.status}")
                kind = name.rsplit("-", 1)[0]
                expected = normalized_protobuf(json.loads((work / (name + ".json")).read_text()), kind)
                if json.loads(body)["data"] != expected:
                    raise RuntimeError(f"{name}: JSON/Protobuf semantic mismatch")
                digest = hashlib.sha256(body).hexdigest()
                # gzip is only output storage. Every measurement is uncompressed.
                (OUTPUT / (name + ".http.json.gz")).write_bytes(gzip.compress(body, mtime=0))
                fixture_sizes[name] = {"http_response_bytes": len(body), "protobuf_response_bytes": sizes[name], "http_sha256": digest,
                                       "protobuf_sha256": hashlib.sha256((work / (name + ".pb")).read_bytes()).hexdigest(),
                                       "rows": len(expected), "semantic_equivalence": True}
                encoding = run_json([ROOT / "bin/api-http-fixture", "-encode", path, "-samples", args.samples])
                if encoding["response_sha256"] != digest:
                    raise RuntimeError("Handler-only response changed")
                results.append({"fixture": name, "boundary": "handler conversion and JSON encoding, no storage or TCP", "client": "go", "workers": 1, **encoding})
                for language in ("go", "python"):
                    for workers in (1, 4):
                        before = server_usage()
                        if language == "go":
                            result = run_json([ROOT / "bin/api-http-fixture", "-url", "http://" + address + path,
                                               "-workers", workers, "-samples", args.samples, "-sha256", digest])
                        else:
                            result = run_json([sys.executable, __file__, "--client", "--address", address, "--path", path,
                                               "--workers", workers, "--samples", args.samples, "--sha256", digest])
                        after = server_usage()
                        result.update({"fixture": name, "boundary": "TCP request through full body read and SHA256 validation", "client": language,
                                       "workers": workers, "server_before": before, "server_after": after,
                                       "request_message_bytes": 0, "request_target_bytes": len(path.encode()), "response_message_bytes": len(body)})
                        results.append(result)
                print(name, fixture_sizes[name], flush=True)
            for row in results:
                ordered = sorted(row["latency_ns"])
                row["latency_percentiles_ns"] = {key: ordered[min(len(ordered)-1, (len(ordered)*percent+99)//100-1)] for key, percent in (("p50",50),("p95",95),("p99",99))}
            source_files = [*sorted((ROOT / "scripts/api/httpfixture").rglob("*.go")), ROOT / "scripts/api/http_baseline.py", ROOT / "go.mod", ROOT / "go.sum",
                            ROOT / "api/go/internal/fixture/server.go", ROOT / "api/proto/marketdata/v1/market_data.proto"]
            report = {"source_revision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
                      "source_status": subprocess.check_output(["git", "status", "--short"], cwd=ROOT, text=True),
                      "source_sha256": {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest() for path in source_files},
                      "command": "make api-http-baseline", "recorded_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                      "environment": {"platform": platform.platform(), "machine": platform.machine(), "processor": platform.processor(), "python": sys.version,
                                      "go": subprocess.check_output(["go", "version"], text=True).strip(), "cpu_count": os.cpu_count()},
                      "protocol": "HTTP/1.1, plaintext local TCP, no compression, one reused connection per worker",
                      "samples_per_worker": args.samples, "warmup_per_worker": 3,
                      "measurement_limits": ["Latency excludes three warmup calls per worker; CPU/allocation/connection totals include them.",
                                             "Connection bytes are counted at net.Conn Read/Write, including HTTP headers/framing; TCP/IP headers and ACK packets are excluded.",
                                             "Client and server are separate processes. RSS is each process high-water mark; server RSS is cumulative across cases.",
                                             "Fixed reader slices exclude application validation, cache/storage and upstream fills. This is a transport baseline, not capacity acceptance.",
                                             "Handler-only measurements include request validation, mapping, decimal conversion and JSON encoding; they do not isolate json.Marshal alone.",
                                             "20 samples per worker are a smoke baseline; p99 is a sample-tail observation, not a stable production estimate.",
                                             "Four-client normal traffic is measured. Overload and partial fills need separate acceptance profiles."],
                      "fixture_sizes": fixture_sizes, "results": results}
            (OUTPUT / "http-baseline.json").write_text(json.dumps(report, indent=2) + "\n")
            (OUTPUT / "message-sizes.json").write_text(json.dumps(fixture_sizes, indent=2) + "\n")
        finally:
            server.stdin.close()
            server.wait(timeout=10)


if __name__ == "__main__":
    main()
