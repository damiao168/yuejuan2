from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import ClassVar
from unittest.mock import patch

from subjective_grading_worker.__main__ import run_cycle
from subjective_grading_worker.api import EduGradeClient
from subjective_grading_worker.config import Settings
from subjective_grading_worker.runner import Runner


class _Response:
    def __init__(self):
        self.headers = {"Content-Type": "application/json"}

    def __enter__(self):
        return self

    def __exit__(self, _exc_type, _exc, _traceback):
        return None

    def read(self, amount=-1) -> bytes:
        return b'{"output":{"request_id":"request-1"}}'[:amount]


def test_execute_uses_the_long_running_request_budget() -> None:
    client = EduGradeClient("http://127.0.0.1:8080", "demo", "worker", "secret", execute_timeout=810)

    with patch("subjective_grading_worker.api.request.urlopen", return_value=_Response()) as urlopen:
        client.execute("run-1", "task-1", "lease-1")

    assert urlopen.call_args.kwargs["timeout"] == 810


class _Handler(BaseHTTPRequestHandler):
    requests: ClassVar[list[dict[str, object]]] = []

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length).decode("utf-8") or "{}")
        self.requests.append({"path": self.path, "headers": dict(self.headers), "payload": payload})

        if self.path == "/api/v1/auth/token":
            response = {"access_token": "test-access-token"}
        elif self.path == "/api/v1/internal/worker/tasks/claim":
            response = {
                "tasks": [
                    {
                        "id": "task-1",
                        "source_id": "run-1",
                        "source_type": "subjective_grading_run",
                        "lease_token": "lease-1",
                    }
                ]
            }
        elif self.path.endswith("/execute"):
            response = {"output": {"request_id": "request-1", "model_version": "model-v1"}}
        else:
            response = {}

        body = json.dumps(response).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format: str, *_args: object) -> None:
        return


# 本地 HTTP 服务器验证登录、认领、心跳、执行及结果写回的协议顺序，不衡量模型质量。
def test_worker_protocol_smoke_from_login_to_persisted_result(tmp_path):
    _Handler.requests = []
    server = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        settings = Settings(
            api_base_url=f"http://127.0.0.1:{server.server_port}",
            tenant_code="demo",
            username="worker",
            password="secret",
            heartbeat_interval=1,
            heartbeat_timeout=0.5,
            health_file=str(tmp_path / "worker.ready"),
        )
        api = EduGradeClient(settings.api_base_url, settings.tenant_code, settings.username, settings.password)

        assert run_cycle(api, Runner(api=api, settings=settings), settings) == 1
    finally:
        server.shutdown()
        thread.join(timeout=2)
        server.server_close()

    paths = [str(item["path"]) for item in _Handler.requests]
    assert paths == [
        "/api/v1/auth/token",
        "/api/v1/internal/worker/tasks/claim",
        "/api/v1/internal/worker/tasks/task-1/heartbeat",
        "/api/v1/internal/subjective-grading/runs/run-1/execute",
        "/api/v1/internal/subjective-grading/runs/run-1/result",
    ]
    result_headers = _Handler.requests[-1]["headers"]
    assert isinstance(result_headers, dict)
    normalized_headers = {str(key).lower(): value for key, value in result_headers.items()}
    assert normalized_headers["authorization"] == "Bearer test-access-token"
    assert normalized_headers["x-edugrade-worker-task-id"] == "task-1"
    assert normalized_headers["x-edugrade-worker-lease-token"] == "lease-1"
