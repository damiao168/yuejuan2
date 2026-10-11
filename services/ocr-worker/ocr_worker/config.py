from __future__ import annotations

import math
import os
from dataclasses import dataclass

from edugrade_worker_runtime import validate_lease_timing, validate_service_url

from .deployment_profile import FormulaRuntimePlan, resolve_formula_runtime_plan


# 数学表达式和内部令牌跨容器传输；默认 HTTPS，由客户端验证内部 CA 和服务主机名。
_DEFAULT_MATH_VERIFY_BASE_URL = "https://math-verification-worker:8092"


@dataclass(frozen=True)
class Settings:
    api_base_url: str
    tenant_code: str
    username: str
    password: str
    engine: str = "paddleocr"
    engine_version: str = "pp-ocrv5"
    model_version: str = "ppocr-v5-mobile"
    min_confidence: float = 0.8
    poll_interval: float = 5.0
    batch_size: int = 1
    device: str = "cpu"
    preprocess_profile: str = "default"
    worker_id: str = "ocr-worker"
    lease_seconds: int = 300
    heartbeat_interval: float = 10.0
    heartbeat_timeout: float = 3.0
    ocr_runtime_enabled: bool = True
    math_runtime_enabled: bool = False
    paper_formula_runtime_enabled: bool = False
    math_verify_base_url: str = _DEFAULT_MATH_VERIFY_BASE_URL
    math_verify_token: str = ""
    formula_model_version: str = "PP-FormulaNet_plus-M"
    formula_fallback_model_version: str = "PP-FormulaNet_plus-L"
    formula_layout_model_version: str = "PP-DocLayout_plus-L"
    cpu_threads: int = 4
    enable_mkldnn: str = "auto"
    enable_hpi: bool = False
    use_textline_orientation: bool = True
    text_det_limit_type: str = "min"
    text_det_limit_side_len: int = 64
    text_recognition_batch_size: int = 1
    formula_recognition_batch_size: int | None = None
    formula_result_cache_size: int = 2048
    formula_max_padding_pixels: int = 96
    formula_max_padding_height_ratio: float = 0.75
    formula_render_similarity_threshold: float = 0.34
    formula_model_lifecycle: str = "auto"
    formula_deployment_profile_path: str = "/home/ocr-worker/.paddlex/edugrade/formula-deployment-profile.json"
    formula_prewarm_fallback: bool = False


