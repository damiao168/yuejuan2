import io
import json
from unittest.mock import patch
from urllib import error

import pytest
from edugrade_worker_runtime.http import MAX_JSON_RESPONSE_BYTES
from page_processing.api import APIError, Client


class CountingBody(io.BytesIO):
    def __init__(self, data: bytes, headers: dict[str, str] | None = None) -> None:
        super().__init__(data)
        self.headers = headers or {}
        self.read_sizes: list[int] = []

    def read(self, size: int = -1) -> bytes:
        self.read_sizes.append(size)
        return super().read(size)


def conflict(data: bytes, content_type: str = "application/json", length: str | None = None, status: int = 409) -> tuple[error.HTTPError, CountingBody]:
    body = CountingBody(data)
    headers = {"Content-Type": content_type, "Content-Length": length or str(len(data))}
    return error.HTTPError("http://api/api/v1/files", status, "upload failed", headers, body), body


def client_and_task() -> tuple[Client, dict]:
    client = Client("http://api", "demo", "worker", "secret", token="token")
    task = {"id": "task-1", "lease_token": "lease-1", "payload": {"exam_id": "exam-1"}}
    client.activate_task(task, "worker-1")
    return client, task


def test_upload_asset_preserves_request_and_single_bounded_success_read() -> None:
    asset = {"id": "asset-1", "hash_sha256": "hash-1"}
    data = json.dumps({"file": asset}).encode()
    response = CountingBody(data, {"Content-Type": "application/json", "Content-Length": str(len(data))})
    client, task = client_and_task()
    with patch("page_processing.api._open_request", return_value=response) as opened:
        assert client.upload_asset(task, "omr_evidence", "run-1", "overlay.png", b"png") == asset
    req = opened.call_args.args[0]
    headers = {key.lower(): value for key, value in req.header_items()}
    assert req.get_method() == "POST"
    assert req.full_url == "http://api/api/v1/files"
    assert opened.call_args.kwargs == {"timeout": 180}
    assert headers["authorization"] == "Bearer token"
    assert headers["x-edugrade-worker-task-id"] == "task-1"
    assert headers["x-edugrade-worker-lease-token"] == "lease-1"
    assert headers["x-edugrade-worker-service"] == "page-processing"
    assert headers["x-edugrade-worker-instance-id"] == "worker-1"
    assert headers["content-type"].startswith("multipart/form-data; boundary=----edugrade-")
    assert headers["content-length"] == str(len(req.data))
    assert b'name="owner_type"\r\n\r\nomr_evidence\r\n' in req.data
    assert b'name="owner_id"\r\n\r\nrun-1\r\n' in req.data
    assert b'name="exam_id"\r\n\r\nexam-1\r\n' in req.data
    assert b'filename="overlay.png"\r\nContent-Type: image/png\r\n' in req.data
    assert response.read_sizes == [MAX_JSON_RESPONSE_BYTES + 1]
    assert response.closed


def test_conflict_reuses_complete_asset_after_one_bounded_read() -> None:
    asset = {"id": "asset-1", "hash_sha256": "hash-1"}
    exc, body = conflict(json.dumps({"existing_file": asset}).encode())
    client, task = client_and_task()
    with patch("page_processing.api._open_request", side_effect=exc):
        assert client.upload_asset(task, "omr_evidence", "run-1", "overlay.png", b"png") == asset
    assert body.read_sizes == [MAX_JSON_RESPONSE_BYTES + 1]
    body.close()


# 409 正文分别触发原三种解析异常或缺失资产字段；这些失败不能被当作上传成功。
@pytest.mark.parametrize(
    ("data", "content_type", "length", "reads"),
    [
        (b'{"existing_file":{"id":"asset-1"}}', "application/json", None, 1),
        (b'{"existing_file":{"hash_sha256":"hash-1"}}', "application/json", None, 1),
        (b"not json", "application/json", None, 1),
        (b"\xff", "application/json", None, 1),
        (b"{}", "text/html", None, 0),
        (b"{}", "application/json", str(MAX_JSON_RESPONSE_BYTES + 1), 0),
    ],
)
def test_unusable_conflict_keeps_upload_failure(data: bytes, content_type: str, length: str | None, reads: int) -> None:
    exc, body = conflict(data, content_type, length)
    client, task = client_and_task()
    with (
        patch("page_processing.api._open_request", side_effect=exc),
        pytest.raises(APIError, match="decoded_page_upload_failed:409") as caught,
    ):
        client.upload_asset(task, "omr_evidence", "run-1", "overlay.png", b"png")
    assert caught.value.__cause__ is exc
    assert body.read_sizes == [MAX_JSON_RESPONSE_BYTES + 1] * reads
    body.close()


def test_other_http_failure_does_not_read_duplicate_response() -> None:
    exc, body = conflict(b'{"existing_file":{"id":"asset-1","hash_sha256":"hash-1"}}', status=403)
    client, task = client_and_task()
    with (
        patch("page_processing.api._open_request", side_effect=exc),
        pytest.raises(APIError, match="decoded_page_upload_failed:403"),
    ):
        client.upload_asset(task, "omr_evidence", "run-1", "overlay.png", b"png")
    assert body.read_sizes == []
    body.close()
