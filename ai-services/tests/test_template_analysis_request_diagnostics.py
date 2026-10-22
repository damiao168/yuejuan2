"""用合成传输核对最终模式；这些记录不证明供应商已经接收或支持请求。"""

import copy
import hashlib
import json
import socket
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from grading_agent import managed_model
from grading_agent.errors import AgentError
from grading_agent.template_analysis_diagnostics import (
    AnalysisTrace,
    capture_provider_request,
)
from grading_agent.template_analysis_model import prepare_template_model
from helpers import settings

_PRIVATE = "PRIVATE_SYNTHETIC_REQUEST_VALUE"
_SCHEMA = {"type": "object", "additionalProperties": False, "required": ["candidates"],
           "properties": {"candidates": {"type": "array", "items": {"type": "object"}}}}


def trace():
    return AnalysisTrace({"request_id": "synthetic-diagnostics-0001", "page": {
        "page_no": 1, "sha256": "a" * 64, "width": 40, "height": 30}}, {}, None)


def digest(value):
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(raw.encode()).hexdigest()


def stub_https(monkeypatch, provider_response):
    # 继续走公开传输函数及响应读取；只有 DNS/TLS socket 被替换，不会连接真实供应商。
    addresses = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", 443))]
    raw = json.dumps(provider_response).encode()
    response = Mock(status=200, headers={"Content-Type": "application/json", "Content-Length": str(len(raw))})
    response.read.return_value = raw
    connection = Mock()
    connection.getresponse.return_value = response
    monkeypatch.setattr(managed_model.socket, "getaddrinfo", Mock(return_value=addresses))
    monkeypatch.setattr(managed_model, "_validated_tls_context", lambda: object())
    monkeypatch.setattr(managed_model.http.client, "HTTPSConnection", Mock(return_value=connection))
    return connection


@pytest.mark.parametrize("adapter,mode", [("openai_compatible", "json_object"),
                                          ("dashscope_native", "schema_in_prompt")])
