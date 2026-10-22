"""认证内部请求的模型连接转换；模型连接本身不授予最终评分权限。"""

import http.client
import ipaddress
import json
import socket
import ssl
from dataclasses import replace
from functools import partial
from urllib.parse import urlsplit

from edugrade_worker_runtime import ResponseValidationError, read_json_response

from .errors import AgentError
from .model import DashScopeNativeAdapter, LocalLlamaCppAdapter
from .template_analysis_diagnostics import capture_provider_request

MAX_PROVIDER_ERROR_BYTES = 16 * 1024


def _provider_error_code(response):
    # 只为排错读取有界错误流，正文永不返回或记录；解析失败不能覆盖已知的 HTTP 错误。
    try:
        raw = response.read(MAX_PROVIDER_ERROR_BYTES + 1)
        if not isinstance(raw, bytes) or len(raw) > MAX_PROVIDER_ERROR_BYTES:
            return None
        value = json.loads(raw)
    except (OSError, ValueError, RecursionError, http.client.HTTPException):
        return None
    error = value.get("error") if isinstance(value, dict) else None
    return error.get("code") if isinstance(error, dict) else None


class ManagedNativePaperAdapter(DashScopeNativeAdapter):
    def request_structured(self, request_id, messages, schema, name):
        messages = [*messages, {"role": "system", "content": "Return a JSON object matching this schema: " + json.dumps(schema, ensure_ascii=False)}]
        return super().request_structured(request_id, messages, schema, name)


def _endpoint(value):
    parsed = urlsplit(value)
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.query or parsed.fragment):
        raise ValueError("invalid managed endpoint")
    _ = parsed.port
    return parsed


def _validated_tls_context():
    """Build a client context that verifies both the chain and hostname."""
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    context.check_hostname = True
    context.verify_mode = ssl.CERT_REQUIRED
    context.load_default_certs()
    return context


def public_json_transport(url, payload, headers, timeout):
    """Pin a public DNS address while preserving TLS hostname verification."""
    parsed = _endpoint(url)
    addresses = socket.getaddrinfo(parsed.hostname, parsed.port or 443, type=socket.SOCK_STREAM)
    if not addresses or any(not ipaddress.ip_address(item[4][0]).is_global for item in addresses):
        raise AgentError("model_request_rejected", "provider must use a public HTTPS endpoint", status=502)
    address = addresses[0][4]
    body = json.dumps(payload, ensure_ascii=False, allow_nan=False).encode("utf-8")
    native_input = payload.get("input")
    messages = payload.get("messages", [])
    if isinstance(native_input, dict):
        messages = [*messages, *native_input.get("messages", [])]
    # 原生视觉把图像放在 input.messages[].content[].image；不能误用纯文本的 2 MB 上限。
    multimodal = any(
        isinstance(message, dict)
        and isinstance(message.get("content"), list)
        and any(
            isinstance(part, dict) and (part.get("type") == "image_url" or isinstance(part.get("image"), str))
            for part in message["content"]
        )
        for message in messages
    )
    max_body_bytes = 48 * 1024 * 1024 if multimodal else 2_000_000
    if len(body) > max_body_bytes:
        raise AgentError("invalid_request", "document model request is too large", status=413)
    # 仅活动的模板诊断会采集摘要；在这里观察兼容转换后的真实参数，不记录地址、凭据或请求内容。
    capture_provider_request(payload)
    connection = http.client.HTTPSConnection(
        parsed.hostname,
        parsed.port or 443,
        timeout=timeout,
        context=_validated_tls_context(),
    )
    # HTTPSConnection 保留原主机名，用于 SNI 和证书检查；
    # 只将套接字目标替换为上面已验证的地址。
    connection._create_connection = lambda _target, timeout, source_address=None: socket.create_connection(address[:2], timeout, source_address)
    try:
        connection.request("POST", parsed.path or "/", body=body, headers=headers)
        response = connection.getresponse()
        if not 200 <= response.status < 300:
            if response.status in {401, 403}:
                code = "model_auth_failed"
            elif response.status == 429:
                code = "model_rate_limited"
            else:
                code = "model_request_rejected"
            raise AgentError(code, f"provider returned HTTP {response.status}", status=502,
                             upstream_http_status=response.status, upstream_error_code=_provider_error_code(response))
        try:
            raw = read_json_response(response, 4_000_000)
        except http.client.HTTPException as exc:
            # 成功状态下响应也可能中途断流；这是传输失败，不能当作完整模型结果或透传残留正文。
            raise AgentError("model_unavailable", "provider response could not be read", status=503, retryable=True) from exc
        except ResponseValidationError as exc:
            raise AgentError("model_output_invalid", "provider response is invalid", status=502) from exc
        return json.loads(raw)
    finally:
        connection.close()


