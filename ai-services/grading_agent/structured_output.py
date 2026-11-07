"""供应商结构化文本的有界解码和 JSON Schema 校验。

容错只提取完整对象；业务语义及执行诊断仍由调用方检查。
"""

import json

from jsonschema import exceptions as schema_exceptions
from jsonschema import validators

from .errors import AgentError

# 结构化协议只需浅层对象；在 schema 校验前也要限制解码深度和容错尝试，避免资源放大。
MAX_STRUCTURED_JSON_DEPTH = 128


MAX_WRAPPED_JSON_ATTEMPTS = 128


def _check_structured_json_depth(content):
    depth = 0
    quoted = False
    escaped = False
    for character in content:
        if quoted:
            if escaped:
                escaped = False
            elif character == "\\":
                escaped = True
            elif character == '"':
                quoted = False
        elif character == '"' and depth:
            quoted = True
        elif character in "[{":
            depth += 1
            if depth > MAX_STRUCTURED_JSON_DEPTH:
                raise ValueError("structured JSON nesting limit exceeded")
        elif character in "]}":
            depth = max(0, depth - 1)


def _validate_structured_output(output, schema, request_id):
    try:
        validator_type = validators.validator_for(schema)
        validator_type.check_schema(schema)
        validator_type(schema).validate(output)
    except schema_exceptions.SchemaError as exc:
        raise RuntimeError("application supplied an invalid JSON Schema") from exc
    except schema_exceptions.ValidationError as exc:
        path = "$" + "".join(
            f"[{part}]" if isinstance(part, int) else f".{part}" for part in exc.path
        )
        raise AgentError(
            "model_output_invalid",
            f"model output did not satisfy the required JSON Schema at {path}",
            status=502,
            request_id=request_id,
        ) from exc
    return output


def _wrapped_object_candidates(content):
    decoder = json.JSONDecoder()
    best = None
    cursor = 0
    attempts = 0
    while (start := content.find("{", cursor)) >= 0:
        attempts += 1
        if attempts > MAX_WRAPPED_JSON_ATTEMPTS:
            raise ValueError("wrapped JSON candidate limit exceeded")
        cursor = start + 1
        try:
            value, end = decoder.raw_decode(content, start)
        except json.JSONDecodeError:
            continue
        # 完整对象内部不能再产生更长候选；跳过已解析区间，只保留当前最佳值。
        cursor = end
        if isinstance(value, dict):
            length = end - start
            if best is None or length > best[0]:
                best = (length, value)
    return [best] if best is not None else []


def _decode_structured_content(content, request_id):
    if isinstance(content, dict):
        return content
    if not isinstance(content, str) or not content.strip():
        raise AgentError(
            "model_output_invalid",
            "model response contained no structured content",
            status=502,
            request_id=request_id,
        )
    stripped = content.strip()
    try:
        _check_structured_json_depth(stripped)
    except ValueError as exc:
        raise AgentError("model_output_invalid", "model response exceeded the structured JSON nesting limit", status=502, request_id=request_id) from exc
    try:
        return json.loads(stripped)
    except (json.JSONDecodeError, RecursionError) as original_error:
        # 兼容供应商的 Markdown 或说明文字；选取最长完整对象，再由调用方校验 schema。
        try:
            candidates = _wrapped_object_candidates(stripped)
        except (ValueError, RecursionError) as exc:
            raise AgentError("model_output_invalid", "model response exceeded the structured JSON decoding limit", status=502, request_id=request_id) from exc
        if candidates:
            return max(candidates, key=lambda item: item[0])[1]
        raise AgentError(
            "model_output_invalid",
            "model response was not valid JSON",
            status=502,
            request_id=request_id,
        ) from original_error
