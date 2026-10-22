"""评分调用的有界事实；不保存模型正文或供应商任意错误字段。"""

import copy
from collections.abc import Mapping

from .errors import AgentError

TOKEN_FIELDS = ("input_tokens", "cached_input_tokens", "output_tokens", "reasoning_tokens", "total_tokens")
FAILURE_KINDS = frozenset({
    "output_truncated", "finish_reason_missing", "finish_reason_unknown",
    "provider_refused", "content_filtered", "unsupported_generation",
    "response_envelope_invalid", "schema_invalid", "evidence_invalid", "capability_unavailable",
})
FINISH_OUTCOMES = frozenset({"completed", "output_limit", "filtered_or_refused", "unsupported_generation",
                             "finish_missing", "finish_unknown", "envelope_invalid", "unavailable"})


def sanitize_execution_diagnostics(value):
    """错误对象也只复制约定字段，防止新增调用方把供应商正文塞入公开 DTO。"""
    if not isinstance(value, Mapping):
        return None
    # 错误对象也可能来自异常调用方；先检查标量类型，避免集合查找因列表或对象而抛错。
    if not isinstance(value.get("finish_outcome"), str) or value["finish_outcome"] not in FINISH_OUTCOMES:
        return None
    if not isinstance(value.get("usage_status"), str) or value["usage_status"] not in {"reported", "partial", "unavailable", "invalid"}:
        return None
    if "failure_kind" in value and (
        not isinstance(value["failure_kind"], str) or value["failure_kind"] not in FAILURE_KINDS
    ):
        return None
    result = {"finish_outcome": value["finish_outcome"], "usage_status": value["usage_status"],
              "recovery_attempted": False}
    for key in ("attempts", "provider_request_count"):
        if type(value.get(key)) is not int or not 0 <= value[key] <= 2:
            return None
        result[key] = value[key]
    if result["attempts"] != result["provider_request_count"] or value.get("recovery_attempted") is not False:
        return None
    if value.get("failure_kind") in FAILURE_KINDS:
        result["failure_kind"] = value["failure_kind"]
    if value.get("recovery_blocked_reason") == "not_enabled":
        result["recovery_blocked_reason"] = "not_enabled"
    if type(value.get("output_token_limit")) is int and 0 < value["output_token_limit"] <= 9_223_372_036_854_775_807:
        result["output_token_limit"] = value["output_token_limit"]
    if isinstance(value.get("usage"), Mapping):
        usage = {key: item for key, item in value["usage"].items()
                 if key in TOKEN_FIELDS and type(item) is int and 0 <= item <= 9_223_372_036_854_775_807}
        if usage:
            result["usage"] = usage
    usage = result.get("usage")
    if result["usage_status"] == "unavailable" and usage:
        return None
    if result["usage_status"] == "partial" and not usage:
        return None
    if result["usage_status"] == "reported" and (
        not usage or any(key not in usage for key in ("input_tokens", "output_tokens", "total_tokens"))
    ):
        return None
    expected = {
        "output_limit": {"output_truncated"}, "finish_missing": {"finish_reason_missing"},
        "finish_unknown": {"finish_reason_unknown"}, "filtered_or_refused": {"provider_refused", "content_filtered"},
        "unsupported_generation": {"unsupported_generation", "capability_unavailable"},
        "envelope_invalid": {"response_envelope_invalid"},
    }
    if result["finish_outcome"] in expected and result.get("failure_kind") not in expected[result["finish_outcome"]]:
        return None
    if result["finish_outcome"] == "completed" and result.get("failure_kind") not in {None, "schema_invalid", "evidence_invalid"}:
        return None
    if result["finish_outcome"] == "unavailable" and result.get("failure_kind") is not None:
        return None
    return result


