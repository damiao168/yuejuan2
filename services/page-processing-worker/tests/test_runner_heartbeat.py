from __future__ import annotations

import io
import time
from unittest.mock import Mock

import pytest
from page_processing.api import APIError, Client
from page_processing.config import Config
from page_processing.runner import Runner, _Heartbeat, _LeaseHeartbeat, _NullHeartbeat
from PIL import Image, ImageDraw


def _sheet(marked: str | None = "B") -> bytes:
    image = Image.new("RGB", (160, 70), "white")
    draw = ImageDraw.Draw(image)
    for label, x in (("A", 20), ("B", 70), ("C", 120)):
        draw.rectangle((x, 20, x + 28, 48), outline="black", width=2)
        if label == marked:
            draw.ellipse((x + 6, 26, x + 22, 42), fill="black")
    output = io.BytesIO()
    image.save(output, "PNG")
    return output.getvalue()


def _omr_task() -> dict:
    return {
        "id": "task-1", "lease_token": "lease-1", "task_type": "omr_extract",
        "payload": {
            "exam_id": "exam-1", "omr_run_id": "run-1",
            "source_download_url": "/api/v1/answer-segments/segment-1/image",
            "profile_hash": "sha256:manual-only-test-profile",
            "option_regions": [
                {"label": "A", "x": 20, "y": 20, "width": 28, "height": 28},
                {"label": "B", "x": 70, "y": 20, "width": 28, "height": 28},
                {"label": "C", "x": 120, "y": 20, "width": 28, "height": 28},
            ],
        },
    }


class HeartbeatRecordingClient:
    def __init__(
        self,
        image: bytes,
        *,
        fail_after: int | None = None,
        failure: APIError | None = None,
    ) -> None:
        self.image = image
        self.fail_after = fail_after
        self.failure = failure or APIError("api_request_failed:409", status_code=409)
        self.heartbeats: list[tuple[str, int, float | None]] = []
        self.attempts = 0
        self.completed: tuple[str, dict] | None = None
        self.failed: tuple[str, dict] | None = None

    def claim(self, worker_id: str, limit: int, lease_seconds: int) -> list[dict]:
        return [_omr_task()]

    def heartbeat(self, task: dict, worker_id: str, lease_seconds: int, timeout: float | None = None, progress: dict | None = None) -> None:
        self.attempts += 1
        if self.fail_after is not None and self.attempts > self.fail_after:
            raise self.failure
        self.heartbeats.append((str(task["id"]), lease_seconds, timeout))

    def download(self, path: str) -> bytes:
        return self.image

    def upload_asset(self, task: dict, owner_type: str, owner_id: str, filename: str, data: bytes) -> dict:
        return {"id": "overlay-1", "hash_sha256": "overlay-hash"}

    def complete_omr(self, run_id: str, payload: dict) -> None:
        self.completed = (run_id, payload)

    def fail_omr(self, run_id: str, payload: dict) -> None:
        self.failed = (run_id, payload)


def test_null_heartbeat_accepts_keywords_without_renewal_or_progress_mutation(monkeypatch: pytest.MonkeyPatch) -> None:
    thread_factory = Mock(side_effect=AssertionError("direct processing must not start a renewal thread"))
    monkeypatch.setattr("page_processing.runner.threading.Thread", thread_factory)
    heartbeat: _Heartbeat = _NullHeartbeat()
    progress = {"stage": "page_decode", "completed": 1}

    heartbeat.set_progress(progress=progress, flush=True)
    heartbeat.set_progress(progress=progress)
    heartbeat.raise_if_failed()
    heartbeat.stop()
    heartbeat.stop(raise_on_error=True)
    heartbeat.stop(raise_on_error=False)

    assert progress == {"stage": "page_decode", "completed": 1}
    thread_factory.assert_not_called()


def test_lease_heartbeat_progress_contract_keeps_deferred_and_immediate_sends() -> None:
    client = Mock(spec=Client)
    task = _omr_task()
    heartbeat = _LeaseHeartbeat(
        client=client, task=task, worker_id="worker-1",
        interval=30.0, lease_seconds=600, request_timeout=1.0,
    )
    contract: _Heartbeat = heartbeat
    progress = {"stage": "page_decode", "completed": 1}
    contract.set_progress(progress=progress)
    client.heartbeat.assert_not_called()
    progress["completed"] = 2

    # 延迟进度在首次续租时发送保存的副本；刷新则立即发送新进度。
    with heartbeat:
        client.heartbeat.assert_called_once_with(task, "worker-1", 600, timeout=1.0, progress={
            "stage": "page_decode", "completed": 1,
        })
        contract.set_progress(progress=progress, flush=True)
        assert client.heartbeat.call_count == 2
        client.heartbeat.assert_called_with(task, "worker-1", 600, timeout=1.0, progress=progress)


def test_lease_heartbeat_renews_in_background() -> None:
    client = HeartbeatRecordingClient(_sheet())
    with _LeaseHeartbeat(
        client=client, task=_omr_task(), worker_id="worker-1",
        interval=0.02, lease_seconds=600, request_timeout=1.0,
    ) as heartbeat:
        deadline = time.monotonic() + 2.0
        while len(client.heartbeats) < 3 and time.monotonic() < deadline:
            time.sleep(0.01)
        heartbeat.raise_if_failed()
    assert len(client.heartbeats) >= 3
    assert client.heartbeats[0] == ("task-1", 600, 1.0)