def test_template_diagnostics_observe_final_transformed_transport_without_request_content(adapter, mode, monkeypatch, caplog):
    content = '{"candidates":[]}'
    choice = {"finish_reason": "stop", "message": {"role": "assistant", "content": content}}
    provider = {"choices": [choice]}
    if adapter == "dashscope_native":
        choice["message"]["content"] = [{"text": content}]
        provider = {"request_id": "synthetic-provider", "output": {"choices": [choice]},
                    "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}
    connection = stub_https(monkeypatch, provider)
    config = {"adapter_type": adapter, "api_key": "synthetic-key-at-least-sixteen", "model_name": "synthetic-vision",
              "model_version": "fixed-v1", "base_url": "https://models.example.test/v1"}
    if adapter == "dashscope_native":
        config["base_url"] = "https://dashscope.aliyuncs.com/api/v1"
    model = prepare_template_model(managed_model.paper_model(SimpleNamespace(settings=settings()), {"managed_model": config}))
    messages = [{"role": "system", "content": "synthetic system"}, {"role": "user", "content": [
        {"type": "text", "text": _PRIVATE},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64,cG5n", "detail": "high"}},
    ]}]
    original = copy.deepcopy(messages)
    record = trace()
    record.bind_request(messages, _SCHEMA, prompt_variant="v7-evidence")
    with record.active():
        assert model.request_structured("synthetic", messages, _SCHEMA, "template") == {"candidates": []}
    actual = json.loads(connection.request.call_args.kwargs["body"])
    actual_messages = actual["messages"] if adapter == "openai_compatible" else actual["input"]["messages"]
    actual_system = actual_messages[0]["content"]
    if adapter == "dashscope_native":
        actual_system = actual_system[0]["text"]
    requested, prepared = record.metadata["request_contract"], record.metadata["provider_request"]
    assert requested["requested_output_mode"] == "schema_contract"
    assert requested["model_output_schema_sha256"] == digest(_SCHEMA)
    assert prepared == {"stage": "transport_prepared", "output_mode": mode,
                        "system_prompt_sha256": hashlib.sha256(actual_system.encode()).hexdigest(),
                        "image_inputs": [{"detail": "high" if adapter == "openai_compatible" else None,
                                          "sha256": hashlib.sha256(b"png").hexdigest(), "bytes": 3}]}
    assert prepared["system_prompt_sha256"] != requested["system_prompt_sha256"]
    assert messages == original
    assert _PRIVATE not in json.dumps(record.metadata)
    assert config["api_key"] not in json.dumps(record.metadata)
    assert _PRIVATE not in caplog.text and config["api_key"] not in caplog.text
    connection.request.assert_called_once()
    connection.close.assert_called_once()


def test_full_page_and_crop_transport_images_remain_separate_without_raw_content():
    record = trace()
    for encoded in ("cG5n", "Y3JvcA=="):
        with record.active():
            capture_provider_request({"messages": [{"role": "user", "content": [{
                "type": "image_url", "image_url": {"url": "data:image/png;base64," + encoded, "detail": "high"}}]}]})
    rows = record.metadata["provider_requests"]
    assert len(rows) == 2
    assert rows[0]["image_inputs"][0]["sha256"] == hashlib.sha256(b"png").hexdigest()
    assert rows[1]["image_inputs"][0]["sha256"] == hashlib.sha256(b"crop").hexdigest()
    assert "base64" not in json.dumps(record.metadata)


@pytest.mark.parametrize("strict,expected", [(True, "json_schema_strict"), (False, "json_schema"),
                                             (None, "json_schema"), ("true", "json_schema")])
def test_schema_mode_records_only_actual_boolean_strict_and_stable_schema_digest(strict, expected):
    record = trace()
    payload = {"messages": [{"role": "system", "content": "stable"}], "response_format": {
        "type": "json_schema", "json_schema": {"strict": strict, "schema": _SCHEMA}}}
    before = copy.deepcopy(payload)
    with record.active():
        capture_provider_request(payload)
    assert payload == before
    assert record.metadata["provider_request"]["output_mode"] == expected
    assert record.metadata["provider_request"]["model_output_schema_sha256"] == digest(_SCHEMA)


def test_request_capture_without_active_trace_does_not_inspect_payload():
    payload = Mock()
    payload.get.side_effect = AssertionError("must not inspect another operation")
    capture_provider_request(payload)
    payload.get.assert_not_called()


@pytest.mark.parametrize("invalid", [None, [], {"response_format": ["invalid"]},
                                      {"messages": None}, {"response_format": {"type": "json_schema", "json_schema": "invalid"}}])
def test_malformed_diagnostic_input_sets_fixed_failure_without_private_value_or_secondary_exception(invalid):
    record = trace()
    with record.active():
        capture_provider_request(invalid)
    assert record.metadata["request_capture_failed"] is True
    assert "provider_request" not in record.metadata


def test_rejected_private_destination_is_not_misreported_as_transport_prepared(monkeypatch):
    record = trace()
    monkeypatch.setattr(managed_model.socket, "getaddrinfo", Mock(return_value=[
        (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("127.0.0.1", 443))]))
    with record.active(), pytest.raises(AgentError):
        managed_model.public_json_transport("https://models.example.test/v1", {}, {}, 1)
    assert "provider_request" not in record.metadata


def test_failed_send_keeps_prepared_stage_without_claiming_delivery(monkeypatch):
    connection = stub_https(monkeypatch, {})
    connection.request.side_effect = OSError(_PRIVATE)
    record = trace()
    with record.active(), pytest.raises(OSError):
        managed_model.public_json_transport("https://models.example.test/v1", {
            "messages": [], "response_format": {"type": "json_object"}}, {}, 1)
    assert record.metadata["provider_request"]["stage"] == "transport_prepared"
    assert "delivered" not in json.dumps(record.metadata)
    assert _PRIVATE not in json.dumps(record.metadata)
    connection.close.assert_called_once()
