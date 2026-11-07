"""按职责保留现有断言与固定响应，不改变模型协议或运行输入。"""





import json
import socket
from types import SimpleNamespace
from unittest.mock import Mock, patch
from urllib import error as urlerror
from urllib import request as urlrequest

import pytest
from grading_agent.errors import AgentError
from grading_agent.managed_model import paper_model, public_json_transport
from grading_agent.template_analysis import TemplateAnalyzer
from grading_agent.template_analysis_coarse import coarse_model_output_schema
from grading_agent.template_analysis_model import prepare_template_model
from helpers import settings
from test_template_analysis import StructuredModel, output_for, payload
from test_template_analysis import http_server as shared_http_server

# pytest 按参数名查找夹具，这里复用原 HTTP 服务的启动与关闭流程。
http_server = shared_http_server

def test_compatible_coarse_response_does_not_gain_legacy_nullable_fields():
    # 真实粗阶段响应曾被兼容层补入旧编号/关联字段，继而被 additionalProperties 整页拒绝。
    supplied = {"candidates": [{"role": "answer", "bbox": {"x": .1, "y": .2, "width": .3, "height": .4},
        "labels": ["15"], "identity_label": None, "static_label": None, "option_pattern": "none",
        "identity_pattern": "none", "barcode_pattern": "none", "boundary_basis": "printed_frame", "anchor_bbox": None}]}
    response = {"choices": [{"finish_reason": "stop", "message": {
        "role": "assistant", "content": json.dumps(supplied)}}]}
    send = Mock(return_value=response)
    adapter = prepare_template_model(SimpleNamespace(settings=settings(adapter_type="openai_compatible"), transport=send))
    with adapter.session("coarse-compatible-regression"):
        output = adapter.request_structured("coarse-compatible-regression", [], coarse_model_output_schema(),
                                            "answer_sheet_template_analysis")
    assert output == supplied
    assert "candidate_id" not in output["candidates"][0]
    assert "printed_question_label" not in output["candidates"][0]
    assert json.loads(response["choices"][0]["message"]["content"]) == supplied


def test_managed_document_model_reuses_image_transport_without_exposing_credentials():
    request = payload()
    config = {"adapter_type": "openai_compatible", "base_url": "https://vision.example.test/v1",
              "api_key": "school-model-api-key-at-least-16", "model_name": "vision-model",
              "model_version": "vision-test-v1", "provider_key": "vision-provider"}
    request["managed_model"] = config
    app = SimpleNamespace(settings=settings(), model=object())
    model = prepare_template_model(paper_model(app, request))
    response = {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps(output_for(request))}}],
                "usage": {"prompt_tokens": 20, "completion_tokens": 10}}
    with patch("grading_agent.managed_model.public_json_transport", return_value=response) as send:
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert result["model_identity"]["model_version"] == "vision-test-v1"
    assert config["api_key"] not in json.dumps(result)
    transmitted = send.call_args.args[1]
    assert transmitted["messages"][1]["content"][1]["type"] == "image_url"
    assert transmitted["messages"][1]["content"][1]["image_url"]["detail"] == "high"
    assert transmitted["max_tokens"] >= 8192
    assert send.call_count == 1
    assert config["api_key"] not in json.dumps(transmitted)


@pytest.mark.parametrize("field", ["candidate_id", "printed_question_label", "identity_label"])
def test_compatible_identity_region_accepts_omitted_nullable_metadata_without_guessing(field):
    request = payload()
    row = output_for(request)["candidates"][0]
    row.update(candidate_id=None, printed_question_label=None, identity_label="姓名", role="identity", association_status="unassociated")
    row["evidence_refs"] = [f"source-page:{request['page']['sha256']}:{request['page']['page_no']}"]
    row.pop(field)
    response = {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps({"candidates": [row]})}}]}
    source = SimpleNamespace(settings=settings(adapter_type="openai_compatible"), transport=Mock(return_value=response))
    result = TemplateAnalyzer(prepare_template_model(source), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert result["candidates"][0]["printed_question_label"] is None
    assert result["candidates"][0]["association_status"] == "unassociated"
    # 空值补齐只解决协议兼容；缺少本地图像证据时仍不能把模型类别当成已核验事实。
    assert result["candidates"][0]["identity_label"] is None
    assert result["candidates"][0]["semantic_check"] == "needs_review"
    assert field not in row


@pytest.mark.parametrize("field", ["role", "proposed_bbox"])
def test_compatible_identity_metadata_repair_cannot_invent_role_or_coordinates(field):
    request = payload()
    row = output_for(request)["candidates"][0]
    row.pop(field)
    response = {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps({"candidates": [row]})}}]}
    source = SimpleNamespace(settings=settings(adapter_type="openai_compatible"), transport=Mock(return_value=response))
    assert TemplateAnalyzer(prepare_template_model(source), prompt_variant="v6").analyze(request)["semantic_status"] == "failed"


