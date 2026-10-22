import base64
import hashlib
import json
import math
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass

from .errors import AgentError
from .execution_diagnostics import completed_content, response_diagnostics

DASHSCOPE_TEXT_GENERATION_PATH = "/services/aigc/text-generation/generation"
DASHSCOPE_MULTIMODAL_GENERATION_PATH = "/services/aigc/multimodal-generation/generation"
_MODEL_NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$")
_REQUEST_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")
_SHA256 = re.compile(r"^[a-f0-9]{64}$")
_MESSAGE_ROLES = frozenset({"system", "user", "assistant"})
_PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
_APPROVED_IMAGE_MEDIA_TYPE = "image/png"
MAX_CROP_BYTES = 5 * 1024 * 1024
MAX_CROP_PIXELS = 12_000_000
FORBIDDEN_EXPORT_KEYS = frozenset(
    {
        "tenant_id",
        "school_id",
        "student_id",
        "student_name",
        "student_no",
        "class_id",
        "exam_id",
        "submission_id",
        "question_id",
        "answer_segment_id",
        "final_score",
        "published_score",
        "answer_image_ref",
    }
)


@dataclass(frozen=True)
class DashScopeNativeResult:
    output: dict
    request_id: str
    input_tokens: int | None
    output_tokens: int | None
    total_tokens: int | None


@dataclass(frozen=True)
class ApprovedImageCrop:
    """Server-attested current-question crop. Attestation fields are never exported."""

    binding: str
    scope: str
    media_type: str
    data_base64: str
    sha256: str
    width_pixels: int
    height_pixels: int
    normalized_bbox: tuple[float, float, float, float]


def _bounded_identifier(value, pattern, field):
    if not isinstance(value, str) or not pattern.fullmatch(value):
        raise ValueError(f"{field} must be a bounded identifier")
    return value


def _validate_messages(messages):
    if not isinstance(messages, Sequence) or isinstance(messages, (str, bytes)) or not messages:
        raise ValueError("messages must be a non-empty sequence")
    normalized = []
    for message in messages:
        if not isinstance(message, Mapping) or set(message) != {"role", "content"}:
            raise ValueError("each message must contain only role and content")
        role = message["role"]
        content = message["content"]
        if role not in _MESSAGE_ROLES:
            raise ValueError("message role is not supported")
        if not isinstance(content, str) or not content.strip() or len(content) > 100_000:
            raise ValueError("message content must be non-empty and bounded")
        normalized.append({"role": role, "content": content})
    if not any("json" in message["content"].lower() for message in normalized):
        raise ValueError("DashScope JSON mode requires a prompt that explicitly requests JSON")
    return normalized


def _forbidden_export_fields(value):
    found = set()
    if isinstance(value, Mapping):
        found.update(key for key in value if key in FORBIDDEN_EXPORT_KEYS)
        for child in value.values():
            found.update(_forbidden_export_fields(child))
    elif isinstance(value, Sequence) and not isinstance(value, (str, bytes)):
        for child in value:
            found.update(_forbidden_export_fields(child))
    return found


def _generation_parameters(max_completion_tokens, temperature, seed):
    if not isinstance(max_completion_tokens, int) or not 1 <= max_completion_tokens <= 16_384:
        raise ValueError("max_completion_tokens must be between 1 and 16384")
    if isinstance(temperature, bool) or not isinstance(temperature, (int, float)) or not 0 <= temperature <= 2:
        raise ValueError("temperature must be between 0 and 2")
    if isinstance(seed, bool) or not isinstance(seed, int) or not 0 <= seed <= 2_147_483_647:
        raise ValueError("seed must be between 0 and 2147483647")
    return {
        "result_format": "message",
        "response_format": {"type": "json_object"},
        "enable_thinking": False,
        "max_completion_tokens": max_completion_tokens,
        "temperature": temperature,
        "seed": seed,
    }


def build_dashscope_text_payload(
    *,
    model,
    messages,
    max_completion_tokens,
    temperature=0.0,
    seed=42,
):
    """Build a native DashScope text-generation body without auth or transport."""

    _bounded_identifier(model, _MODEL_NAME, "model")
    return {
        "model": model,
        "input": {"messages": _validate_messages(messages)},
        "parameters": _generation_parameters(max_completion_tokens, temperature, seed),
    }


