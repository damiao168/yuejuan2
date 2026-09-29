from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any
from urllib import error, request
from urllib.parse import urlsplit

from edugrade_worker_runtime import (
    MAX_DOCUMENT_RESPONSE_BYTES,
    ResponseValidationError,
    read_bounded,
    read_json_response,
)

PAPER_IMPORT_COMPLETION_TIMEOUT_SECONDS = 720


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

    def login(self) -> None:
        response = self._request(
            "POST",
            "/api/v1/auth/token",
            {
                "tenant_code": self.tenant_code,
                "username": self.username,
                "password": self.password,
                "client_type": "service",
                "device_name": "OCR Worker",
            },
            require_auth=False,
        )
        token = response.get("access_token")
        if not isinstance(token, str) or not token:
            raise AuthenticationError("login response did not include access_token")
        self.token = token

    def list_pending(self, limit: int) -> list[dict[str, Any]]:
        response = self._request("GET", f"/api/v1/ocr-tasks/pending?limit={limit}")
        tasks = response.get("tasks", [])
        return tasks if isinstance(tasks, list) else []

    def claim_tasks(self, worker_instance_id: str, limit: int, lease_seconds: int) -> list[dict[str, Any]]:
        self.worker_instance_id = worker_instance_id
        response = self._request(
            "POST",
            "/api/v1/internal/worker/tasks/claim",
            {
                "queue_name": "ocr",
                "worker_service": "ocr-worker",
                "worker_instance_id": worker_instance_id,
                "limit": limit,
                "lease_seconds": lease_seconds,
            },
        )
        tasks = response.get("tasks", [])
        return tasks if isinstance(tasks, list) else []

    def claim_math_tasks(self, worker_instance_id: str, lease_seconds: int) -> list[dict[str, Any]]:
        self.worker_instance_id = worker_instance_id
        response = self._request(
            "POST",
            "/api/v1/internal/worker/tasks/claim",
            {
                "queue_name": "math-understanding",
                "worker_service": "ocr-worker",
                "worker_instance_id": worker_instance_id,
                "limit": 1,
                "lease_seconds": lease_seconds,
            },
        )
        tasks = response.get("tasks", [])
        return tasks if isinstance(tasks, list) else []

    def claim_paper_formula_tasks(self, worker_instance_id: str, lease_seconds: int) -> list[dict[str, Any]]:
        self.worker_instance_id = worker_instance_id
        response = self._request("POST", "/api/v1/internal/worker/tasks/claim", {
            "queue_name": "paper-formula", "worker_service": "ocr-worker",
            "worker_instance_id": worker_instance_id, "limit": 1, "lease_seconds": lease_seconds,
        })
        tasks = response.get("tasks", [])
        return tasks if isinstance(tasks, list) else []

    def claim_math_verification_tasks(self, worker_instance_id: str, lease_seconds: int) -> list[dict[str, Any]]:
        self.worker_instance_id = worker_instance_id
        response = self._request("POST", "/api/v1/internal/worker/tasks/claim", {
            "queue_name": "math-verification", "worker_service": "ocr-worker",
            "worker_instance_id": worker_instance_id, "limit": 1, "lease_seconds": lease_seconds,
        })
        tasks = response.get("tasks", [])
        return tasks if isinstance(tasks, list) else []

    def get_math_verification_input(self, task_id: str, tenant_id: str) -> dict[str, Any]:
        return self._request("GET", f"/api/v1/internal/math-verification/tasks/{task_id}/input", tenant_id=tenant_id)

    def complete_math_verification_task(self, task_id: str, payload: dict[str, Any], tenant_id: str) -> None:
        self._request("POST", f"/api/v1/internal/math-verification/tasks/{task_id}/complete", payload, tenant_id=tenant_id)

    def fail_math_verification_task(self, task_id: str, payload: dict[str, Any], tenant_id: str) -> None:
        self._request("POST", f"/api/v1/internal/math-verification/tasks/{task_id}/fail", payload, tenant_id=tenant_id)

    def get_math_task_input(self, runtime_task_id: str, tenant_id: str | None = None) -> dict[str, Any]:
        return self._request("GET", f"/api/v1/internal/math-understanding/tasks/{runtime_task_id}/input", tenant_id=tenant_id)

    def complete_math_task(
        self,
        runtime_task_id: str,
        lease_token: str,
        artifact: dict[str, Any],
        duration_ms: int,
        tenant_id: str | None = None,
    ) -> None:
        self._request(
            "POST",
            f"/api/v1/internal/math-understanding/tasks/{runtime_task_id}/complete",
            {"lease_token": lease_token, "duration_ms": duration_ms, "artifact": artifact},
            tenant_id=tenant_id,
        )

    def fail_runtime_task(
        self,
        runtime_task_id: str,
        lease_token: str,
        error_code: str,
        retryable: bool,
        duration_ms: int,
        tenant_id: str | None = None,
    ) -> None:
        self._request(
            "POST",
            f"/api/v1/internal/worker/tasks/{runtime_task_id}/fail",
            {
                "lease_token": lease_token,
                "retryable": retryable,
                "error_code": error_code,
                "error_detail": {},
                "duration_ms": duration_ms,
            },
            tenant_id=tenant_id,
        )

    def activate_task(self, task: dict[str, Any], worker_instance_id: str | None = None) -> None:
        task_id = str(task.get("id") or "").strip()
        lease_token = str(task.get("lease_token") or "").strip()
        instance_id = str(worker_instance_id or self.worker_instance_id or "").strip()
        if not task_id or not lease_token or not instance_id:
            raise APIError("OCR task is missing its task capability")
        self.current_task = {
            "task_id": task_id,
            "lease_token": lease_token,
            "worker_service": "ocr-worker",
            "worker_instance_id": instance_id,
        }

    def heartbeat_task(
        self,
        runtime_task_id: str,
        lease_token: str,
        worker_instance_id: str,
        lease_seconds: int,
        timeout_seconds: float,
        tenant_id: str | None = None,
        progress: dict[str, Any] | None = None,
    ) -> None:
        self.current_task = {
            "task_id": runtime_task_id,
            "lease_token": lease_token,
            "worker_service": "ocr-worker",
            "worker_instance_id": worker_instance_id,
        }
        self._request(
            "POST",
            f"/api/v1/internal/worker/tasks/{runtime_task_id}/heartbeat",
            {
                "lease_token": lease_token,
                "worker_service": "ocr-worker",
                "worker_instance_id": worker_instance_id,
                "state": "running",
                "progress": progress or {},
                "lease_seconds": lease_seconds,
            },
            timeout=timeout_seconds,
        )

    def start_task(self, task_id: str, tenant_id: str | None = None) -> None:
        self._request("POST", f"/api/v1/ocr-tasks/{task_id}/start", {}, tenant_id=tenant_id)

    def get_task_input(self, task_id: str, tenant_id: str | None = None) -> dict[str, Any]:
        return self._request("GET", f"/api/v1/ocr-tasks/{task_id}/input", tenant_id=tenant_id)

    def complete_task(self, task_id: str, payload: dict[str, Any], tenant_id: str | None = None) -> None:
        self._request("POST", f"/api/v1/ocr-tasks/{task_id}/results", payload, tenant_id=tenant_id)

    def fail_task(
        self,
        task_id: str,
        message: str,
        runtime_task_id: str,
        lease_token: str,
        retryable: bool,
        tenant_id: str | None = None,
    ) -> None:
        self._request(
            "POST",
            f"/api/v1/ocr-tasks/{task_id}/fail",
            {
                "error_message": message,
                "runtime_task_id": runtime_task_id,
                "runtime_lease_token": lease_token,
                "retryable": retryable,
            },
            tenant_id=tenant_id,
        )

    def complete_paper_import_ocr(self, import_id: str, payload: dict[str, Any], tenant_id: str | None = None) -> None:
        # The gateway performs model-backed question parsing after persisting the
        # OCR result. Local CPU inference for a full paper can legitimately take
        # several minutes, so this terminal callback must not inherit the generic
        # 60-second control-plane timeout.
        self._request(
            "POST",
            f"/api/v1/internal/paper-imports/{import_id}/ocr-result",
            payload,
            timeout=PAPER_IMPORT_COMPLETION_TIMEOUT_SECONDS,
            tenant_id=tenant_id,
        )

    def complete_paper_import_formula(self, import_id: str, payload: dict[str, Any], tenant_id: str | None = None) -> None:
        self._request("POST", f"/api/v1/internal/paper-imports/{import_id}/formula-result", payload,
                      timeout=PAPER_IMPORT_COMPLETION_TIMEOUT_SECONDS, tenant_id=tenant_id)

    def fail_paper_import(self, import_id: str, runtime_task_id: str, lease_token: str, error_code: str, retryable: bool, tenant_id: str | None = None, error_detail: dict[str, Any] | None = None, duration_ms: int = 0) -> None:
        self._request("POST", f"/api/v1/internal/paper-imports/{import_id}/failure", {
            "task_id": runtime_task_id, "lease_token": lease_token, "retryable": retryable,
            "error_code": error_code, "error_detail": error_detail or {}, "duration_ms": duration_ms,
        }, tenant_id=tenant_id)

    def download(self, url: str, tenant_id: str | None = None) -> bytes:
        req = self._build_request("GET", url, None, tenant_id=tenant_id)
        try:
            with request.urlopen(req, timeout=60) as response:
                return read_bounded(response, MAX_DOCUMENT_RESPONSE_BYTES)
        except error.HTTPError as exc:
            if exc.code == 401:
                raise AuthenticationError(f"download unauthorized: {exc.code}") from exc
            raise APIError(f"download failed: {exc.code}", status_code=exc.code) from exc
        except (OSError, ResponseValidationError) as exc:
            raise APIError("download failed") from exc

    def _request(
        self,
        method: str,
        path: str,
        payload: dict[str, Any] | None = None,
        require_auth: bool = True,
        timeout: float = 60,
        tenant_id: str | None = None,
    ) -> dict[str, Any]:
        req = self._build_request(
            method,
            path,
            payload if method != "GET" else None,
            require_auth=require_auth,
            tenant_id=tenant_id,
        )
        try:
            with request.urlopen(req, timeout=timeout) as response:
                raw = read_json_response(response)
        except error.HTTPError as exc:
            if exc.code == 401:
                raise AuthenticationError(f"unauthorized: {exc.code}") from exc
            raise APIError(f"api request failed: {exc.code}", status_code=exc.code) from exc
        except (OSError, ResponseValidationError) as exc:
            raise APIError("api request failed") from exc
        if not raw:
            return {}
        return json.loads(raw.decode("utf-8"))

    def _build_request(
        self,
        method: str,
        path_or_url: str,
        payload: dict[str, Any] | None,
        require_auth: bool = True,
        tenant_id: str | None = None,
    ) -> request.Request:
        # 附带令牌前先校验初始目标同源；任务权限来自租约头，不发送调用方指定的租户头。
        url = _trusted_service_url(self.base_url, path_or_url)
        body = None if payload is None else json.dumps(payload).encode("utf-8")
        headers = {"Accept": "application/json"}
        if payload is not None:
            headers["Content-Type"] = "application/json"
        if require_auth and self.token:
            headers["Authorization"] = f"Bearer {self.token}"
            if self.current_task:
                headers.update(_task_capability_headers(self.current_task))
        return request.Request(url, data=body, headers=headers, method=method)


