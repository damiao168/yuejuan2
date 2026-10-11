from __future__ import annotations

from io import BytesIO

import pytest
from edugrade_worker_runtime import (
    ResponseTooLarge,
    UnexpectedContentType,
    read_bounded,
    read_json_response,
    validate_service_url,
)


class Response:
    def __init__(self, body: bytes, headers: dict[str, str] | None = None) -> None:
        self.body = BytesIO(body)
        self.headers = headers or {}

    def read(self, amount: int = -1) -> bytes:
        return self.body.read(amount)


def test_content_length_over_limit_is_rejected_before_read() -> None:
    response = Response(b"small", {"Content-Length": "11"})
    with pytest.raises(ResponseTooLarge):
        read_bounded(response, 10)
    assert response.body.tell() == 0


def test_body_over_limit_without_content_length_is_rejected() -> None:
    response = Response(b'x' * 11)
    with pytest.raises(ResponseTooLarge):
        read_bounded(response, 10)


def test_chunked_style_body_is_read_only_to_limit_plus_one() -> None:
    response = Response(b"x" * 100)
    with pytest.raises(ResponseTooLarge):
        read_bounded(response, 10)
    assert response.body.tell() == 11


def test_json_response_rejects_wrong_content_type() -> None:
    response = Response(b"{}", {"Content-Type": "text/html", "Content-Length": "2"})
    with pytest.raises(UnexpectedContentType):
        read_json_response(response)
    assert response.body.tell() == 0


def test_production_service_url_requires_https_outside_loopback() -> None:
    with pytest.raises(ValueError, match="must use https"):
        validate_service_url("EDUGRADE_API_BASE_URL", "http://api-gateway:8080", "production")
    validate_service_url("EDUGRADE_API_BASE_URL", "https://api-gateway:8443", "production")
    validate_service_url("EDUGRADE_API_BASE_URL", "http://127.0.0.1:8080", "production")
