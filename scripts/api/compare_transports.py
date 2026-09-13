"""Compare frozen HTTP and current gRPC on identical local TCP fixtures."""
import argparse
import asyncio
import concurrent.futures
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
import time

from http_baseline import paths, normalized_protobuf

ROOT = pathlib.Path(__file__).resolve().parents[2]


def usage():
    value = resource.getrusage(resource.RUSAGE_SELF)
    return {"cpu_seconds": value.ru_utime + value.ru_stime,
            "peak_rss_bytes": value.ru_maxrss * (1 if sys.platform == "darwin" else 1024)}


def rpc_call(name):
    from google.protobuf.timestamp_pb2 import Timestamp
    from marketdata.v1 import market_data_pb2 as pb
    kind, count = name.rsplit("-", 1)
    options = {"symbol": "full"} if count == "20000" else {}
    if kind == "klines":
        return "GetKlines", pb.GetKlinesRequest(exchange="binance", market="spot", symbol="S0000USDT", interval="1m", **{"from": Timestamp(seconds=1789154400), "to": Timestamp(seconds=1789154400 + int(count)*60)})
    method, request = {"instruments": ("ListInstruments", pb.ListInstrumentsRequest), "tickers": ("ListTickers", pb.ListTickersRequest), "market-stats": ("ListMarketStats", pb.ListMarketStatsRequest)}[kind]
    return method, request(**options)


def python_client(args):
    import grpc
    from marketdata.v1 import market_data_pb2_grpc as rpc
    method, request = rpc_call(args.fixture)
    def worker():
        channel = grpc.insecure_channel(args.address, options=[("grpc.max_receive_message_length", 16 << 20)])
        call = getattr(rpc.MarketDataServiceStub(channel), method)
        times = []
        try:
            for sample in range(-3, args.samples):
                started = time.perf_counter_ns()
                response = call(request, timeout=30)
                if hashlib.sha256(response.SerializeToString(deterministic=True)).hexdigest() != args.sha256:
                    raise RuntimeError("gRPC response differs from fixture")
                if sample >= 0:
                    times.append(time.perf_counter_ns() - started)
        finally:
            channel.close()
        return times
    before = usage()
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        times = [t for batch in pool.map(lambda _: worker(), range(args.workers)) for t in batch]
    return {"latency_ns": times, "before": before, "after": usage(), "request_message_bytes": request.ByteSize()}


async def async_clients(args):
    async def worker():
        times = []
        if args.protocol == "grpc":
            import grpc
            from marketdata.v1 import market_data_pb2_grpc as rpc
            method, request = rpc_call(args.fixture)
            async with grpc.aio.insecure_channel(args.address, options=[("grpc.max_receive_message_length", 16 << 20)]) as channel:
                call = getattr(rpc.MarketDataServiceStub(channel), method)
                for sample in range(-3, args.samples):
                    started = time.perf_counter_ns()
                    response = await call(request, timeout=30)
                    if hashlib.sha256(response.SerializeToString(deterministic=True)).hexdigest() != args.sha256:
                        raise RuntimeError("Async response differs from fixture")
                    if sample >= 0:
                        times.append(time.perf_counter_ns() - started)
        else:
            host, port = args.address.rsplit(":", 1)
            reader, writer = await asyncio.open_connection(host, int(port))
            try:
                for sample in range(-3, args.samples):
                    started = time.perf_counter_ns()
                    writer.write(f"GET {args.path} HTTP/1.1\r\nHost: {args.address}\r\nAccept-Encoding: identity\r\n\r\n".encode())
                    await writer.drain()
                    status = await reader.readline()
                    if not status.startswith(b"HTTP/1.1 200 "):
                        raise RuntimeError(status)
                    headers = {}
                    while (line := await reader.readline()) != b"\r\n":
                        if not line:
                            raise RuntimeError("Truncated HTTP headers")
                        name, value = line.decode().split(":", 1)
                        headers[name.lower()] = value.strip()
                    if "content-length" in headers:
                        body = await reader.readexactly(int(headers["content-length"]))
                    else:
                        chunks = []
                        while True:
                            length = int((await reader.readline()).split(b";", 1)[0], 16)
                            if not length:
                                await reader.readline()
                                break
                            chunks.append(await reader.readexactly(length))
                            await reader.readexactly(2)
                        body = b"".join(chunks)
                    if hashlib.sha256(body).hexdigest() != args.sha256:
                        raise RuntimeError("Async HTTP response differs")
                    if sample >= 0:
                        times.append(time.perf_counter_ns() - started)
            finally:
                writer.close()
                await writer.wait_closed()
        return times
    return [value for batch in await asyncio.wait_for(asyncio.gather(*(worker() for _ in range(args.workers))), timeout=300) for value in batch]


def run_json(command, **kwargs):
    return json.loads(subprocess.check_output([str(x) for x in command], text=True, **kwargs))


