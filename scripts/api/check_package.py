"""Build a wheel, install wheel and pinned local Git package, test outside source."""
import json
import hashlib
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time
import tomllib
import http.client
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[2]


def run(command, **kwargs):
    subprocess.run([str(value) for value in command], check=True, **kwargs)


def main():
    package_version = tomllib.loads((ROOT / "api/python/pyproject.toml").read_text())["project"]["version"]
    with tempfile.TemporaryDirectory(prefix="market-data-package-") as directory:
        work = pathlib.Path(directory)
        project = work / "repo"
        shutil.copytree(ROOT / "api/python", project / "api/python", ignore=shutil.ignore_patterns("__pycache__", "*.egg-info", "build"))
        run([sys.executable, "-m", "build", "--wheel", "--no-isolation", "--outdir", work / "wheels", project / "api/python"])
        wheel = next((work / "wheels").glob("*.whl"))
        artifacts = ROOT / "bin/api-dist"
        artifacts.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(wheel, artifacts / wheel.name)
        with zipfile.ZipFile(wheel) as archive:
            names = set(archive.namelist())
            required = {"marketdata/__init__.py", "marketdata/py.typed", "marketdata/CLIENT_GUIDE.md", "marketdata/v1/__init__.py", "marketdata/v1/market_data.proto", "marketdata/v1/market_data_pb2.py", "marketdata/v1/market_data_pb2.pyi", "marketdata/v1/market_data_pb2_grpc.py"}
            if not required <= names:
                raise RuntimeError(f"Missing wheel files: {required - names}")
            if any(not (name.startswith("marketdata/") or name.startswith(f"market_data_api-{package_version}.dist-info/")) for name in names):
                raise RuntimeError("Wheel contains unexpected files")
        run(["git", "init", "-q", project])
        run(["git", "-C", project, "add", "api/python"])
        run(["git", "-C", project, "-c", "user.name=Contract Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "Add client fixture"])
        revision = subprocess.check_output(["git", "-C", str(project), "rev-parse", "HEAD"], text=True).strip()
        server_path = work / "contract-fixture"
        example_path = work / "go-example"
        run(["go", "build", "-o", server_path, "./cmd/contract-fixture"], cwd=ROOT / "api/go")
        run(["go", "build", "-o", example_path, "./examples/client"], cwd=ROOT / "api/go")
        application_path = work / "application-fixture"
        application_check = work / "application-check"
        consumer = work / "go-consumer"
        consumer.mkdir()
        shutil.copytree(ROOT / "api/go", project / "api/go")
        shutil.copyfile(ROOT / "api/go/cmd/application-check/main.go", consumer / "main.go")
        shutil.copyfile(ROOT / "api/go/go.sum", consumer / "go.sum")
        module = (ROOT / "api/go/go.mod").read_text().replace("module github.com/imbpp123/market-data/api/go", "module installed-consumer")
        module += f"\nrequire github.com/imbpp123/market-data/api/go v0.0.0\nreplace github.com/imbpp123/market-data/api/go => {project / 'api/go'}\n"
        (consumer / "go.mod").write_text(module)
        run(["go", "build", "-o", application_path, "./scripts/api/grpcfixture"], cwd=ROOT)
        run(["go", "build", "-o", application_check, "."], cwd=consumer)
        module_dir = pathlib.Path(subprocess.check_output([
            "go", "list", "-m", "-f", "{{.Dir}}", "github.com/imbpp123/market-data/api/go",
        ], cwd=consumer, text=True).strip())
        if (module_dir / "CLIENT_GUIDE.md").read_bytes() != (ROOT / "api/CLIENT_GUIDE.md").read_bytes():
            raise RuntimeError("Go consumer guide differs from the source")
        package_help = subprocess.check_output([
            "go", "doc", "github.com/imbpp123/market-data/api/go/marketdata/v1",
        ], cwd=consumer, text=True)
        if "CLIENT_GUIDE.md" not in package_help or "# Connect" not in package_help:
            raise RuntimeError("Go package help is missing client guidance")
        composition_path = work / "composition-fixture"
        run(["go", "test", "-c", "-o", composition_path, "./internal/bootstrap"], cwd=ROOT)
        manifest_path = work / "composition.json"
        composition = subprocess.Popen([composition_path, "-test.run", "^TestInstalledCompositionFixture$", "-test.timeout", "10m"], env=dict(os.environ, MDS_COMPOSITION_MANIFEST=str(manifest_path)))
        application = subprocess.Popen([application_path], stdout=subprocess.PIPE, text=True)
        server = subprocess.Popen([server_path], stdout=subprocess.PIPE, text=True)
        try:
            application_address = application.stdout.readline().strip()
            if not application_address:
                raise RuntimeError("Application fixture failed to start")
            deadline = time.monotonic() + 30
            while not manifest_path.exists():
                if composition.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("Final composition failed to start")
                time.sleep(0.02)
            composition_manifest = json.loads(manifest_path.read_text())
            connection = http.client.HTTPConnection(composition_manifest["operations"], timeout=5)
            for route in ("/health", "/ready"):
                connection.request("GET", route)
                response = connection.getresponse()
                assert response.status == 200, route
                response.read()
            connection.close()
            address = server.stdout.readline().strip()
            if not address:
                raise RuntimeError("Contract fixture failed to start")
            run([example_path, address])
            clean_bin = work / "path"
            clean_bin.mkdir()
            (clean_bin / "git").symlink_to(shutil.which("git"))
            test_dir = work / "consumer"
            test_dir.mkdir()
            composition_env = dict(os.environ, API_CANDLE_FROM=str(composition_manifest["from"]), API_CANDLE_TO=str(composition_manifest["to"]))
            with (test_dir / "composition-go.json").open("w") as output:
                run([application_check, composition_manifest["address"]], stdout=output, env=composition_env)
            with (test_dir / "application-go.json").open("w") as output:
                run([application_check, application_address], stdout=output)
            for source in [ROOT / "scripts/api/test_contract.py", ROOT / "scripts/api/test_application.py", *sorted((ROOT / "api/examples").glob("*.py"))]:
                shutil.copyfile(source, test_dir / source.name)
            versions = os.environ.get("API_PYTHONS", "python3.13 python3.14").split()
            for version in versions:
                python = shutil.which(version)
                if python is None:
                    raise RuntimeError(f"Required Python boundary interpreter missing: {version}; set API_PYTHONS in a CI matrix")
                for mode in ("wheel", "git"):
                    environment = work / f"{version}-{mode}"
                    run([python, "-m", "venv", environment])
                    executable = environment / "bin/python"
                    env = dict(os.environ, PATH=str(clean_bin), PYTHONNOUSERSITE="1", PYTHONDONTWRITEBYTECODE="1", API_FIXTURE_ADDRESS=address, API_APPLICATION_ADDRESS=application_address,
                               API_PACKAGE_VERSION=package_version,
                               API_CLIENT_GUIDE_SHA256=hashlib.sha256((ROOT / "api/CLIENT_GUIDE.md").read_bytes()).hexdigest(),
                               API_SCHEMA_SHA256=hashlib.sha256((ROOT / "api/proto/marketdata/v1/market_data.proto").read_bytes()).hexdigest())
                    env.pop("PYTHONPATH", None)
                    target = str(wheel) if mode == "wheel" else f"market-data-api @ git+{project.as_uri()}@{revision}#subdirectory=api/python"
                    run([executable, "-m", "pip", "install", "--disable-pip-version-check", target], cwd=test_dir, env=env)
                    run([executable, "-m", "pip", "check"], cwd=test_dir, env=env)
                    if mode == "git":
                        code = "import importlib.metadata,json; d=importlib.metadata.distribution('market-data-api'); print(d.read_text('direct_url.json'))"
                        direct = json.loads(subprocess.check_output([executable, "-c", code], text=True, cwd=test_dir, env=env))
                        if direct["vcs_info"]["commit_id"] != revision or direct["subdirectory"] != "api/python":
                            raise RuntimeError("Installed Git identity differs")
                    run([executable, "test_contract.py", "-v"], cwd=test_dir, env=env)
                    run([executable, "test_application.py", "-v"], cwd=test_dir, env=env)
                    final_env = dict(env, API_APPLICATION_ADDRESS=composition_manifest["address"], API_CANDLE_FROM=str(composition_manifest["from"]), API_CANDLE_TO=str(composition_manifest["to"]), API_EXPECTED_FILE="composition-go.json")
                    run([executable, "test_application.py", "-v"], cwd=test_dir, env=final_env)
                    for example in ("client.py", "client_async.py"):
                        run([executable, example, address], cwd=test_dir, env=env)
        finally:
            server.terminate()
            server.wait(timeout=10)
            application.terminate()
            application.wait(timeout=10)
            composition.terminate()
            if composition.wait(timeout=40) != 0:
                raise RuntimeError("Final composition did not shut down cleanly")
        print("Wheel and pinned local Git installations passed:", ", ".join(versions))


if __name__ == "__main__":
    main()
