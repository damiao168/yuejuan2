import io
import json
from unittest.mock import patch
from urllib import error

import pytest
from page_processing.api import APIError, Client


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
