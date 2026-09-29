from __future__ import annotations

import json
import uuid
from dataclasses import dataclass
from typing import Any
from urllib import error, request
from urllib.parse import urlsplit

from edugrade_worker_runtime import (
    MAX_IMAGE_RESPONSE_BYTES,
    ResponseValidationError,
    read_bounded,
    read_json_response,
)


class APIError(RuntimeError):
    def __init__(self, message: str, *, status_code: int | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code


class AuthenticationError(APIError):
    pass


@dataclass
class EduGradeImageQualityClient:
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
            {"tenant_code": self.tenant_code, "username": self.username, "password": self.password, "client_type": "service", "device_name": "Image Quality Worker"},
            require_auth=False,
        )
        token = response.get("access_token")
        if not isinstance(token, str) or not token:
            raise AuthenticationError("login response did not include access_token")
        self.token = token

    def claim_jobs(self, worker_instance_id: str, limit: int, lease_seconds: int) -> list[dict[str, Any]]:
        self.worker_instance_id = worker_instance_id
        response = self._request(
            "POST",
            "/api/v1/internal/image-quality/jobs/claim",
            {"worker_instance_id": worker_instance_id, "limit": limit, "lease_seconds": lease_seconds},
        )
        jobs = response.get("jobs", [])
        return jobs if isinstance(jobs, list) else []

    def activate_job(self, job: dict[str, Any]) -> None:
        task_id = str(job.get("runtime_task_id") or "").strip()
        lease_token = str(job.get("lease_token") or "").strip()
        worker_instance_id = str(self.worker_instance_id or "").strip()
        if not task_id or not lease_token or not worker_instance_id:
            raise APIError("image quality job is missing its task capability")
        self.current_task = {
            "task_id": task_id,
            "lease_token": lease_token,
            "worker_service": "image-quality-worker",
            "worker_instance_id": worker_instance_id,
        }

    def heartbeat(self, job: dict[str, Any], worker_instance_id: str, lease_seconds: int, timeout: float) -> None:
        task_id = str(job.get("runtime_task_id") or "").strip()
        lease_token = str(job.get("lease_token") or "").strip()
        if not task_id or not lease_token:
            raise APIError("image quality job is missing its task capability")
        self._request(
            "POST",
            f"/api/v1/internal/worker/tasks/{task_id}/heartbeat",
            {
                "lease_token": lease_token,
                "worker_service": "image-quality-worker",
                "worker_instance_id": worker_instance_id,
                "state": "running",
                "lease_seconds": lease_seconds,
            },
            timeout=timeout,
        )

    def download(self, url: str) -> bytes:
        req = self._build_request("GET", url, None)
        try:
            with request.urlopen(req, timeout=60) as response:
                return read_bounded(response, MAX_IMAGE_RESPONSE_BYTES)
        except error.HTTPError as exc:
            if exc.code == 401:
                raise AuthenticationError(f"download unauthorized: {exc.code}") from exc
            raise APIError(f"download failed: {exc.code}", status_code=exc.code) from exc
        except (OSError, ResponseValidationError) as exc:
            raise APIError("download failed") from exc

    def request_normalized_asset(self, run_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return self._request("POST", f"/api/v1/internal/image-quality/runs/{run_id}/normalized-assets", payload)

    def upload_normalized(self, slot: dict[str, Any], job: dict[str, Any], normalized_png: bytes, sha256: str) -> str:
        upload_url = str(slot.get("upload_url") or "/api/v1/files")
        boundary = "----edugrade-" + uuid.uuid4().hex
        fields = {
            "owner_type": "submission_page_normalized",
            "owner_id": str(job["submission_page_id"]),
            "submission_id": str(job["submission_id"]),
            "exam_id": str(job["exam_id"]),
        }
        body = _multipart_body(boundary, fields, "file", "normalized-page.png", "image/png", normalized_png)
        req = self._build_request("POST", upload_url, None)
        req.data = body
        req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
        req.add_header("Content-Length", str(len(body)))
        try:
            with request.urlopen(req, timeout=120) as response:
                raw = read_json_response(response)
        except error.HTTPError as exc:
            raise APIError(f"normalized upload failed: {exc.code}") from exc
        except ResponseValidationError as exc:
            raise APIError("normalized upload returned an invalid response") from exc
        payload = json.loads(raw.decode("utf-8"))
        file_info = payload.get("file") or {}
        if file_info.get("hash_sha256") not in (sha256, "sha256:" + sha256):
            raise APIError("normalized upload hash mismatch")
        file_id = file_info.get("id")
        if not isinstance(file_id, str) or not file_id:
            raise APIError("normalized upload response missing file id")
        return file_id

    def submit_result(self, run_id: str, payload: dict[str, Any]) -> None:
        self._request("POST", f"/api/v1/internal/image-quality/runs/{run_id}/result", payload)

    def submit_failure(self, run_id: str, payload: dict[str, Any]) -> None:
        self.submit_result(run_id, payload)

    def _request(self, method: str, path: str, payload: dict[str, Any] | None = None, require_auth: bool = True, timeout: float = 60) -> dict[str, Any]:
        req = self._build_request(method, path, payload if method != "GET" else None, require_auth=require_auth)
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

    def _build_request(self, method: str, path_or_url: str, payload: dict[str, Any] | None, require_auth: bool = True) -> request.Request:
        # 下载和上传地址可能来自任务响应；发送服务令牌前必须验证仍为配置的 API 同源。
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


def _multipart_body(boundary: str, fields: dict[str, str], file_field: str, filename: str, content_type: str, data: bytes) -> bytes:
    chunks: list[bytes] = []
    for name, value in fields.items():
        chunks.extend(
            [
                f"--{boundary}\r\n".encode(),
                f'Content-Disposition: form-data; name="{name}"\r\n\r\n'.encode(),
                value.encode(),
                b"\r\n",
            ]
        )
    chunks.extend(
        [
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="{file_field}"; filename="{filename}"\r\n'.encode(),
            f"Content-Type: {content_type}\r\n\r\n".encode(),
            data,
            b"\r\n",
            f"--{boundary}--\r\n".encode(),
        ]
    )
    return b"".join(chunks)


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
