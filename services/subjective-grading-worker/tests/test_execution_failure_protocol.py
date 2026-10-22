"""本地错误信封验证网关已结算事实，避免 worker 再次安排推理。"""

import io
import json
from unittest.mock import patch
from urllib.error import HTTPError

import pytest

from subjective_grading_worker.api import APIError, AuthenticationError, EduGradeClient
from subjective_grading_worker.config import Settings
from subjective_grading_worker.runner import Runner


@pytest.mark.parametrize("code", [
    "ai_output_incomplete", "ai_service_unavailable", "math_human_review_required",
    "math_evidence_unavailable", "math_evidence_version_conflict", "invalid_model_output",
])
def test_execute_accepts_server_recorded_failure_for_current_endpoint(code):
    body = {"error": {"code": code, "message": "private model response"},
            "task_failure_recorded": True, "retryable": False}
    client = EduGradeClient("http://127.0.0.1:8080", "demo", "worker", "secret")
    error = HTTPError(client.base_url, 502, "failed", {}, io.BytesIO(json.dumps(body).encode()))
    with patch("subjective_grading_worker.api._open_request", side_effect=error):
        with pytest.raises(APIError) as caught:
            client.execute("run", "task", "lease")
    assert caught.value.task_failure_recorded is True
    assert caught.value.retryable is False
    assert caught.value.code == code
    assert "private" not in str(caught.value)


@pytest.mark.parametrize("body", [
    b"{", b"x" * 65537,
    json.dumps({"error": {"code": []}, "task_failure_recorded": True}).encode(),
    json.dumps({"error": {"code": "untrusted"}, "task_failure_recorded": True}).encode(),
    json.dumps({"error": {"code": "ai_output_incomplete"}, "task_failure_recorded": "true"}).encode(),
], ids=["malformed", "oversize", "non_string_code", "unknown_code", "non_boolean_marker"])
def test_untrusted_or_missing_recorded_marker_keeps_failure_callback(body):
    client = EduGradeClient("http://127.0.0.1:8080", "demo", "worker", "secret")
    error = HTTPError(client.base_url, 502, "failed", {}, io.BytesIO(body))
    with patch("subjective_grading_worker.api._open_request", side_effect=error):
        with pytest.raises(APIError) as caught:
            client.execute("run", "task", "lease")
    assert caught.value.task_failure_recorded is False


def test_non_execute_endpoint_cannot_suppress_failure_callback():
    client = EduGradeClient("http://127.0.0.1:8080", "demo", "worker", "secret")
    body = b'{"error":{"code":"ai_output_incomplete"},"task_failure_recorded":true}'
    error = HTTPError(client.base_url, 502, "failed", {}, io.BytesIO(body))
    with patch("subjective_grading_worker.api._open_request", side_effect=error):
        with pytest.raises(APIError) as caught:
            client.complete("run", "task", "lease", {}, 0)
    assert caught.value.task_failure_recorded is False


def test_unauthorized_does_not_schedule_paid_retry():
    client = EduGradeClient("http://127.0.0.1:8080", "demo", "worker", "secret")
    error = HTTPError(client.base_url, 401, "unauthorized", {}, io.BytesIO(b"{}"))
    with patch("subjective_grading_worker.api._open_request", side_effect=error):
        with pytest.raises(AuthenticationError) as caught:
            client.execute("run", "task", "lease")
    assert caught.value.retryable is False


@pytest.mark.parametrize("recorded", [True, False])
def test_runner_skips_duplicate_failure_only_after_server_commit(recorded):
    class API:
        calls = []

        def claim(self, *_args):
            return [{"id": "task", "source_id": "run", "source_type": "subjective_grading_run", "lease_token": "lease"}]

        def heartbeat(self, *_args):
            pass

        def execute(self, *_args):
            raise APIError("failed", code="ai_output_incomplete", retryable=False, task_failure_recorded=recorded)

        def complete(self, *_args):
            self.calls.append(("complete",))

        def fail(self, *args):
            self.calls.append(("fail", *args))

    api = API()
    settings = Settings("http://127.0.0.1:8080", "demo", "worker", "secret", heartbeat_interval=1, heartbeat_timeout=.5)
    assert Runner(api=api, settings=settings).process_once() == 1
    assert not any(call[0] == "complete" for call in api.calls)
    assert len(api.calls) == (0 if recorded else 1)
    if not recorded:
        assert api.calls[0][4:6] == ("ai_output_incomplete", False)
