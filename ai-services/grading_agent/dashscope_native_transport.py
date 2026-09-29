import hashlib
import json
from dataclasses import dataclass

from .dashscope_native_contract import (
    DASHSCOPE_MULTIMODAL_GENERATION_PATH,
    DASHSCOPE_TEXT_GENERATION_PATH,
    FORBIDDEN_EXPORT_KEYS,
    map_dashscope_error,
    parse_dashscope_multimodal_response,
    parse_dashscope_text_response,
)
from .errors import AgentError

MAX_DASHSCOPE_TEXT_REQUEST_BYTES = 512 * 1024
MAX_DASHSCOPE_IMAGE_REQUEST_BYTES = 8 * 1024 * 1024
MAX_DASHSCOPE_RESPONSE_BYTES = 1024 * 1024
FIXTURE_AUTHORIZATION = "Bearer synthetic-dashscope-fixture-token"


@dataclass(frozen=True)
class FixtureDashScopeResponse:
    status: int
    content_type: str
    body: bytes

    @classmethod
    def from_json(cls, status, payload, content_type="application/json"):
        return cls(
            status=status,
            content_type=content_type,
            body=json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8"),
        )


class FixtureDashScopeTransport:
    """Deterministic transport with no socket or URL implementation."""

    def __init__(self, responses):
        self._responses = list(responses)
        self.calls = []

    def send(self, *, path, body, headers, timeout_seconds):
        self.calls.append(
            {
                "path": path,
                "body_sha256": hashlib.sha256(body).hexdigest(),
                "body_size": len(body),
                "header_names": tuple(sorted(headers)),
                "timeout_seconds": timeout_seconds,
            }
        )
        if not self._responses:
            raise AssertionError("fixture transport received more calls than configured")
        response = self._responses.pop(0)
        if isinstance(response, Exception):
            raise response
        if type(response) is not FixtureDashScopeResponse:
            raise TypeError("fixture transport response type is invalid")
        return response


