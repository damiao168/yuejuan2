import ssl
import unittest
from dataclasses import replace
from unittest.mock import patch

from grading_agent.config import Settings
from grading_agent.server import serve


class ServerTLSTests(unittest.TestCase):
    def setUp(self):
        self.settings = Settings(
            service_token="test-service-token-with-at-least-32-characters",
            tls_cert_file="server.crt",
            tls_key_file="server.key",
        )

    @patch("grading_agent.server.GradingAgentApplication")
    @patch("grading_agent.server.GradingAgentHTTPServer")
    @patch("grading_agent.server.ssl.SSLContext")
    def test_tls_is_wrapped_before_serving_and_closed_on_exit(self, context_type, server_type, _app_type):
        context = context_type.return_value
        server = server_type.return_value
        original_socket = server.socket

        serve(self.settings)

        context_type.assert_called_once_with(ssl.PROTOCOL_TLS_SERVER)
        self.assertEqual(context.minimum_version, ssl.TLSVersion.TLSv1_2)
        context.load_cert_chain.assert_called_once_with("server.crt", "server.key")
        context.wrap_socket.assert_called_once_with(original_socket, server_side=True)
        self.assertIs(server.socket, context.wrap_socket.return_value)
        server.serve_forever.assert_called_once_with()
        server.server_close.assert_called_once_with()

    @patch("grading_agent.server.GradingAgentApplication")
    @patch("grading_agent.server.GradingAgentHTTPServer")
    @patch("grading_agent.server.ssl.SSLContext")
    def test_invalid_certificate_closes_server_without_serving(self, context_type, server_type, _app_type):
        context_type.return_value.load_cert_chain.side_effect = OSError("invalid certificate")

        with self.assertRaisesRegex(OSError, "invalid certificate"):
            serve(self.settings)

        server_type.return_value.serve_forever.assert_not_called()
        server_type.return_value.server_close.assert_called_once_with()

    @patch("grading_agent.server.GradingAgentApplication")
    @patch("grading_agent.server.GradingAgentHTTPServer")
    @patch("grading_agent.server.ssl.SSLContext")
    def test_socket_wrap_failure_closes_server_without_serving(self, context_type, server_type, _app_type):
        context_type.return_value.wrap_socket.side_effect = OSError("socket wrap failed")

        with self.assertRaisesRegex(OSError, "socket wrap failed"):
            serve(self.settings)

        server_type.return_value.serve_forever.assert_not_called()
        server_type.return_value.server_close.assert_called_once_with()

    def assert_rejected_before_application_or_bind(self, settings, message="TLS certificate"):
        with (
            patch("grading_agent.server.GradingAgentApplication") as application_type,
            patch("grading_agent.server.GradingAgentHTTPServer") as server_type,
            patch("grading_agent.server.ssl.SSLContext") as context_type,
        ):
            with self.assertRaisesRegex(ValueError, message):
                serve(settings)
            application_type.assert_not_called()
            server_type.assert_not_called()
            context_type.assert_not_called()

    def test_direct_settings_rejects_plaintext_outside_explicit_development(self):
        for environment in ("production", "staging", "unknown", "", "testing"):
            for opt_in in (False, True):
                with self.subTest(environment=environment, opt_in=opt_in):
                    self.assert_rejected_before_application_or_bind(
                        replace(
                            self.settings, environment=environment, host="127.0.0.1",
                            tls_cert_file="", tls_key_file="", allow_insecure_loopback_http=opt_in,
                        )
                    )

    def test_direct_settings_rejects_development_plaintext_without_opt_in(self):
        for environment in ("local", "development", "dev", "test"):
            with self.subTest(environment=environment):
                self.assert_rejected_before_application_or_bind(
                    replace(
                        self.settings, environment=environment, host="127.0.0.1",
                        tls_cert_file="", tls_key_file="",
                    )
                )

    def test_direct_settings_rejects_non_boolean_opt_in(self):
        for opt_in in ("true", "false", "", 1, 0, None):
            for certificate_configured in (False, True):
                with self.subTest(opt_in=opt_in, certificate_configured=certificate_configured):
                    self.assert_rejected_before_application_or_bind(
                        replace(
                            self.settings, environment="test", host="127.0.0.1",
                            tls_cert_file="server.crt" if certificate_configured else "",
                            tls_key_file="server.key" if certificate_configured else "",
                            allow_insecure_loopback_http=opt_in,
                        ),
                        "must be a boolean",
                    )

    def test_direct_settings_rejects_wildcard_dns_remote_ipv6_and_invalid_hosts(self):
        for host in (
            "0.0.0.0", "192.0.2.1", "8.8.8.8", "localhost", "grading-agent",
            "127.1", "127.000.0.1", "127.0.0.1.example", "::1", "::ffff:127.0.0.1", "invalid",
        ):
            with self.subTest(host=host):
                self.assert_rejected_before_application_or_bind(
                    replace(
                        self.settings, environment="test", host=host, tls_cert_file="", tls_key_file="",
                        allow_insecure_loopback_http=True,
                    )
                )

    def test_direct_settings_rejects_empty_or_non_string_hosts(self):
        for host in ("", 2130706433, None):
            with self.subTest(host=host):
                self.assert_rejected_before_application_or_bind(
                    replace(
                        self.settings, environment="test", host=host, tls_cert_file="", tls_key_file="",
                        allow_insecure_loopback_http=True,
                    ),
                    "HOST",
                )

    def test_direct_settings_rejects_missing_or_whitespace_credentials_before_bind(self):
        for service_token in ("", "too-short", " " * 32, "test-service-token-with whitespace-characters"):
            with self.subTest(service_token=service_token):
                self.assert_rejected_before_application_or_bind(
                    replace(self.settings, service_token=service_token), "TOKEN"
                )

    def test_direct_partial_tls_cannot_fall_back_to_development_http(self):
        for tls_cert_file, tls_key_file in (("server.crt", ""), ("", "server.key")):
            with self.subTest(tls_cert_file=tls_cert_file, tls_key_file=tls_key_file):
                self.assert_rejected_before_application_or_bind(
                    replace(
                        self.settings, environment="dev", host="127.0.0.1",
                        tls_cert_file=tls_cert_file, tls_key_file=tls_key_file,
                        allow_insecure_loopback_http=True,
                    ),
                    "configured together",
                )

    def test_direct_explicit_development_loopback_http_skips_tls_and_closes(self):
        for environment in ("local", "development", "dev", "test"):
            for host in ("127.0.0.1", "127.1.2.3"):
                with (
                    self.subTest(environment=environment, host=host),
                    patch("grading_agent.server.GradingAgentApplication") as application_type,
                    patch("grading_agent.server.GradingAgentHTTPServer") as server_type,
                    patch("grading_agent.server.ssl.SSLContext") as context_type,
                ):
                    settings = replace(
                        self.settings, environment=environment, host=host,
                        tls_cert_file="", tls_key_file="", allow_insecure_loopback_http=True,
                    )
                    serve(settings)
                    application_type.assert_called_once_with(settings)
                    server_type.assert_called_once_with((host, settings.port), application_type.return_value)
                    context_type.assert_not_called()
                    server_type.return_value.serve_forever.assert_called_once_with()
                    server_type.return_value.server_close.assert_called_once_with()

    @patch("grading_agent.server.GradingAgentApplication")
    @patch("grading_agent.server.GradingAgentHTTPServer")
    @patch("grading_agent.server.ssl.SSLContext")
    def test_configured_tls_takes_precedence_over_development_http_opt_in(
        self, context_type, server_type, _app_type
    ):
        serve(replace(self.settings, environment="dev", host="127.0.0.1", allow_insecure_loopback_http=True))

        context_type.return_value.load_cert_chain.assert_called_once_with("server.crt", "server.key")
        context_type.return_value.wrap_socket.assert_called_once()
        self.assertIs(server_type.return_value.socket, context_type.return_value.wrap_socket.return_value)
        server_type.return_value.serve_forever.assert_called_once_with()
        server_type.return_value.server_close.assert_called_once_with()
