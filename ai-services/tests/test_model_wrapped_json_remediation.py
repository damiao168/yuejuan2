import json
import tracemalloc

import pytest
from grading_agent.errors import AgentError
from grading_agent.model import (
    MAX_STRUCTURED_JSON_DEPTH,
    MAX_WRAPPED_JSON_ATTEMPTS,
    _decode_structured_content,
)


def test_nested_wrapped_object_does_not_retain_each_inner_decoding():
    content = '```json\n' + '{"inner":' * 96 + '{"text":"' + 'a' * 65536 + '"}' + '}' * 96 + '\n```'
    # 合成输入只用几十 KiB；防止按每层复制并保留同一长字符串，无需耗尽服务内存。
    tracemalloc.start()
    try:
        result = _decode_structured_content(content, "synthetic-nested")
        _, peak = tracemalloc.get_traced_memory()
    finally:
        tracemalloc.stop()
    for _ in range(96):
        result = result["inner"]
    assert result["text"] == 'a' * 65536
    assert peak < len(content) * 10


@pytest.mark.parametrize("wrapped", [False, True])
def test_excessive_depth_is_a_controlled_model_output_error(wrapped):
    content = '[' * (MAX_STRUCTURED_JSON_DEPTH + 1) + '0' + ']' * (MAX_STRUCTURED_JSON_DEPTH + 1)
    if wrapped:
        content = '```json\n' + content + '\n```'
    with pytest.raises(AgentError) as error:
        _decode_structured_content(content, "synthetic-too-deep")
    assert error.value.code == "model_output_invalid"


def test_many_malformed_candidate_starts_have_a_bounded_decode_budget():
    # 每个失败前缀都保持括号平衡，单独覆盖尝试上限而非嵌套深度门禁。
    content = '{bad}' * (MAX_WRAPPED_JSON_ATTEMPTS + 1)
    with pytest.raises(AgentError) as error:
        _decode_structured_content(content, "synthetic-candidates")
    assert error.value.code == "model_output_invalid"


def test_braces_in_strings_and_equal_length_candidate_order_are_preserved():
    expected = {"text": ('[{"escaped\\' * 256)}
    assert _decode_structured_content('answer: ' + json.dumps(expected), "synthetic-escaped") == expected
    assert _decode_structured_content('before {"a":1} after {"b":2}', "synthetic-tie") == {"a": 1}
    assert _decode_structured_content('{bad} then {"value":{"ok":true}}', "synthetic-recover") == {"value": {"ok": True}}
