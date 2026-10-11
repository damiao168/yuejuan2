from __future__ import annotations

import json

import pytest
from image_quality.api import APIError, EduGradeImageQualityClient


def test_download_rejects_cross_origin_url_before_sending_worker_token() -> None:
    client = EduGradeImageQualityClient(
        "http://api-gateway:8080", "demo", "worker", "secret", token="worker-token", worker_instance_id="worker-1"
    )

    with pytest.raises(APIError, match="configured API origin"):
        client.download("https://attacker.invalid/collect")


def test_heartbeat_renews_runtime_and_source_lease(monkeypatch) -> None:
    captured: dict = {}

    class Response:
        def __init__(self):
            self.headers = {"Content-Type": "application/json"}

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return None

        def read(self, amount=-1) -> bytes:
            return b"{}"[:amount]

    def urlopen(req, timeout):
        captured.update(url=req.full_url, timeout=timeout, payload=json.loads(req.data))
        return Response()

    monkeypatch.setattr("image_quality.api._open_request", urlopen)
    client = EduGradeImageQualityClient(
        "http://api-gateway:8080", "demo", "worker", "secret", token="worker-token", worker_instance_id="worker-1"
    )
    job = {"runtime_task_id": "task-1", "lease_token": "lease-1"}
    client.activate_job(job | {"run_id": "run-1"})

    client.heartbeat(job, "worker-1", 300, 3)

    assert captured["url"].endswith("/api/v1/internal/worker/tasks/task-1/heartbeat")
    assert captured["timeout"] == 3
    assert captured["payload"]["lease_token"] == "lease-1"
    assert captured["payload"]["lease_seconds"] == 300
