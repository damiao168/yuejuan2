from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any
from urllib import error, request
from urllib.parse import urlsplit

from edugrade_worker_runtime import ResponseValidationError, read_json_response


class APIError(RuntimeError):
    def __init__(self, message: str, *, status_code: int | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code


class AuthenticationError(APIError):
    pass


@dataclass
class EduGradeClient:
    base_url: str
    tenant_code: str
    username: str
    password: str
    token: str | None = None
    worker_instance_id: str | None = None
    current_task: dict[str, str] | None = None
    execute_timeout: float = 810.0

    def login(self) -> None:
        response = self._request("POST", "/api/v1/auth/token", {"tenant_code": self.tenant_code, "username": self.username, "password": self.password, "client_type": "service", "device_name": "Subjective Grading Worker"}, auth=False)
        self.token = str(response.get("access_token", ""))
        if not self.token:
            raise APIError("login response did not include access_token")

    def claim(self, worker_id: str, lease_seconds: int) -> list[dict[str, Any]]:
        self.worker_instance_id = worker_id
        response = self._request("POST", "/api/v1/internal/worker/tasks/claim", {"queue_name": "subjective-grading", "worker_service": "subjective-grading-worker", "worker_instance_id": worker_id, "limit": 1, "lease_seconds": lease_seconds})
        tasks = response.get("tasks", [])
        return tasks if isinstance(tasks, list) else []

    def activate_task(self, task: dict[str, Any], worker_id: str | None = None) -> None:
        task_id = str(task.get("id") or "").strip()
        lease_token = str(task.get("lease_token") or "").strip()
        worker_instance_id = str(worker_id or self.worker_instance_id or "").strip()
        if not task_id or not lease_token or not worker_instance_id:
            raise APIError("subjective grading task is missing its task capability")
        self.current_task = {
            "task_id": task_id,
            "lease_token": lease_token,
            "worker_service": "subjective-grading-worker",
            "worker_instance_id": worker_instance_id,
        }

    def heartbeat(self, task_id: str, token: str, worker_id: str, lease_seconds: int, timeout: float) -> None:
        self.current_task = {
            "task_id": task_id,
            "lease_token": token,
            "worker_service": "subjective-grading-worker",
            "worker_instance_id": worker_id,
        }
        self._request("POST", f"/api/v1/internal/worker/tasks/{task_id}/heartbeat", {"lease_token": token, "worker_service": "subjective-grading-worker", "worker_instance_id": worker_id, "state": "running", "lease_seconds": lease_seconds}, timeout=timeout)

    def execute(self, run_id: str, task_id: str, lease_token: str) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/api/v1/internal/subjective-grading/runs/{run_id}/execute",
            {"task_id": task_id, "lease_token": lease_token},
            timeout=self.execute_timeout,
        )

    def complete(self, run_id: str, task_id: str, lease_token: str, output: dict[str, Any], duration_ms: int) -> None:
        self._request("POST", f"/api/v1/internal/subjective-grading/runs/{run_id}/result", {"task_id": task_id, "lease_token": lease_token, "result_schema_version": "subjective-grade-result-v1", "duration_ms": duration_ms, "output": output})

    def fail(self, run_id: str, task_id: str, lease_token: str, code: str, retryable: bool, duration_ms: int) -> None:
        self._request("POST", f"/api/v1/internal/subjective-grading/runs/{run_id}/failure", {"task_id": task_id, "lease_token": lease_token, "retryable": retryable, "error_code": code, "error_detail": {}, "duration_ms": duration_ms})

    def fail_task(self, task_id: str, lease_token: str, code: str, retryable: bool) -> None:
        self._request("POST", f"/api/v1/internal/worker/tasks/{task_id}/fail", {"lease_token": lease_token, "retryable": retryable, "error_code": code, "error_detail": {}, "duration_ms": 0})

    def _request(self, method: str, path: str, payload: dict[str, Any], *, auth: bool = True, timeout: float = 60) -> dict[str, Any]:
        url = _trusted_url(self.base_url, path)
        headers = {"Accept": "application/json", "Content-Type": "application/json"}
        if auth and self.token:
            headers["Authorization"] = f"Bearer {self.token}"
            # 服务账号会话与当前租约能力同时发送，结果写回绑定已认领的任务。
            if self.current_task:
                headers.update(_task_capability_headers(self.current_task))
        req = request.Request(url, data=json.dumps(payload).encode(), headers=headers, method=method)
        try:
            with request.urlopen(req, timeout=timeout) as response:
                return json.loads(read_json_response(response).decode() or "{}")
        except error.HTTPError as exc:
            if exc.code == 401:
                raise AuthenticationError(f"unauthorized: {exc.code}", status_code=exc.code) from exc
            raise APIError(f"api request failed: {exc.code}", status_code=exc.code) from exc
        except (OSError, ResponseValidationError, ValueError) as exc:
            raise APIError("api request failed") from exc


def _task_capability_headers(task: dict[str, str]) -> dict[str, str]:
    return {
        "X-EduGrade-Worker-Task-ID": task["task_id"],
        "X-EduGrade-Worker-Lease-Token": task["lease_token"],
        "X-EduGrade-Worker-Service": task["worker_service"],
        "X-EduGrade-Worker-Instance-ID": task["worker_instance_id"],
    }


def _trusted_url(base_url: str, path: str) -> str:
    base = urlsplit(base_url)
    target = urlsplit(f"{base_url.rstrip('/')}/{path.lstrip('/')}")
    if (target.scheme, target.hostname, target.port) != (base.scheme, base.hostname, base.port) or target.username or target.password or target.fragment:
        raise APIError("service URL must remain on configured API origin")
    return target.geturl()