def summarize(value):
    times = sorted(value.pop("latency_ns"))
    if not times or any(t < 0 for t in times):
        raise ValueError("Latency samples must be nonempty and nonnegative")
    value["raw_latency_ns"] = times
    value["samples"] = len(times)
    value["latency_ns"] = {"p50": times[len(times)//2], "p95": times[min(len(times)-1, len(times)*95//100)], "p99": times[min(len(times)-1, len(times)*99//100)]}
    return value


def connection_delta(before, after, minimum_response_bytes):
    result = {key: after[key] - before[key] for key in ("connection_read_bytes", "connection_written_bytes")}
    if any(value < 0 for value in result.values()) or result["connection_written_bytes"] < minimum_response_bytes:
        raise ValueError("Invalid connection byte counts")
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--client", choices=["python", "async"])
    parser.add_argument("--protocol", choices=["http", "grpc"])
    parser.add_argument("--address")
    parser.add_argument("--path")
    parser.add_argument("--fixture")
    parser.add_argument("--sha256")
    parser.add_argument("--workers", type=int, default=4)
    parser.add_argument("--samples", type=int, default=20)
    parser.add_argument("--baseline", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    parser.add_argument("--grpc-binary", type=pathlib.Path, default=ROOT / "bin/api-comparison")
    parser.add_argument("--fixtures", type=pathlib.Path)
    args = parser.parse_args()
    if args.client:
        if args.client == "async":
            before = usage()
            times = asyncio.run(async_clients(args))
            value = {"latency_ns": times, "before": before, "after": usage()}
        elif args.protocol == "grpc":
            value = python_client(args)
        else:
            from http_baseline import python_client as old_python_client
            value = old_python_client(args.address, args.path, args.sha256, args.workers, args.samples)
        print(json.dumps(value))
        return
    if args.baseline is None or args.output is None:
        parser.error("--baseline and --output are required")
    args.output.mkdir(parents=True, exist_ok=True)
    fixed = args.fixtures or ROOT / "bin/phase04-fixtures"
    if args.fixtures is None:
        fixed.mkdir(exist_ok=True)
        run_json(["go", "run", "./cmd/contract-sizes", fixed.resolve()], cwd=ROOT / "api/go")
    results = []
    identity = []
    for protocol, binary, flags in [("http", args.baseline, []), ("grpc", args.grpc_binary, ["-grpc"])]:
        server = subprocess.Popen([str(binary), *flags], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        try:
            address = json.loads(server.stdout.readline())["address"]
            def server_usage(command="stats"):
                server.stdin.write(command+"\n")
                server.stdin.flush()
                return json.loads(server.stdout.readline())
            for name, path in paths().items():
                kind = name.rsplit("-", 1)[0]
                old = gzip.decompress((ROOT / f"testdata/http-baseline/{name}.http.json.gz").read_bytes())
                normalized = normalized_protobuf(json.loads((fixed / f"{name}.json").read_text()), kind)
                if json.loads(old)["data"] != normalized:
                    raise RuntimeError(f"Semantic mismatch before timing: {name}")
                if protocol == "http":
                    connection = http.client.HTTPConnection(address, timeout=30)
                    connection.request("GET", path, headers={"Accept-Encoding": "identity"})
                    response = connection.getresponse()
                    actual = response.read()
                    connection.close()
                    if response.status != 200 or actual != old:
                        raise RuntimeError(f"Frozen HTTP mismatch: {name}")
                    body = old
                else:
                    import grpc
                    from marketdata.v1 import market_data_pb2_grpc as rpc
                    method, request = rpc_call(name)
                    with grpc.insecure_channel(address, options=[("grpc.max_receive_message_length", 16 << 20)]) as channel:
                        response = getattr(rpc.MarketDataServiceStub(channel), method)(request, timeout=30)
                    body = response.SerializeToString(deterministic=True)
                    if body != (fixed / f"{name}.pb").read_bytes():
                        raise RuntimeError(f"Actual gRPC response mismatch: {name}")
                expected = hashlib.sha256(body).hexdigest()
                identity.append({"protocol": protocol, "fixture": name, "response_message_bytes": len(body), "sha256": expected, "semantic_identity": True})
                for client in ("go", "python", "async"):
                    for workers in (1, 4):
                        before = server_usage()
                        if client == "go":
                            command = [binary, *flags, "-url", ("http://"+address+path) if protocol == "http" else address, "-workers", workers, "-samples", args.samples, "-sha256", expected]
                            if protocol == "grpc":
                                command += ["-fixture", name]
                        else:
                            command = [sys.executable, __file__, "--client", client, "--protocol", protocol, "--address", address, "--path", path, "--fixture", name, "--sha256", expected, "--workers", workers, "--samples", args.samples]
                        result = run_json(command)
                        after = server_usage()
                        if len(result["latency_ns"]) != args.samples * workers:
                            raise RuntimeError("Wrong measured sample count")
                        traffic = connection_delta(before, after, len(body)*(args.samples+3)*workers)
                        retained = server_usage("gc")
                        results.append({"protocol": protocol, "fixture": name, "client": client, "workers": workers, "client_result": summarize(result), "server_before": before, "server_after": after, "server_retained_after_gc": retained, "connection_bytes": traffic, "request_message_bytes": rpc_call(name)[1].ByteSize() if protocol == "grpc" else 0, "response_message_bytes": len(body), "request_target_bytes": len(path.encode()) if protocol == "http" else None})
                        (args.output / "comparison.json").write_text(json.dumps({"environment": {"platform": platform.platform(), "machine": platform.machine(), "python": sys.version, "cpu_count": os.cpu_count()}, "samples_per_worker": args.samples, "warmup_per_worker": 3, "identity": identity, "results": results}, indent=2)+"\n")
                        print(protocol, name, client, workers, flush=True)
                command = [binary, "-samples", args.samples]
                if protocol == "http":
                    command += ["-encode", path]
                else:
                    command += ["-grpc", "-fixture", name, "-proto-file", fixed / f"{name}.pb"]
                value = summarize(run_json(command))
                (args.output / f"{protocol}-{name}-encoding.json").write_text(json.dumps(value, indent=2)+"\n")
        finally:
            server.stdin.close()
            if server.wait(timeout=40) != 0:
                raise RuntimeError("Measurement server failed")
    print("All fixture identities and TCP comparison cases passed")


if __name__ == "__main__":
    main()