def load_settings() -> Settings:
    settings = Settings(
        api_base_url=os.environ["EDUGRADE_API_BASE_URL"].rstrip("/"),
        tenant_code=os.environ["EDUGRADE_OCR_WORKER_TENANT_CODE"],
        username=os.environ["EDUGRADE_OCR_WORKER_USERNAME"],
        password=os.environ["EDUGRADE_OCR_WORKER_PASSWORD"],
        engine=os.environ.get("EDUGRADE_OCR_ENGINE", "paddleocr"),
        engine_version=os.environ.get("EDUGRADE_OCR_ENGINE_VERSION", "pp-ocrv5"),
        model_version=os.environ.get("EDUGRADE_OCR_MODEL_VERSION", "ppocr-v5-mobile"),
        min_confidence=float(os.environ.get("EDUGRADE_OCR_MIN_CONFIDENCE", "0.8")),
        poll_interval=float(os.environ.get("EDUGRADE_OCR_POLL_INTERVAL", "5")),
        batch_size=int(os.environ.get("EDUGRADE_OCR_BATCH_SIZE", "1")),
        device=os.environ.get("EDUGRADE_OCR_DEVICE", "cpu"),
        preprocess_profile=os.environ.get("EDUGRADE_OCR_PREPROCESS_PROFILE", "default"),
        worker_id=os.environ.get("EDUGRADE_OCR_WORKER_ID", "ocr-worker"),
        lease_seconds=int(os.environ.get("EDUGRADE_OCR_LEASE_SECONDS", "300")),
        heartbeat_interval=float(os.environ.get("EDUGRADE_OCR_HEARTBEAT_INTERVAL", "10")),
        heartbeat_timeout=float(os.environ.get("EDUGRADE_OCR_HEARTBEAT_TIMEOUT", "3")),
        ocr_runtime_enabled=os.environ.get("EDUGRADE_OCR_RUNTIME_ENABLED", "true").lower() in {"1", "true", "yes"},
        math_runtime_enabled=os.environ.get("EDUGRADE_MATH_RUNTIME_ENABLED", "false").lower() in {"1", "true", "yes"},
        paper_formula_runtime_enabled=os.environ.get("EDUGRADE_PAPER_FORMULA_RUNTIME_ENABLED", "false").lower() in {"1", "true", "yes"},
        math_verify_base_url=os.environ.get("EDUGRADE_MATH_VERIFY_BASE_URL", _DEFAULT_MATH_VERIFY_BASE_URL),
        math_verify_token=os.environ.get("EDUGRADE_MATH_VERIFY_TOKEN", ""),
        formula_model_version=os.environ.get("EDUGRADE_FORMULA_MODEL_VERSION", "PP-FormulaNet_plus-M"),
        formula_fallback_model_version=os.environ.get("EDUGRADE_FORMULA_FALLBACK_MODEL_VERSION", "PP-FormulaNet_plus-L"),
        formula_layout_model_version=os.environ.get("EDUGRADE_FORMULA_LAYOUT_MODEL_VERSION", "PP-DocLayout_plus-L"),
        cpu_threads=int(os.environ.get("EDUGRADE_OCR_CPU_THREADS", "4")),
        enable_mkldnn=os.environ.get("EDUGRADE_OCR_ENABLE_MKLDNN", "auto").strip().lower(),
        enable_hpi=os.environ.get("EDUGRADE_OCR_ENABLE_HPI", "false").lower() in {"1", "true", "yes"},
        use_textline_orientation=os.environ.get("EDUGRADE_OCR_USE_TEXTLINE_ORIENTATION", "true").lower() in {"1", "true", "yes"},
        text_det_limit_type=os.environ.get("EDUGRADE_OCR_TEXT_DET_LIMIT_TYPE", "min").strip().lower(),
        text_det_limit_side_len=int(os.environ.get("EDUGRADE_OCR_TEXT_DET_LIMIT_SIDE_LEN", "64")),
        text_recognition_batch_size=int(os.environ.get("EDUGRADE_OCR_TEXT_RECOGNITION_BATCH_SIZE", "1")),
        formula_recognition_batch_size=_optional_positive_int(
            os.environ.get("EDUGRADE_FORMULA_RECOGNITION_BATCH_SIZE", "auto"),
            "EDUGRADE_FORMULA_RECOGNITION_BATCH_SIZE",
        ),
        formula_result_cache_size=int(os.environ.get("EDUGRADE_FORMULA_RESULT_CACHE_SIZE", "2048")),
        formula_max_padding_pixels=int(os.environ.get("EDUGRADE_FORMULA_MAX_PADDING_PIXELS", "96")),
        formula_max_padding_height_ratio=float(os.environ.get("EDUGRADE_FORMULA_MAX_PADDING_HEIGHT_RATIO", "0.75")),
        formula_render_similarity_threshold=float(os.environ.get("EDUGRADE_FORMULA_RENDER_SIMILARITY_THRESHOLD", "0.34")),
        formula_model_lifecycle=os.environ.get("EDUGRADE_FORMULA_MODEL_LIFECYCLE", "auto").strip().lower(),
        formula_deployment_profile_path=os.environ.get(
            "EDUGRADE_FORMULA_DEPLOYMENT_PROFILE_PATH",
            "/home/ocr-worker/.paddlex/edugrade/formula-deployment-profile.json",
        ).strip(),
        formula_prewarm_fallback=os.environ.get("EDUGRADE_FORMULA_PREWARM_FALLBACK", "false").lower() in {"1", "true", "yes"},
    )
    _validate_settings(settings)
    return settings


def _validate_settings(settings: Settings) -> None:
    environment = os.environ.get("EDUGRADE_ENV", "development")
    # 保留校验顺序；多项配置同时出错时，仍先报告原来最早检查的一项。
    validate_service_url("EDUGRADE_API_BASE_URL", settings.api_base_url, environment)
    _validate_required_settings(settings)
    _validate_ocr_settings(settings)
    _validate_formula_settings(settings)
    validate_lease_timing(settings.lease_seconds, settings.heartbeat_interval, settings.heartbeat_timeout)
    _validate_enabled_runtimes(settings, environment)


def _validate_required_settings(settings: Settings) -> None:
    for name, value in (
        ("EDUGRADE_OCR_WORKER_TENANT_CODE", settings.tenant_code),
        ("EDUGRADE_OCR_WORKER_USERNAME", settings.username),
        ("EDUGRADE_OCR_WORKER_PASSWORD", settings.password),
        ("EDUGRADE_OCR_WORKER_ID", settings.worker_id),
        ("EDUGRADE_OCR_ENGINE", settings.engine),
        ("EDUGRADE_OCR_ENGINE_VERSION", settings.engine_version),
        ("EDUGRADE_OCR_MODEL_VERSION", settings.model_version),
        ("EDUGRADE_OCR_PREPROCESS_PROFILE", settings.preprocess_profile),
    ):
        if not value.strip():
            raise ValueError(f"{name} must not be empty")


