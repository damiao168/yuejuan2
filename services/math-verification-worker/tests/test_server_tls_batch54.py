import json
import os
import shutil
import socket
import ssl
import subprocess
import threading
from contextlib import contextmanager
from pathlib import Path
from urllib import error, request

import pytest
from math_verification_worker import server

TOKEN = "synthetic-batch54-math-token-" + "x" * 32


def _openssl(executable: str, directory: Path, *arguments: str) -> None:
    subprocess.run(
        [executable, *arguments], cwd=directory, check=True, capture_output=True, text=True, timeout=30,
    )


@pytest.fixture(scope="module")
def tls_files(tmp_path_factory) -> dict[str, Path]:
    # 每个包独立签发短期合成材料；缺少工具必须失败，不能把跳过记作握手验证通过。
    override = os.environ.get("EDUGRADE_TEST_OPENSSL", "").strip()
    if override and (not Path(override).is_absolute() or not Path(override).is_file()):
        pytest.fail("EDUGRADE_TEST_OPENSSL 必须是现有 openssl 可执行文件的绝对路径")
    executable = override or shutil.which("openssl")
    if not executable:
        pytest.fail("真实 TLS 回归需要 openssl；可用 EDUGRADE_TEST_OPENSSL 指定可执行文件")
    directory = tmp_path_factory.mktemp("math-tls-batch54")
    config = directory / "request.cnf"
    config.write_text("[req]\ndistinguished_name=subject\n[subject]\n", encoding="ascii")
    for name in ("ca", "other-ca"):
        _openssl(
            executable, directory, "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-sha256",
            "-keyout", f"{name}.key", "-out", f"{name}.crt", "-days", "2",
            "-config", str(config), "-subj", f"/CN=EduGrade Batch54 {name}",
            "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign",
        )
    for name, san in (
        ("server", "DNS:math-verification-worker,IP:127.0.0.1"),
        ("wrong-san", "DNS:unrelated.example"),
    ):
        _openssl(
            executable, directory, "req", "-new", "-newkey", "rsa:2048", "-nodes", "-sha256",
            "-keyout", f"{name}.key", "-out", f"{name}.csr", "-config", str(config),
            "-subj", "/CN=math-verification-worker",
        )
        extensions = directory / f"{name}.ext"
        extensions.write_text(
            f"subjectAltName={san}\nextendedKeyUsage=serverAuth\nbasicConstraints=critical,CA:FALSE\n"
            "keyUsage=critical,digitalSignature,keyEncipherment\n",
            encoding="ascii",
        )
        _openssl(
            executable, directory, "x509", "-req", "-in", f"{name}.csr", "-CA", "ca.crt",
            "-CAkey", "ca.key", "-CAcreateserial", "-out", f"{name}.crt", "-days", "2", "-sha256",
            "-extfile", str(extensions),
        )
    return {name: directory / name for name in (
        "ca.crt", "ca.key", "other-ca.crt", "server.crt", "server.key", "wrong-san.crt", "wrong-san.key",
    )}


@contextmanager
def _running_verifier(monkeypatch, tls_files: dict[str, Path], certificate: str = "server"):
    monkeypatch.setenv("EDUGRADE_ENV", "production")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", "127.0.0.1")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_PORT", "0")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", "false")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", str(tls_files[f"{certificate}.crt"]))
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", str(tls_files[f"{certificate}.key"]))
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TOKEN", TOKEN)
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TIMEOUT_SECONDS", "10")
    ready = threading.Event()
    listeners = []
    failures = []
    original_listener = server.ThreadingHTTPServer

    class ObservedListener(original_listener):
        def serve_forever(self, poll_interval: float = 0.05) -> None:
            ready.set()
            super().serve_forever(poll_interval)

    def create_listener(address, handler):
        listener = ObservedListener(address, handler)
        listeners.append(listener)
        return listener

    def run() -> None:
        try:
            server.main()
        except Exception as exc:
            # 将启动失败同步给主测试线程，同时保留线程自身的异常诊断。
            failures.append(exc)
            ready.set()
            raise

    monkeypatch.setattr(server, "ThreadingHTTPServer", create_listener)
    thread = threading.Thread(target=run, daemon=True, name="math-tls-batch54")
    thread.start()
    try:
        assert ready.wait(10), "数学 TLS 监听器未在预算内启动"
        assert not failures
        listener = listeners[0]
        yield listener.server_address[1]
    finally:
        if listeners and ready.is_set() and thread.is_alive() and not failures:
            listeners[0].shutdown()
        thread.join(10)
        assert not thread.is_alive(), "数学 TLS 测试线程未回收"
        assert not failures
        if listeners:
            listener = listeners[0]
            assert listener.socket.fileno() == -1
            # 每个真实握手场景都证明 main 的 finally 释放了端口，避免泄漏污染后续测试。
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
                probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                probe.bind(listener.server_address)


def _trusted_context(tls_files: dict[str, Path]) -> ssl.SSLContext:
    context = ssl.create_default_context(cafile=str(tls_files["ca.crt"]))
    assert context.verify_mode == ssl.CERT_REQUIRED
    assert context.check_hostname is True
    return context


def _health(port: int, context: ssl.SSLContext) -> dict:
    with request.urlopen(f"https://127.0.0.1:{port}/healthz", context=context, timeout=10) as response:
        assert response.status == 200
        return json.load(response)


