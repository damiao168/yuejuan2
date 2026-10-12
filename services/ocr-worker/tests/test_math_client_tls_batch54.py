import json
import os
import shutil
import socket
import ssl
import subprocess
import threading
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib import error, request

import pytest
from ocr_worker.api import APIError
from ocr_worker.math_runner import MathVerificationClient, _build_artifact
from ocr_worker.recognition_router import (
    FormulaRecognitionResult,
    RegionKind,
    RoutedRecognitionResult,
)

TOKEN = "synthetic-batch54-ocr-token-" + "x" * 32
RESULT = {"ast": {"kind": "number", "value": "2"}, "canonical": "2"}


def _openssl(executable: str, directory: Path, *arguments: str) -> None:
    subprocess.run(
        [executable, *arguments], cwd=directory, check=True, capture_output=True, text=True, timeout=30,
    )


@pytest.fixture(scope="module")
def tls_files(tmp_path_factory) -> dict[str, Path]:
    # OCR 的 CI 不安装数学包；材料在本包独立签发，不读取部署密钥或跨包 fixture。
    override = os.environ.get("EDUGRADE_TEST_OPENSSL", "").strip()
    if override and (not Path(override).is_absolute() or not Path(override).is_file()):
        pytest.fail("EDUGRADE_TEST_OPENSSL 必须是现有 openssl 可执行文件的绝对路径")
    executable = override or shutil.which("openssl")
    if not executable:
        pytest.fail("真实 TLS 回归需要 openssl；可用 EDUGRADE_TEST_OPENSSL 指定可执行文件")
    directory = tmp_path_factory.mktemp("ocr-math-tls-batch54")
    config = directory / "request.cnf"
    config.write_text("[req]\ndistinguished_name=subject\n[subject]\n", encoding="ascii")
    for name in ("ca", "other-ca"):
        _openssl(
            executable, directory, "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-sha256",
            "-keyout", f"{name}.key", "-out", f"{name}.crt", "-days", "2",
            "-config", str(config), "-subj", f"/CN=EduGrade Batch54 OCR {name}",
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
    empty_ca_directory = directory / "empty-ca-directory"
    empty_ca_directory.mkdir()
    return {name: directory / name for name in (
        "ca.crt", "other-ca.crt", "server.crt", "server.key", "wrong-san.crt", "wrong-san.key",
        "missing-ca.crt", "empty-ca-directory",
    )}


def _configure_trust(monkeypatch, tls_files: dict[str, Path], certificate: str = "ca") -> None:
    monkeypatch.setenv("SSL_CERT_FILE", str(tls_files[f"{certificate}.crt"]))
    monkeypatch.setenv("SSL_CERT_DIR", str(tls_files["empty-ca-directory"]))
    monkeypatch.setenv("NO_PROXY", "127.0.0.1,localhost")
    monkeypatch.setenv("no_proxy", "127.0.0.1,localhost")
    # 使用 urllib 的正常 HTTPSHandler；避免其他测试已安装的全局 opener 改变信任边界。
    monkeypatch.setattr(request, "_opener", None)


@contextmanager
def _running_https_verifier(tls_files: dict[str, Path], certificate: str = "server", response_status: int = 200):
    received = []
    failures = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            received.append({
                "path": self.path,
                "token": self.headers.get("X-EduGrade-Internal-Token"),
                "payload": payload,
                "protocol": self.connection.version(),
            })
            status = response_status if self.headers.get("X-EduGrade-Internal-Token") == TOKEN else 401
            body = json.dumps(RESULT if status == 200 else {"error": "synthetic-test-rejection"}).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, _format: str, *_args: object) -> None:
            return

    listener = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = None
    ready = threading.Event()
    try:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(str(tls_files[f"{certificate}.crt"]), str(tls_files[f"{certificate}.key"]))
        listener.socket = context.wrap_socket(listener.socket, server_side=True)

        def run() -> None:
            ready.set()
            try:
                listener.serve_forever(poll_interval=0.05)
            except Exception as exc:
                # 主线程检查错误列表；重新抛出也保留后台监听器的异常诊断。
                failures.append(exc)
                raise

        thread = threading.Thread(target=run, daemon=True, name="ocr-math-tls-batch54")
        thread.start()
        assert ready.wait(10), "OCR 测试 HTTPS 监听器未在预算内启动"
        yield listener.server_address[1], received
    finally:
        if thread is not None:
            if thread.is_alive():
                listener.shutdown()
            thread.join(10)
            assert not thread.is_alive(), "OCR 测试 HTTPS 线程未回收"
        listener.server_close()
        assert not failures
        assert listener.socket.fileno() == -1
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            probe.bind(listener.server_address)


