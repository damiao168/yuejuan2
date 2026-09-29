from __future__ import annotations

import time
from typing import Any

from edugrade_worker_runtime import LeaseHeartbeat

from .api import APIError
from .config import Settings


class Runner:
    def __init__(self, *, api: Any, settings: Settings) -> None:
        self.api = api
        self.settings = settings

    def process_once(self) -> int:
        processed = 0
        for task in self.api.claim(self.settings.worker_id, self.settings.lease_seconds):
            self._process(task)
            processed += 1
        return processed

    def _process(self, task: dict[str, Any]) -> None:
        started = time.monotonic()
        runtime_id = str(task.get("id", ""))
        lease = str(task.get("lease_token", ""))
        run_id = str(task.get("source_id", ""))
        activate_task = getattr(self.api, "activate_task", None)
        if callable(activate_task):
            activate_task(task, self.settings.worker_id)
        if task.get("source_type") != "subjective_grading_run" or not runtime_id or not lease or not run_id:
            fail_task = getattr(self.api, "fail_task", None)
            if not runtime_id or not lease:
                raise APIError("subjective grading task is missing its runtime capability")
            if callable(fail_task):
                fail_task(runtime_id, lease, "invalid_subjective_runtime_payload", False)
            else:
                self.api.fail(run_id, runtime_id, lease, "invalid_subjective_runtime_payload", False, 0)
            return
        with _Heartbeat(self.api, runtime_id, lease, self.settings) as heartbeat:
            try:
                heartbeat.raise_if_failed()
                response = self.api.execute(run_id, runtime_id, lease)
                output = response.get("output")
                if not isinstance(output, dict):
                    raise APIError("subjective agent response did not include output")
                # 模型执行期间可能丢失租约；写回前再次检查，不能把旧任务结果当作当前执行完成。
                heartbeat.raise_if_failed()
                self.api.complete(run_id, runtime_id, lease, output, int((time.monotonic() - started) * 1000))
            except APIError:
                heartbeat.stop()
                self.api.fail(run_id, runtime_id, lease, "subjective_agent_failed", True, int((time.monotonic() - started) * 1000))


class _Heartbeat(LeaseHeartbeat):
    def __init__(self, api: Any, task_id: str, token: str, settings: Settings) -> None:
        self.api, self.task_id, self.token, self.settings = api, task_id, token, settings
        super().__init__(
            send=self._send,
            interval=settings.heartbeat_interval,
            lease_seconds=settings.lease_seconds,
            request_timeout=settings.heartbeat_timeout,
            thread_name=f"subjective-grading-heartbeat-{settings.worker_id}",
        )

    def _send(self) -> None:
        self.api.heartbeat(self.task_id, self.token, self.settings.worker_id, self.settings.lease_seconds, self.settings.heartbeat_timeout)
