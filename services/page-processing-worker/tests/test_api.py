import io
import json
import threading
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import patch
from urllib import error

import pytest
from page_processing.api import APIError, AuthenticationError, Client


def test_upload_page_reuses_authorized_duplicate_asset() -> None:
    existing = {"id": "asset-1", "hash_sha256": "abc123"}
    response = json.dumps({"error": {"code": "duplicate_file"}, "existing_file": existing}).encode()
    conflict = error.HTTPError(
        "http://api/files",
        409,
        "Conflict",
        {"Content-Type": "application/json", "Content-Length": str(len(response))},
        io.BytesIO(response),
    )
    client = Client("http://api", "demo", "worker", "secret", token="token")
    task = {"payload": {"capture_file_id": "capture-1", "exam_id": "exam-1"}}

    with patch("page_processing.api._open_request", side_effect=conflict):
        assert client.upload_page(task, 2, b"png") == existing


def test_login_error_preserves_status_and_retry_after() -> None:
    response = io.BytesIO(b'{"error":{"code":"login_rate_limited"}}')
    limited = error.HTTPError(
        "http://api/api/v1/auth/token",
        429,
        "Too Many Requests",
        {"Retry-After": "17"},
        response,
    )
    client = Client("http://api", "demo", "worker", "secret")

    with (
        patch("page_processing.api._open_request", side_effect=limited),
        pytest.raises(APIError, match="api_request_failed:429") as caught,
    ):
        client.login()

    assert caught.value.status_code == 429
    assert caught.value.retry_after == 17.0


def test_login_transport_error_is_exposed_as_retryable_api_error() -> None:
    client = Client("http://api", "demo", "worker", "secret")

    with (
        patch("page_processing.api._open_request", side_effect=error.URLError("connection refused")),
        pytest.raises(APIError, match="api_request_failed:transport") as caught,
    ):
        client.login()

    assert caught.value.status_code is None
    assert caught.value.retry_after is None


def _json_response(payload: dict) -> io.BytesIO:
    response = io.BytesIO(json.dumps(payload).encode())
    response.headers = {"Content-Type": "application/json"}  # type: ignore[attr-defined]
    return response


@pytest.mark.parametrize("operation", ["claim", "heartbeat", "complete", "download", "upload"])
@pytest.mark.parametrize("initial_token", ["expired", None])
def test_expired_session_reauthenticates_and_replays_same_task_request(operation: str, initial_token: str | None) -> None:
    client = Client("http://api", "demo", "worker", "secret", token=initial_token)
    task = {"id": "task-1", "lease_token": "lease-1", "payload": {"capture_file_id": "capture-1", "exam_id": "exam-1"}}
    client.activate_task(task, "worker-1")
    expired = error.HTTPError("http://api", 401, "Unauthorized", {}, io.BytesIO())
    result = {"tasks": [], "file": {"id": "asset-1", "hash_sha256": "abc"}}
    sent: list[tuple[str, str | None, str | None, bytes | None]] = []
    responses = [expired, _json_response({"access_token": "fresh"}), _json_response(result)]

    def send(req, *, timeout):
        sent.append((req.full_url, req.get_header("Authorization"), req.get_header("X-edugrade-worker-lease-token"), req.data))
        response = responses.pop(0)
        if isinstance(response, Exception):
            raise response
        return response

    with patch("page_processing.api._open_request", side_effect=send):
        if operation == "claim":
            assert client.claim("worker-1", 1, 600) == []
        elif operation == "heartbeat":
            client.heartbeat(task, "worker-1", 600)
        elif operation == "complete":
            client.complete("capture-1", {"task_id": "task-1", "lease_token": "lease-1"})
        elif operation == "download":
            assert client.download("/files/asset-1/download")
        else:
            assert client.upload_page(task, 1, b"png")["id"] == "asset-1"
    assert len(sent) == 3
    assert sent[1][0].endswith("/auth/token") and sent[1][1:3] == (None, None)
    assert sent[0][0] == sent[2][0] and sent[0][3] == sent[2][3]
    assert sent[2][1:3] == ("Bearer fresh", "lease-1")


def test_second_unauthorized_response_does_not_loop_login() -> None:
    client = Client("http://api", "demo", "worker", "secret", token="expired")
    unauthorized = lambda: error.HTTPError("http://api", 401, "Unauthorized", {}, io.BytesIO())
    with (
        patch("page_processing.api._open_request", side_effect=[unauthorized(), _json_response({"access_token": "fresh"}), unauthorized()]) as send,
        pytest.raises(AuthenticationError, match="session_refresh_rejected"),
    ):
        client.claim("worker-1", 1, 600)
    assert send.call_count == 3 and client.token is None


def test_concurrent_expired_requests_share_one_session_refresh() -> None:
    client = Client("http://api", "demo", "worker", "secret", token="expired")
    expired_requests = threading.Barrier(2)
    logins: list[str] = []

    def send(req, *, timeout):
        token = req.get_header("Authorization")
        if token == "Bearer expired":
            expired_requests.wait(timeout=5)
            raise error.HTTPError(req.full_url, 401, "Unauthorized", {}, io.BytesIO())
        if token is None:
            logins.append(req.full_url)
            return _json_response({"access_token": "fresh"})
        assert token == "Bearer fresh"
        return _json_response({"tasks": []})

    # 让主线程与心跳同时看到旧会话；后获得认证锁的请求必须复用第一次刷新结果。
    with patch("page_processing.api._open_request", side_effect=send), ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(lambda _index: client.claim("worker-1", 1, 600), range(2)))
    assert results == [[], []] and len(logins) == 1


def test_download_rejects_cross_origin_url_before_sending_worker_token() -> None:
    client = Client("http://api", "demo", "worker", "secret", token="worker-token")

    with pytest.raises(APIError, match="configured API origin"):
        client.download("https://attacker.invalid/collect")


def test_download_preserves_forbidden_status_as_stable_error() -> None:
    forbidden = error.HTTPError("http://api/files/file-1/download", 403, "Forbidden", {}, io.BytesIO())
    client = Client("http://api", "demo", "worker", "secret", token="worker-token")

    with (
        patch("page_processing.api._open_request", side_effect=forbidden),
        pytest.raises(APIError, match="source_download_forbidden") as caught,
    ):
        client.download("/files/file-1/download")

    assert caught.value.status_code == 403
