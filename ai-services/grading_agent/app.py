import copy
import hashlib
import json
import threading
import time
from collections import OrderedDict

from .app_v2 import ProductionMathV2Application
from .capabilities import CapabilityMatrix
from .contract import normalize_model_output, validate_request
from .errors import AgentError
from .guardrails import detect_prompt_injection
from .model import PromptRegistry, sum_model_usage
from .provider_adapter import build_provider_adapter


class GradingAgentApplication:
    def __init__(self, settings, matrix=None, model=None, clock=None, logger=None, adapter_registry=None):
        self.settings = settings
        self.matrix = matrix or CapabilityMatrix.load(settings.contract_root)
        if self.matrix.profile_id != settings.capability_profile:
            raise ValueError("configured capability profile does not match the governed capability matrix")
        self.model = model or build_provider_adapter(settings, adapter_registry)
        self.prompt_registry = PromptRegistry(settings.prompt_root, settings.prompt_version)
        self.clock = clock or time.time
        self.logger = logger or self._default_logger
        self._cache = OrderedDict()
        self._cache_lock = threading.Lock()
        self._inflight = {}
        self._readiness_lock = threading.Lock()
        self._readiness_ready = False
        self.math_v2 = ProductionMathV2Application(settings, self.model, clock=self.clock, logger=self.logger)

    def grade_v2(self, payload, idempotency_key, *, body_size, content_encoding=""):
        return self.math_v2.grade(payload, idempotency_key, body_size=body_size, content_encoding=content_encoding)

    def grade(self, payload, idempotency_key):
        request = copy.deepcopy(payload)
        validate_request(request)
        request_id = request["request_id"]
        if idempotency_key != request_id:
            raise AgentError(
                "idempotency_conflict",
                "Idempotency-Key must equal request_id",
                status=409,
                request_id=request_id,
            )
        if request["model_policy"]["model_version"] != self.settings.model_version:
            raise AgentError(
                "invalid_request",
                "requested model version is not the configured governed model",
                status=400,
                request_id=request_id,
            )
        if request["prompt_version"] != self.settings.prompt_version:
            raise AgentError(
                "invalid_request",
                "requested prompt version is not the configured governed prompt",
                status=400,
                request_id=request_id,
            )

        local_guard = detect_prompt_injection(request["answer_text"])
        if local_guard["detected"]:
            request["prompt_guard"]["suspected_injection"] = True
            request["prompt_guard"]["signals"] = list(
                dict.fromkeys(request["prompt_guard"]["signals"] + local_guard["signals"])
            )
        route = self.matrix.route(
            request["grade_level"], request["subject"], request["question_type"], request_id
        )
        digest = hashlib.sha256(
            json.dumps(request, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest()

        owner = False
        with self._cache_lock:
            self._purge_cache()
            existing = self._cache.get(idempotency_key)
            if existing:
                if existing[1] != digest:
                    raise AgentError(
                        "idempotency_conflict",
                        "idempotency key was already used for a different request",
                        status=409,
                        request_id=request_id,
                    )
                self._cache.move_to_end(idempotency_key)
                self._log(request_id, "replayed", attempts=existing[2]["telemetry"]["attempts"])
                return copy.deepcopy(existing[2]), True
            inflight = self._inflight.get(idempotency_key)
            if inflight:
                if inflight["digest"] != digest:
                    raise AgentError(
                        "idempotency_conflict",
                        "idempotency key is in flight for a different request",
                        status=409,
                        request_id=request_id,
                    )
            else:
                inflight = {
                    "digest": digest,
                    "event": threading.Event(),
                    "result": None,
                    "error": None,
                }
                self._inflight[idempotency_key] = inflight
                owner = True

        if not owner:
            wait_seconds = (
                self.settings.model_queue_timeout_seconds
                + self.settings.model_timeout_seconds * (self.settings.model_max_retries + 1)
                + 5
            )
            if not inflight["event"].wait(wait_seconds):
                raise AgentError(
                    "model_timeout",
                    "timed out waiting for the in-flight idempotent request",
                    status=504,
                    retryable=True,
                    request_id=request_id,
                )
            if inflight["error"]:
                error = inflight["error"]
                raise AgentError(
                    error.code,
                    error.message,
                    status=error.status,
                    retryable=error.retryable,
                    request_id=request_id,
                )
            self._log(request_id, "replayed", attempts=inflight["result"]["telemetry"]["attempts"])
            return copy.deepcopy(inflight["result"]), True

        try:
            suggestion = self._grade_uncached(request, route)
        except AgentError as exc:
            with self._cache_lock:
                inflight["error"] = exc
                inflight["event"].set()
                self._inflight.pop(idempotency_key, None)
            raise
        except Exception as exc:
            internal_error = AgentError(
                "internal_error",
                "grading-agent operation failed",
                status=500,
                request_id=request_id,
            )
            with self._cache_lock:
                inflight["error"] = internal_error
                inflight["event"].set()
                self._inflight.pop(idempotency_key, None)
            raise internal_error from exc

        with self._cache_lock:
            self._cache[idempotency_key] = (self.clock(), digest, copy.deepcopy(suggestion))
            while len(self._cache) > self.settings.idempotency_max_entries:
                self._cache.popitem(last=False)
            inflight["result"] = copy.deepcopy(suggestion)
            inflight["event"].set()
            self._inflight.pop(idempotency_key, None)
        return suggestion, False

    def _grade_uncached(self, request, route):
        started = time.monotonic()
        prior_error_codes = []
        usage = {}
        request_id = request["request_id"]
        with self.model.session(request_id):
            for attempt in range(self.settings.model_max_retries + 1):
                try:
                    try:
                        raw = self.model.request(
                            request,
                            repair_reason=prior_error_codes[-1] if prior_error_codes else None,
                        )
                    finally:
                        usage = sum_model_usage(usage, getattr(self.model, "last_usage", lambda: {})())
                    telemetry = {
                        "adapter": self.settings.adapter_type,
                        "provider": self.settings.provider_key,
                        "deployment": self.settings.deployment_key,
                        "region": self.settings.deployment_region,
                        "attempts": attempt + 1,
                        "repair_attempted": attempt > 0,
                        "prior_error_codes": list(prior_error_codes),
                        "elapsed_ms": round((time.monotonic() - started) * 1000),
                        "usage": usage,
                    }
                    suggestion = normalize_model_output(
                        raw,
                        request,
                        route,
                        self.settings.model_version,
                        self.settings.prompt_version,
                        self.matrix.profile_id,
                        telemetry,
                    )
                    self._log(request_id, "succeeded", attempts=attempt + 1, elapsed_ms=telemetry["elapsed_ms"])
                    return suggestion
                except AgentError as exc:
                    prior_error_codes.append(exc.code)
                    if attempt >= self.settings.model_max_retries or not self._repairable(exc):
                        self._log(
                            request_id,
                            "failed",
                            attempts=attempt + 1,
                            elapsed_ms=round((time.monotonic() - started) * 1000),
                            error_code=exc.code,
                        )
                        raise
        raise AgentError("internal_error", "grading attempt ended unexpectedly", request_id=request_id)

    @staticmethod
    def _repairable(error):
        return error.code in {
            "model_output_invalid",
            "evidence_verification_failed",
            "model_unavailable",
            "model_rate_limited",
        } and (error.retryable or error.code in {"model_output_invalid", "evidence_verification_failed"})

    def _purge_cache(self):
        cutoff = self.clock() - self.settings.idempotency_ttl_seconds
        expired = [key for key, value in self._cache.items() if value[0] < cutoff]
        for key in expired:
            del self._cache[key]

    def readiness(self):
        with self._readiness_lock:
            return self._readiness_ready

    def refresh_readiness(self):
        try:
            ready = bool(self.model.ready())
        except Exception:  # noqa: BLE001 - dependency probes fail closed.
            ready = False
        with self._readiness_lock:
            self._readiness_ready = ready
        return ready

    def prompt_snapshot(self):
        return self.prompt_registry.snapshot()

    def _log(self, request_id, status, **fields):
        self.logger({"event": "grading_agent_request", "request_id": request_id, "status": status, **fields})

    @staticmethod
    def _default_logger(event):
        print(json.dumps(event, ensure_ascii=True, separators=(",", ":")), flush=True)
