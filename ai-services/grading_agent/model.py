import json
import re
import threading
from contextlib import contextmanager
from urllib import error as urlerror
from urllib import request as urlrequest

from edugrade_worker_runtime import ResponseValidationError, read_json_response

from . import structured_output
from .dashscope_native_contract import (
    DASHSCOPE_TEXT_GENERATION_PATH,
    build_dashscope_grading_payload,
    map_dashscope_error,
)
from .dashscope_native_transport import (
    JSON_CONTENT_TYPE,
    MAX_DASHSCOPE_RESPONSE_BYTES,
    MAX_DASHSCOPE_TEXT_REQUEST_BYTES,
)
from .errors import AgentError
from .execution_diagnostics import (
    LEGACY_TOKEN_FIELDS,
    MAX_TOKEN_COUNT,
    completed_content,
    response_diagnostics,
)
from .grading_prompts import (
    PromptRegistry,
    grading_output_schema,
)
from .structured_output import (
    _decode_structured_content,
    _validate_structured_output,
)

# 保留模型模块的输出限制常量，结构化解码使用同一来源。
MAX_STRUCTURED_JSON_DEPTH = structured_output.MAX_STRUCTURED_JSON_DEPTH
MAX_WRAPPED_JSON_ATTEMPTS = structured_output.MAX_WRAPPED_JSON_ATTEMPTS


def _nonnegative_token_count(value):
    return value if isinstance(value, int) and not isinstance(value, bool) and value >= 0 else 0


def _usage_detail_count(usage, details_name, count_name):
    details = usage.get(details_name)
    return _nonnegative_token_count(details.get(count_name, 0)) if isinstance(details, dict) else 0


def _openai_usage(response):
    """Normalize provider usage without trusting it for billing decisions."""
    usage = response.get("usage") if isinstance(response, dict) else None
    if not isinstance(usage, dict):
        return {}

    def count(name):
        return _nonnegative_token_count(usage.get(name, 0))

    prompt = count("prompt_tokens") or count("input_tokens")
    completion = count("completion_tokens") or count("output_tokens")
    total = count("total_tokens") or prompt + completion
    # 两种缓存字段可能描述同一批 token，取较大值，不能相加重复计入。
    cached = max(
        _usage_detail_count(usage, "prompt_tokens_details", "cached_tokens"),
        count("prompt_cache_hit_tokens"),
    )
    reasoning = _usage_detail_count(usage, "completion_tokens_details", "reasoning_tokens")
    return {
        "input_tokens": prompt,
        "cached_input_tokens": cached,
        "output_tokens": completion,
        "reasoning_tokens": reasoning,
        "total_tokens": total,
    }


def sum_model_usage(accumulated, latest):
    """累计已知字段；缺失用量不能补零后伪装成完整调用统计。"""
    fields = LEGACY_TOKEN_FIELDS
    latest = latest if isinstance(latest, dict) else {}
    # 此接口仍供旧结算回执使用，不能携带仅诊断可用的缓存拆分字段。
    normalized = {key: value for key, value in accumulated.items()
                  if key in fields and type(value) is int and 0 <= value <= MAX_TOKEN_COUNT}
    for field in fields:
        if field not in latest:
            continue
        value = latest[field]
        if type(value) is not int or not 0 <= value <= MAX_TOKEN_COUNT:
            continue
        total = normalized.get(field, 0) + value
        if total <= MAX_TOKEN_COUNT:
            normalized[field] = total
        else:
            # 跨语言回执使用 int64；溢出时省略该字段，不发送无法解码的累计值。
            normalized.pop(field, None)
    return normalized


def describe_model_usage(model, usage, request_count=1):
    """Add the non-secret model identity needed by the platform usage ledger."""
    normalized = sum_model_usage({}, usage)
    if "total_tokens" not in normalized and all(field in normalized for field in ("input_tokens", "output_tokens")):
        # 旧文档解析摘要使用输入与输出之和；评分执行事实仍单独保留供应商字段是否缺失。
        total = normalized["input_tokens"] + normalized["output_tokens"]
        if total <= MAX_TOKEN_COUNT:
            normalized["total_tokens"] = total
    settings = getattr(model, "settings", None)
    normalized["provider_key"] = str(getattr(settings, "provider_key", "") or "")
    normalized["model_name"] = str(getattr(settings, "model_name", "") or "")
    normalized["request_count"] = max(int(request_count or 0), 0)
    return normalized


