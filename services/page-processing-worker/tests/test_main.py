from __future__ import annotations

import json

import page_processing.__main__ as worker_main
import pytest
from page_processing.api import APIError
from page_processing.config import Config


class FakeClient:
    def __init__(self, *args: object) -> None:
        self.args = args
        self.logged_in = False

    def login(self) -> None:
        self.logged_in = True


class FakeRunner:
    def __init__(self, client: FakeClient, config: Config) -> None:
        self.client = client
        self.config = config
        self.forever_called = False
        self.once_called = False

    def run_once(self) -> int:
        self.once_called = True
        return 2

    def run_forever(self) -> None:
        self.forever_called = True


class SequencedLoginClient:
    def __init__(self, outcomes: list[APIError | None]) -> None:
        self.outcomes = list(outcomes)
        self.login_calls = 0

    def login(self) -> None:
        self.login_calls += 1
        outcome = self.outcomes.pop(0)
        if outcome is not None:
            raise outcome


def _config(*, username: str = "worker", password: str = "secret") -> Config:
    return Config("http://api", "demo", username, password, "worker-1")


def test_once_claims_one_batch_and_prints_a_machine_readable_result(monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]) -> None:
    runner_instances: list[FakeRunner] = []

    def new_runner(client: FakeClient, config: Config) -> FakeRunner:
        runner = FakeRunner(client, config)
        runner_instances.append(runner)
        return runner

    monkeypatch.setattr(worker_main, "load_config", _config)
    monkeypatch.setattr(worker_main, "Client", FakeClient)
    monkeypatch.setattr(worker_main, "Runner", new_runner)

    assert worker_main.main(["--once"]) == 0
    assert len(runner_instances) == 1
    assert runner_instances[0].client.logged_in is True
    assert runner_instances[0].once_called is True
    assert runner_instances[0].forever_called is False
    assert json.loads(capsys.readouterr().out) == {"claimed_tasks": 2, "mode": "once"}


def test_default_mode_preserves_the_long_running_worker(monkeypatch: pytest.MonkeyPatch) -> None:
    runner_instances: list[FakeRunner] = []

    def new_runner(client: FakeClient, config: Config) -> FakeRunner:
        runner = FakeRunner(client, config)
        runner_instances.append(runner)
        return runner

    monkeypatch.setattr(worker_main, "load_config", _config)
    monkeypatch.setattr(worker_main, "Client", FakeClient)
    monkeypatch.setattr(worker_main, "Runner", new_runner)

    assert worker_main.main([]) == 0
    assert len(runner_instances) == 1
    assert runner_instances[0].client.logged_in is True
    assert runner_instances[0].once_called is False
    assert runner_instances[0].forever_called is True


def test_credentials_are_required_before_client_or_runner_start(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(worker_main, "load_config", lambda: _config(username="", password=""))

    def fail_if_started(*args: object) -> object:
        raise AssertionError("client or runner must not start without credentials")

    monkeypatch.setattr(worker_main, "Client", fail_if_started)
    monkeypatch.setattr(worker_main, "Runner", fail_if_started)

    with pytest.raises(SystemExit, match="credentials are required"):
        worker_main.main(["--once"])


def test_login_retries_temporary_failures_with_bounded_exponential_backoff() -> None:
    client = SequencedLoginClient(
        [
            APIError("api_request_failed:503", status_code=503),
            APIError("api_request_failed:401", status_code=401),
            None,
        ]
    )
    sleeps: list[float] = []

    worker_main.login_with_retry(
        client,  # type: ignore[arg-type]
        max_attempts=5,
        initial_backoff=1.0,
        max_backoff=2.0,
        sleep=sleeps.append,
    )

    assert client.login_calls == 3
    assert sleeps == [1.0, 2.0]


def test_login_retry_honors_retry_after() -> None:
    client = SequencedLoginClient(
        [APIError("api_request_failed:429", status_code=429, retry_after=17.0), None]
    )
    sleeps: list[float] = []

    worker_main.login_with_retry(client, sleep=sleeps.append)  # type: ignore[arg-type]

    assert client.login_calls == 2
    assert sleeps == [17.0]


def test_login_retry_stops_at_attempt_limit() -> None:
    client = SequencedLoginClient(
        [
            APIError("api_request_failed:401", status_code=401),
            APIError("api_request_failed:401", status_code=401),
            APIError("api_request_failed:401", status_code=401),
        ]
    )
    sleeps: list[float] = []

    with pytest.raises(APIError, match="api_request_failed:401"):
        worker_main.login_with_retry(
            client,  # type: ignore[arg-type]
            max_attempts=3,
            initial_backoff=1.0,
            max_backoff=2.0,
            sleep=sleeps.append,
        )

    assert client.login_calls == 3
    assert sleeps == [1.0, 2.0]
