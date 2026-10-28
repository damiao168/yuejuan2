import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("rotation", Path(__file__).with_name("rotate-local-tls.py"))
rotation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rotation)


class RotationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.env = self.root / ".env"
        self.original = b"EDUGRADE_ENV=local\nEDUGRADE_INTERNAL_CA_DIR=./old-ca\nEDUGRADE_GRADING_AGENT_TLS_CERTS_DIR=./old-agent\nEDUGRADE_MATH_VERIFY_TLS_CERTS_DIR=./old-math\nUNCHANGED=secret-fixture\n"
        self.env.write_bytes(self.original)
        self.calls = []
        self.failures = 0
        self.validator = lambda path: None
        self.environment = patch.dict("os.environ", {}, clear=True)
        self.environment.start()
    def tearDown(self):
        self.environment.stop()
        self.temporary.cleanup()
    def generator(self, root, executable, days):
        root.mkdir(parents=True)
        (root / "kept-private-key").write_text("fixture")
    def runner(self, directory, env_file, arguments):
        self.calls.append(arguments)
        if arguments[0] == "ps":
            return "api-gateway\ngrading-agent\nnginx\n"
        if arguments[0] == "up" and self.failures:
            self.failures -= 1
            raise RuntimeError("health fixture failed")
        return ""
    def rotate(self, recover=False):
        return rotation.rotate(self.env, self.root, Path("unused"), recover=recover, runner=self.runner, generator=self.generator, validator=self.validator)
    def test_healthy_switch_changes_only_tls_directories_and_preserves_keys(self):
        self.assertIn("services healthy", self.rotate())
        self.assertIn(b"UNCHANGED=secret-fixture", self.env.read_bytes())
        self.assertNotIn(b"./old-ca", self.env.read_bytes())
        self.assertTrue(list((self.root / "certs/rotations").glob("*/kept-private-key")))
        self.assertFalse(self.env.with_name(".env.tls-rotation.json").exists())
        self.assertIn("--wait", self.calls[-1])
    def test_failed_health_restores_configuration_and_restarts_old_services(self):
        self.failures = 1
        with self.assertRaisesRegex(RuntimeError, "previous configuration and services restored"):
            self.rotate()
        self.assertEqual(self.env.read_bytes(), self.original)
        self.assertEqual(sum(call[0] == "up" for call in self.calls), 2)
    def test_preflight_failure_never_switches_or_restarts(self):
        def reject(path):
            raise ValueError("invalid fixture certificate")
        self.validator = reject
        with self.assertRaisesRegex(ValueError, "invalid fixture"):
            self.rotate()
        self.assertEqual(self.env.read_bytes(), self.original)
        self.assertFalse(any(call[0] == "up" for call in self.calls))
    def test_concurrent_configuration_edit_is_not_overwritten(self):
        self.validator = lambda path: self.env.write_bytes(self.original + b"OTHER=changed\n")
        with self.assertRaisesRegex(ValueError, "changed during preparation"):
            self.rotate()
        self.assertTrue(self.env.read_bytes().endswith(b"OTHER=changed\n"))
    def test_failed_rollback_keeps_journal_and_can_resume(self):
        self.failures = 2
        with self.assertRaisesRegex(RuntimeError, "recovery failed"):
            self.rotate()
        journal = self.env.with_name(".env.tls-rotation.json")
        self.assertTrue(journal.exists())
        self.assertNotIn("secret-fixture", journal.read_text())
        self.assertIn("restored and services healthy", self.rotate(recover=True))
        self.assertFalse(journal.exists())
    def test_production_and_process_directory_override_are_rejected(self):
        self.env.write_text("EDUGRADE_ENV=production\n")
        with self.assertRaisesRegex(ValueError, "limited to local"):
            self.rotate()
        with patch.dict("os.environ", {rotation.KEYS[0]: "override"}):
            with self.assertRaisesRegex(ValueError, "overrides"):
                self.rotate()
    def test_custom_api_https_is_not_left_trusting_an_old_ca(self):
        self.env.write_bytes(self.original + b"EDUGRADE_INTERNAL_TLS_PORT=8443\n")
        with self.assertRaisesRegex(ValueError, "coordinated PKI"):
            self.rotate()
        self.assertFalse(self.calls)


if __name__ == "__main__":
    unittest.main()
