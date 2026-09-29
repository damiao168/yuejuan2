from __future__ import annotations

import json
import multiprocessing
import os
import ssl
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from queue import Empty

from .parser import UnsupportedExpression, parse_restricted_latex
from .verifier import (
    VerificationError,
    normalize,
    solve,
    verify_equivalence,
    verify_transition,
)

MAX_BODY = 256 * 1024


def _execute(operation: str, payload: dict[str, object]) -> dict[str, object]:
    if operation == "__test_sleep__":
        time.sleep(float(payload.get("seconds", 0)))
        return {"status": "completed"}
    domain = payload.get("domain", {})
    if not isinstance(domain, dict):
        raise TypeError("domain must be an object")
    if operation == "normalize":
        ast = payload.get("ast") or parse_restricted_latex(str(payload["latex"]))
        if not isinstance(ast, dict):
            raise TypeError("ast must be an object")
        return {"ast": ast, **normalize(ast, domain)}
    if operation == "verify-equivalence":
        return verify_equivalence(payload["left_ast"], payload["right_ast"], domain).__dict__
    if operation == "verify-transition":
        return verify_transition(payload["previous_ast"], payload["next_ast"], domain).__dict__
    if operation == "solve":
        return solve(payload["ast"], str(payload["variable"]), domain)
    raise ValueError("unknown operation")


def _process_entry(queue: multiprocessing.Queue, operation: str, payload: dict[str, object]) -> None:
    try:
        queue.put(("ok", _execute(operation, payload)))
    except Exception as exc:  # noqa: BLE001 - subprocess converts all symbolic failures to bounded data.
        queue.put(("error", exc.__class__.__name__, str(exc)))


def run_bounded(operation: str, payload: dict[str, object], timeout_seconds: float) -> dict[str, object]:
    deadline = time.monotonic() + max(0, timeout_seconds)
    # 请求运行在 HTTP 线程中；使用 spawn，避免 fork 继承线程锁和进程状态。
    context = multiprocessing.get_context("spawn")
    queue = context.Queue(maxsize=1)
    process = context.Process(target=_process_entry, args=(queue, operation, payload), daemon=True)
    started = False
    try:
        process.start()
        started = True
        try:
            # 先读取结果再 join；结果超过系统管道容量时，子进程必须等父进程消费，不能先互相等待。
            item = queue.get(timeout=max(0, deadline - time.monotonic()))
        except Empty:
            reason = "verification_timeout" if process.is_alive() else "verification_failed"
            return {"status": "uncertain", "relation": "unknown", "reason_code": reason}
        process.join(max(0, deadline - time.monotonic()))
        if process.is_alive():
            return {"status": "uncertain", "relation": "unknown", "reason_code": "verification_timeout"}
        if item[0] == "ok":
            return item[1]
        raise VerificationError(item[2])
    # 预算耗尽仍需回收子进程；清理等待不属于数学运算的可用预算。
    finally:
        if started and process.is_alive():
            process.terminate()
            process.join(1)
            if process.is_alive():
                process.kill()
                process.join(1)
        if not started or not process.is_alive():
            process.close()
        queue.close()
        queue.join_thread()


class Handler(BaseHTTPRequestHandler):
    server_version = "EduGradeMathVerify/1"

    def do_GET(self) -> None:
        if self.path == "/healthz":
            self._json(200, {"status": "ok"})
            return
        self._json(404, {"error": "not_found"})

    def do_POST(self) -> None:
        token = os.environ.get("EDUGRADE_MATH_VERIFY_TOKEN", "")
        if len(token) < 32 or self.headers.get("X-EduGrade-Internal-Token") != token:
            self._json(401, {"error": "unauthorized"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > MAX_BODY:
                raise ValueError("invalid body size")
            payload = json.loads(self.rfile.read(length))
            if not isinstance(payload, dict):
                raise TypeError("request body must be an object")
            operations = {
                "/internal/math/normalize": "normalize",
                "/internal/math/verify-equivalence": "verify-equivalence",
                "/internal/math/verify-transition": "verify-transition",
                "/internal/math/solve": "solve",
            }
            operation = operations.get(self.path)
            if operation is None:
                self._json(404, {"error": "not_found"})
                return
            result = run_bounded(operation, payload, float(os.environ.get("EDUGRADE_MATH_VERIFY_TIMEOUT_SECONDS", "3")))
            self._json(200, result)
        except (KeyError, TypeError, ValueError, UnsupportedExpression, VerificationError) as exc:
            self._json(422, {"status": "uncertain", "reason_code": "unsupported_expression", "message": str(exc)})
        except Exception:  # noqa: BLE001 - symbolic failures must fail closed.
            self._json(200, {"status": "uncertain", "relation": "unknown", "reason_code": "verification_failed"})

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def _json(self, status: int, payload: object) -> None:
        raw = json.dumps(payload, ensure_ascii=False, default=list).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main() -> None:
    port = int(os.environ.get("EDUGRADE_MATH_VERIFY_PORT", "8092"))
    cert_file = os.environ.get("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "").strip()
    key_file = os.environ.get("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", "").strip()
    if bool(cert_file) != bool(key_file):
        raise ValueError("math verifier TLS certificate and key must be configured together")
    environment = os.environ.get("EDUGRADE_ENV", "development").strip().lower()
    if environment not in {"", "local", "development", "dev", "test"} and not cert_file:
        raise ValueError("math verifier TLS certificate and key are required in production-like environments")
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    if cert_file:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(cert_file, key_file)
        server.socket = context.wrap_socket(server.socket, server_side=True)
    server.serve_forever()
