from __future__ import annotations

import math
import os
from dataclasses import dataclass

from edugrade_worker_runtime import validate_lease_timing, validate_service_url


@dataclass(frozen=True)
class Settings:
    api_base_url: str
    tenant_code: str
    username: str
    password: str
    worker_id: str = "subjective-grading-worker"
    poll_interval: float = 5.0
    lease_seconds: int = 300
    heartbeat_interval: float = 10.0
    heartbeat_timeout: float = 3.0
    # API 网关允许受治理的 AI 任务耗时 750 秒，外层
    # HTTP 响应耗时 780 秒。外层工作进程必须等待得更久。
    execute_timeout: float = 810.0
    # 容器以 0700 权限创建此目录，并将其归属于
    # 工作进程用户。使用其他路径的调用方必须显式提供私有
    # 目录；共享 /tmp 中的标记可能被伪造。
    health_file: str = "/app/state/edugrade-subjective-grading-worker.ready"
    health_max_age: float = 120.0


def load_settings() -> Settings:
    settings = Settings(
        api_base_url=os.environ["EDUGRADE_API_BASE_URL"].rstrip("/"),
        tenant_code=os.environ["EDUGRADE_SUBJECTIVE_WORKER_TENANT_CODE"],
        username=os.environ["EDUGRADE_SUBJECTIVE_WORKER_USERNAME"],
        password=os.environ["EDUGRADE_SUBJECTIVE_WORKER_PASSWORD"],
        worker_id=os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_ID", "subjective-grading-worker"),
        poll_interval=float(os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_POLL_INTERVAL", "5")),
        lease_seconds=int(os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_LEASE_SECONDS", "300")),
        heartbeat_interval=float(os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_HEARTBEAT_INTERVAL", "10")),
        heartbeat_timeout=float(os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_HEARTBEAT_TIMEOUT", "3")),
        execute_timeout=float(os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_EXECUTE_TIMEOUT", "810")),
        health_file=os.environ.get(
            "EDUGRADE_SUBJECTIVE_WORKER_HEALTH_FILE",
            "/app/state/edugrade-subjective-grading-worker.ready",
        ),
        health_max_age=float(os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_HEALTH_MAX_AGE", "120")),
    )
    validate_settings(settings)
    return settings


def validate_settings(settings: Settings) -> None:
    validate_service_url("EDUGRADE_API_BASE_URL", settings.api_base_url, os.getenv("EDUGRADE_ENV", "development"))
    for name, value in (("tenant_code", settings.tenant_code), ("username", settings.username), ("password", settings.password), ("worker_id", settings.worker_id)):
        if not value.strip():
            raise ValueError(f"{name} must not be empty")
    if not math.isfinite(settings.poll_interval) or settings.poll_interval <= 0:
        raise ValueError("poll_interval must be greater than 0")
    validate_lease_timing(settings.lease_seconds, settings.heartbeat_interval, settings.heartbeat_timeout)
    if not math.isfinite(settings.execute_timeout) or settings.execute_timeout <= 0:
        raise ValueError("execute_timeout must be greater than 0")
    if not settings.health_file.strip():
        raise ValueError("health_file must not be empty")
    if not math.isfinite(settings.health_max_age) or settings.health_max_age <= 0:
        raise ValueError("health_max_age must be greater than 0")
