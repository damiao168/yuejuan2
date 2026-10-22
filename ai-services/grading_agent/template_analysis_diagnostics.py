"""模板分析只记录有界元数据；原文须显式启用并写入独立受控目录。"""

import base64
import contextvars
import hashlib
import json
import logging
import os
import re
import threading
import time
import uuid
from contextlib import contextmanager
from pathlib import Path

_ACTIVE_TRACE = contextvars.ContextVar("template_analysis_trace", default=None)
_LOGGER = logging.getLogger(__name__)
_MAX_CONTENT_BYTES = 2 * 1024 * 1024
_MAX_PROVIDER_REQUESTS = 6
_PRUNE_LOCK = threading.Lock()
_TRACE_FILENAME = re.compile(r"^[0-9a-f]{32}\.json$")


def _schema_digest(schema):
    serialized = json.dumps(schema, ensure_ascii=False, sort_keys=True,
                            separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(serialized.encode("utf-8")).hexdigest()


def _system_text(messages):
    texts = []
    for message in messages:
        if not isinstance(message, dict) or message.get("role") != "system":
            continue
        content = message.get("content")
        if isinstance(content, str):
            texts.append(content)
        elif isinstance(content, list):
            texts.extend(part["text"] for part in content
                         if isinstance(part, dict) and isinstance(part.get("text"), str))
    return "\n".join(texts)


def _system_digest(messages):
    return hashlib.sha256(_system_text(messages).encode("utf-8")).hexdigest()


def capture_provider_request(payload):
    """只记录最终传输边界的模式和摘要，不代表供应商已收到或支持该请求。"""
    trace = _ACTIVE_TRACE.get()
    if trace is None:
        return
    try:
        response_format = payload.get("response_format") or {}
        schema_config = response_format.get("json_schema") or {}
        native = payload.get("input")
        messages = native.get("messages", []) if isinstance(native, dict) else payload.get("messages", [])
        mode = "unknown"
        if response_format.get("type") == "json_schema":
            mode = "json_schema_strict" if schema_config.get("strict") is True else "json_schema"
        elif response_format.get("type") == "json_object":
            mode = "json_object"
        elif isinstance(native, dict) and "Return a JSON object matching this schema:" in _system_text(messages):
            mode = "schema_in_prompt"
        recorded = {"stage": "transport_prepared", "output_mode": mode,
                    "system_prompt_sha256": _system_digest(messages), "image_inputs": _image_digests(messages)}
        if isinstance(schema_config.get("schema"), dict):
            recorded["model_output_schema_sha256"] = _schema_digest(schema_config["schema"])
        trace.metadata["provider_request"] = recorded
        # 整页和裁图分开留摘要，不能用最后一次请求覆盖掉首轮图像是否真正进入传输层的证据。
        history = trace.metadata.setdefault("provider_requests", [])
        if len(history) < _MAX_PROVIDER_REQUESTS:
            history.append(recorded)
    except (TypeError, ValueError, AttributeError, RecursionError):
        trace.metadata["request_capture_failed"] = True


def _image_digests(messages):
    summaries = []
    for message in messages:
        content = message.get("content") if isinstance(message, dict) else None
        if not isinstance(content, list):
            continue
        for part in content:
            if not isinstance(part, dict):
                continue
            config = part.get("image_url")
            config = config if isinstance(config, dict) else {}
            url = config.get("url", part.get("image"))
            if not isinstance(url, str):
                continue
            detail = config.get("detail")
            summary = {"detail": detail if detail in {"auto", "high", "low"} else None}
            # 只处理本操作已校验的有界data图；网络地址与原图内容均不进入普通日志。
            header, separator, encoded = url.partition(",")
            if header.startswith("data:image/") and header.endswith(";base64") and separator and len(encoded) <= 12 * 1024 * 1024:
                try:
                    raw = base64.b64decode(encoded, validate=True)
                    summary.update(sha256=hashlib.sha256(raw).hexdigest(), bytes=len(raw))
                except ValueError:
                    summary["invalid_data"] = True
            else:
                summary["digest_unavailable"] = True
            summaries.append(summary)
            if len(summaries) == 8:
                return summaries
    return summaries


def _positive_setting(name, default):
    try:
        value = int(os.getenv(name, str(default)))
    except (TypeError, ValueError):
        return default
    return value if value > 0 else default


def _prune_records(directory):
    # 只处理本模块独占的 UUID 文件；目录、符号链接和其他名称不能因容量清理被删除。
    retention = _positive_setting("EDUGRADE_TEMPLATE_ANALYSIS_RETENTION_SECONDS", 604800)
    maximum_files = _positive_setting("EDUGRADE_TEMPLATE_ANALYSIS_MAX_FILES", 1000)
    maximum_bytes = _positive_setting("EDUGRADE_TEMPLATE_ANALYSIS_MAX_BYTES", 268435456)
    now, remaining = time.time(), []
    for child in directory.iterdir():
        if not _TRACE_FILENAME.fullmatch(child.name) or child.is_symlink() or not child.is_file():
            continue
        try:
            stat = child.stat()
            if now - stat.st_mtime > retention:
                child.unlink(missing_ok=True)
            else:
                remaining.append((stat.st_mtime, stat.st_size, child))
        except FileNotFoundError:
            continue
    remaining.sort(key=lambda item: (item[0], item[2].name))
    total = sum(item[1] for item in remaining)
    count = len(remaining)
    for _, size, child in remaining:
        if count <= maximum_files and total <= maximum_bytes:
            break
        child.unlink(missing_ok=True)
        count -= 1
        total -= size


def capture_provider_response(response):
    """传输返回后、空值补齐和 JSON 解析前调用，不接收请求、认证头或配置。"""
    trace = _ACTIVE_TRACE.get()
    if trace is not None:
        try:
            trace.capture(response, provider=True)
        except (TypeError, ValueError, KeyError, IndexError, RecursionError):
            # 诊断不能成为另一条供应商解析路径；坏响应仍交由主协议处理，诊断只标记采集失败。
            trace.metadata["response_capture_failed"] = True


class AnalysisTrace:
    def __init__(self, payload, versions, model):
        settings = getattr(model, "settings", None)
        self.metadata = {
            "event": "answer_sheet_analysis", "run_id": uuid.uuid4().hex,
            "request_id": payload["request_id"], "page_no": payload["page"]["page_no"],
            "source_sha256": payload["page"]["sha256"], "versions": versions,
            "model_name": str(getattr(settings, "model_name", "") or "")[:128],
            "attempts": [],
            "page_width": payload["page"]["width"], "page_height": payload["page"]["height"],
        }
        # 请求 schema 已限制这些字段；只记录网关版本/摘要，不记录来源地址或完整请求。
        if "diagnostic_context" in payload:
            self.metadata["diagnostic_context"] = dict(payload["diagnostic_context"])
        self._started = time.monotonic()
        self._content = []
        self._content_bytes = 0
        self._finished = False

    def bind_request(self, messages, schema, *, prompt_variant):
        # 调用方的 schema 契约与传输模式分开记；兼容接口随后可能改成 JSON 模式。
        self.metadata["request_contract"] = {
            "prompt_variant": prompt_variant, "requested_output_mode": "schema_contract",
            "system_prompt_sha256": _system_digest(messages),
            "model_output_schema_sha256": _schema_digest(schema),
        }

    @contextmanager
    def completion(self):
        """处理异常也写出有界失败元数据，随后仍把原异常交回服务错误边界。"""
        try:
            yield
        except Exception:
            if not self._finished:
                self.finish("failed", [], ["ANALYSIS_PROCESSING_FAILED"])
            raise

    @contextmanager
    def active(self):
        token = _ACTIVE_TRACE.set(self)
        try:
            yield
        finally:
            _ACTIVE_TRACE.reset(token)

    def capture(self, value, provider=False):
        # 普通日志只含摘要、长度和完成状态。原文只保留助手响应，不存整个供应商对象或任何请求凭据。
        content, finish, actual_model = value, None, None
        if provider and isinstance(value, dict):
            choice = (value.get("choices") or [None])[0]
            if isinstance(choice, dict):
                message = choice.get("message") or {}
                content = message.get("content") if isinstance(message, dict) else None
                finish = choice.get("finish_reason")
            elif isinstance(value.get("output"), dict):
                output = value["output"]
                choices = output.get("choices") or []
                choice = choices[0] if choices and isinstance(choices[0], dict) else {}
                message = choice.get("message") or {}
                content = message.get("content", output.get("text")) if isinstance(message, dict) else None
                finish = choice.get("finish_reason", output.get("finish_reason"))
            else:
                content = None
            actual_model = value.get("model")
        finish = finish if isinstance(finish, str) else None
        try:
            raw = json.dumps(content, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        except (TypeError, ValueError, RecursionError):
            raw = b"null"
        self.metadata["attempts"].append({
            "attempt": len(self.metadata["attempts"]) + 1,
            "content_sha256": hashlib.sha256(raw).hexdigest(), "content_bytes": len(raw),
            "finish_reason": finish if finish in {"stop", "length", "content_filter", "tool_calls", "null"} else "unknown",
            "actual_model": actual_model[:128] if isinstance(actual_model, str) else None,
            "partial": finish not in {None, "stop"},
        })
        if os.getenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "").lower() == "true":
            remaining = max(0, _MAX_CONTENT_BYTES - self._content_bytes)
            self._content.append({"attempt": len(self.metadata["attempts"]),
                                  "truncated": len(raw) > remaining,
                                  "content_json": raw[:remaining].decode("utf-8", errors="replace")})
            self._content_bytes += min(len(raw), remaining)

    def finish(self, status, rows, reason_codes):
        if self._finished:
            return
        self._finished = True
        self.metadata.update(status=status, elapsed_ms=round((time.monotonic() - self._started) * 1000),
                             candidate_count=len(rows), reason_codes=reason_codes,
                             needs_review_count=sum(row.get("semantic_check") == "needs_review" for row in rows))
        directory = os.getenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", "")
        self.metadata["content_captured"] = False
        if directory:
            try:
                path = Path(directory)
                if not path.is_absolute() or path.is_symlink():
                    raise OSError("diagnostic directory must be an absolute controlled path")
                path.mkdir(mode=0o700, parents=True, exist_ok=True)
                self.metadata["content_captured"] = bool(self._content)
                record = {"metadata": self.metadata,
                          "candidate_summary": [{key: row.get(key) for key in (
                              "candidate_id", "role", "origin", "printed_question_label", "identity_label",
                              "source_bbox", "proposed_bbox", "association_status", "semantic_check", "reason_codes",
                          )} for row in rows]}
                if self._content:
                    record["responses"] = self._content
                # 独占文件名和最小权限防止并发覆盖；写完再按时间/容量删旧记录，存储上限不足时本次也可能被清理。
                target = path / f"{self.metadata['run_id']}.json"
                with _PRUNE_LOCK:
                    with os.fdopen(os.open(target, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "w", encoding="utf-8") as handle:
                        json.dump(record, handle, ensure_ascii=False, separators=(",", ":"))
                    _prune_records(path)
                    self.metadata["content_captured"] = bool(self._content) and target.exists()
            except OSError:
                self.metadata["content_captured"] = False
                self.metadata["diagnostics_write_failed"] = True
        # 不记录供应商错误原文、OCR 文本、个人填写值或目录路径；存储失败不影响可审核的识别结果。
        _LOGGER.info("%s", json.dumps(self.metadata, ensure_ascii=False, separators=(",", ":")))
