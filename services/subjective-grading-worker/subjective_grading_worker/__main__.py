from __future__ import annotations

import logging
import time

from .api import APIError, AuthenticationError, EduGradeClient
from .config import Settings, load_settings
from .healthcheck import mark_healthy
from .runner import Runner

LOGGER = logging.getLogger("edugrade.subjective_grading_worker")


def run_cycle(api: EduGradeClient, runner: Runner, settings: Settings) -> int:
    if not api.token:
        api.login()
    processed = runner.process_once()
    # 只有登录与本轮任务处理均正常返回才刷新健康标记，异常重试不会掩盖持续故障。
    mark_healthy(settings.health_file)
    return processed


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = load_settings()
    api = EduGradeClient(
        settings.api_base_url,
        settings.tenant_code,
        settings.username,
        settings.password,
        execute_timeout=settings.execute_timeout,
    )
    runner = Runner(api=api, settings=settings)
    while True:
        try:
            processed = run_cycle(api, runner, settings)
            if processed:
                LOGGER.info("processed %d subjective grading task(s)", processed)
        except AuthenticationError:
            api.token = None
            LOGGER.warning("subjective grading worker authentication expired; login will be retried")
        except APIError as exc:
            LOGGER.warning("subjective grading API cycle failed; retrying: %s", exc)
        except Exception:
            api.token = None
            LOGGER.exception("unexpected subjective grading worker failure; retrying")
        time.sleep(settings.poll_interval)


if __name__ == "__main__":
    main()
