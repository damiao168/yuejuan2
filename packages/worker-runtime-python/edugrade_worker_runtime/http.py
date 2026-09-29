from __future__ import annotations

import ipaddress
from collections.abc import Iterable
from typing import Any
from urllib.parse import urlsplit

MAX_JSON_RESPONSE_BYTES = 4 * 1024 * 1024
MAX_IMAGE_RESPONSE_BYTES = 32 * 1024 * 1024
MAX_DOCUMENT_RESPONSE_BYTES = 100 * 1024 * 1024


class ResponseValidationError(RuntimeError):
    """Raised before an untrusted HTTP response can exhaust worker memory."""


class ResponseTooLarge(ResponseValidationError):
    pass


class UnexpectedContentType(ResponseValidationError):
    pass


def validate_service_url(name: str, value: str, environment: str = "development") -> None:
    try:
        parsed = urlsplit(value)
        _ = parsed.port
    except ValueError as exc:
        raise ValueError(f"{name} must be a valid HTTP(S) URL") from exc
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        raise ValueError(f"{name} must be a valid HTTP(S) URL")
    production_like = environment.strip().lower() not in {"", "local", "development", "dev", "test"}
    if production_like and parsed.scheme != "https" and not _loopback_host(parsed.hostname):
        raise ValueError(f"{name} must use https outside loopback in production-like environments")


def _loopback_host(hostname: str) -> bool:
    # 只接受 localhost 或回环 IP 字面量，不因域名当前解析到本机就放宽 HTTPS 要求。
    if hostname.lower() == "localhost":
        return True
    try:
        return ipaddress.ip_address(hostname).is_loopback
    except ValueError:
        return False


def _header(response: Any, name: str) -> str:
    headers = getattr(response, "headers", None)
    if headers is None:
        return ""
    value = headers.get(name)
    return "" if value is None else str(value).strip()


def read_bounded(response: Any, limit: int) -> bytes:
    if limit <= 0:
        raise ValueError("response limit must be positive")
    content_length = _header(response, "Content-Length")
    if content_length:
        try:
            declared = int(content_length, 10)
        except ValueError as exc:
            raise ResponseValidationError("response Content-Length is invalid") from exc
        if declared < 0:
            raise ResponseValidationError("response Content-Length is invalid")
        if declared > limit:
            raise ResponseTooLarge(f"response exceeds {limit} bytes")
    # 多读一个字节用于判定超限，不能只信缺失或与实际不符的 Content-Length。
    body = response.read(limit + 1)
    if len(body) > limit:
        raise ResponseTooLarge(f"response exceeds {limit} bytes")
    return body


def read_json_response(
    response: Any,
    limit: int = MAX_JSON_RESPONSE_BYTES,
    accepted_types: Iterable[str] = ("application/json",),
) -> bytes:
    content_type = _header(response, "Content-Type").split(";", 1)[0].strip().lower()
    accepted = {item.strip().lower() for item in accepted_types}
    if content_type not in accepted and not content_type.endswith("+json"):
        raise UnexpectedContentType("response Content-Type is not JSON")
    return read_bounded(response, limit)