def _compatible_transport(config, parsed, url, body, headers, timeout):
    body.pop("chat_template_kwargs", None)
    body.pop("seed", None)
    if parsed.hostname == "api.deepseek.com" or config["model_name"].strip().lower().startswith("deepseek"):
        # 解析试卷不需要推理 token；显式关闭供应商默认思考，不依赖提示词后缀。
        body["thinking"] = {"type": "disabled"}
        body["reasoning_effort"] = "none"
    schema = body["response_format"]["json_schema"]["schema"]
    body["response_format"] = {"type": "json_object"}
    if any(isinstance(message.get("content"), list) for message in body["messages"] if isinstance(message, dict)):
        # 原图结构较密集，保留现有输出预算下限；本地仍按原 schema 校验结果。
        body["max_tokens"] = max(int(body.get("max_tokens", 0)), 8_192)
    schema_instruction = "Return a JSON object matching this schema: " + json.dumps(
        schema, ensure_ascii=False, separators=(",", ":")
    )
    messages = list(body["messages"])
    if messages and messages[0].get("role") == "system" and isinstance(messages[0].get("content"), str):
        messages[0] = {**messages[0], "content": messages[0]["content"] + "\n" + schema_instruction}
    else:
        messages.insert(0, {"role": "system", "content": schema_instruction})
    # 稳定的提示词和 schema 必须在每份试卷的图像之前，供供应商复用前缀缓存。
    body["messages"] = messages
    return public_json_transport(url, body, headers, timeout)


def paper_model(application, payload):
    # 学校托管模型仅在当前解析请求内生效，同时移除凭据字段，避免进入后续题目内容。
    config = payload.pop("managed_model", None) if isinstance(payload, dict) else None
    if config is None:
        return application.model
    return create_managed_model(application.settings, config)


def create_managed_model(base_settings, config):
    """复用受控 HTTPS transport；调用方负责学校选择及冻结配置版本校验。"""
    try:
        if not isinstance(config, dict):
            raise TypeError("invalid managed model")
        fields = ("adapter_type", "base_url", "api_key", "model_name", "model_version")
        if any(not isinstance(config.get(key), str) or not config[key].strip() for key in fields):
            raise ValueError("missing managed model fields")
        if len(config["api_key"]) < 16 or len(config["api_key"]) > 1024:
            raise ValueError("invalid managed credential")
        parsed = _endpoint(config["base_url"])
        adapter = config["adapter_type"]
        if adapter not in {"openai_compatible", "dashscope_native"}:
            raise ValueError("unsupported managed adapter")
        if adapter == "dashscope_native" and (parsed.hostname != "dashscope.aliyuncs.com" or parsed.path.rstrip("/") != "/api/v1"):
            raise ValueError("invalid native endpoint")
        settings = replace(base_settings, model_base_url=config["base_url"].rstrip("/"),
                           model_api_key=config["api_key"], model_name=config["model_name"],
                           model_version=config["model_version"], adapter_type=adapter,
                           provider_key=str(config.get("provider_key") or adapter).strip())
        if adapter == "dashscope_native":
            return ManagedNativePaperAdapter(settings, transport=public_json_transport)
        return LocalLlamaCppAdapter(settings, transport=partial(_compatible_transport, config, parsed))
    except (ValueError, TypeError) as exc:
        raise AgentError("invalid_request", "school model configuration is invalid", status=400) from exc