def test_lease_heartbeat_failure_surfaces_via_raise_if_failed() -> None:
    client = HeartbeatRecordingClient(_sheet(), fail_after=1)
    heartbeat = _LeaseHeartbeat(
        client=client, task=_omr_task(), worker_id="worker-1",
        interval=0.02, lease_seconds=600, request_timeout=1.0,
    )
    heartbeat.__enter__()
    deadline = time.monotonic() + 2.0
    while time.monotonic() < deadline:
        try:
            heartbeat.raise_if_failed()
        except APIError:
            break
        time.sleep(0.01)
    with pytest.raises(APIError, match="409"):
        heartbeat.stop()


def test_run_once_keeps_lease_alive_and_completes_omr() -> None:
    client = HeartbeatRecordingClient(_sheet())
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1", heartbeat_interval=0.02, heartbeat_timeout=1.0))

    assert runner.run_once() == 1

    assert client.completed is not None
    run_id, result = client.completed
    assert run_id == "run-1"
    assert result["selected"] == ["B"]
    # 上下文在处理器启动前发送首次续租请求。
    assert len(client.heartbeats) >= 1


def test_run_once_reports_failure_and_survives_lost_lease(capsys: pytest.CaptureFixture[str]) -> None:
    client = HeartbeatRecordingClient(_sheet(), fail_after=0)
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1", heartbeat_interval=0.02, heartbeat_timeout=1.0))

    # 首次心跳在 __enter__ 内抛出异常，因此任务被报告为失败。
    assert runner.run_once() == 1

    assert client.completed is None
    assert client.failed is not None
    run_id, payload = client.failed
    assert run_id == "run-1"
    assert payload["retryable"] is True


def test_run_once_does_not_crash_when_fail_report_also_fails(capsys: pytest.CaptureFixture[str]) -> None:
    class BrokenFailClient(HeartbeatRecordingClient):
        def fail_omr(self, run_id: str, payload: dict) -> None:
            raise APIError("api_request_failed:409", status_code=409)

    client = BrokenFailClient(_sheet(), fail_after=0)
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1", heartbeat_interval=0.02, heartbeat_timeout=1.0))

    assert runner.run_once() == 1

    stderr = capsys.readouterr().err
    # 原始失败和报告失败都必须能从日志中恢复查看，
    # 因为两者都没有送达服务端。
    assert "fail-report also failed" in stderr
    assert "failed: APIError" in stderr


def test_config_rejects_heartbeat_slower_than_lease() -> None:
    with pytest.raises(ValueError, match="shorter than lease_seconds"):
        Config("http://api", "demo", "worker", "secret", "worker-1", lease_seconds=30, heartbeat_interval=30.0, heartbeat_timeout=10.0)


def test_config_rejects_lease_outside_runtime_bounds() -> None:
    # 运行时会将这些值静默调整为 300 秒，因此越界配置
    # 会使工作进程的续租间隔远大于实际持有租约的有效期。
    with pytest.raises(ValueError, match="between 30 and 3600"):
        Config("http://api", "demo", "worker", "secret", "worker-1", lease_seconds=7200, heartbeat_interval=600.0)
    with pytest.raises(ValueError, match="between 30 and 3600"):
        Config("http://api", "demo", "worker", "secret", "worker-1", lease_seconds=10, heartbeat_interval=1.0, heartbeat_timeout=1.0)


def test_transient_heartbeat_failure_is_retried_not_fatal() -> None:
    # 单次 503 不能丢弃正在处理的文档：上一次续租
    # 在几乎整个租约窗口内仍然有效。
    client = HeartbeatRecordingClient(
        _sheet(), fail_after=1, failure=APIError("api_request_failed:503", status_code=503)
    )
    with _LeaseHeartbeat(
        client=client, task=_omr_task(), worker_id="worker-1",
        interval=0.02, lease_seconds=600, request_timeout=1.0,
    ) as heartbeat:
        deadline = time.monotonic() + 2.0
        while client.attempts < 4 and time.monotonic() < deadline:
            time.sleep(0.01)
        # 暂时失败后继续发送心跳，不把错误锁定为永久状态。
        assert client.attempts >= 4, "heartbeat thread must keep retrying after a transient failure"
        heartbeat.raise_if_failed()


def test_lost_lease_aborts_immediately() -> None:
    for status in (403, 404, 409):
        client = HeartbeatRecordingClient(
            _sheet(), fail_after=1, failure=APIError(f"api_request_failed:{status}", status_code=status)
        )
        heartbeat = _LeaseHeartbeat(
            client=client, task=_omr_task(), worker_id="worker-1",
            interval=0.02, lease_seconds=600, request_timeout=1.0,
        )
        heartbeat.__enter__()
        deadline = time.monotonic() + 2.0
        failed = False
        while time.monotonic() < deadline:
            try:
                heartbeat.raise_if_failed()
            except APIError:
                failed = True
                break
            time.sleep(0.01)
        assert failed, f"status {status} must be treated as a lost lease"
        with pytest.raises(APIError):
            heartbeat.stop()


def test_stop_can_ignore_heartbeat_error_so_completion_still_reports() -> None:
    client = HeartbeatRecordingClient(
        _sheet(), fail_after=1, failure=APIError("api_request_failed:409", status_code=409)
    )
    heartbeat = _LeaseHeartbeat(
        client=client, task=_omr_task(), worker_id="worker-1",
        interval=0.02, lease_seconds=600, request_timeout=1.0,
    )
    heartbeat.__enter__()
    deadline = time.monotonic() + 2.0
    while time.monotonic() < deadline:
        try:
            heartbeat.raise_if_failed()
        except APIError:
            break
        time.sleep(0.01)

    heartbeat.stop(raise_on_error=False)  # 不能抛出异常；服务端会重新校验租约。