def _validate_ocr_settings(settings: Settings) -> None:
    if not math.isfinite(settings.min_confidence) or not 0 <= settings.min_confidence <= 1:
        raise ValueError("EDUGRADE_OCR_MIN_CONFIDENCE must be between 0 and 1")
    if not math.isfinite(settings.poll_interval) or settings.poll_interval <= 0:
        raise ValueError("EDUGRADE_OCR_POLL_INTERVAL must be greater than 0")
    # OCR 按顺序处理。一次领取多个任务会导致
    # 后续任务在第一个模型推理期间没有心跳。
    if settings.batch_size != 1:
        raise ValueError("EDUGRADE_OCR_BATCH_SIZE must be 1 for the sequential OCR worker")
    if settings.cpu_threads < 1 or settings.cpu_threads > 64:
        raise ValueError("EDUGRADE_OCR_CPU_THREADS must be between 1 and 64")
    if settings.enable_mkldnn not in {"auto", "true", "false"}:
        raise ValueError("EDUGRADE_OCR_ENABLE_MKLDNN must be auto, true, or false")
    if settings.text_det_limit_type not in {"max", "min"}:
        raise ValueError("EDUGRADE_OCR_TEXT_DET_LIMIT_TYPE must be max or min")
    if settings.text_det_limit_side_len < 32 or settings.text_det_limit_side_len > 4096:
        raise ValueError("EDUGRADE_OCR_TEXT_DET_LIMIT_SIDE_LEN must be between 32 and 4096")
    if settings.text_recognition_batch_size < 1 or settings.text_recognition_batch_size > 64:
        raise ValueError("EDUGRADE_OCR_TEXT_RECOGNITION_BATCH_SIZE must be between 1 and 64")


def _validate_formula_settings(settings: Settings) -> None:
    if settings.formula_recognition_batch_size is not None and not 1 <= settings.formula_recognition_batch_size <= 16:
        raise ValueError("EDUGRADE_FORMULA_RECOGNITION_BATCH_SIZE must be between 1 and 16")
    if settings.formula_result_cache_size < 0 or settings.formula_result_cache_size > 100_000:
        raise ValueError("EDUGRADE_FORMULA_RESULT_CACHE_SIZE must be between 0 and 100000")
    if settings.formula_max_padding_pixels < 12 or settings.formula_max_padding_pixels > 512:
        raise ValueError("EDUGRADE_FORMULA_MAX_PADDING_PIXELS must be between 12 and 512")
    if not math.isfinite(settings.formula_max_padding_height_ratio) or not 0.25 <= settings.formula_max_padding_height_ratio <= 2:
        raise ValueError("EDUGRADE_FORMULA_MAX_PADDING_HEIGHT_RATIO must be between 0.25 and 2")
    if not math.isfinite(settings.formula_render_similarity_threshold) or not 0 <= settings.formula_render_similarity_threshold <= 1:
        raise ValueError("EDUGRADE_FORMULA_RENDER_SIMILARITY_THRESHOLD must be between 0 and 1")
    if settings.formula_model_lifecycle not in {"auto", "resident", "per_job"}:
        raise ValueError("EDUGRADE_FORMULA_MODEL_LIFECYCLE must be auto, resident, or per_job")
    if not settings.formula_deployment_profile_path:
        raise ValueError("EDUGRADE_FORMULA_DEPLOYMENT_PROFILE_PATH must not be empty")


def _validate_enabled_runtimes(settings: Settings, environment: str) -> None:
    if not settings.ocr_runtime_enabled and not settings.math_runtime_enabled and not settings.paper_formula_runtime_enabled:
        raise ValueError("at least one OCR worker runtime must be enabled")
    if settings.math_runtime_enabled:
        validate_service_url("EDUGRADE_MATH_VERIFY_BASE_URL", settings.math_verify_base_url, environment)
        if len(settings.math_verify_token) < 32:
            raise ValueError("EDUGRADE_MATH_VERIFY_TOKEN must contain at least 32 characters")
        if settings.formula_model_version not in {"PP-FormulaNet_plus-M", "PP-FormulaNet_plus-L"}:
            raise ValueError("EDUGRADE_FORMULA_MODEL_VERSION is unsupported")
    if settings.paper_formula_runtime_enabled and (
        settings.formula_model_version != "PP-FormulaNet_plus-M"
        or settings.formula_fallback_model_version != "PP-FormulaNet_plus-L"
    ):
        raise ValueError("paper formula runtime requires governed plus-M then plus-L models")


def resolve_formula_model_lifecycle(settings: Settings) -> str:
    """Compatibility wrapper for callers that only need the lifecycle."""

    return resolve_formula_runtime_plan(settings).lifecycle


def resolve_formula_plan(settings: Settings) -> FormulaRuntimePlan:
    return resolve_formula_runtime_plan(settings)


def _optional_positive_int(raw: str, name: str) -> int | None:
    value = raw.strip().lower()
    if value == "auto":
        return None
    try:
        parsed = int(value)
    except ValueError as exc:
        raise ValueError(f"{name} must be auto or an integer") from exc
    return parsed