def _map_model_http_error(exc, request_id, provider="local model"):
    if exc.code in {400, 422}:
        return AgentError(
            "model_request_rejected",
            f"{provider} rejected the request with HTTP {exc.code}",
            status=502,
            request_id=request_id,
        )
    if exc.code in {401, 403}:
        return AgentError(
            "model_auth_failed",
            f"{provider} authentication failed with HTTP {exc.code}",
            status=502,
            request_id=request_id,
        )
    if exc.code == 429:
        return AgentError(
            "model_rate_limited",
            f"{provider} rate limit was reached",
            status=503,
            retryable=True,
            request_id=request_id,
        )
    return AgentError(
        "model_unavailable" if exc.code >= 500 else "model_request_rejected",
        f"{provider} returned HTTP {exc.code}",
        status=503 if exc.code >= 500 else 502,
        retryable=exc.code >= 500,
        request_id=request_id,
    )


class _RejectModelRedirects(urlrequest.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # 模型凭据只授权初始部署地址；同源跳转也拒绝，不能让供应商响应扩大授权边界。
        fp.close()
        raise urlerror.HTTPError(req.full_url, code, msg, headers, None)


def _open_model_request(request, timeout):
    # 仅替换当前请求的跳转策略，沿用 urllib 的 TLS 校验、代理和超时行为。
    return urlrequest.build_opener(_RejectModelRedirects()).open(request, timeout=timeout)


def _default_transport(url, payload, headers, timeout):
    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode(
        "utf-8"
    )
    req = urlrequest.Request(url, data=body, headers=headers, method="POST")
    try:
        with _open_model_request(req, timeout=timeout) as response:
            return json.loads(read_json_response(response).decode("utf-8"))
    except ResponseValidationError as exc:
        raise AgentError(
            "model_output_invalid",
            "model response envelope is invalid",
            status=502,
        ) from exc


class _ExecutionFactsAdapter:
    @contextmanager
    def material_execution(self, observer):
        self._usage.material_observer = observer
        try:
            yield
        finally:
            self._usage.material_observer = None
    def last_provider_request_id(self):
        """只供服务端日志关联；不能并入教师或评分诊断 DTO。"""
        return getattr(self._usage, "provider_request_id", "")

    def last_execution_diagnostics(self):
        return dict(getattr(self._usage, "diagnostics", {}))

    def _reset_execution(self, output_token_limit):
        self._usage.value = {}
        self._usage.provider_request_id = ""
        self._usage.diagnostics = response_diagnostics(None, output_token_limit)
        self._usage.diagnostics.update(attempts=0, provider_request_count=0)

    def _dispatched(self):
        self._usage.diagnostics.update(attempts=1, provider_request_count=1)

    def _invalid_transport_envelope(self, request_id):
        facts = dict(self._usage.diagnostics)
        facts.update(finish_outcome="envelope_invalid", failure_kind="response_envelope_invalid",
                     recovery_blocked_reason="not_enabled")
        self._usage.diagnostics = facts
        return AgentError("model_output_invalid", "provider response envelope is invalid", status=502,
                          request_id=request_id, execution_diagnostics=facts)

    def _validated_output(self, response, request_id, schema, *, native=False):
        provider_id = response.get("request_id" if native else "id") if isinstance(response, dict) else None
        # 供应商关联号只接受有界标识符，禁止把任意正文、URL 或多行信息写入服务日志。
        self._usage.provider_request_id = provider_id if isinstance(provider_id, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:-]{0,127}", provider_id) else ""
        limit = self._usage.diagnostics.get("output_token_limit")
        facts = response_diagnostics(response, limit, native=native,
                                     cache_breakdown=self.settings.cache_usage_diagnostics)
        self._usage.diagnostics = facts
        # 旧用量接口与材料调用回执只接受原有字段；新计数留在诊断中，避免改变结算输入。
        self._usage.value = {key: value for key, value in facts.get("usage", {}).items()
                             if key in LEGACY_TOKEN_FIELDS}
        observer = getattr(self._usage, "material_observer", None)
        if observer is not None:
            observer.response(self._usage.value, facts.get("usage_status", "unavailable"))
        try:
            content = completed_content(response, facts, request_id, native=native, native_parts=native)
            output = _decode_structured_content(content, request_id)
            return _validate_structured_output(output, schema, request_id)
        except AgentError as exc:
            if not exc.execution_diagnostics:
                # 正常结束后的 JSON/schema 错误仍沿用原有一次修复，不能与截断混为同一失败类别。
                facts.update(failure_kind="schema_invalid", recovery_blocked_reason="not_enabled")
                exc.execution_diagnostics = dict(facts)
            raise


class LocalLlamaCppAdapter(_ExecutionFactsAdapter):
    def __init__(self, settings, transport=None, ready_transport=None):
        self.settings = settings
        self.prompt_registry = PromptRegistry(
            settings.prompt_root, settings.prompt_version
        )
        self.transport = transport or _default_transport
        self.ready_transport = ready_transport
        self._semaphore = threading.BoundedSemaphore(value=1)
        # 用量保存在请求线程上下文，避免并行 HTTP 请求读取另一请求的供应商统计。
        self._usage = threading.local()

    def last_usage(self):
        return dict(getattr(self._usage, "value", {}))

    @contextmanager
    def session(self, request_id):
        acquired = self._semaphore.acquire(
            timeout=self.settings.model_queue_timeout_seconds
        )
        if not acquired:
            raise AgentError(
                "model_unavailable",
                "local model is busy and the grading queue wait limit was reached",
                status=503,
                retryable=True,
                request_id=request_id,
            )
        try:
            yield
        finally:
            self._semaphore.release()

    def request(self, grading_request, repair_reason=None):
        request_id = grading_request["request_id"]
        self._reset_execution(self.settings.model_max_output_tokens)
        payload = {
            "model": self.settings.model_name,
            "messages": self.prompt_registry.messages(grading_request, repair_reason),
            "stream": False,
            "temperature": self.settings.model_temperature,
            "seed": self.settings.model_seed,
            "max_tokens": self.settings.model_max_output_tokens,
            "chat_template_kwargs": {"enable_thinking": False},
            "response_format": {
                "type": "json_schema",
                "json_schema": {
                    "name": "grading_output",
                    "strict": True,
                    "schema": grading_output_schema(grading_request),
                },
            },
        }
        headers = {"Content-Type": JSON_CONTENT_TYPE}
        if self.settings.model_api_key:
            headers["Authorization"] = f"Bearer {self.settings.model_api_key}"
        try:
            self._dispatched()
            response = self.transport(
                f"{self.settings.model_base_url}/chat/completions",
                payload,
                headers,
                self.settings.model_timeout_seconds,
            )
        except AgentError as exc:
            if exc.code == "model_output_invalid" and not exc.execution_diagnostics:
                raise self._invalid_transport_envelope(request_id) from exc
            raise
        except TimeoutError as exc:
            raise AgentError(
                "model_timeout",
                "local model request timed out",
                status=504,
                retryable=True,
                request_id=request_id,
            ) from exc
        except urlerror.HTTPError as exc:
            raise _map_model_http_error(exc, request_id) from exc
        except ValueError as exc:
            raise self._invalid_transport_envelope(request_id) from exc
        except OSError as exc:
            raise AgentError(
                "model_unavailable",
                "local model request failed",
                status=503,
                retryable=True,
                request_id=request_id,
            ) from exc
        return self._validated_output(response, request_id, grading_output_schema(grading_request))

    def request_structured(self, request_id, messages, schema, name):
        self._reset_execution(max(self.settings.model_max_output_tokens, 4096))
        payload = {
            "model": self.settings.model_name,
            "messages": messages,
            "stream": False,
            "temperature": 0,
            "seed": self.settings.model_seed,
            "max_tokens": max(self.settings.model_max_output_tokens, 4096),
            "chat_template_kwargs": {"enable_thinking": False},
            "response_format": {"type": "json_schema", "json_schema": {"name": name, "strict": True, "schema": schema}},
        }
        headers = {"Content-Type": JSON_CONTENT_TYPE}
        if self.settings.model_api_key:
            headers["Authorization"] = f"Bearer {self.settings.model_api_key}"
        try:
            observer = getattr(self._usage, "material_observer", None)
            if observer is not None:
                observer.consume(payload)
            self._dispatched()
            try:
                response = self.transport(f"{self.settings.model_base_url}/chat/completions", payload, headers, self.settings.model_timeout_seconds)
            finally:
                if observer is not None:
                    observer.attempted()
            return self._validated_output(response, request_id, schema)
        except AgentError as exc:
            if exc.code == "model_output_invalid" and not exc.execution_diagnostics:
                raise self._invalid_transport_envelope(request_id) from exc
            raise
        except TimeoutError as exc:
            raise AgentError("model_timeout", "document parsing model request timed out", status=504, retryable=True, request_id=request_id) from exc
        except urlerror.HTTPError as exc:
            raise _map_model_http_error(exc, request_id) from exc
        except ValueError as exc:
            raise self._invalid_transport_envelope(request_id) from exc
        except OSError as exc:
            raise AgentError("model_unavailable", "document parsing model request failed", status=503, retryable=True, request_id=request_id) from exc

    def _parse_content(self, response, request_id):
        try:
            content = response["choices"][0]["message"]["content"]
        except (KeyError, IndexError, TypeError) as exc:
            raise AgentError(
                "model_output_invalid",
                "local model response contained no structured content",
                status=502,
                request_id=request_id,
            ) from exc
        return _decode_structured_content(content, request_id)

    def ready(self):
        if self.ready_transport:
            return bool(self.ready_transport())
        headers = {}
        if self.settings.model_api_key:
            headers["Authorization"] = f"Bearer {self.settings.model_api_key}"
        req = urlrequest.Request(
            f"{self.settings.model_base_url}/models", headers=headers, method="GET"
        )
        try:
            with _open_model_request(
                req, timeout=self.settings.model_ready_timeout_seconds
            ) as response:
                return 200 <= response.status < 300
        except OSError:
            return False


class DashScopeNativeAdapter(_ExecutionFactsAdapter):
    """DashScope's native generation protocol; no OpenAI-compatible endpoint."""

    def __init__(self, settings, transport=None):
        self.settings = settings
        self.prompt_registry = PromptRegistry(
            settings.prompt_root, settings.prompt_version
        )
        self.transport = transport or self._http_transport
        self._usage = threading.local()

    def last_usage(self):
        return dict(getattr(self._usage, "value", {}))

    @contextmanager
    def session(self, _request_id):
        yield

    def request(self, grading_request, repair_reason=None):
        request_id = grading_request["request_id"]
        self._reset_execution(self.settings.model_max_output_tokens)
        try:
            payload = build_dashscope_grading_payload(
                grading_request=grading_request,
                prompt_registry=self.prompt_registry,
                model=self.settings.model_name,
                max_completion_tokens=self.settings.model_max_output_tokens,
                temperature=self.settings.model_temperature,
                seed=self.settings.model_seed,
                repair_reason=repair_reason,
            )
            self._dispatched()
            response = self.transport(
                f"{self.settings.model_base_url}{DASHSCOPE_TEXT_GENERATION_PATH}",
                payload,
                {
                    "Accept": JSON_CONTENT_TYPE,
                    "Authorization": f"Bearer {self.settings.model_api_key}",
                    "Content-Type": JSON_CONTENT_TYPE,
                    "X-DashScope-SSE": "disable",
                },
                self.settings.model_timeout_seconds,
            )
            return self._validated_output(response, request_id, grading_output_schema(grading_request), native=True)
        except AgentError as exc:
            if exc.code == "model_output_invalid" and not exc.execution_diagnostics:
                raise self._invalid_transport_envelope(request_id) from exc
            raise
        except TimeoutError as exc:
            raise AgentError(
                "model_timeout",
                "external model request timed out",
                status=504,
                retryable=True,
                request_id=request_id,
            ) from exc
        except urlerror.HTTPError as exc:
            error_payload = self._read_error_payload(exc)
            mapped = map_dashscope_error(exc.code, error_payload)
            # 对外 request_id 绑定逻辑评分请求，供应商自己的关联号不能替换幂等身份。
            mapped.request_id = request_id
            raise mapped from exc
        except ValueError as exc:
            raise self._invalid_transport_envelope(request_id) from exc
        except OSError as exc:
            raise AgentError(
                "model_unavailable",
                "external model request failed",
                status=503,
                retryable=True,
                request_id=request_id,
            ) from exc

    def request_structured(self, request_id, messages, schema, _name):
        self._reset_execution(max(self.settings.model_max_output_tokens, 4096))
        visual = any(isinstance(message.get("content"), list) for message in messages)
        # 普通评分与数学 v2 共用真实视觉 transport；不能仅凭模型名称把图片发往文本端点。
        endpoint = f"{self.settings.model_base_url}{DASHSCOPE_TEXT_GENERATION_PATH}"
        parameters = {"result_format": "message", "temperature": 0, "seed": self.settings.model_seed,
                      "max_tokens": max(self.settings.model_max_output_tokens, 4096)}
        if visual and not getattr(self, "supports_native_visual_wrapper", False):
            from .visual_transport import native_visual_request
            endpoint, messages, parameters = native_visual_request(self.settings.model_base_url, messages, parameters)
            messages = [*messages, {"role": "user", "content": [{"text": "Return JSON matching this schema: " + json.dumps(schema, ensure_ascii=False)}]}]
        payload = {"model": self.settings.model_name, "input": {"messages": messages}, "parameters": parameters}
        try:
            self._dispatched()
            response = self.transport(
                endpoint, payload,
                {"Accept": JSON_CONTENT_TYPE, "Authorization": f"Bearer {self.settings.model_api_key}", "Content-Type": JSON_CONTENT_TYPE, "X-DashScope-SSE": "disable"},
                self.settings.model_timeout_seconds,
            )
            return self._validated_output(response, request_id, schema, native=True)
        except AgentError as exc:
            if exc.code == "model_output_invalid" and not exc.execution_diagnostics:
                raise self._invalid_transport_envelope(request_id) from exc
            raise
        except urlerror.HTTPError as exc:
            error_payload = self._read_error_payload(exc)
            mapped = map_dashscope_error(exc.code, error_payload)
            mapped.request_id = request_id
            raise mapped from exc
        except ValueError as exc:
            raise self._invalid_transport_envelope(request_id) from exc
        except (KeyError, IndexError, TypeError, OSError) as exc:
            raise AgentError("model_unavailable", "document parsing model request failed", status=503, retryable=True, request_id=request_id) from exc

    @staticmethod
    def _http_transport(url, payload, headers, timeout):
        body = json.dumps(
            payload,
            ensure_ascii=False,
            allow_nan=False,
            separators=(",", ":"),
        ).encode("utf-8")
        request_limit = 8 * 1024 * 1024 if "/multimodal-generation/" in url else MAX_DASHSCOPE_TEXT_REQUEST_BYTES
        if not body or len(body) > request_limit:
            raise AgentError(
                "invalid_request",
                "native provider request exceeds its approved size",
                status=413,
            )
        req = urlrequest.Request(url, data=body, headers=headers, method="POST")
        with _open_model_request(req, timeout=timeout) as response:
            try:
                raw = read_json_response(response, MAX_DASHSCOPE_RESPONSE_BYTES)
            except ResponseValidationError as exc:
                raise AgentError(
                    "model_output_invalid",
                    "provider response envelope is invalid",
                    status=502,
                ) from exc
            if not raw:
                raise AgentError(
                    "model_output_invalid",
                    "provider response envelope is invalid",
                    status=502,
                )
            return DashScopeNativeAdapter._strict_json(raw)

    @staticmethod
    def _strict_json(raw):
        def reject_duplicates(pairs):
            value = {}
            for key, item in pairs:
                if key in value:
                    raise ValueError("duplicate JSON field")
                value[key] = item
            return value

        def reject_nonfinite(_value):
            raise ValueError("non-finite JSON number")

        return json.loads(
            raw.decode("utf-8"),
            object_pairs_hook=reject_duplicates,
            parse_constant=reject_nonfinite,
        )

    @staticmethod
    def _read_error_payload(error):
        try:
            raw = read_json_response(error, MAX_DASHSCOPE_RESPONSE_BYTES)
            if not raw:
                return {}
            payload = json.loads(raw.decode("utf-8"))
            return payload if isinstance(payload, dict) else {}
        except (OSError, ResponseValidationError, UnicodeDecodeError, json.JSONDecodeError):
            return {}

    def ready(self):
        # 就绪检查不发起计费模型调用。启动校验已
        # 固定原生 HTTPS 主机，并要求 API 密钥非空。
        return bool(self.settings.model_api_key)
