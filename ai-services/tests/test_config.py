import os
import unittest
from unittest.mock import patch

from grading_agent.config import Settings

SERVICE_TOKEN = "test-service-token-with-at-least-32-characters"
INSECURE_HTTP_ENV = "EDUGRADE_GRADING_AGENT_ALLOW_INSECURE_LOOPBACK_HTTP"


class SettingsTests(unittest.TestCase):
    @patch.dict(
        os.environ,
        {
            "EDUGRADE_GRADING_AGENT_TOKEN": "test-service-token-with-at-least-32-characters",
            "EDUGRADE_GRADING_MODEL_TEMPERATURE": "NaN",
            "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/run/secrets/tls/server.crt",
            "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE": "/run/secrets/tls/server.key",
        },
        clear=True,
    )
    def test_non_finite_float_configuration_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "TEMPERATURE"):
            Settings.from_env()

    @patch.dict(
        os.environ,
        {"EDUGRADE_GRADING_AGENT_TOKEN": " " * 32},
        clear=True,
    )
    def test_whitespace_service_token_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "TOKEN"):
            Settings.from_env()

    @patch.dict(
        os.environ,
        {
            "EDUGRADE_GRADING_AGENT_TOKEN": "test-service-token-with-at-least-32-characters",
            "EDUGRADE_GRADING_MODEL_BASE_URL": "not-a-url",
            "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/run/secrets/tls/server.crt",
            "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE": "/run/secrets/tls/server.key",
        },
        clear=True,
    )
    def test_invalid_model_url_is_rejected_at_startup(self):
        with self.assertRaisesRegex(ValueError, "MODEL_BASE_URL"):
            Settings.from_env()

    @patch.dict(
        os.environ,
        {
            "EDUGRADE_ENV": "production",
            "EDUGRADE_GRADING_AGENT_TOKEN": "test-service-token-with-at-least-32-characters",
            "EDUGRADE_GRADING_MODEL_BASE_URL": "http://model.internal:8087/v1",
            "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/run/secrets/tls/server.crt",
            "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE": "/run/secrets/tls/server.key",
        },
        clear=True,
    )
    def test_production_rejects_plaintext_remote_model(self):
        with self.assertRaisesRegex(ValueError, "must use https"):
            Settings.from_env()

    @patch.dict(
        os.environ,
        {
            "EDUGRADE_ENV": "production",
            "EDUGRADE_GRADING_AGENT_TOKEN": "test-service-token-with-at-least-32-characters",
            "EDUGRADE_GRADING_MODEL_BASE_URL": "https://model.internal/v1",
        },
        clear=True,
    )
    def test_production_requires_server_tls_certificate(self):
        with self.assertRaisesRegex(ValueError, "TLS certificate"):
            Settings.from_env()

    @patch.dict(
        os.environ,
        {
            "EDUGRADE_GRADING_AGENT_TOKEN": "test-service-token-with-at-least-32-characters",
            "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/run/secrets/tls/server.crt",
        },
        clear=True,
    )
    def test_tls_certificate_requires_its_matching_private_key(self):
        with self.assertRaisesRegex(ValueError, "configured together"):
            Settings.from_env()

    @patch.dict(
        os.environ,
        {
            "EDUGRADE_GRADING_AGENT_TOKEN": "test-service-token-with-at-least-32-characters",
            "EDUGRADE_GRADING_ADAPTER_TYPE": "dashscope_native",
            "EDUGRADE_GRADING_MODEL_BASE_URL": "https://dashscope.aliyuncs.com/api/v1",
            "EDUGRADE_GRADING_MODEL_API_KEY": "synthetic-key-with-16-characters",
            "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/run/secrets/tls/server.crt",
            "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE": "/run/secrets/tls/server.key",
        },
        clear=True,
    )
    def test_dashscope_native_accepts_only_the_native_https_endpoint(self):
        current = Settings.from_env()
        self.assertEqual(current.adapter_type, "dashscope_native")

    @patch.dict(
        os.environ,
        {
            "EDUGRADE_GRADING_AGENT_TOKEN": "test-service-token-with-at-least-32-characters",
            "EDUGRADE_GRADING_ADAPTER_TYPE": "dashscope_native",
            "EDUGRADE_GRADING_MODEL_BASE_URL": "https://example.com/api/v1",
            "EDUGRADE_GRADING_MODEL_API_KEY": "synthetic-key-with-16-characters",
            "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/run/secrets/tls/server.crt",
            "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE": "/run/secrets/tls/server.key",
        },
        clear=True,
    )
    def test_dashscope_native_rejects_compatible_or_proxy_hosts(self):
        with self.assertRaisesRegex(ValueError, "dashscope_native requires"):
            Settings.from_env()

    def test_default_environment_requires_tls_even_on_loopback(self):
        self.assertEqual(Settings().environment, "production")
        with patch.dict(
            os.environ,
            {
                "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                "EDUGRADE_GRADING_AGENT_HOST": "127.0.0.1",
                INSECURE_HTTP_ENV: "true",
            },
            clear=True,
        ), self.assertRaisesRegex(ValueError, "TLS certificate"):
            Settings.from_env()

    def test_plaintext_requires_explicit_development_environment(self):
        for environment in ("", "production", "staging", "unknown", "testing"):
            with self.subTest(environment=environment), patch.dict(
                os.environ,
                {
                    "EDUGRADE_ENV": environment,
                    "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                    "EDUGRADE_GRADING_AGENT_HOST": "127.0.0.1",
                    INSECURE_HTTP_ENV: "true",
                },
                clear=True,
            ), self.assertRaisesRegex(ValueError, "TLS certificate"):
                Settings.from_env()

    def test_development_plaintext_requires_explicit_opt_in(self):
        for environment in ("local", "development", "dev", "test"):
            for opt_in in (None, "false"):
                values = {
                    "EDUGRADE_ENV": environment,
                    "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                    "EDUGRADE_GRADING_AGENT_HOST": "127.0.0.1",
                }
                if opt_in is not None:
                    values[INSECURE_HTTP_ENV] = opt_in
                with self.subTest(environment=environment, opt_in=opt_in), patch.dict(
                    os.environ, values, clear=True
                ), self.assertRaisesRegex(ValueError, "TLS certificate"):
                    Settings.from_env()

    def test_explicit_development_http_accepts_only_literal_ipv4_loopback(self):
        for environment in ("local", "development", "dev", "test"):
            for host in ("127.0.0.1", "127.1.2.3"):
                with self.subTest(environment=environment, host=host), patch.dict(
                    os.environ,
                    {
                        "EDUGRADE_ENV": environment,
                        "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                        "EDUGRADE_GRADING_AGENT_HOST": host,
                        INSECURE_HTTP_ENV: "true",
                    },
                    clear=True,
                ):
                    current = Settings.from_env()
                    self.assertTrue(current.allow_insecure_loopback_http)
                    self.assertEqual(current.host, host)
                    self.assertEqual(current.tls_cert_file, "")

    def test_development_http_rejects_wildcard_dns_remote_and_ipv6_hosts(self):
        for host in (
            "0.0.0.0", "192.0.2.1", "8.8.8.8", "localhost", "grading-agent",
            "127.1", "127.000.0.1", "127.0.0.1.example", "::1", "::ffff:127.0.0.1", "invalid",
        ):
            with self.subTest(host=host), patch.dict(
                os.environ,
                {
                    "EDUGRADE_ENV": "development",
                    "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                    "EDUGRADE_GRADING_AGENT_HOST": host,
                    INSECURE_HTTP_ENV: "true",
                },
                clear=True,
            ), self.assertRaisesRegex(ValueError, "TLS certificate"):
                Settings.from_env()

    def test_plaintext_opt_in_environment_value_must_be_canonical_boolean(self):
        for raw in ("", "TRUE", "False", "1", "0", "yes", " true ", " false ", "null"):
            with self.subTest(raw=raw), patch.dict(
                os.environ,
                {
                    "EDUGRADE_ENV": "test",
                    "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                    "EDUGRADE_GRADING_AGENT_HOST": "127.0.0.1",
                    INSECURE_HTTP_ENV: raw,
                },
                clear=True,
            ), self.assertRaisesRegex(ValueError, "must be true or false"):
                Settings.from_env()

    def test_complete_tls_configuration_accepts_all_environment_classes(self):
        for environment in (None, "", "production", "staging", "unknown", "local", "dev", "test"):
            values = {
                "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/run/secrets/tls/server.crt",
                "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE": "/run/secrets/tls/server.key",
                INSECURE_HTTP_ENV: "false",
            }
            if environment is not None:
                values["EDUGRADE_ENV"] = environment
            with self.subTest(environment=environment), patch.dict(os.environ, values, clear=True):
                current = Settings.from_env()
                self.assertEqual(current.environment, "production" if environment is None else environment)
                self.assertFalse(current.allow_insecure_loopback_http)
                self.assertEqual(current.tls_key_file, "/run/secrets/tls/server.key")

    def test_partial_tls_never_falls_back_to_opted_in_development_http(self):
        for configured_path in ("TLS_CERT_FILE", "TLS_KEY_FILE"):
            with self.subTest(configured_path=configured_path), patch.dict(
                os.environ,
                {
                    "EDUGRADE_ENV": "dev",
                    "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                    "EDUGRADE_GRADING_AGENT_HOST": "127.0.0.1",
                    INSECURE_HTTP_ENV: "true",
                    f"EDUGRADE_GRADING_AGENT_{configured_path}": "/run/secrets/tls/server.pem",
                },
                clear=True,
            ), self.assertRaisesRegex(ValueError, "configured together"):
                Settings.from_env()

    def test_empty_listener_host_is_rejected_before_tls_selection(self):
        with patch.dict(
            os.environ,
            {
                "EDUGRADE_ENV": "test",
                "EDUGRADE_GRADING_AGENT_TOKEN": SERVICE_TOKEN,
                "EDUGRADE_GRADING_AGENT_HOST": "",
                INSECURE_HTTP_ENV: "true",
            },
            clear=True,
        ), self.assertRaisesRegex(ValueError, "HOST"):
            Settings.from_env()


if __name__ == "__main__":
    unittest.main()
