"""参考图调用的一次性持久许可；内存缓存不能替代 Gateway 的共享 CAS。"""

import hashlib
import inspect
import json
from urllib import request as urlrequest

from .errors import AgentError


def math_wrapper_hash():
    from .app_v2 import math_candidate_messages, math_candidate_schema
    from .contract_v2 import validate_reference_materials, validate_response_v2
    sources = ["math-material-wrapper-v1", *(inspect.getsource(f) for f in (
        math_candidate_messages, math_candidate_schema, validate_reference_materials, validate_response_v2
    ))]
    return hashlib.sha256("\n".join(sources).encode("utf-8")).hexdigest()


class MaterialDispatch:
    def __init__(self, settings, request):
        self.request_id = request["request_id"]
        self.binding = request["request_binding_hash"]
        self.permit = request["execution_permit"]
        self.base_url = settings.material_callback_base_url
        self.wire_hash = ""
        self._consume_started = False
        self.consumed = False
        self.responded = False
        if not self.base_url or settings.adapter_type != "local_llama_cpp" or request["math_wrapper_hash"] != math_wrapper_hash():
            raise AgentError("reference_material_capability_unavailable", "reference image dispatch capability is unavailable", status=422, request_id=self.request_id)

    def _record(self, stage, usage=None, usage_state="unavailable"):
        from .model import _open_model_request
        path = f"/api/v1/internal/subjective-grading/runs/{self.permit['run_id']}/material-permits/{self.permit['permit_id']}/record"
        payload = {"stage": stage, "request_binding_hash": self.binding, "wire_payload_hash": self.wire_hash,
                   "usage": usage or {}, "usage_state": usage_state}
        body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        req = urlrequest.Request(self.base_url + path, data=body, method="POST", headers={
            "Authorization": "Bearer " + self.permit["credential"], "Content-Type": "application/json"
        })
        try:
            with _open_model_request(req, 10) as response:
                raw = response.read(4097)
                if response.status != 200 or len(raw)>4096 or json.loads(raw) != {"accepted": True}:
                    raise ValueError("callback rejected")
        except (OSError, ValueError) as exc:
            raise AgentError("material_dispatch_unknown" if self.consumed else "material_permit_unavailable",
                             "material dispatch requires reconciliation", status=409, retryable=False, request_id=self.request_id) from exc
        finally:
            # 网络异常的 traceback 可能短暂保留 Request；先移除头部，避免继续持有许可凭据。
            req.remove_header("Authorization")

    def consume(self, payload):
        # 同一个实例发生消费失败也不能再次尝试；共享许可是否已经落库只能由 Gateway 判断。
        if self._consume_started:
            raise AgentError("material_dispatch_unknown", "material dispatch requires reconciliation", status=409, retryable=False, request_id=self.request_id)
        self._consume_started = True
        encoded = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        if len(encoded) > 8 * 1024 * 1024:
            raise AgentError("invalid_request", "actual image request exceeds the approved byte limit", status=413, request_id=self.request_id)
        self.wire_hash = hashlib.sha256(encoded).hexdigest()
        self._record("consume")
        self.consumed = True

    def attempted(self):
        self._record("provider_request_attempted")

    def response(self, usage=None, usage_state="unavailable"):
        self._record("provider_response_received", usage, usage_state)
        self.responded = True

    def unknown(self):
        if self.consumed and not self.responded:
            try:
                self._record("unknown_after_attempt")
            except AgentError:
                # 已消费且没有可靠记录同样会被投影为未知；失败不能触发供应商重试。
                pass
