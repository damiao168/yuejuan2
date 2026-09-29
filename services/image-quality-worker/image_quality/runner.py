from __future__ import annotations

import hashlib
import json
import time
from typing import Protocol

from edugrade_worker_runtime import LeaseHeartbeat

from image_quality.config import EngineConfig
from image_quality.engine import ImageQualityError, analyze_and_normalize


class ImageQualityClient(Protocol):
    def claim_jobs(self, worker_instance_id: str, limit: int, lease_seconds: int) -> list[dict]:
        ...

    def download(self, url: str) -> bytes:
        ...

    def heartbeat(self, job: dict, worker_instance_id: str, lease_seconds: int, timeout: float) -> None:
        ...

    def request_normalized_asset(self, run_id: str, payload: dict) -> dict:
        ...

    def upload_normalized(self, slot: dict, job: dict, normalized_png: bytes, sha256: str) -> str:
        ...

    def submit_result(self, run_id: str, payload: dict) -> None:
        ...

    def submit_failure(self, run_id: str, payload: dict) -> None:
        ...


class ImageQualityRunner:
    def __init__(self, client: ImageQualityClient, config: EngineConfig) -> None:
        self.client = client
        self.config = config

    def run_once(self) -> int:
        jobs = self.client.claim_jobs(self.config.worker_instance_id, self.config.batch_size, self.config.lease_seconds)
        processed = 0
        for job in jobs:
            activate_job = getattr(self.client, "activate_job", None)
            if callable(activate_job):
                activate_job(job)
            try:
                with LeaseHeartbeat(
                    send=lambda active_job=job: self.client.heartbeat(
                        active_job, self.config.worker_instance_id, self.config.lease_seconds, self.config.heartbeat_timeout
                    ),
                    interval=self.config.heartbeat_interval,
                    lease_seconds=self.config.lease_seconds,
                    request_timeout=self.config.heartbeat_timeout,
                    thread_name=f"image-quality-heartbeat-{self.config.worker_instance_id}",
                ) as heartbeat:
                    self._process_job(job, heartbeat)
            except Exception as exc:  # noqa: BLE001 - task boundary must report arbitrary decoder/client failures.
                self._fail_job(job, exc)
            processed += 1
        return processed

    def run_forever(self) -> None:
        while True:
            processed = self.run_once()
            if processed == 0:
                time.sleep(self.config.poll_interval)

    def _process_job(self, job: dict, heartbeat: LeaseHeartbeat) -> None:
        started = time.perf_counter()
        image_bytes = self.client.download(str(job["download_url"]))
        heartbeat.raise_if_failed()
        result = analyze_and_normalize(image_bytes)
        heartbeat.raise_if_failed()
        png_hash = hashlib.sha256(result.normalized_png).hexdigest()
        slot = self.client.request_normalized_asset(
            str(job["run_id"]),
            {
                "lease_token": job["lease_token"],
                "content_type": "image/png",
                "byte_size": len(result.normalized_png),
                "sha256": png_hash,
                "pixel_width": result.quality_report["pixel_width"],
                "pixel_height": result.quality_report["pixel_height"],
            },
        )
        # 先完成标准化文件上传再提交结果，文件哈希与结果版本共同绑定这次处理内容。
        normalized_file_asset_id = self.client.upload_normalized(slot, job, result.normalized_png, png_hash)
        heartbeat.raise_if_failed()
        payload = {
            "lease_token": job["lease_token"],
            "attempt_no": job["attempt_no"],
            "result_version": _result_version(result.quality_status, png_hash, result.quality_report, result.quality_issues),
            "duration_ms": int((time.perf_counter() - started) * 1000),
            "processing_status": "completed",
            "quality_status": result.quality_status,
            "normalized_file_asset_id": normalized_file_asset_id,
            "quality_report": result.quality_report,
            "quality_issues": result.quality_issues,
            "normalization_transform": result.normalization_transform,
        }
        heartbeat.stop(raise_on_error=False)
        self.client.submit_result(str(job["run_id"]), payload)

    def _fail_job(self, job: dict, exc: Exception) -> None:
        error_code = "image_quality_processing_failed"
        processing_status = "terminal_error" if isinstance(exc, ImageQualityError) else "retryable_error"
        error_detail = {"phase": "process", "error_type": type(exc).__name__}
        result_version = "sha256:" + hashlib.sha256(
            json.dumps({"error_code": error_code, "attempt_no": job["attempt_no"]}, sort_keys=True).encode("utf-8")
        ).hexdigest()
        self.client.submit_failure(
            str(job["run_id"]),
            {
                "lease_token": job["lease_token"],
                "attempt_no": job["attempt_no"],
                "result_version": result_version,
                "duration_ms": 0,
                "processing_status": processing_status,
                "quality_status": "",
                "normalized_file_asset_id": "",
                "quality_report": {},
                "quality_issues": [],
                "normalization_transform": {},
                "error_code": error_code,
                "error_detail": error_detail,
            },
        )


def _result_version(status: str, png_hash: str, report: dict, issues: list[dict]) -> str:
    raw = json.dumps({"status": status, "png_hash": png_hash, "report": report, "issues": issues}, sort_keys=True).encode("utf-8")
    return "sha256:" + hashlib.sha256(raw).hexdigest()