def build_dashscope_grading_payload(
    *,
    grading_request,
    prompt_registry,
    model,
    max_completion_tokens,
    temperature=0.0,
    seed=42,
    repair_reason=None,
):
    """Project a governed grading request onto the native provider whitelist."""

    payload = build_dashscope_text_payload(
        model=model,
        messages=prompt_registry.messages(grading_request, repair_reason),
        max_completion_tokens=max_completion_tokens,
        temperature=temperature,
        seed=seed,
    )
    leaked = sorted(_forbidden_export_fields(payload))
    if leaked:
        raise ValueError(f"DashScope payload contains forbidden export fields: {', '.join(leaked)}")
    return payload


def _validate_crop_dimensions(crop):
    if (
        isinstance(crop.width_pixels, bool)
        or isinstance(crop.height_pixels, bool)
        or not isinstance(crop.width_pixels, int)
        or not isinstance(crop.height_pixels, int)
        or crop.width_pixels <= 0
        or crop.height_pixels <= 0
        or crop.width_pixels * crop.height_pixels > MAX_CROP_PIXELS
    ):
        raise ValueError("crop dimensions are invalid or too large")


def _validate_crop_bbox(bbox):
    if not isinstance(bbox, tuple) or len(bbox) != 4:
        raise ValueError("crop bbox must contain four normalized values")
    x, y, width, height = bbox
    if any(isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) for value in bbox):
        raise ValueError("crop bbox values must be finite numbers")
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1 or y + height > 1:
        raise ValueError("crop bbox must stay within its source page")
    if width * height >= 0.9:
        raise ValueError("whole-page or near-whole-page images cannot be exported")


def _validate_crop_image(crop):
    if not isinstance(crop.data_base64, str) or not crop.data_base64:
        raise ValueError("crop image data is required")
    try:
        image_bytes = base64.b64decode(crop.data_base64, validate=True)
    except ValueError as exc:
        raise ValueError("crop image data is not valid base64") from exc
    if not image_bytes or len(image_bytes) > MAX_CROP_BYTES:
        raise ValueError("crop image data is empty or too large")
    if len(image_bytes) < 24 or not image_bytes.startswith(_PNG_SIGNATURE) or image_bytes[12:16] != b"IHDR":
        raise ValueError("crop bytes do not match the declared media type")
    decoded_width = int.from_bytes(image_bytes[16:20], "big")
    decoded_height = int.from_bytes(image_bytes[20:24], "big")
    if (decoded_width, decoded_height) != (crop.width_pixels, crop.height_pixels):
        raise ValueError("crop dimensions do not match the decoded PNG")
    if not _SHA256.fullmatch(crop.sha256) or hashlib.sha256(image_bytes).hexdigest() != crop.sha256:
        raise ValueError("crop content hash does not match its attestation")


def _validate_approved_crop(crop, expected_binding):
    if not isinstance(crop, ApprovedImageCrop):
        raise TypeError("crop must be an ApprovedImageCrop")
    if crop.scope != "answer_segment_crop":
        raise ValueError("only answer-segment crops may be exported")
    _bounded_identifier(crop.binding, _SHA256, "crop binding")
    _bounded_identifier(expected_binding, _SHA256, "expected crop binding")
    if crop.binding != expected_binding:
        raise ValueError("crop is not bound to the current answer segment")
    if crop.media_type != _APPROVED_IMAGE_MEDIA_TYPE:
        raise ValueError("crop media type is not approved")
    # 先拒绝错题绑定、范围和尺寸，再解码图像；每一步仍保留原有首个失败原因。
    _validate_crop_dimensions(crop)
    _validate_crop_bbox(crop.normalized_bbox)
    _validate_crop_image(crop)
    return f"data:{crop.media_type};base64,{crop.data_base64}"


def build_dashscope_multimodal_payload(
    *,
    model,
    system_message,
    user_message,
    crop,
    expected_binding,
    max_completion_tokens,
    temperature=0.0,
    seed=42,
):
    """Build one-image native multimodal payload from a server-approved crop."""

    _bounded_identifier(model, _MODEL_NAME, "model")
    messages = _validate_messages(
        [
            {"role": "system", "content": system_message},
            {"role": "user", "content": user_message},
        ]
    )
    image_data = _validate_approved_crop(crop, expected_binding)
    messages[1]["content"] = [{"image": image_data}, {"text": messages[1]["content"]}]
    return {
        "model": model,
        "input": {"messages": messages},
        "parameters": _generation_parameters(max_completion_tokens, temperature, seed),
    }


