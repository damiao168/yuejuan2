import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("upgrade_config", Path(__file__).with_name("check-upgrade-config.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class UpgradeConfigTest(unittest.TestCase):
    def test_missing_secret_and_host_identity_never_enter_patch(self):
        issues, patch = module.check({}, {"EDUGRADE_POSTGRES_TENANT_RLS": "true", "EDUGRADE_POSTGRES_APP_PASSWORD": "synthetic-secret", "EDUGRADE_INTERNAL_CA_DIR": "./certs"})
        self.assertEqual(len(issues), 3)
        self.assertEqual(patch, {"EDUGRADE_POSTGRES_TENANT_RLS": "true"})

    def test_existing_values_are_preserved_even_if_invalid(self):
        current = {"EDUGRADE_POSTGRES_TENANT_RLS": "false", "EDUGRADE_AI_SERVICE_URL": "http://grading-agent:8100", "EDUGRADE_POSTGRES_APP_PASSWORD": "synthetic-secret"}
        before = current.copy()
        issues, patch = module.check(current, {**current, "EDUGRADE_POSTGRES_TENANT_RLS": "true"})
        self.assertEqual(current, before)
        self.assertEqual(patch, {})
        self.assertEqual(len(issues), 2)

    def test_cli_hides_credentials_and_refuses_patch_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            env = root / "deployment.env"
            example = root / "example.env"
            patch = root / "review.patch"
            env.write_text("EDUGRADE_POSTGRES_APP_PASSWORD=synthetic-current-secret\n", encoding="utf-8")
            example.write_text("EDUGRADE_POSTGRES_APP_PASSWORD=synthetic-example-secret\nEDUGRADE_QDRANT_API_KEY=synthetic-api-key\nEDUGRADE_POSTGRES_TENANT_RLS=true\n", encoding="utf-8")
            command = [sys.executable, str(Path(module.__file__)), "--env-file", str(env), "--example-file", str(example), "--patch-file", str(patch)]
            first = subprocess.run(command, capture_output=True)
            self.assertEqual(first.returncode, 0)
            self.assertNotIn(b"synthetic-", first.stdout)
            self.assertNotIn("synthetic-", patch.read_text(encoding="utf-8"))
            before = patch.read_bytes()
            second = subprocess.run(command, capture_output=True)
            self.assertEqual(second.returncode, 2)
            self.assertEqual(patch.read_bytes(), before)
            self.assertIn("synthetic-current-secret", env.read_text(encoding="utf-8"))

    def test_duplicate_key_is_rejected_without_echoing_value(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "duplicate.env"
            path.write_text("KEY=synthetic-one\nKEY=synthetic-two\n", encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "Duplicate configuration key: KEY"):
                module.read_env(path)


if __name__ == "__main__":
    unittest.main()
