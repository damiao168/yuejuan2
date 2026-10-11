from __future__ import annotations

import math
import os
import socket
from dataclasses import dataclass

from edugrade_worker_runtime import validate_lease_timing, validate_service_url


# 仅本地开发支持 HTTP 部署；当 EDUGRADE_ENV 选择
# 类生产环境时，validate_service_url 会拒绝这个默认地址。
_LOCAL_API_BASE_URL = "http" + "://localhost:8080"


@dataclass(frozen=True)
class Config:
    base_url: str
    tenant_code: str
    username: str
    password: str
    worker_id: str
    batch_size: int = 1
    lease_seconds: int = 600
    poll_interval: float = 4.0
    max_pages: int = 500
    max_page_pixels: int = 50_000_000
    max_total_pixels: int = 1_000_000_000
    heartbeat_interval: float = 30.0
    heartbeat_timeout: float = 10.0

    def __post_init__(self) -> None:
        validate_service_url("EDUGRADE_API_BASE_URL", self.base_url, os.getenv("EDUGRADE_ENV", "development"))
        if not self.tenant_code.strip() or not self.worker_id.strip():
            raise ValueError("tenant_code and worker_id must not be empty")
        # 只有处理任务时才启动心跳。提前领取多个
        # 任务可能导致后续租约在长时间解码期间过期。
        if self.batch_size != 1:
            raise ValueError("batch_size must be 1 for the sequential page-processing worker")
        # 与运行时自身的边界一致（internal/workerruntime/validation.go）：
        # 越界的租约会被静默调整为 300 秒，因此若用
        # 未调整的值校验心跳频率，就会拿服务端
        # 从未授予的租约进行比较。
        validate_lease_timing(self.lease_seconds, self.heartbeat_interval, self.heartbeat_timeout)
        if not math.isfinite(self.poll_interval) or self.poll_interval <= 0:
            raise ValueError("poll_interval must be greater than zero")
        for name, value in (
            ("max_pages", self.max_pages),
            ("max_page_pixels", self.max_page_pixels),
            ("max_total_pixels", self.max_total_pixels),
        ):
            if not isinstance(value, int) or isinstance(value, bool) or value <= 0:
                raise ValueError(f"{name} must be a positive integer")


def load_config() -> Config:
    return Config(
        base_url=os.getenv("EDUGRADE_API_BASE_URL", _LOCAL_API_BASE_URL).rstrip("/"),
        tenant_code=os.getenv("EDUGRADE_PAGE_PROCESSING_TENANT_CODE", "demo"),
        username=os.getenv("EDUGRADE_PAGE_PROCESSING_USERNAME", ""),
        password=os.getenv("EDUGRADE_PAGE_PROCESSING_PASSWORD", ""),
        worker_id=os.getenv("EDUGRADE_PAGE_PROCESSING_WORKER_ID") or f"{socket.gethostname()}-page-processing",
        batch_size=_positive_int("EDUGRADE_PAGE_PROCESSING_BATCH_SIZE", 1),
        lease_seconds=_positive_int("EDUGRADE_PAGE_PROCESSING_LEASE_SECONDS", 600),
        poll_interval=float(os.getenv("EDUGRADE_PAGE_PROCESSING_POLL_INTERVAL", "4")),
        max_pages=_positive_int("EDUGRADE_PAGE_PROCESSING_MAX_PAGES", 500),
        max_page_pixels=_positive_int("EDUGRADE_PAGE_PROCESSING_MAX_PAGE_PIXELS", 50_000_000),
        max_total_pixels=_positive_int("EDUGRADE_PAGE_PROCESSING_MAX_TOTAL_PIXELS", 1_000_000_000),
        heartbeat_interval=_positive_float("EDUGRADE_PAGE_PROCESSING_HEARTBEAT_INTERVAL", 30.0),
        heartbeat_timeout=_positive_float("EDUGRADE_PAGE_PROCESSING_HEARTBEAT_TIMEOUT", 10.0),
    )


def _positive_int(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if value <= 0:
        raise ValueError(f"{name} must be greater than zero")
    return value


def _positive_float(name: str, default: float) -> float:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return default
    try:
        value = float(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be a number") from exc
    if not math.isfinite(value) or value <= 0:
        raise ValueError(f"{name} must be a finite number greater than zero")
    return value