class DashScopeNativeTransportSeam:
    """Unregistered native protocol executor restricted to fixture transport."""

    def __init__(
        self,
        transport,
        *,
        timeout_seconds=30,
        max_retries=1,
        logger=None,
    ):
        if type(transport) is not FixtureDashScopeTransport:
            raise TypeError("native transport seam accepts only FixtureDashScopeTransport")
        if (
            isinstance(timeout_seconds, bool)
            or not isinstance(timeout_seconds, (int, float))
            or not 0 < timeout_seconds <= 120
        ):
            raise ValueError("native transport timeout must be between 0 and 120 seconds")
        if isinstance(max_retries, bool) or not isinstance(max_retries, int) or max_retries not in {0, 1}:
            raise ValueError("native transport retries must be zero or one")
        self.transport = transport
        self.timeout_seconds = timeout_seconds
        self.max_retries = max_retries
        self.logger = logger or self._default_logger

    def execute_text(self, payload):
        self._validate_payload(payload, multimodal=False)
        return self._execute(
            path=DASHSCOPE_TEXT_GENERATION_PATH,
            payload=payload,
            request_limit=MAX_DASHSCOPE_TEXT_REQUEST_BYTES,
            parser=parse_dashscope_text_response,
        )

    def execute_multimodal(self, payload):
        self._validate_payload(payload, multimodal=True)
        return self._execute(
            path=DASHSCOPE_MULTIMODAL_GENERATION_PATH,
            payload=payload,
            request_limit=MAX_DASHSCOPE_IMAGE_REQUEST_BYTES,
            parser=parse_dashscope_multimodal_response,
        )

    def _execute(self, *, path, payload, request_limit, parser):
        body = self._request_body(payload, request_limit)
        headers = {
            "Accept": "application/json",
            "Authorization": FIXTURE_AUTHORIZATION,
            "Content-Type": "application/json",
            "X-DashScope-SSE": "disable",
        }
        try:
            for attempt in range(self.max_retries + 1):
                try:
                    response = self.transport.send(
                        path=path,
                        body=body,
                        headers=headers,
                        timeout_seconds=self.timeout_seconds,
                    )
                except TimeoutError as exc:
                    error = AgentError(
                        "model_timeout",
                        "external model request timed out",
                        status=504,
                        retryable=True,
                    )
                    if attempt < self.max_retries:
                        self._log("retrying", path, attempt + 1, error.code)
                        continue
                    self._log("failed", path, attempt + 1, error.code)
                    raise error from exc

                try:
                    response_payload = self._response_json(response)
                except AgentError as error:
                    self._log("failed", path, attempt + 1, error.code)
                    raise
                if response.status != 200:
                    error = map_dashscope_error(response.status, response_payload)
                    if error.retryable and attempt < self.max_retries:
                        self._log("retrying", path, attempt + 1, error.code)
                        continue
                    self._log("failed", path, attempt + 1, error.code)
                    raise error
                try:
                    result = parser(response_payload)
                except AgentError as error:
                    self._log("failed", path, attempt + 1, error.code)
                    raise
                self._log("succeeded", path, attempt + 1)
                return result
        finally:
            # 仅清除此 seam 持有的可变请求缓冲，不声称能擦除调用方对象或序列化临时副本。
            body[:] = b"\x00" * len(body)
            body.clear()
        raise AgentError("internal_error", "native transport seam ended unexpectedly", status=500)

    @staticmethod
    def _request_body(payload, limit):
        try:
            body = json.dumps(
                payload,
                ensure_ascii=False,
                allow_nan=False,
                separators=(",", ":"),
            ).encode("utf-8")
        except (TypeError, ValueError) as exc:
            raise AgentError(
                "invalid_request",
                "native provider request is not valid JSON",
                status=400,
            ) from exc
        if not body or len(body) > limit:
            raise AgentError(
                "invalid_request",
                "native provider request exceeds its approved size",
                status=413,
            )
        return bytearray(body)

    @staticmethod
    def _validate_payload(payload, *, multimodal):
        if not isinstance(payload, dict) or set(payload) != {"model", "input", "parameters"}:
            raise AgentError(
                "invalid_request",
                "native provider payload fields are invalid",
                status=400,
            )
        forbidden = set()
        image_values = []

        def inspect(value):
            if isinstance(value, dict):
                forbidden.update(key for key in value if key in FORBIDDEN_EXPORT_KEYS)
                for key, child in value.items():
                    if key == "image":
                        image_values.append(child)
                    inspect(child)
            elif isinstance(value, list):
                for child in value:
                    inspect(child)

        inspect(payload)
        if forbidden:
            raise AgentError(
                "invalid_request",
                "native provider payload contains forbidden export fields",
                status=400,
            )
        if not multimodal and image_values:
            raise AgentError(
                "invalid_request",
                "text payload cannot contain images",
                status=400,
            )
        if multimodal and (
            len(image_values) != 1
            or not isinstance(image_values[0], str)
            or not image_values[0].startswith("data:image/png;base64,")
            or any(
                scheme in image_values[0].lower()
                for scheme in ("http://", "https://", "file://", "oss://")
            )
        ):
            raise AgentError(
                "invalid_request",
                "multimodal payload must contain one inline PNG",
                status=400,
            )

    @staticmethod
    def _response_json(response):
        if type(response) is not FixtureDashScopeResponse:
            raise AgentError("model_output_invalid", "provider response type is invalid", status=502)
        if response.content_type.split(";", 1)[0].strip().lower() != "application/json":
            raise AgentError(
                "model_output_invalid",
                "provider response content type is invalid",
                status=502,
            )
        if not response.body or len(response.body) > MAX_DASHSCOPE_RESPONSE_BYTES:
            raise AgentError(
                "model_output_invalid",
                "provider response size is invalid",
                status=502,
            )

        def reject_duplicates(pairs):
            value = {}
            for key, item in pairs:
                if key in value:
                    raise ValueError("duplicate JSON field")
                value[key] = item
            return value

        try:
            return json.loads(
                response.body.decode("utf-8"),
                object_pairs_hook=reject_duplicates,
                parse_constant=lambda _value: (_ for _ in ()).throw(ValueError("non-finite JSON number")),
            )
        except (UnicodeDecodeError, json.JSONDecodeError, ValueError) as exc:
            raise AgentError(
                "model_output_invalid",
                "provider response was not strict UTF-8 JSON",
                status=502,
            ) from exc

    def _log(self, status, path, attempts, error_code=None):
        event = {
            "event": "dashscope_native_fixture_transport",
            "status": status,
            "path": path,
            "attempts": attempts,
        }
        if error_code:
            event["error_code"] = error_code
        self.logger(event)

    @staticmethod
    def _default_logger(event):
        print(json.dumps(event, ensure_ascii=True, separators=(",", ":")), flush=True)