def usage_facts(response):
    """缺失和非法用量保持未知；仅累计供应商明确报告的非负整数。"""
    raw = response.get("usage") if isinstance(response, Mapping) else None
    if raw is None:
        return {}, "unavailable"
    if not isinstance(raw, Mapping):
        return {}, "invalid"
    aliases = {
        "input_tokens": ("prompt_tokens", "input_tokens"),
        "output_tokens": ("completion_tokens", "output_tokens"),
        "total_tokens": ("total_tokens",),
        "cached_input_tokens": ("prompt_cache_hit_tokens", "cached_input_tokens"),
        "reasoning_tokens": ("reasoning_tokens",),
    }
    usage = {}
    invalid = False
    for field, names in aliases.items():
        values = []
        for name in names:
            if name not in raw:
                continue
            value = raw[name]
            if type(value) is int and 0 <= value <= 9_223_372_036_854_775_807:
                values.append(value)
            else:
                invalid = True
        if values:
            usage[field] = values[0]
    for detail, name, target in (
        ("prompt_tokens_details", "cached_tokens", "cached_input_tokens"),
        ("completion_tokens_details", "reasoning_tokens", "reasoning_tokens"),
    ):
        nested = raw.get(detail)
        if nested is None:
            continue
        if not isinstance(nested, Mapping):
            invalid = True
        elif name in nested:
            value = nested[name]
            if type(value) is int and 0 <= value <= 9_223_372_036_854_775_807:
                usage[target] = max(usage.get(target, value), value)
            else:
                invalid = True
    if invalid:
        return usage, "invalid"
    if all(field in usage for field in ("input_tokens", "output_tokens", "total_tokens")):
        return usage, "reported"
    return usage, "partial" if usage else "unavailable"


def response_diagnostics(response, output_token_limit):
    usage, status = usage_facts(response)
    # 未收到 envelope 时只能记为未知；网络失败不能冒充模型的结束原因或结构错误。
    facts = {"finish_outcome": "unavailable" if response is None else "envelope_invalid", "attempts": 1, "provider_request_count": 1,
             "usage_status": status, "recovery_attempted": False}
    if usage:
        facts["usage"] = usage
    if type(output_token_limit) is int and output_token_limit > 0:
        facts["output_token_limit"] = output_token_limit
    return facts


def reject_finish(facts, outcome, kind, request_id, message="provider response did not complete normally"):
    facts.update(finish_outcome=outcome, failure_kind=kind, recovery_blocked_reason="not_enabled")
    raise AgentError("model_output_invalid", message, status=502, request_id=request_id,
                     execution_diagnostics=facts)


def completed_content(response, facts, request_id, *, native=False, native_parts=False):
    """先校验结束事实，再读取正文；合法 JSON 不能证明输出完整。"""
    try:
        envelope = response["output"] if native else response
        choices = envelope["choices"]
        if not isinstance(choices, list) or len(choices) != 1 or not isinstance(choices[0], Mapping):
            raise ValueError("choices")
        choice = choices[0]
        message = choice["message"]
        if not isinstance(message, Mapping) or message.get("role") != "assistant":
            raise ValueError("role")
    except (KeyError, IndexError, TypeError, ValueError):
        reject_finish(facts, "envelope_invalid", "response_envelope_invalid", request_id,
                      "provider response envelope or role was invalid")
    if message.get("refusal"):
        reject_finish(facts, "filtered_or_refused", "provider_refused", request_id)
    if message.get("tool_calls") or message.get("function_call"):
        reject_finish(facts, "unsupported_generation", "unsupported_generation", request_id)
    finish = choice.get("finish_reason")
    if finish is None or finish == "":
        reject_finish(facts, "finish_missing", "finish_reason_missing", request_id)
    if not isinstance(finish, str):
        reject_finish(facts, "envelope_invalid", "response_envelope_invalid", request_id)
    if finish == "length":
        reject_finish(facts, "output_limit", "output_truncated", request_id)
    if finish == "content_filter":
        reject_finish(facts, "filtered_or_refused", "content_filtered", request_id)
    if finish in {"tool_calls", "function_call"}:
        reject_finish(facts, "unsupported_generation", "unsupported_generation", request_id)
    if finish != "stop":
        reject_finish(facts, "finish_unknown", "finish_reason_unknown", request_id)
    if facts["usage_status"] == "invalid":
        reject_finish(facts, "envelope_invalid", "response_envelope_invalid", request_id,
                      "provider response contained invalid usage facts")
    content = message.get("content")
    if native_parts and isinstance(content, list):
        if len(content) != 1 or not isinstance(content[0], Mapping) or set(content[0]) != {"text"}:
            reject_finish(facts, "envelope_invalid", "response_envelope_invalid", request_id,
                          "multimodal response did not contain exactly one text result")
        content = content[0]["text"]
    if not isinstance(content, str) or not content.strip():
        reject_finish(facts, "envelope_invalid", "response_envelope_invalid", request_id,
                      "provider response contained no structured content")
    facts["finish_outcome"] = "completed"
    return content


