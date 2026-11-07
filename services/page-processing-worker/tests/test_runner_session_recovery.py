from unittest.mock import Mock

import pytest
from page_processing.api import APIError
from page_processing.config import Config
from page_processing.runner import Runner


def test_poll_failure_waits_and_continues_instead_of_crashing(monkeypatch: pytest.MonkeyPatch) -> None:
    runner = Runner(Mock(), Config("http://api", "demo", "worker", "secret", "worker-1"))
    stop = KeyboardInterrupt()
    runner.run_once = Mock(side_effect=[APIError("outage", status_code=503, retry_after=7), 0, stop])
    sleep = Mock()
    monkeypatch.setattr("page_processing.runner.time.sleep", sleep)
    with pytest.raises(KeyboardInterrupt):
        runner.run_forever()
    assert runner.run_once.call_count == 3
    assert [call.args[0] for call in sleep.call_args_list] == [7, 4]