@pytest.mark.parametrize(
    ("operation", "arguments", "expected_payload"),
    [
        ("normalize", ("x=2",), {"latex": "x=2", "domain": {"domain": "real"}}),
        (
            "verify_transition",
            ({"kind": "number", "value": "1"}, {"kind": "number", "value": "2"}),
            {
                "previous_ast": {"kind": "number", "value": "1"},
                "next_ast": {"kind": "number", "value": "2"},
                "domain": {"domain": "real"},
            },
        ),
        (
            "solve", ({"kind": "symbol", "value": "x"}, "x"),
            {"ast": {"kind": "symbol", "value": "x"}, "variable": "x", "domain": {"domain": "real"}},
        ),
    ],
)
def test_actual_client_posts_operations_and_token_over_trusted_tls(
    monkeypatch, tls_files, operation, arguments, expected_payload,
) -> None:
    _configure_trust(monkeypatch, tls_files)
    context = ssl.create_default_context()
    assert context.verify_mode == ssl.CERT_REQUIRED
    assert context.check_hostname is True
    with _running_https_verifier(tls_files) as (port, received):
        client = MathVerificationClient(f"https://127.0.0.1:{port}", TOKEN, timeout=10)
        assert getattr(client, operation)(*arguments) == RESULT
        assert len(received) == 1
        assert received[0]["path"] == f"/internal/math/{operation.replace('_', '-')}"
        assert received[0]["payload"] == expected_payload
        assert received[0]["token"] == TOKEN
        assert received[0]["protocol"] in {"TLSv1.2", "TLSv1.3"}


@pytest.mark.parametrize("certificate", ["missing-ca", "other-ca"])
def test_actual_client_rejects_untrusted_ca_before_sending_token(monkeypatch, tls_files, certificate) -> None:
    _configure_trust(monkeypatch, tls_files, certificate)
    with _running_https_verifier(tls_files) as (port, received):
        client = MathVerificationClient(f"https://127.0.0.1:{port}", TOKEN, timeout=10)
        with pytest.raises(APIError, match="math verification unavailable") as rejected:
            client.normalize("x=2")
        assert isinstance(rejected.value.__cause__, error.URLError)
        assert isinstance(rejected.value.__cause__.reason, ssl.SSLCertVerificationError)
        assert received == []


def test_actual_client_rejects_wrong_hostname_with_the_correct_ca(monkeypatch, tls_files) -> None:
    _configure_trust(monkeypatch, tls_files)
    with _running_https_verifier(tls_files, "wrong-san") as (port, received):
        client = MathVerificationClient(f"https://127.0.0.1:{port}", TOKEN, timeout=10)
        with pytest.raises(APIError, match="math verification unavailable") as rejected:
            client.normalize("x=2")
        assert isinstance(rejected.value.__cause__, error.URLError)
        assert isinstance(rejected.value.__cause__.reason, ssl.SSLCertVerificationError)
        assert rejected.value.__cause__.reason.verify_code == 64
        assert received == []


def test_actual_tls_certificate_failure_preserves_reviewable_recognition_evidence(monkeypatch, tls_files) -> None:
    _configure_trust(monkeypatch, tls_files, "other-ca")
    task = {
        "answer_segment_id": "synthetic-segment", "exam_question_snapshot_id": "synthetic-question",
        "subject_code": "mathematics", "input_hash": "synthetic-input-hash",
    }
    routed = RoutedRecognitionResult(kind=RegionKind.FORMULA, formula=FormulaRecognitionResult(
        "x=2", "x=2", 0.9, "synthetic-test-engine", "synthetic-v1", "recognized",
    ))
    with _running_https_verifier(tls_files) as (port, received):
        client = MathVerificationClient(f"https://127.0.0.1:{port}", TOKEN, timeout=10)
        artifact = _build_artifact(task, routed, client)
        assert received == []
    assert artifact["formulas"][0]["canonical_latex"] == "x=2"
    assert artifact["formulas"][0]["ast"] is None
    assert artifact["verifications"][0]["status"] == "uncertain"
    assert artifact["verifications"][0]["reason_code"] == "verification_unavailable"
    assert artifact["verifications"][0]["confidence"] == 0


def test_actual_client_preserves_token_failure_contract_over_tls(monkeypatch, tls_files) -> None:
    _configure_trust(monkeypatch, tls_files)
    with _running_https_verifier(tls_files) as (port, received):
        client = MathVerificationClient(f"https://127.0.0.1:{port}", "incorrect-token", timeout=10)
        with pytest.raises(APIError, match="math verification request failed") as rejected:
            client.normalize("x=2")
        assert isinstance(rejected.value.__cause__, error.HTTPError)
        assert rejected.value.__cause__.code == 401
        rejected.value.__cause__.close()
        assert len(received) == 1
        assert received[0]["token"] == "incorrect-token"


def test_actual_client_preserves_unsupported_expression_fallback_over_tls(monkeypatch, tls_files) -> None:
    _configure_trust(monkeypatch, tls_files)
    with _running_https_verifier(tls_files, response_status=422) as (port, received):
        client = MathVerificationClient(f"https://127.0.0.1:{port}", TOKEN, timeout=10)
        assert client.normalize("synthetic-unsupported-expression") == {
            "status": "uncertain", "reason_code": "unsupported_expression",
        }
        assert len(received) == 1