def test_actual_main_serves_health_and_bounded_normalization_over_tls(monkeypatch, tls_files) -> None:
    with _running_verifier(monkeypatch, tls_files) as port:
        context = _trusted_context(tls_files)
        assert _health(port, context) == {"status": "ok"}
        body = json.dumps({"latex": "0.1+0.2", "domain": {"domain": "real"}}).encode()
        req = request.Request(
            f"https://127.0.0.1:{port}/internal/math/normalize", data=body,
            headers={"Content-Type": "application/json", "X-EduGrade-Internal-Token": TOKEN}, method="POST",
        )
        with request.urlopen(req, context=context, timeout=15) as response:
            assert response.status == 200
            result = json.load(response)
        assert result["canonical"] == "3/10"
        assert result["ast"] == {
            "kind": "operator", "value": "+",
            "children": [{"kind": "number", "value": "0.1"}, {"kind": "number", "value": "0.2"}],
        }


def test_actual_tls_listener_preserves_internal_token_rejection(monkeypatch, tls_files) -> None:
    with _running_verifier(monkeypatch, tls_files) as port:
        req = request.Request(
            f"https://127.0.0.1:{port}/internal/math/normalize", data=b'{"latex":"x=2"}',
            headers={"Content-Type": "application/json", "X-EduGrade-Internal-Token": "incorrect-token"},
            method="POST",
        )
        context = _trusted_context(tls_files)
        with pytest.raises(error.HTTPError) as rejected:
            request.urlopen(req, context=context, timeout=10)
        assert rejected.value.code == 401
        assert json.load(rejected.value) == {"error": "unauthorized"}
        rejected.value.close()


@pytest.mark.parametrize("trust", ["empty", "other-ca"])
def test_actual_tls_listener_rejects_untrusted_certificate(monkeypatch, tls_files, trust) -> None:
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    if trust == "other-ca":
        context.load_verify_locations(cafile=str(tls_files["other-ca.crt"]))
    with _running_verifier(monkeypatch, tls_files) as port:
        with pytest.raises(error.URLError) as rejected:
            _health(port, context)
        assert isinstance(rejected.value.reason, ssl.SSLCertVerificationError)
        assert _health(port, _trusted_context(tls_files)) == {"status": "ok"}


def test_actual_tls_listener_rejects_wrong_certificate_san(monkeypatch, tls_files) -> None:
    with _running_verifier(monkeypatch, tls_files, "wrong-san") as port:
        context = _trusted_context(tls_files)
        with pytest.raises(error.URLError) as rejected:
            _health(port, context)
        assert isinstance(rejected.value.reason, ssl.SSLCertVerificationError)
        assert rejected.value.reason.verify_code == 64


def test_actual_tls_listener_rejects_plaintext_http(monkeypatch, tls_files) -> None:
    with _running_verifier(monkeypatch, tls_files) as port:
        with socket.create_connection(("127.0.0.1", port), timeout=10) as connection:
            connection.sendall(b"GET /healthz HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
            try:
                response = connection.recv(1024)
            except ConnectionResetError:
                response = b""
        assert not response.startswith(b"HTTP/")
        assert b'"status": "ok"' not in response
        assert _health(port, _trusted_context(tls_files)) == {"status": "ok"}


@pytest.mark.parametrize(
    ("version", "expected"), [(ssl.TLSVersion.TLSv1_2, "TLSv1.2"), (ssl.TLSVersion.TLSv1_3, "TLSv1.3")],
)
def test_actual_tls_listener_negotiates_supported_protocols(monkeypatch, tls_files, version, expected) -> None:
    context = _trusted_context(tls_files)
    context.minimum_version = version
    context.maximum_version = version
    with _running_verifier(monkeypatch, tls_files) as port:
        with (
            socket.create_connection(("127.0.0.1", port), timeout=10) as raw,
            context.wrap_socket(raw, server_hostname="math-verification-worker") as connection,
        ):
            assert connection.version() == expected
        assert _health(port, context) == {"status": "ok"}


def test_actual_tls_listener_rejects_a_tls11_client_hello(monkeypatch, tls_files) -> None:
    # 直接发送合法 TLS 1.1 ClientHello，避免客户端 OpenSSL 自己禁用旧版本后伪装成服务端拒绝。
    hello = b"\x03\x02" + os.urandom(32) + b"\x00\x00\x04\xc0\x13\x00\x2f\x01\x00"
    handshake = b"\x01" + len(hello).to_bytes(3, "big") + hello
    record = b"\x16\x03\x02" + len(handshake).to_bytes(2, "big") + handshake
    with _running_verifier(monkeypatch, tls_files) as port:
        with socket.create_connection(("127.0.0.1", port), timeout=10) as connection:
            connection.sendall(record)
            alert = b""
            while len(alert) < 7:
                part = connection.recv(7 - len(alert))
                if not part:
                    break
                alert += part
        assert len(alert) == 7
        assert alert[0] == 21
        assert alert[-2:] == b"\x02\x46"
        assert _health(port, _trusted_context(tls_files)) == {"status": "ok"}


def test_actual_certificate_load_failure_releases_listener(monkeypatch, tls_files) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "production")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", "127.0.0.1")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_PORT", "0")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", "false")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", str(tls_files["server.crt"]))
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", str(tls_files["ca.key"]))
    original_listener = server.ThreadingHTTPServer
    listeners = []

    def create_listener(address, handler):
        listener = original_listener(address, handler)
        listeners.append(listener)
        return listener

    monkeypatch.setattr(server, "ThreadingHTTPServer", create_listener)
    with pytest.raises(ssl.SSLError):
        server.main()
    assert len(listeners) == 1
    assert listeners[0].socket.fileno() == -1
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.bind(listeners[0].server_address)