def build_dashscope_multimodal_grading_payload(
    *,
    grading_request,
    prompt_registry,
    model,
    crop,
    expected_binding,
    max_completion_tokens,
    temperature=0.0,
    seed=42,
    repair_reason=None,
):
    source_messages = prompt_registry.messages(grading_request, repair_reason)
    system_message = "\n\n".join(message["content"] for message in source_messages if message["role"] == "system")
    user_message = "\n\n".join(message["content"] for message in source_messages if message["role"] == "user")
    payload = build_dashscope_multimodal_payload(
        model=model,
        system_message=system_message,
        user_message=user_message,
        crop=crop,
        expected_binding=expected_binding,
        max_completion_tokens=max_completion_tokens,
        temperature=temperature,
        seed=seed,
    )
    leaked = sorted(_forbidden_export_fields(payload))
    if leaked:
        raise ValueError(f"DashScope payload contains forbidden export fields: {', '.join(leaked)}")
    return payload


def _dashscope_response_facts(response):
    """Read the common non-streaming response envelope."""

    facts = response_diagnostics(response, None)
    request_id = response.get("request_id", "") if isinstance(response, Mapping) else ""
    try:
        if request_id:
            request_id = _bounded_identifier(request_id, _REQUEST_ID, "request_id")
    except ValueError:
        request_id = ""
    content = completed_content(response, facts, request_id, native=True, native_parts=True)
    usage = facts.get("usage", {})
    return request_id, content, usage.get("input_tokens"), usage.get("output_tokens"), usage.get("total_tokens")


def _parse_json_content(content, request_id):
    if not isinstance(content, str) or not content.strip():
        raise AgentError(
            "model_output_invalid",
            "provider response contained no structured content",
            status=502,
            request_id=request_id,
        )
    try:
        output = json.loads(content)
    except json.JSONDecodeError as exc:
        raise AgentError(
            "model_output_invalid",
            "provider response was not valid JSON",
            status=502,
            request_id=request_id,
        ) from exc
    if not isinstance(output, dict):
        raise AgentError(
            "model_output_invalid",
            "provider response JSON was not an object",
            status=502,
            request_id=request_id,
        )
    return output


def _native_result(output, request_id, input_tokens, output_tokens, total_tokens):
    return DashScopeNativeResult(
        output=output,
        request_id=request_id,
        input_tokens=input_tokens,
        output_tokens=output_tokens,
        total_tokens=total_tokens,
    )


def parse_dashscope_text_response(response):
    """Parse a non-streaming native text response and retain governed facts."""

    request_id, content, input_tokens, output_tokens, total_tokens = _dashscope_response_facts(response)
    output = _parse_json_content(content, request_id)
    return _native_result(output, request_id, input_tokens, output_tokens, total_tokens)


def parse_dashscope_multimodal_response(response):
    """Parse Qwen-VL message content, which is returned as an array of parts."""

    request_id, content, input_tokens, output_tokens, total_tokens = _dashscope_response_facts(response)
    if not isinstance(response["output"]["choices"][0]["message"]["content"], list):
        raise AgentError(
            "model_output_invalid",
            "multimodal response content was not an array",
            status=502,
            request_id=request_id,
        )
    output = _parse_json_content(content, request_id)
    return _native_result(output, request_id, input_tokens, output_tokens, total_tokens)


def map_dashscope_error(http_status, body):
    """Map native provider errors to the existing public grading-agent error set."""

    if isinstance(http_status, bool) or not isinstance(http_status, int) or not isinstance(body, Mapping):
        raise TypeError("provider error fixture is invalid")
    request_id = body.get("request_id", "")
    if request_id:
        try:
            request_id = _bounded_identifier(request_id, _REQUEST_ID, "request_id")
        except ValueError:
            request_id = ""
    code = body.get("code", "")
    if not isinstance(code, str):
        code = ""
    normalized_code = code.lower()
    if http_status == 408 or "timeout" in normalized_code:
        return AgentError(
            "model_timeout",
            "external model request timed out",
            status=504,
            retryable=True,
            request_id=request_id,
        )
    retryable = http_status == 429 or http_status >= 500 or normalized_code.startswith("throttling")
    return AgentError(
        "model_unavailable",
        "external model request failed",
        status=503,
        retryable=retryable,
        request_id=request_id,
    )
