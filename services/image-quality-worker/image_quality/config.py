from __future__ import annotations

import math
import os
import socket
from dataclasses import dataclass

from edugrade_worker_runtime import validate_lease_timing, validate_service_url


# 仅本地开发支持 HTTP 部署；当 EDUGRADE_ENV 选择
# 类生产环境时，validate_service_url 会拒绝这个默认地址。
_LOCAL_API_BASE_URL = "http" + "://localhost:8080"


@dataclass
class EngineConfig:
    batch_size: int = 1
    lease_seconds: int = 300
    worker_instance_id: str = "image-quality-worker"
    poll_interval: float = 5.0
    heartbeat_interval: float = 10.0
    heartbeat_timeout: float = 3.0

    def __post_init__(self) -> None:
        # 任务按顺序处理，图像质量 API 没有
        # 提供源租约续期接口。提前领取会导致后续任务
        # 在第一张图像下载和上传期间过期。
        if self.batch_size != 1:
            raise ValueError("EDUGRADE_IMAGE_QUALITY_BATCH_SIZE must be 1")
        validate_lease_timing(self.lease_seconds, self.heartbeat_interval, self.heartbeat_timeout)
        if not self.worker_instance_id.strip():
            raise ValueError("EDUGRADE_IMAGE_QUALITY_WORKER_ID must not be empty")
        if not math.isfinite(self.poll_interval) or self.poll_interval <= 0:
            raise ValueError("EDUGRADE_IMAGE_QUALITY_POLL_INTERVAL must be greater than 0")


@dataclass
class APIConfig:
    base_url: str
    tenant_code: str
    username: str
    password: str

    def __post_init__(self) -> None:
        validate_service_url("EDUGRADE_API_BASE_URL", self.base_url, os.getenv("EDUGRADE_ENV", "development"))
        for name, value in (
            ("EDUGRADE_IMAGE_QUALITY_TENANT_CODE", self.tenant_code),
            ("EDUGRADE_IMAGE_QUALITY_USERNAME", self.username),
            ("EDUGRADE_IMAGE_QUALITY_PASSWORD", self.password),
        ):
            if not value.strip():
                raise ValueError(f"{name} must not be empty")


def load_engine_config() -> EngineConfig:
    return EngineConfig(
        batch_size=_int_env("EDUGRADE_IMAGE_QUALITY_BATCH_SIZE", 1),
        lease_seconds=_int_env("EDUGRADE_IMAGE_QUALITY_LEASE_SECONDS", 300),
        worker_instance_id=os.getenv("EDUGRADE_IMAGE_QUALITY_WORKER_ID") or f"{socket.gethostname()}-image-quality",
        poll_interval=float(os.getenv("EDUGRADE_IMAGE_QUALITY_POLL_INTERVAL", "5")),
        heartbeat_interval=float(os.getenv("EDUGRADE_IMAGE_QUALITY_HEARTBEAT_INTERVAL", "10")),
        heartbeat_timeout=float(os.getenv("EDUGRADE_IMAGE_QUALITY_HEARTBEAT_TIMEOUT", "3")),
    )


def load_api_config() -> APIConfig:
    return APIConfig(
        base_url=os.getenv("EDUGRADE_API_BASE_URL", _LOCAL_API_BASE_URL).rstrip("/"),
        tenant_code=os.getenv("EDUGRADE_IMAGE_QUALITY_TENANT_CODE", "demo"),
        username=os.getenv("EDUGRADE_IMAGE_QUALITY_USERNAME", ""),
        password=os.getenv("EDUGRADE_IMAGE_QUALITY_PASSWORD", ""),
    )


def _int_env(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if value <= 0:
        raise ValueError(f"{name} must be greater than 0")
    return value
