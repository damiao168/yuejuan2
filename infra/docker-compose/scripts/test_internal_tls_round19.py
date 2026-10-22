import importlib.util
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SOURCE = Path(__file__).with_name("ensure-internal-tls.py")
SPEC = importlib.util.spec_from_file_location("internal_tls_round19", SOURCE)
TLS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(TLS)
EXAMPLE = SOURCE.parent.parent / ".env.example"


class InternalTlsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.openssl = TLS.openssl_executable()
        cls.fixture = tempfile.TemporaryDirectory()
        cls.fixture_root = Path(cls.fixture.name)
        cls.fixture_env = cls.fixture_root / ".env"
        cls.fixture_env.write_bytes(EXAMPLE.read_bytes())
        with patch.dict(os.environ, {}, clear=True):
            TLS.prepare_tls(cls.fixture_env, cls.fixture_root, cls.openssl, generate_local=True)

    @classmethod
    def tearDownClass(cls):
        cls.fixture.cleanup()

    def prepare_directory(self, directory, certificates=True):
        env = directory / ".env"
        env.write_bytes(EXAMPLE.read_bytes())
        if certificates:
            shutil.copytree(self.fixture_root / "certs", directory / "certs")
        return env

    def test_fresh_default_local_generation_and_repeat_preserve_every_byte(self):
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {}, clear=True):
            directory = Path(temporary)
            env = self.prepare_directory(directory, certificates=False)
            TLS.prepare_tls(env, directory, self.openssl, generate_local=True)
            before = {str(path.relative_to(directory)): path.read_bytes() for path in directory.rglob("*") if path.is_file()}
            TLS.prepare_tls(env, directory, self.openssl, generate_local=True)
            after = {str(path.relative_to(directory)): path.read_bytes() for path in directory.rglob("*") if path.is_file()}
            self.assertEqual(before, after)

    def test_missing_custom_production_or_partial_directories_never_generate(self):
        for change, partial in (("", True), ("EDUGRADE_ENV=production", False), ("EDUGRADE_INTERNAL_CA_DIR=./custom-ca", False)):
            with self.subTest(change=change, partial=partial), tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {}, clear=True):
                directory = Path(temporary)
                env = self.prepare_directory(directory, certificates=False)
                if change:
                    env.write_text(env.read_text(encoding="utf-8") + "\n" + change + "\n", encoding="utf-8")
                if partial:
                    (directory / "certs").mkdir()
                    (directory / "certs" / "keep.key").write_text("existing key", encoding="utf-8")
                with self.assertRaisesRegex(ValueError, "preserved"):
                    TLS.prepare_tls(env, directory, self.openssl, generate_local=True)
                self.assertFalse((directory / "certs/internal-ca/ca.crt").exists())
                if partial:
                    self.assertEqual((directory / "certs/keep.key").read_text(), "existing key")

    def test_key_mismatch_and_untrusted_ca_stop_validation(self):
        for fault in ("key", "ca"):
            with self.subTest(fault=fault), tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {}, clear=True):
                directory = Path(temporary)
                env = self.prepare_directory(directory)
                target = directory / ("certs/grading-agent/server.key" if fault == "key" else "certs/internal-ca/ca.crt")
                target.chmod(0o600)
                replacement = directory / ("certs/math-verifier/server.key" if fault == "key" else "certs/grading-agent/server.crt")
                target.write_bytes(replacement.read_bytes())
                before = target.read_bytes()
                with self.assertRaises((ValueError, ssl.SSLError)):
                    TLS.prepare_tls(env, directory, self.openssl, generate_local=True)
                self.assertEqual(target.read_bytes(), before)

    def test_actual_hostname_http_and_escaping_paths_are_rejected(self):
        for change in ("EDUGRADE_AI_SERVICE_URL=https://wrong-host.invalid:8100", "EDUGRADE_GRADING_AGENT_HEALTH_URL=http://grading-agent:8100/health", "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE=/run/secrets/grading-agent-tls/../private-ca/ca.key"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {}, clear=True):
                directory = Path(temporary)
                env = self.prepare_directory(directory)
                env.write_text(env.read_text(encoding="utf-8") + "\n" + change + "\n", encoding="utf-8")
                with self.assertRaises(ValueError):
                    TLS.prepare_tls(env, directory, self.openssl, generate_local=True)

    def test_expired_certificate_stops_without_rotation(self):
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {}, clear=True):
            directory = Path(temporary)
            env = self.prepare_directory(directory)
            cert = directory / "certs/grading-agent/server.crt"
            cert.chmod(0o600)
            subprocess.run([str(self.openssl), "x509", "-in", str(cert), "-signkey", str(directory / "certs/grading-agent/server.key"), "-days", "-1", "-out", str(cert)], check=True, capture_output=True)
            before = cert.read_bytes()
            with self.assertRaises(ValueError):
                TLS.prepare_tls(env, directory, self.openssl, generate_local=True)
            self.assertEqual(cert.read_bytes(), before)

    def test_compose_host_environment_overrides_are_validated(self):
        with patch.dict(os.environ, {"EDUGRADE_AI_SERVICE_URL": "https://wrong-host.invalid:8100"}, clear=True):
            with self.assertRaisesRegex(ValueError, "TLS validation failed"):
                TLS.prepare_tls(self.fixture_env, self.fixture_root, self.openssl)

    @unittest.skipUnless(os.name == "nt", "Windows entrypoint ordering")
    def test_direct_init_stops_before_docker_on_missing_tls(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            env = self.prepare_directory(directory, certificates=False)
            env.write_text(env.read_text(encoding="utf-8") + "\nEDUGRADE_INTERNAL_CA_DIR=./custom-ca\n", encoding="utf-8")
            compose = directory / "docker-compose.yml"
            compose.write_text("services: {}\n", encoding="utf-8")
            (directory / "docker.cmd").write_text("@echo DOCKER_MUST_NOT_RUN\r\n@exit /b 81\r\n", encoding="ascii")
            environment = {**os.environ, "PATH": str(directory) + os.pathsep + str(Path(sys.executable).parent), "EDUGRADE_TEST_OPENSSL": str(self.openssl)}
            powershell = Path(os.environ["SystemRoot"]) / "System32/WindowsPowerShell/v1.0/powershell.exe"
            result = subprocess.run([str(powershell), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(SOURCE.with_name("init.ps1")), "-ComposeFile", str(compose), "-EnvFile", str(env)], env=environment, text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Internal TLS preparation failed", result.stdout + result.stderr)
            self.assertNotIn("DOCKER_MUST_NOT_RUN", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
