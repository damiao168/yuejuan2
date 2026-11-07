"""本地替身验证缓存计数边界；可选诊断不能扩展旧用量回执。"""

import copy
import os
from unittest.mock import patch

import pytest
from grading_agent.app import GradingAgentApplication
from grading_agent.config import Settings
from grading_agent.errors import AgentError
from grading_agent.execution_diagnostics import (
    MAX_TOKEN_COUNT,
    merge_attempt_diagnostics,
    response_diagnostics,
    usage_facts,
)
from grading_agent.model import (
    LocalLlamaCppAdapter,
    describe_model_usage,
    sum_model_usage,
)
from helpers import settings, valid_request
from test_output_truncation import provider_response


def test_cache_usage_diagnostics_requires_explicit_configuration():
    env = {
        "EDUGRADE_GRADING_AGENT_TOKEN": "synthetic-service-token-with-at-least-32-characters",
        "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE": "/synthetic/server.crt",
        "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE": "/synthetic/server.key",
    }
    assert Settings().cache_usage_diagnostics is False
    for value, expected in ((None, False), ("false", False), ("true", True)):
        with patch.dict(os.environ, env, clear=True):
            if value is not None:
                os.environ["EDUGRADE_GRADING_CACHE_USAGE_DIAGNOSTICS"] = value
            assert Settings.from_env().cache_usage_diagnostics is expected
    with (patch.dict(os.environ, {**env, "EDUGRADE_GRADING_CACHE_USAGE_DIAGNOSTICS": "1"}, clear=True),
          pytest.raises(ValueError, match="CACHE_USAGE_DIAGNOSTICS")):
        Settings.from_env()


@pytest.mark.parametrize("native", [False, True])
def test_cache_reads_use_the_actual_protocol_and_do_not_double_count_aliases(native):
    detail = "input_tokens_details" if native else "prompt_tokens_details"
    raw = {"input_tokens": 12, "cached_input_tokens": 4, "prompt_cache_hit_tokens": 4,
           detail: {"cached_tokens": 4, "cache_creation_input_tokens": 3}}
    usage, _ = usage_facts({"usage": raw}, native=native, cache_breakdown=True)
    assert usage == {"input_tokens": 12, "cached_input_tokens": 4,
                     "cache_write_input_tokens": 3, "uncached_input_tokens": 5}
    disabled, _ = usage_facts({"usage": raw}, native=native)
    assert disabled == {"input_tokens": 12, "cached_input_tokens": 4}
    raw[detail]["cached_tokens"] = True
    assert usage_facts({"usage": raw}, native=native)[1] == "invalid"


@pytest.mark.parametrize("native", [False, True])
@pytest.mark.parametrize("value", [None, True, -1, 1.5, "2", MAX_TOKEN_COUNT + 1])
def test_invalid_optional_cache_writes_remain_unknown(native, value):
    detail = "input_tokens_details" if native else "prompt_tokens_details"
    raw = {"input_tokens": 12, detail: {"cached_tokens": 4, "cache_creation_input_tokens": value}}
    usage, status = usage_facts({"usage": raw}, native=native, cache_breakdown=True)
    assert usage == {"input_tokens": 12, "cached_input_tokens": 4}
    assert status == "partial"


def test_cache_write_without_a_read_count_cannot_fabricate_a_cache_miss():
    usage, _ = usage_facts({"usage": {"input_tokens": 12, "prompt_tokens_details": {
        "cache_creation_input_tokens": 3}}}, cache_breakdown=True)
    assert usage == {"input_tokens": 12, "cache_write_input_tokens": 3}
    usage, _ = usage_facts({"usage": {"input_tokens": MAX_TOKEN_COUNT, "prompt_tokens_details": {
        "cached_tokens": 0, "cache_creation_input_tokens": MAX_TOKEN_COUNT}}}, cache_breakdown=True)
    assert usage["cache_write_input_tokens"] == MAX_TOKEN_COUNT
    assert usage["uncached_input_tokens"] == 0


def test_retry_cache_breakdown_sums_known_zero_and_drops_overflow():
    response = {"usage": {"input_tokens": MAX_TOKEN_COUNT, "output_tokens": 0,
                          "total_tokens": MAX_TOKEN_COUNT, "prompt_tokens_details": {
                              "cached_tokens": 0, "cache_creation_input_tokens": MAX_TOKEN_COUNT}}}
    facts = response_diagnostics(response, None, cache_breakdown=True)
    merged = merge_attempt_diagnostics(facts, facts, attempts=2)
    assert merged["usage_status"] == "invalid"
    assert merged["usage"] == {"cached_input_tokens": 0, "uncached_input_tokens": 0, "output_tokens": 0}
    assert merged["provider_request_count"] == 2
    assert sum_model_usage(facts["usage"], facts["usage"]) == {"cached_input_tokens": 0, "output_tokens": 0}


def test_fallback_usage_also_obeys_int64_bounds():
    facts = merge_attempt_diagnostics({}, {}, attempts=1, fallback_usage={
        "input_tokens": MAX_TOKEN_COUNT + 1, "output_tokens": 0, "cache_write_input_tokens": True})
    assert facts["usage_status"] == "invalid"
    assert facts["usage"] == {"output_tokens": 0}


def test_legacy_usage_summary_cannot_reintroduce_an_overflowed_total():
    usage = describe_model_usage(None, {"input_tokens": MAX_TOKEN_COUNT, "output_tokens": 1})
    assert usage["input_tokens"] == MAX_TOKEN_COUNT
    assert "total_tokens" not in usage
    assert describe_model_usage(None, {"input_tokens": 0, "output_tokens": 0})["total_tokens"] == 0


def test_failed_retry_keeps_cache_diagnostics_separate_from_legacy_usage():
    # 第一次结构错误触发已有的一次修复；第二次截断终止，只累计实际发生的两次调用。
    responses = [provider_response(content={}), provider_response(finish="length")]
    for response in responses:
        response["usage"]["prompt_tokens_details"] = {"cached_tokens": 0, "cache_creation_input_tokens": 4}
    calls = []

    def transport(_url, payload, _headers, _timeout):
        calls.append(copy.deepcopy(payload))
        return responses.pop(0)

    class Observer:
        def response(self, usage, status):
            assert status == "reported"
            assert usage == {"input_tokens": 7, "cached_input_tokens": 0, "output_tokens": 3, "total_tokens": 10}

    configured = settings(cache_usage_diagnostics=True)
    model = LocalLlamaCppAdapter(configured, transport=transport)
    app = GradingAgentApplication(configured, model=model, logger=lambda _event: None)
    request = valid_request()
    with model.material_execution(Observer()), pytest.raises(AgentError) as caught:
        app.grade(request, request["request_id"])
    assert caught.value.execution_diagnostics["usage"] == {
        "input_tokens": 14, "cached_input_tokens": 0, "output_tokens": 6, "total_tokens": 20,
        "cache_write_input_tokens": 8, "uncached_input_tokens": 6,
    }
    assert caught.value.payload()["execution_diagnostics"]["provider_request_count"] == 2
    assert len(calls) == 2