def _task_capability_headers(task: dict[str, str]) -> dict[str, str]:
    return {
        "X-EduGrade-Worker-Task-ID": task["task_id"],
        "X-EduGrade-Worker-Lease-Token": task["lease_token"],
        "X-EduGrade-Worker-Service": task["worker_service"],
        "X-EduGrade-Worker-Instance-ID": task["worker_instance_id"],
    }


def _trusted_service_url(base_url: str, path_or_url: str) -> str:
    candidate = path_or_url if path_or_url.startswith(("http://", "https://")) else f"{base_url.rstrip('/')}/{path_or_url.lstrip('/')}"
    try:
        base = urlsplit(base_url)
        target = urlsplit(candidate)
        default_ports = {"http": 80, "https": 443}
        base_origin = (base.scheme.lower(), (base.hostname or "").lower(), base.port or default_ports.get(base.scheme.lower()))
        target_origin = (
            target.scheme.lower(),
            (target.hostname or "").lower(),
            target.port or default_ports.get(target.scheme.lower()),
        )
    except ValueError as exc:
        raise APIError("service URL is invalid") from exc
    if (
        target.scheme.lower() not in default_ports
        or target_origin != base_origin
        or target.username is not None
        or target.password is not None
        or target.fragment
    ):
        raise APIError("service URL must remain on the configured API origin")
    return candidate