def terminal_execution_error(error):
    facts = error.execution_diagnostics
    return bool(facts and facts.get("failure_kind") in FAILURE_KINDS and not error.retryable)


def repairable_model_error(error):
    facts = error.execution_diagnostics or {}
    if facts and facts.get("finish_outcome") != "completed" and error.code in {"model_output_invalid", "capability_not_supported"}:
        return False
    return (error.code in {"model_output_invalid", "evidence_verification_failed"}
            or (error.retryable and error.code in {"model_unavailable", "model_rate_limited"}))


def attach_failure(facts, error):
    result = copy.deepcopy(facts)
    source = error.execution_diagnostics or {}
    kind = source.get("failure_kind") or result.get("failure_kind")
    if not kind:
        kind = {"model_output_invalid": "schema_invalid", "evidence_verification_failed": "evidence_invalid"}.get(error.code)
    if kind:
        result["failure_kind"] = kind
        result["recovery_blocked_reason"] = "not_enabled"
    error.execution_diagnostics = sanitize_execution_diagnostics(result)
    return result


def merge_attempt_diagnostics(accumulated, latest, *, attempts, fallback_usage=None, failure=None):
    """累计本轮尝试的已知用量；任一次未知都不能把总量描述成完整报告。"""
    latest = copy.deepcopy(latest or {})
    if not latest:
        usage = {key: value for key, value in (fallback_usage or {}).items()
                 if key in TOKEN_FIELDS and type(value) is int and value >= 0}
        latest = {"finish_outcome": "completed", "provider_request_count": 1,
                  "usage_status": "reported" if all(k in usage for k in ("input_tokens", "output_tokens", "total_tokens"))
                  else "partial" if usage else "unavailable"}
        if usage:
            latest["usage"] = usage
    prior_status = accumulated.get("usage_status") if accumulated else None
    status = latest["usage_status"]
    if prior_status:
        if "invalid" in {prior_status, status}:
            status = "invalid"
        elif prior_status == status == "reported":
            status = "reported"
        elif prior_status == status == "unavailable":
            status = "unavailable"
        else:
            status = "partial"
    usage = dict((accumulated or {}).get("usage", {}))
    for key, value in latest.get("usage", {}).items():
        total = usage.get(key, 0) + value
        if total <= 9_223_372_036_854_775_807:
            usage[key] = total
        else:
            # 聚合值也受跨语言整数上限约束；溢出字段保持未知，不能截断或绕回负数。
            usage.pop(key, None)
            status = "invalid"
    count = (accumulated or {}).get("provider_request_count", 0) + latest.get("provider_request_count", 1)
    facts = {"finish_outcome": latest.get("finish_outcome", "envelope_invalid"),
             "attempts": count,
             "provider_request_count": count,
             "usage_status": status, "recovery_attempted": False}
    if latest.get("output_token_limit"):
        facts["output_token_limit"] = latest["output_token_limit"]
    if usage:
        facts["usage"] = usage
    if latest.get("failure_kind"):
        facts["failure_kind"] = latest["failure_kind"]
        facts["recovery_blocked_reason"] = "not_enabled"
    if failure:
        kind = latest.get("failure_kind")
        if not kind:
            kind = {"model_output_invalid": "schema_invalid", "evidence_verification_failed": "evidence_invalid"}.get(failure.code)
        if kind:
            facts["failure_kind"] = kind
            facts["recovery_blocked_reason"] = "not_enabled"
    return facts