def test_compatible_template_model_rejects_truncated_json_with_known_usage():
    request = payload()
    config = {"adapter_type": "openai_compatible", "base_url": "https://vision.example.test/v1",
              "api_key": "school-model-api-key-at-least-16", "model_name": "vision-model", "model_version": "v1"}
    request["managed_model"] = config
    app = SimpleNamespace(settings=settings(), model=object())
    response = {"choices": [{"finish_reason": "length", "message": {
        "role": "assistant", "content": json.dumps(output_for(request)),
    }}], "usage": {"prompt_tokens": 20, "completion_tokens": 10}}
    with patch("grading_agent.managed_model.public_json_transport", return_value=response) as send:
        model = prepare_template_model(paper_model(app, request))
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert result["model_usage"]["total_tokens"] == 30
    assert result["model_usage"]["request_count"] == 1
    assert send.call_count == 1
    assert send.call_args.args[1]["max_tokens"] >= 8192
    assert all(row["printed_question_label"] is None for row in result["candidates"])


def test_compatible_template_budget_is_local_to_the_single_template_request():
    request = payload()
    response = {"choices": [{"finish_reason": "stop", "message": {
        "role": "assistant", "content": json.dumps(output_for(request)),
    }}], "usage": {"prompt_tokens": 10, "completion_tokens": 20}}
    transport = Mock(return_value=response)
    original_settings = settings(adapter_type="openai_compatible", model_max_output_tokens=512)
    source = SimpleNamespace(settings=original_settings, transport=transport)
    model = prepare_template_model(source)
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert transport.call_count == 1
    assert transport.call_args.args[1]["max_tokens"] == 8192
    assert source.settings.model_max_output_tokens == 512
    assert model.settings.model_max_output_tokens == 512


@pytest.mark.parametrize("finish_reason", ["stop", "length"])
def test_native_template_model_uses_real_multimodal_protocol_and_keeps_failed_usage(finish_reason):
    request = payload()
    config = {"adapter_type": "dashscope_native", "base_url": "https://dashscope.aliyuncs.com/api/v1",
              "api_key": "school-model-api-key-at-least-16", "model_name": "qwen3-vl-plus",
              "model_version": "vision-test-v1", "provider_key": "dashscope"}
    request["managed_model"] = config
    app = SimpleNamespace(settings=settings(), model=object())
    response = {"request_id": "native-template-test-1", "output": {"choices": [{
        "finish_reason": finish_reason, "message": {"role": "assistant", "content": [{
            "text": json.dumps(output_for(request)),
        }]},
    }]}, "usage": {"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}}
    with patch("grading_agent.managed_model.public_json_transport", return_value=response) as send:
        model = prepare_template_model(paper_model(app, request))
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    url, body, headers, _ = send.call_args.args
    assert url == "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
    assert body["input"]["messages"][0]["role"] == "system"
    assert body["input"]["messages"][1]["content"][1]["image"].endswith(request["page"]["data_base64"])
    assert "image_url" not in json.dumps(body)
    assert body["parameters"]["enable_thinking"] is False
    assert body["parameters"]["max_tokens"] >= 8192
    assert headers["Authorization"] == "Bearer " + config["api_key"]
    assert config["api_key"] not in json.dumps(body)
    assert config["api_key"] not in json.dumps(result)
    assert result["model_usage"]["total_tokens"] == 120
    assert result["semantic_status"] == ("completed" if finish_reason == "stop" else "failed")


def test_native_visual_transport_uses_image_budget_while_text_keeps_its_budget():
    response = Mock(status=200)
    connection = Mock()
    connection.getresponse.return_value = response
    resolved = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", 443))]
    image = "data:image/png;base64," + "A" * 2_100_000
    native = {"input": {"messages": [{"role": "user", "content": [{"image": image}]}]}}
    with (
        patch("socket.getaddrinfo", return_value=resolved),
        patch("http.client.HTTPSConnection", return_value=connection),
        patch("grading_agent.managed_model.read_json_response", return_value=b"{}"),
    ):
        assert public_json_transport("https://dashscope.aliyuncs.com/api/v1", native, {}, 1) == {}
        with pytest.raises(AgentError) as caught:
            public_json_transport("https://dashscope.aliyuncs.com/api/v1", {"messages": [{"role": "user", "content": image}]}, {}, 1)
    assert caught.value.status == 413
    connection.request.assert_called_once()




def test_internal_analysis_http_requires_service_auth_and_does_not_require_grading_idempotency(http_server):
    base_url, app = http_server
    request = urlrequest.Request(f"{base_url}/answer-sheet/analyze", data=json.dumps(payload(candidates=[])).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
    with pytest.raises(urlerror.HTTPError) as caught:
        urlrequest.urlopen(request, timeout=3)
    assert caught.value.code == 401
    request.add_header("Authorization", f"Bearer {app.settings.service_token}")
    with urlrequest.urlopen(request, timeout=3) as response:
        result = json.loads(response.read())
    assert result["status"] == "review_required"
    assert result["semantic_status"] == "not_configured"
    assert len(result["candidates"]) == 2


def test_internal_analysis_http_uses_request_scoped_managed_vision_model(http_server, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT", "v6")
    base_url, app = http_server
    body = payload()
    model = StructuredModel(output_for(body))
    body["managed_model"] = {"injected_by_gateway": True}

    def select_model(_app, request_payload):
        request_payload.pop("managed_model")
        return model

    request = urlrequest.Request(f"{base_url}/answer-sheet/analyze", data=json.dumps(body).encode(), headers={
        "Content-Type": "application/json", "Authorization": f"Bearer {app.settings.service_token}",
    }, method="POST")
    with (
        patch("grading_agent.server.paper_model", side_effect=select_model),
        urlrequest.urlopen(request, timeout=3) as response,
    ):
        result = json.loads(response.read())
    assert result["semantic_status"] == "completed"
    assert len(model.calls) == 1
