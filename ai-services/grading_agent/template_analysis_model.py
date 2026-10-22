"""模板分析专用的原生视觉适配，不改变既有试卷解析或评分供应商协议。"""

import copy
import json
import threading
from functools import partial

from .dashscope_native_contract import (
    DASHSCOPE_MULTIMODAL_GENERATION_PATH,
    DASHSCOPE_TEXT_GENERATION_PATH,
    parse_dashscope_multimodal_response,
)
from .errors import AgentError
from .managed_model import ManagedNativePaperAdapter
from .model import LocalLlamaCppAdapter, _decode_structured_content
from .template_analysis_diagnostics import capture_provider_response


def prepare_template_model(model):
    """模型配置及公共 HTTPS 传输仍由 paper_model 校验，只替换此操作的消息协议。"""
    settings = getattr(model, "settings", None)
    adapter_type = getattr(settings, "adapter_type", "")
    if adapter_type == "dashscope_native":
        return NativeTemplateModel(model)
    if adapter_type == "openai_compatible":
        return CompatibleTemplateModel(model)
    return model


class CompatibleTemplateModel(LocalLlamaCppAdapter):
    def __init__(self, model):
        self._provider_response = threading.local()
        super().__init__(model.settings, transport=partial(self._capture_transport, model.transport))

    def request_structured(self, request_id, messages, schema, name):
        output = super().request_structured(request_id, messages, schema, name)
        try:
            choice = self._provider_response.value["choices"][0]
            completed = choice["finish_reason"] == "stop" and choice["message"]["role"] == "assistant"
        except (KeyError, IndexError, TypeError):
            completed = False
        if not completed:
            raise AgentError("model_output_invalid", "template model response did not complete normally",
                             status=502, request_id=request_id)
        return output

    def _capture_transport(self, transport, url, payload, headers, timeout):
        # 整页可能包含二十多个区域；只提高本次模板请求的输出预算，不修改评分模型设置或重试调用。
        payload = copy.deepcopy(payload)
        schema = payload.get("response_format", {}).get("json_schema", {}).get("schema")
        payload["max_tokens"] = max(payload.get("max_tokens", 0), 8192)
        response = transport(url, payload, headers, timeout)
        capture_provider_response(response)
        response = _materialize_nullable_candidate_fields(response, schema)
        if payload.get("response_format", {}).get("json_schema", {}).get("name") == "answer_sheet_page_order":
            response = _materialize_nullable_page_fields(response)
        self._provider_response.value = response
        return response


def _materialize_nullable_candidate_fields(response, schema=None):
    # 兼容接口的 JSON 模式有时省略 null。只补回无语义的空字段；题号、角色和坐标仍须通过原 schema。
    # 不修改供应商原对象，且保留 finish_reason/usage，让截断和计费证据继续按原规则处理。
    nullable = ("candidate_id", "printed_question_label", "identity_label")
    if isinstance(schema, dict):
        properties = schema.get("properties", {}).get("candidates", {}).get("items", {}).get("properties", {})
        # 粗布局契约没有候选编号及单题关联字段；补入旧字段会使正确 JSON 被 additionalProperties 拒绝。
        nullable = tuple(field for field in nullable if field in properties)
    if not nullable:
        return response
    try:
        content = _decode_structured_content(response["choices"][0]["message"]["content"], "")
    except (AgentError, KeyError, IndexError, TypeError):
        return response
    if not isinstance(content, dict) or not isinstance(content.get("candidates"), list):
        return response
    content = copy.deepcopy(content)
    for row in content["candidates"]:
        if isinstance(row, dict):
            for field in nullable:
                row.setdefault(field, None)
    updated = copy.deepcopy(response)
    updated["choices"][0]["message"]["content"] = json.dumps(content, ensure_ascii=False)
    return updated


def _materialize_nullable_page_fields(response):
    # 无印刷页码时部分兼容接口会省略 null；只补空值，不推断页码或排序依据。
    try:
        content = _decode_structured_content(response["choices"][0]["message"]["content"], "")
    except (AgentError, KeyError, IndexError, TypeError):
        return response
    if not isinstance(content, dict) or not isinstance(content.get("pages"), list):
        return response
    content = copy.deepcopy(content)
    for row in content["pages"]:
        if isinstance(row, dict):
            for field in ("printed_page_no", "printed_total_pages"):
                row.setdefault(field, None)
    updated = copy.deepcopy(response)
    updated["choices"][0]["message"]["content"] = json.dumps(content, ensure_ascii=False)
    return updated


class NativeTemplateModel(ManagedNativePaperAdapter):
    # 此包装器在 transport 前执行原生多模态转换；普通评分 adapter 不继承这项能力。
    supports_native_visual_wrapper = True
    def __init__(self, model):
        self._native_response = threading.local()
        super().__init__(model.settings, transport=partial(self._vision_transport, model.transport))

    def request_structured(self, request_id, messages, schema, name):
        output = super().request_structured(request_id, messages, schema, name)
        # 通用适配器先记录供应商用量，再检查截断/拒答，失败也能保留已知费用证据。
        parse_dashscope_multimodal_response(self._native_response.value)
        return output

    def _vision_transport(self, transport, url, payload, headers, timeout):
        url, payload = _native_vision_request(url, payload)
        response = transport(url, payload, headers, timeout)
        capture_provider_response(response)
        self._native_response.value = response
        return response


def _native_vision_request(url, payload):
    payload = copy.deepcopy(payload)
    messages = payload["input"]["messages"]
    system = []
    converted = []
    for message in messages:
        content = message["content"]
        if message["role"] == "system":
            system.append(content)
            continue
        if isinstance(content, list):
            parts = []
            for part in content:
                if part["type"] == "image_url":
                    parts.append({"image": part["image_url"]["url"]})
                else:
                    parts.append({"text": part["text"]})
        else:
            parts = [{"text": content}]
        converted.append({"role": message["role"], "content": parts})
    # 通用文档适配器把 schema 放在尾部系统消息；原生视觉请求合并到首条系统消息。
    payload["input"]["messages"] = [{"role": "system", "content": [{"text": "\n".join(system)}]}, *converted]
    payload["parameters"]["max_tokens"] = max(payload["parameters"].get("max_tokens", 0), 8192)
    payload["parameters"]["enable_thinking"] = False
    native_url = url.removesuffix(DASHSCOPE_TEXT_GENERATION_PATH) + DASHSCOPE_MULTIMODAL_GENERATION_PATH
    return native_url, payload
