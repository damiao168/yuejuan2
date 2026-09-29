from __future__ import annotations

import os
import time
from pathlib import Path

DEFAULT_HEALTH_FILE = "/tmp/edugrade-subjective-grading-worker.ready"


def mark_healthy(path: str) -> None:
    marker = Path(path)
    marker.touch(exist_ok=True)


def is_healthy(path: str, max_age: float, *, now: float | None = None) -> bool:
    marker = Path(path)
    try:
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
