from __future__ import annotations

import math
import threading
import time
from collections.abc import Callable
from typing import Self

LEASE_LOST_HTTP_STATUSES = frozenset({403, 404, 409})


def validate_lease_timing(lease_seconds: int, heartbeat_interval: float, heartbeat_timeout: float) -> None:
    if lease_seconds < 30 or lease_seconds > 3600:
        raise ValueError("lease_seconds must be between 30 and 3600")
    if not math.isfinite(heartbeat_interval) or not math.isfinite(heartbeat_timeout):
        raise ValueError("heartbeat interval and timeout must be finite")
    if heartbeat_interval <= 0 or heartbeat_timeout <= 0:
        raise ValueError("heartbeat interval and timeout must be greater than zero")
    if heartbeat_interval >= lease_seconds:
        raise ValueError("heartbeat_interval must be shorter than lease_seconds")
    if heartbeat_interval + heartbeat_timeout >= lease_seconds:
        raise ValueError("heartbeat interval plus timeout must be shorter than the lease")


def lease_is_lost(exc: Exception) -> bool:
    return getattr(exc, "status_code", None) in LEASE_LOST_HTTP_STATUSES


class LeaseHeartbeat:
    """Renew one worker lease and tolerate transient failures while it is safe.

    A 403/404/409 is an authoritative loss of ownership. Transport, 429, and
    5xx failures are retried until there is no longer enough time to renew
    before the last successful lease expires.
    """

    def __init__(
        self,
        *,
        send: Callable[[], None],
        interval: float,
        lease_seconds: int,
        request_timeout: float,
        thread_name: str,
    ) -> None:
        validate_lease_timing(lease_seconds, interval, request_timeout)
        self._send = send
        self.interval = interval
        self.lease_seconds = lease_seconds
        self.request_timeout = request_timeout
        self.thread_name = thread_name
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        self._error: Exception | None = None
        self._last_success = time.monotonic()

    def __enter__(self) -> Self:
        # 先同步确认能续租，再让调用方开始处理；首次失败不交给后台线程吞掉。
        self._send()
        self._last_success = time.monotonic()
        self._thread = threading.Thread(target=self._run, name=self.thread_name, daemon=True)
        self._thread.start()
        return self

    def __exit__(self, exc_type: object, _exc: object, _traceback: object) -> None:
        try:
            self.stop()
        except Exception:
            # 业务本身已抛错时保留原异常，避免清理错误覆盖真正的失败原因。
            if exc_type is None:
                raise

    def stop(self, raise_on_error: bool = True) -> None:
        self._stop.set()
        thread = self._thread
        if thread is None:
            return
        thread.join(timeout=self.request_timeout + 0.5)
        if thread.is_alive():
            raise RuntimeError("heartbeat request did not stop within its bounded timeout")
        self._thread = None
        if raise_on_error:
            self.raise_if_failed()

    def raise_if_failed(self) -> None:
        if self._error is not None:
            raise self._error

    def _run(self) -> None:
        while not self._stop.wait(self.interval):
            try:
                self._send()
                self._last_success = time.monotonic()
            except Exception as exc:  # noqa: BLE001 - transports expose heterogeneous failures.
                if lease_is_lost(exc) or self._renewal_window_exhausted():
                    self._error = exc
                    return

    def _renewal_window_exhausted(self) -> bool:
        # 秒数预算须容纳下一次等待和完整请求；单调时钟不受系统时间校准影响。
        elapsed = time.monotonic() - self._last_success
        return elapsed + self.interval + self.request_timeout >= self.lease_seconds
