"""Verify local Compose packaging without access to exchange networks."""

import json
from pathlib import Path
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ContainerReleaseTest(unittest.TestCase):
    def test_compose_lifecycle(self):
        with tempfile.TemporaryDirectory(prefix="market-data-release-") as directory:
            override = Path(directory) / "compose.yaml"
            override.write_text(
                "services:\n"
                "  market-data-service:\n"
                "    ports: !reset []\n"
                "    volumes:\n"
                f"      - {ROOT / 'bin/release-probe'}:/release-probe:ro\n"
                "networks:\n"
                "  default:\n"
                "    internal: true\n"
            )
            project = Path(directory).name
            compose = ["docker", "compose", "-p", project, "-f", str(ROOT / "compose.yaml"), "-f", str(override)]

            def command(*args):
                result = subprocess.run(args, cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=90)
                if result.returncode:
                    self.fail(result.stdout)
                return result.stdout.strip()

            base = json.loads(command("docker", "compose", "-f", str(ROOT / "compose.yaml"), "config", "--format", "json"))
            ports = base["services"]["market-data-service"]["ports"]
            self.assertEqual({8080, 9090}, {int(port["target"]) for port in ports})
            self.assertTrue(all(port["host_ip"] == "127.0.0.1" for port in ports))
            try:
                command(*compose, "up", "-d", "--wait", "--wait-timeout", "30", "--no-build")
                container = command(*compose, "ps", "-q", "market-data-service")
                info = json.loads(command("docker", "inspect", container))[0]
                self.assertEqual("65532:65532", info["Config"]["User"])
                self.assertTrue(info["HostConfig"]["ReadonlyRootfs"])
                self.assertEqual(1_000_000_000, info["HostConfig"]["Memory"])
                self.assertEqual(1_000_000_000, info["HostConfig"]["MemorySwap"])
                self.assertEqual("SIGTERM", info["Config"]["StopSignal"])
                self.assertEqual(["/market-data-service"], info["Config"]["Entrypoint"])
                self.assertEqual("no", info["HostConfig"]["RestartPolicy"]["Name"])
                self.assertEqual("healthy", info["State"]["Health"]["Status"])
                config = next(m for m in info["Mounts"] if m["Destination"] == "/etc/market-data/config.yaml")
                self.assertFalse(config["RW"])
                self.assertEqual(str(ROOT / "docs/examples/config-v1.yaml"), config["Source"])
                network = next(iter(info["NetworkSettings"]["Networks"]))
                self.assertTrue(json.loads(command("docker", "network", "inspect", network))[0]["Internal"])
                command("docker", "exec", "-e", "MDS_RELEASE_CONTAINER_PROBE=1", container, "/release-probe", "-test.run", "^TestReleaseContainerProbe$", "-test.v")
                command("docker", "exec", container, "/market-data-service", "-config", "/etc/market-data/config.yaml", "-check-config")
                started = time.monotonic()
                command(*compose, "stop")
                elapsed = time.monotonic() - started
                stopped = json.loads(command("docker", "inspect", container))[0]
                self.assertEqual(0, stopped["State"]["ExitCode"])
                self.assertFalse(stopped["State"]["OOMKilled"])
                self.assertLess(elapsed, 35)
                print(json.dumps({"non_root": True, "config_read_only": True, "root_read_only": True, "certificates": True, "health": 200, "ready": 200, "grpc_unready_data": "UNAVAILABLE/data_not_ready", "legacy_http_data": 404, "network": "internal", "stop_seconds": round(elapsed, 3), "exit_code": 0}))
            finally:
                command(*compose, "down")


if __name__ == "__main__":
    unittest.main()
