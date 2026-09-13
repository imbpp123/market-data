"""Build a wheel, install wheel and pinned local Git package, test outside source."""
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[2]


def run(command, **kwargs):
    subprocess.run([str(value) for value in command], check=True, **kwargs)


def main():
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
            required = {"marketdata/__init__.py", "marketdata/py.typed", "marketdata/v1/__init__.py", "marketdata/v1/market_data_pb2.py", "marketdata/v1/market_data_pb2.pyi", "marketdata/v1/market_data_pb2_grpc.py"}
            if not required <= names:
                raise RuntimeError(f"Missing wheel files: {required - names}")
            if any(not (name.startswith("marketdata/") or name.startswith("market_data_api-0.1.0.dist-info/")) for name in names):
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
        run(["go", "build", "-o", application_path, "./scripts/api/grpcfixture"], cwd=ROOT)
        run(["go", "build", "-o", application_check, "./cmd/application-check"], cwd=ROOT / "api/go")
        application = subprocess.Popen([application_path], stdout=subprocess.PIPE, text=True)
        server = subprocess.Popen([server_path], stdout=subprocess.PIPE, text=True)
        try:
            application_address = application.stdout.readline().strip()
            if not application_address:
                raise RuntimeError("Application fixture failed to start")
            address = server.stdout.readline().strip()
            if not address:
                raise RuntimeError("Contract fixture failed to start")
            run([example_path, address])
            clean_bin = work / "path"
            clean_bin.mkdir()
            (clean_bin / "git").symlink_to(shutil.which("git"))
            test_dir = work / "consumer"
            test_dir.mkdir()
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
                    env = dict(os.environ, PATH=str(clean_bin), PYTHONNOUSERSITE="1", PYTHONDONTWRITEBYTECODE="1", API_FIXTURE_ADDRESS=address, API_APPLICATION_ADDRESS=application_address)
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
                    for example in ("client.py", "client_async.py"):
                        run([executable, example, address], cwd=test_dir, env=env)
        finally:
            server.terminate()
            server.wait(timeout=10)
            application.terminate()
            application.wait(timeout=10)
        print("Wheel and pinned local Git installations passed:", ", ".join(versions))


if __name__ == "__main__":
    main()
