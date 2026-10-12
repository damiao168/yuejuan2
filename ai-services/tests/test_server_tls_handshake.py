import json
import os
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import unittest
from contextlib import contextmanager
from dataclasses import replace
from http.client import HTTPSConnection
from pathlib import Path
from unittest.mock import patch

from grading_agent.config import Settings
from grading_agent.server import GradingAgentHTTPServer, serve


class _TLSReadinessModel:
    # 只替换外部模型依赖；应用、TLS 上下文、监听 socket 和 HTTP 处理均使用真实实现。
    def ready(self):
        return True


class ServerTLSHandshakeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary_directory = tempfile.TemporaryDirectory(prefix="grading-agent-tls-")
        cls.addClassCleanup(cls.temporary_directory.cleanup)
        cls.certificate_root = Path(cls.temporary_directory.name)
        openssl = os.environ.get("EDUGRADE_TEST_OPENSSL") or shutil.which("openssl")
        if not openssl:
            raise RuntimeError("OpenSSL is required for grading-agent TLS handshake tests")
        executable = Path(openssl)
        if not executable.is_absolute():
            raise ValueError("EDUGRADE_TEST_OPENSSL must be an absolute executable path")
        cls.openssl = executable.resolve(strict=True)
        if not cls.openssl.is_file():
            raise ValueError("OpenSSL executable must be a regular file")
        configuration = cls.certificate_root / "openssl.cnf"
        configuration.write_text(
            "[req]\ndistinguished_name = dn\nprompt = no\n"
            "[dn]\nCN = grading-agent-test\n"
            "[v3_ca]\nbasicConstraints = critical,CA:TRUE\n"
            "keyUsage = critical,keyCertSign,cRLSign\nsubjectKeyIdentifier = hash\n"
            "[server_cert]\nbasicConstraints = critical,CA:FALSE\n"
            "keyUsage = critical,digitalSignature,keyEncipherment\n"
            "extendedKeyUsage = serverAuth\n"
            "subjectAltName = IP:127.0.0.1,DNS:grading-agent\n"
            "authorityKeyIdentifier = keyid,issuer\n",
            encoding="ascii",
        )
        cls.ca_cert = cls.certificate_root / "ca.crt"
        cls.ca_key = cls.certificate_root / "ca.key"
        cls.wrong_ca_cert = cls.certificate_root / "wrong-ca.crt"
        cls.wrong_ca_key = cls.certificate_root / "wrong-ca.key"
        for common_name, certificate, key in (
            ("grading-agent-test-ca", cls.ca_cert, cls.ca_key),
            ("untrusted-test-ca", cls.wrong_ca_cert, cls.wrong_ca_key),
        ):
            cls._openssl(
                "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-sha256", "-days", "2",
                "-subj", f"/CN={common_name}", "-config", str(configuration),
                "-extensions", "v3_ca", "-keyout", str(key), "-out", str(certificate),
            )
        cls.server_cert = cls.certificate_root / "server.crt"
        cls.server_key = cls.certificate_root / "server.key"
        signing_request = cls.certificate_root / "server.csr"
        cls._openssl(
            "req", "-new", "-newkey", "rsa:2048", "-nodes", "-sha256",
            "-subj", "/CN=grading-agent", "-config", str(configuration),
            "-keyout", str(cls.server_key), "-out", str(signing_request),
        )
        cls._openssl(
            "x509", "-req", "-in", str(signing_request), "-CA", str(cls.ca_cert),
            "-CAkey", str(cls.ca_key), "-CAcreateserial", "-days", "2", "-sha256",
            "-extfile", str(configuration), "-extensions", "server_cert", "-out", str(cls.server_cert),
        )
        repository = Path(__file__).resolve().parents[2]
        cls.settings = Settings(
            environment="production",
            host="127.0.0.1",
            port=0,
            service_token="tls-handshake-synthetic-service-token-with-32-characters",
            contract_root=str(repository / "contracts" / "grading-agent" / "v1"),
            prompt_root=str(repository / "ai-services" / "prompts"),
            model_readiness_cache_seconds=5,
            tls_cert_file=str(cls.server_cert),
            tls_key_file=str(cls.server_key),
        )

    @classmethod
    def _openssl(cls, *arguments):
        subprocess.run(
            [str(cls.openssl), *arguments], check=True, capture_output=True, text=True, timeout=30,
        )

    @contextmanager
    def _running_server(self):
        servers = []
        failures = []
        serving = threading.Event()
        finished = threading.Event()

        class ObservedServer(GradingAgentHTTPServer):
            # 仅记录真实实例和已进入循环的时刻，避免 shutdown 与启动尚未完成时竞争。
            def __init__(self, address, application):
                super().__init__(address, application)
                servers.append(self)

            def service_actions(self):
                super().service_actions()
                serving.set()

        def run():
            try:
                serve(self.settings)
            except Exception as exc:  # noqa: BLE001 - 在线程外重新抛出真实启动失败。
                failures.append(exc)
            finally:
                finished.set()

        with (
            patch("grading_agent.app.build_provider_adapter", return_value=_TLSReadinessModel()),
            patch("grading_agent.server.GradingAgentHTTPServer", ObservedServer),
        ):
            thread = threading.Thread(target=run, name="grading-agent-tls-test", daemon=True)
            thread.start()
            try:
                deadline = time.monotonic() + 5
                while not serving.wait(0.02) and not finished.is_set() and time.monotonic() < deadline:
                    pass
                if failures:
                    raise failures[0]
                self.assertTrue(serving.is_set(), "real serve() did not enter its service loop")
                yield servers[0]
            finally:
                if serving.is_set() and thread.is_alive():
                    servers[0].shutdown()
                thread.join(timeout=5)
                self.assertFalse(thread.is_alive(), "serve() thread leaked after listener shutdown")
                if servers:
                    self._assert_closed(servers[0])
            if failures:
                raise failures[0]

    def _connect_after_close(self, address):
        # 意外连接成功也会在退出上下文时关闭；失败断言只接受建立连接的 OSError。
        with socket.create_connection(address, timeout=1):
            self.fail("closed listener still accepted a connection")

    def _assert_closed(self, server):
        self.assertTrue(server._readiness_stop.is_set())
        self.assertFalse(server._readiness_thread.is_alive(), "readiness thread leaked after server_close()")
        self.assertEqual(server.socket.fileno(), -1)
        with self.assertRaises(OSError):
            self._connect_after_close(server.server_address)

    def _trusted_context(self):
        context = ssl.create_default_context(cafile=str(self.ca_cert))
        self.assertTrue(context.check_hostname)
        self.assertEqual(context.verify_mode, ssl.CERT_REQUIRED)
        return context

    def _get(self, server, path, headers=None):
        connection = HTTPSConnection(
            "127.0.0.1", server.server_port, context=self._trusted_context(), timeout=3,
        )
        try:
            connection.connect()
            version = connection.sock.version()
            connection.request("GET", path, headers=headers or {})
            response = connection.getresponse()
            return response.status, json.loads(response.read()), version
        finally:
            connection.close()

    def test_production_https_preserves_health_readiness_and_service_authentication(self):
        with self._running_server() as server:
            status, health, version = self._get(server, "/health")
            self.assertEqual(status, 200)
            self.assertEqual(health["status"], "healthy")
            self.assertIn(version, {"TLSv1.2", "TLSv1.3"})
            status, ready, _version = self._get(server, "/ready")
            self.assertEqual(status, 200)
            self.assertEqual(ready["status"], "ready")
            for headers in ({}, {"Authorization": "Bearer invalid-synthetic-token"}):
                with self.subTest(headers=headers):
                    status, _error, _version = self._get(server, "/grading/prompts/current", headers)
                    self.assertEqual(status, 401)
            status, snapshot, _version = self._get(
                server, "/grading/prompts/current",
                {"Authorization": f"Bearer {self.settings.service_token}"},
            )
            self.assertEqual(status, 200)
            self.assertEqual(snapshot["prompt"]["prompt_version"], self.settings.prompt_version)

    def test_wrong_or_missing_ca_rejects_the_same_real_listener(self):
        with self._running_server() as server:
            for trust in (self.wrong_ca_cert, None):
                with self.subTest(trust=trust):
                    context = ssl.create_default_context(cafile=str(trust) if trust else None)
                    connection = HTTPSConnection("127.0.0.1", server.server_port, context=context, timeout=3)
                    try:
                        with self.assertRaises(ssl.SSLCertVerificationError):
                            connection.connect()
                    finally:
                        connection.close()
            self.assertEqual(self._get(server, "/health")[0], 200)

    def test_san_mismatch_rejects_a_trusted_certificate(self):
        with self._running_server() as server:
            # TCP 仍连接同一个 IPv4 listener，只改变验证名称，故失败不是 DNS 或连接错误。
            with socket.create_connection(server.server_address, timeout=3) as connection:
                context = self._trusted_context()
                with self.assertRaises(ssl.SSLCertVerificationError) as caught:
                    context.wrap_socket(connection, server_hostname="unmatched.grading.invalid")
            self.assertIn("mismatch", caught.exception.verify_message.lower())
            self.assertEqual(self._get(server, "/health")[0], 200)

    def test_plaintext_does_not_receive_an_http_or_json_response(self):
        with self._running_server() as server:
            with socket.create_connection(server.server_address, timeout=3) as connection:
                connection.sendall(b"GET /health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
                try:
                    response = connection.recv(4096)
                except (ConnectionResetError, ConnectionAbortedError, BrokenPipeError):
                    response = b""
            self.assertFalse(response.startswith(b"HTTP/"))
            self.assertNotIn(b'"healthy"', response)
            if response:
                # TLS 告警可能不是 UTF-8；可解码时也必须拒绝把它识别成业务 JSON。
                try:
                    decoded = response.decode("utf-8")
                except UnicodeDecodeError:
                    pass
                else:
                    with self.assertRaises(json.JSONDecodeError):
                        json.loads(decoded)
            self.assertEqual(self._get(server, "/health")[0], 200)

    def test_missing_invalid_or_mismatched_certificate_closes_real_resources(self):
        invalid_certificate = self.certificate_root / "invalid.crt"
        invalid_certificate.write_text("not a certificate\n", encoding="ascii")
        cases = (
            (str(self.certificate_root / "missing.crt"), str(self.server_key), FileNotFoundError),
            (str(self.server_cert), str(self.certificate_root / "missing.key"), FileNotFoundError),
            (str(invalid_certificate), str(self.server_key), ssl.SSLError),
            (str(self.server_cert), str(self.wrong_ca_key), ssl.SSLError),
        )
        for certificate, key, error_type in cases:
            servers = []

            class ObservedServer(GradingAgentHTTPServer):
                # 实例归属当前证书场景，避免闭包随下一轮复用资源观察列表。
                def __init__(self, address, application, servers=servers):
                    super().__init__(address, application)
                    servers.append(self)

            with (
                self.subTest(certificate=certificate, key=key),
                patch("grading_agent.app.build_provider_adapter", return_value=_TLSReadinessModel()),
                patch("grading_agent.server.GradingAgentHTTPServer", ObservedServer),
            ):
                invalid_settings = replace(self.settings, tls_cert_file=certificate, tls_key_file=key)
                with self.assertRaises(error_type):
                    serve(invalid_settings)
                self.assertEqual(len(servers), 1)
                self._assert_closed(servers[0])


if __name__ == "__main__":
    unittest.main()
