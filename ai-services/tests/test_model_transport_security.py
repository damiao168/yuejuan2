import importlib.util
import json
import os
import shutil
import ssl
import subprocess
import tempfile
import threading
import time
import unittest
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest.mock import Mock, patch
from urllib import error as urlerror

from grading_agent.errors import AgentError
from grading_agent.model import (
    DashScopeNativeAdapter,
    LocalLlamaCppAdapter,
    _default_transport,
)
from helpers import settings, valid_request

_story_path = Path(__file__).resolve().parents[2] / "tests/e2e/story060_grading_agent.py"
_story_spec = importlib.util.spec_from_file_location("story060_transport_fixture", _story_path)
story060 = importlib.util.module_from_spec(_story_spec)
_story_spec.loader.exec_module(story060)


class _HTTPFixtureHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self._reply()

    def do_POST(self):
        self._reply()

    def _reply(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        self.server.received.append((self.path, dict(self.headers), body))
        if self.server.delay:
            time.sleep(self.server.delay)
        self.send_response(self.server.response_status)
        self.send_header("Content-Type", "application/json")
        if self.server.location:
            self.send_header("Location", self.server.location)
        if self.server.content_length is not None:
            self.send_header("Content-Length", self.server.content_length)
        self.end_headers()
        try:
            self.wfile.write(self.server.response_body)
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError, ssl.SSLError):
            pass

    def log_message(self, _format, *_args):
        pass


@contextmanager
def _running_server(tls_context=None):
    server = ThreadingHTTPServer(("127.0.0.1", 0), _HTTPFixtureHandler)
    server.received = []
    server.response_status = 200
    server.response_body = b'{"ok":true}'
    server.content_length = None
    server.location = None
    server.delay = 0
    if tls_context:
        server.socket = tls_context.wrap_socket(server.socket, server_side=True)
    server.base_url = f'{"https" if tls_context else "http"}://127.0.0.1:{server.server_port}'
    thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02}, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
        if thread.is_alive():
            raise RuntimeError("HTTP transport fixture thread did not stop")


class ModelTransportSecurityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary_directory = tempfile.TemporaryDirectory(prefix="model-transport-tls-")
        cls.addClassCleanup(cls.temporary_directory.cleanup)
        executable = os.environ.get("EDUGRADE_TEST_OPENSSL") or shutil.which("openssl")
        if not executable or not Path(executable).is_absolute():
            raise RuntimeError("OpenSSL is required for model transport TLS tests")
        openssl = Path(executable).resolve(strict=True)
        directory = Path(cls.temporary_directory.name)
        certificate, key = directory / "server.crt", directory / "server.key"
        configuration = directory / "openssl.cnf"
        configuration.write_text(
            "[req]\ndistinguished_name=dn\nprompt=no\n[dn]\nCN=model-transport-untrusted-test\n",
            encoding="ascii",
        )
        subprocess.run(
            [str(openssl), "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-sha256",
             "-days", "2", "-subj", "/CN=model-transport-untrusted-test",
             "-config", str(configuration),
             "-keyout", str(key), "-out", str(certificate)],
            check=True, capture_output=True, timeout=30,
        )
        cls.tls_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        cls.tls_context.load_cert_chain(certificate, key)

    @staticmethod
    def _calls(url):
        headers = {"Authorization": "Bearer synthetic-model-credential", "Content-Type": "application/json"}
        return {
            "local_model_post": lambda: _default_transport(url, {"input": "synthetic"}, headers, 1),
            "native_model_post": lambda: DashScopeNativeAdapter._http_transport(url, {"input": "synthetic"}, headers, 1),
            "e2e_get": lambda: story060.call_json(url, headers=headers, timeout=1),
            "e2e_post": lambda: story060.call_json(url, "POST", {"password": "synthetic-login-password"}, headers, 1),
        }

    def test_all_redirect_codes_never_reach_another_origin_or_forward_credentials(self):
        # 两个随机回环端口代表不同 origin；目标记录为空才证明没有先泄露凭据再报错。
        with _running_server() as source, _running_server() as target:
            source.location = target.base_url + "/received"
            for status in (301, 302, 303, 307, 308):
                source.response_status = status
                for name, call in self._calls(source.base_url + "/redirect").items():
                    with self.subTest(status=status, transport=name):
                        with self.assertRaises(urlerror.HTTPError) as caught:
                            call()
                        self.assertEqual(caught.exception.code, status)
                        caught.exception.close()
                        self.assertEqual(source.received[-1][1]["Authorization"], "Bearer synthetic-model-credential")
                        self.assertEqual(target.received, [])
                adapter = LocalLlamaCppAdapter(settings(model_base_url=source.base_url, model_api_key="synthetic-ready-key"))
                self.assertFalse(adapter.ready())
                self.assertEqual(target.received, [])

    def test_same_origin_redirect_is_also_rejected(self):
        with _running_server() as source:
            source.response_status = 302
            source.location = source.base_url + "/received"
            for name, call in self._calls(source.base_url + "/redirect").items():
                with self.subTest(transport=name), self.assertRaises(urlerror.HTTPError) as caught:
                    call()
                caught.exception.close()
            adapter = LocalLlamaCppAdapter(settings(model_base_url=source.base_url))
            self.assertFalse(adapter.ready())
            self.assertTrue(source.received)
            self.assertTrue(all(path in {"/redirect", "/models"} for path, _headers, _body in source.received))

    def test_successful_json_and_readiness_keep_existing_behavior_without_global_opener_changes(self):
        with _running_server() as source, patch("urllib.request.install_opener") as install:
            for name, call in self._calls(source.base_url + "/success").items():
                with self.subTest(transport=name):
                    result = call()
                    self.assertEqual(result, (200, {"ok": True}) if name.startswith("e2e") else {"ok": True})
            adapter = LocalLlamaCppAdapter(settings(model_base_url=source.base_url))
            self.assertTrue(adapter.ready())
            install.assert_not_called()

    def test_model_errors_still_map_to_the_existing_public_error_codes(self):
        with _running_server() as source:
            source.response_status = 401
            source.response_body = b'{"code":"InvalidApiKey"}'
            cases = (
                (LocalLlamaCppAdapter(settings(model_base_url=source.base_url)), "model_auth_failed", 502),
                (DashScopeNativeAdapter(settings(model_base_url=source.base_url)), "model_unavailable", 503),
            )
            for adapter, code, status in cases:
                request_data = valid_request()
                with self.subTest(adapter=type(adapter).__name__), self.assertRaises(AgentError) as caught:
                    adapter.request(request_data)
                self.assertEqual(caught.exception.code, code)
                self.assertEqual(caught.exception.status, status)
                self.assertFalse(caught.exception.retryable)
                caught.exception.__cause__.close()

    def test_tls_rejects_an_untrusted_certificate_for_every_transport(self):
        with _running_server(self.tls_context) as source:
            for name, call in self._calls(source.base_url + "/tls").items():
                with self.subTest(transport=name), self.assertRaises(urlerror.URLError) as caught:
                    call()
                self.assertIsInstance(caught.exception.reason, ssl.SSLCertVerificationError)
            adapter = LocalLlamaCppAdapter(settings(model_base_url=source.base_url))
            self.assertFalse(adapter.ready())
            self.assertEqual(source.received, [])

    def test_timeout_still_bounds_transport_and_readiness_requests(self):
        with _running_server() as source:
            source.delay = 0.2
            for transport in (_default_transport, DashScopeNativeAdapter._http_transport):
                with self.subTest(transport=transport.__name__), self.assertRaises(TimeoutError):
                    transport(source.base_url, {}, {}, 0.03)
            with self.assertRaises(TimeoutError):
                story060.call_json(source.base_url, timeout=0.03)
            adapter = LocalLlamaCppAdapter(settings(model_base_url=source.base_url, model_ready_timeout_seconds=0.03))
            self.assertFalse(adapter.ready())

    def test_e2e_response_size_limit_handles_declared_and_missing_lengths(self):
        with _running_server() as source, patch.object(story060, "MAX_JSON_RESPONSE_BYTES", 16):
            source.response_body = json.dumps({"text": "x" * 32}).encode()
            for declared_length in (str(len(source.response_body)), None):
                source.content_length = declared_length
                with self.subTest(content_length=declared_length), self.assertRaisesRegex(ValueError, "size limit"):
                    story060.call_json(source.base_url, timeout=1)

    def test_e2e_invalid_declared_lengths_fail_before_reading_and_reads_are_bounded(self):
        for length in ("invalid", "-1", str(story060.MAX_JSON_RESPONSE_BYTES + 1)):
            response = Mock(headers={"Content-Length": length})
            with self.subTest(length=length), self.assertRaises(ValueError):
                story060._read_json_response(response)
            response.read.assert_not_called()
        response = Mock(headers={}, read=Mock(return_value=b"{}"))
        self.assertEqual(story060._read_json_response(response), {})
        response.read.assert_called_once_with(story060.MAX_JSON_RESPONSE_BYTES + 1)


if __name__ == "__main__":
    unittest.main()
