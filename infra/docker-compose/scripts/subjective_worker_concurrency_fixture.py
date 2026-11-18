"""Runs the existing Story 060 text-grading requests for a synthetic batch."""

from __future__ import annotations

import json
import os
import sys
import time

import story060_grading_agent as api
import story060_model_stub as model


def serve_model() -> None:
    delay = float(os.environ["EDUGRADE_E2E_MODEL_DELAY"])
    original = model._grading_output

    def with_delay(payload):
        result = original(payload)
        # 固定延迟保留真实模型适配器的排队行为，让两组使用相同的外部执行耗时。
        time.sleep(delay)
        return result

    model._grading_output = with_delay
    model.ThreadingHTTPServer(("0.0.0.0", 8087), model.Handler).serve_forever()


def enqueue(command: str, segment_ids: list[str]) -> None:
    status, login = api.call_json(
        f"{api.API}/api/v1/auth/token",
        method="POST",
        payload={
            "tenant_code": "platform",
            "username": "platform_admin",
            "password": os.environ["EDUGRADE_E2E_ADMIN_PASSWORD"],
            "client_type": "desktop",
            "device_name": "Subjective concurrency fixture",
        },
    )
    assert status == 200 and login.get("access_token")
    headers = {"Authorization": f"Bearer {login['access_token']}"}
    base = f"{api.API}/api/v1/subjective-grading-batches"
    status, created = api.call_json(
        base, method="POST", headers=headers,
        payload={"idempotency_key": command, "segment_ids": segment_ids},
    )
    batch = created["batch"]
    assert status == 201 and batch["total_count"] == len(segment_ids), created
    status, queued = api.call_json(
        f"{base}/{batch['id']}/enqueue", method="POST", headers=headers, payload={},
    )
    result = queued["enqueue_result"]
    assert status == 200 and result["failed_count"] == 0, result
    assert result["task_count"] == len(segment_ids), result
    print(batch["id"])


if __name__ == "__main__":
    if sys.argv[1:] == ["serve"]:
        serve_model()
    else:
        command, segments = sys.argv[1:]
        enqueue(command, json.loads(segments))
