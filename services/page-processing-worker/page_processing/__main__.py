from __future__ import annotations

import argparse
import json
import sys
import time
from collections.abc import Callable, Sequence

from page_processing.api import APIError, Client
from page_processing.config import load_config
from page_processing.runner import Runner

LOGIN_MAX_ATTEMPTS = 5
LOGIN_INITIAL_BACKOFF_SECONDS = 1.0
LOGIN_MAX_BACKOFF_SECONDS = 30.0
LOGIN_RETRYABLE_STATUS_CODES = frozenset({401, 408, 425, 429, 500, 502, 503, 504})


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run the EduGrade page-processing worker.")
    parser.add_argument(
        "--once",
        action="store_true",
        help="claim and process at most one task batch, print its claimed-task count, then exit",
    )
    return parser


def login_with_retry(
    client: Client,
    *,
    max_attempts: int = LOGIN_MAX_ATTEMPTS,
    initial_backoff: float = LOGIN_INITIAL_BACKOFF_SECONDS,
    max_backoff: float = LOGIN_MAX_BACKOFF_SECONDS,
    sleep: Callable[[float], None] = time.sleep,
) -> None:
    """Retry startup authentication without turning a brief outage into a crash loop."""
    if max_attempts <= 0:
        raise ValueError("max_attempts must be positive")
    delay = max(0.0, initial_backoff)
    delay_cap = max(delay, max(0.0, max_backoff))
    for attempt in range(1, max_attempts + 1):
        try:
            client.login()
            return
        except APIError as exc:
            retryable = exc.status_code is None or exc.status_code in LOGIN_RETRYABLE_STATUS_CODES
            if not retryable or attempt >= max_attempts:
                raise
            # 服务端 Retry-After 优先；本地上限仅约束指数退避值，不截短服务端等待要求。
            wait_seconds = exc.retry_after if exc.retry_after is not None else delay
            wait_seconds = max(0.0, wait_seconds)
            print(
                f"page-processing login attempt {attempt} failed ({exc}); retrying in {wait_seconds:.1f}s",
                file=sys.stderr,
                flush=True,
            )
            sleep(wait_seconds)
            delay = min(delay_cap, max(delay * 2.0, 0.001))


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    config = load_config()
    if not config.username or not config.password:
        raise SystemExit("page-processing worker credentials are required")
    client = Client(config.base_url, config.tenant_code, config.username, config.password)
    login_with_retry(client)
    runner = Runner(client, config)
    if args.once:
        claimed_tasks = runner.run_once()
        print(json.dumps({"claimed_tasks": claimed_tasks, "mode": "once"}, sort_keys=True))
        return 0
    runner.run_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
