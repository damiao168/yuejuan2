import json
import os
import time
from urllib import request as urlrequest


API = os.environ.get("EDUGRADE_E2E_API_BASE_URL", "http://api-gateway:8080").rstrip("/")
AGENT = os.environ.get("EDUGRADE_E2E_AGENT_BASE_URL", "http://grading-agent:8100").rstrip("/")
PASSWORD = os.environ["EDUGRADE_E2E_ADMIN_PASSWORD"]
SEGMENT_ID = "00000000-0000-0000-0000-000000000614"


def call_json(url, method="GET", payload=None, headers=None, timeout=30):
    body = None if payload is None else json.dumps(payload, ensure_ascii=False).encode("utf-8")
    merged = {"Accept": "application/json", **(headers or {})}
    if body is not None:
        merged["Content-Type"] = "application/json"
    req = urlrequest.Request(url, data=body, headers=merged, method=method)
    with urlrequest.urlopen(req, timeout=timeout) as response:
        return response.status, json.loads(response.read().decode("utf-8"))


status, ready = call_json(f"{AGENT}/ready", timeout=10)
assert status == 200 and ready["status"] == "ready", ready

status, login = call_json(
    f"{API}/api/v1/auth/token",
    method="POST",
    payload={"tenant_code": "platform", "username": "platform_admin", "password": PASSWORD, "client_type": "desktop", "device_name": "STORY-060 E2E"},
)
assert status == 200 and login.get("access_token"), login
headers = {"Authorization": f"Bearer {login['access_token']}"}

status, created = call_json(
    f"{API}/api/v1/subjective-grading-batches",
    method="POST",
    payload={"idempotency_key": "story060-real-worker-batch", "segment_ids": [SEGMENT_ID]},
    headers=headers,
)
assert status == 201, created
batch_id = created["batch"]["id"]

status, enqueued = call_json(
    f"{API}/api/v1/subjective-grading-batches/{batch_id}/enqueue",
    method="POST",
    payload={},
    headers=headers,
)
assert status == 200, enqueued
assert enqueued["enqueue_result"] == {
    "requested_count": 1,
    "accepted_count": 1,
    "task_count": 1,
    "failed_count": 0,
    "partial_success": False,
    "failures": [],
}, enqueued

deadline = time.monotonic() + 180
while time.monotonic() < deadline:
    status, current = call_json(
        f"{API}/api/v1/subjective-grading-batches/{batch_id}",
        headers=headers,
    )
    assert status == 200, current
    if current["batch"]["status"] == "completed":
        break
    assert current["batch"]["status"] != "failed", current
    time.sleep(1)
else:
    raise AssertionError("subjective grading batch did not reach a terminal state")

status, grades = call_json(
    f"{API}/api/v1/answer-segments/{SEGMENT_ID}/ai-grades",
    headers=headers,
)
assert status == 200 and len(grades["grades"]) == 1, grades
grade = grades["grades"][0]
assert grade["grader_type"] == "llm_subjective"
# 这里验证真实适配器链路的标记；默认上游仍是协议桩，不能据此认定模型评分质量。
assert grade["mock"] is False
assert grade["needs_human_review"] is True
assert grade["confidence"] == 0
assert grade["suggested_score"] == sum(point["score"] for point in grade["matched_points"])
assert grade["evidence"]
assert all(
    item["answer_text"] == "因为他对家乡有责任感，也希望帮助村里的孩子继续读书。"
    for item in grade["evidence"]
)
raw_output = grade.get("raw_output", {})
assert raw_output["schema_version"] == "grading-agent-v1"
assert raw_output["delivery"] == "teacher_suggestion"
assert raw_output["capability_profile"] == "local-pilot-v1"
assert raw_output["telemetry"]["adapter"] == "local_llama_cpp"
assert 1 <= raw_output["telemetry"]["attempts"] <= 2
assert "tenant_id" not in raw_output
assert "student_id" not in raw_output

print("STORY-060 real batch worker and model-adapter verification passed")
