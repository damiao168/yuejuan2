from __future__ import annotations

import os
import stat
import time
from pathlib import Path

DEFAULT_HEALTH_FILE = "/app/state/edugrade-subjective-grading-worker.ready"


def mark_healthy(path: str) -> None:
    marker = Path(path)
    parent = marker.parent
    try:
        mode = parent.stat().st_mode
    except OSError as exc:
        raise RuntimeError("health marker directory is unavailable") from exc
    # Windows 不提供 POSIX 目录写权限位；容器和
    # 其他 POSIX 部署仍会拒绝所有人可写的标记目录。
    if os.name != "nt" and mode & stat.S_IWOTH:
        raise RuntimeError("health marker directory must not be world-writable")
    marker.touch(exist_ok=True)


def is_healthy(path: str, max_age: float, *, now: float | None = None) -> bool:
    marker = Path(path)
    try:
        if os.name != "nt" and marker.parent.stat().st_mode & stat.S_IWOTH:
            return False
        modified = marker.stat().st_mtime
    except OSError:
        return False
    current = time.time() if now is None else now
    age = current - modified
    # 未来时间戳也不视为健康，避免时钟回拨让过期标记持续有效。
    return 0 <= age <= max_age


def main() -> None:
    path = os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_HEALTH_FILE", DEFAULT_HEALTH_FILE)
    try:
        max_age = float(os.environ.get("EDUGRADE_SUBJECTIVE_WORKER_HEALTH_MAX_AGE", "120"))
    except ValueError as exc:
        raise SystemExit(1) from exc
    raise SystemExit(0 if max_age > 0 and is_healthy(path, max_age) else 1)


if __name__ == "__main__":
    main()
