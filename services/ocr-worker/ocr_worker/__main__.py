from __future__ import annotations

import logging
import time

from .api import APIError, AuthenticationError, EduGradeClient
from .config import Settings, load_settings, resolve_formula_plan
from .engine import PaddleOCREngine
from .formula_validation import FormulaValidator
from .math_layout import PaddleFormulaLayoutDetector
from .math_runner import MathUnderstandingRunner, MathVerificationClient
from .paper_formula import PaperFormulaRunner
from .recognition_router import PaddleFormulaNetEngine, RecognitionRouter
from .runner import OCRRunner, WorkerConfig
from .symbolic_runner import SymbolicVerificationRunner


def _initialize_ocr_engine(settings: Settings, log: logging.Logger) -> PaddleOCREngine:
    engine = PaddleOCREngine(
        device=settings.device,
        model_version=settings.model_version,
        cpu_threads=getattr(settings, "cpu_threads", 4),
        enable_mkldnn=getattr(settings, "enable_mkldnn", "auto"),
        enable_hpi=getattr(settings, "enable_hpi", False),
        use_textline_orientation=getattr(settings, "use_textline_orientation", True),
        text_det_limit_type=getattr(settings, "text_det_limit_type", "min"),
        text_det_limit_side_len=getattr(settings, "text_det_limit_side_len", 64),
        text_recognition_batch_size=getattr(settings, "text_recognition_batch_size", 1),
    )
    if getattr(settings, "ocr_runtime_enabled", True) or getattr(settings, "math_runtime_enabled", False):
        log.info("initializing OCR engine before worker registration")
        engine.initialize()
        log.info(
            "OCR engine ready: model=%s device=%s cpu_threads=%s effective_mkldnn=%s hpi=%s orientation=%s",
            engine.model_version, engine.device, engine.cpu_threads, engine.effective_mkldnn,
            engine.enable_hpi, engine.use_textline_orientation,
        )
    return engine


def _build_worker_config(settings: Settings) -> WorkerConfig:
    return WorkerConfig(
        worker_id=settings.worker_id,
        batch_size=settings.batch_size,
        engine=settings.engine,
        engine_version=settings.engine_version,
        preprocess_profile=settings.preprocess_profile,
        lease_seconds=settings.lease_seconds,
        heartbeat_interval=settings.heartbeat_interval,
        heartbeat_timeout=settings.heartbeat_timeout,
        cpu_threads=getattr(settings, "cpu_threads", 4),
        enable_mkldnn=getattr(settings, "enable_mkldnn", "auto"),
        enable_hpi=getattr(settings, "enable_hpi", False),
        use_textline_orientation=getattr(settings, "use_textline_orientation", True),
        text_det_limit_type=getattr(settings, "text_det_limit_type", "min"),
        text_det_limit_side_len=getattr(settings, "text_det_limit_side_len", 64),
        text_recognition_batch_size=getattr(settings, "text_recognition_batch_size", 1),
    )


def _build_math_runners(
    settings: Settings,
    client: EduGradeClient,
    engine: PaddleOCREngine,
    worker_config: WorkerConfig,
    log: logging.Logger,
) -> tuple[MathUnderstandingRunner | None, SymbolicVerificationRunner | None]:
    if not getattr(settings, "math_runtime_enabled", False):
        return None, None
    formula_engine = None
    formula_detector = None
    # 两个模型独立降级；其中一个不可用时仍保留另一个及后续验证运行器。
    try:
        formula_engine = PaddleFormulaNetEngine(model_version=settings.formula_model_version, device=settings.device)
        formula_engine.initialize()
    except Exception:
        formula_engine = None
        log.exception("formula engine unavailable; mathematical formula regions will require human review")
    try:
        formula_detector = PaddleFormulaLayoutDetector(settings.formula_layout_model_version, settings.device)
        formula_detector.initialize()
    except Exception:
        formula_detector = None
        log.exception("formula layout detector unavailable; mixed mathematical regions will require human review")
    math_runner = MathUnderstandingRunner(
        api=client,
        router=RecognitionRouter(
            text_engine=engine,
            formula_engine=formula_engine,
            formula_detector=formula_detector,
            formula_batch_size=getattr(settings, "formula_recognition_batch_size", None) or 4,
            formula_max_padding=getattr(settings, "formula_max_padding_pixels", 96),
            formula_max_padding_height_ratio=getattr(settings, "formula_max_padding_height_ratio", 0.75),
        ),
        verifier=MathVerificationClient(settings.math_verify_base_url, settings.math_verify_token),
        config=worker_config,
    )
    symbolic_runner = SymbolicVerificationRunner(
        api=client, verifier=MathVerificationClient(settings.math_verify_base_url, settings.math_verify_token),
        config=worker_config,
    )
    log.info(
        "math understanding runtime ready: formula_model=%s formula_available=%s layout_model=%s layout_available=%s",
        settings.formula_model_version,
        formula_engine is not None,
        settings.formula_layout_model_version,
        formula_detector is not None,
    )
    return math_runner, symbolic_runner


def _build_paper_formula_runner(
    settings: Settings,
    client: EduGradeClient,
    worker_config: WorkerConfig,
    log: logging.Logger,
) -> tuple[PaperFormulaRunner | None, str]:
    if not getattr(settings, "paper_formula_runtime_enabled", False):
        return None, "resident"
    detector = PaddleFormulaLayoutDetector(settings.formula_layout_model_version, settings.device)
    formula_cache_size = getattr(settings, "formula_result_cache_size", 2048)
    primary = PaddleFormulaNetEngine(
        model_version=settings.formula_model_version,
        device=settings.device,
        cache_size=formula_cache_size,
    )
    fallback = PaddleFormulaNetEngine(
        model_version=settings.formula_fallback_model_version,
        device=settings.device,
        cache_size=formula_cache_size,
    )
    formula_plan = resolve_formula_plan(settings)
    formula_model_lifecycle = formula_plan.lifecycle
    log.info(
        "paper formula runtime plan: requested=%s effective=%s batch=%s source=%s reason=%s device=%s profile=%s",
        settings.formula_model_lifecycle,
        formula_model_lifecycle,
        formula_plan.batch_size,
        formula_plan.source,
        formula_plan.reason,
        settings.device,
        formula_plan.profile_sha256 or "none",
    )
    if formula_model_lifecycle == "resident":
        log.info("preloading paper formula detector and plus-M model")
        detector.initialize()
        primary.initialize()
    else:
        log.info("paper formula detector and plus-M will load after a task is claimed")
    if formula_model_lifecycle == "resident" and getattr(settings, "formula_prewarm_fallback", False):
        log.info("preloading governed plus-L fallback model")
        fallback.initialize()
    paper_formula_runner = PaperFormulaRunner(
        api=client,
        config=worker_config,
        detector=detector,
        primary=primary,
        fallback=fallback,
        max_padding=getattr(settings, "formula_max_padding_pixels", 96),
        max_padding_height_ratio=getattr(settings, "formula_max_padding_height_ratio", 0.75),
        batch_size=formula_plan.batch_size,
        runtime_plan={
            "runtime_mode": formula_plan.lifecycle,
            "runtime_plan_source": formula_plan.source,
            "runtime_batch_size": formula_plan.batch_size,
        },
        validator=FormulaValidator(
            render_similarity_threshold=getattr(settings, "formula_render_similarity_threshold", 0.34),
        ),
    )
    log.info("paper formula runtime ready: detector=%s primary=%s fallback=%s", settings.formula_layout_model_version, settings.formula_model_version, settings.formula_fallback_model_version)
    return paper_formula_runner, formula_model_lifecycle


def _process_runners_once(
    runner: OCRRunner | None,
    math_runner: MathUnderstandingRunner | None,
    symbolic_runner: SymbolicVerificationRunner | None,
    paper_formula_runner: PaperFormulaRunner | None,
    formula_model_lifecycle: str,
    formula_processed_since_start: bool,
) -> tuple[int, bool, bool]:
    processed = runner.process_once() if runner is not None else 0
    if math_runner is not None:
        processed += math_runner.process_once()
    if symbolic_runner is not None:
        processed += symbolic_runner.process_once()
    if paper_formula_runner is not None:
        formula_processed = paper_formula_runner.process_once()
        processed += formula_processed
        formula_processed_since_start = formula_processed_since_start or formula_processed > 0
        # 首次空队列不会退出；处理过公式任务后再遇到空队列，才结束 per_job 进程。
        if formula_processed_since_start and formula_processed == 0 and formula_model_lifecycle == "per_job":
            return processed, formula_processed_since_start, True
    return processed, formula_processed_since_start, False


def _poll_runners(
    settings: Settings,
    client: EduGradeClient,
    runner: OCRRunner | None,
    math_runners: tuple[MathUnderstandingRunner | None, SymbolicVerificationRunner | None],
    paper_formula_runner: PaperFormulaRunner | None,
    formula_model_lifecycle: str,
    log: logging.Logger,
) -> None:
    math_runner, symbolic_runner = math_runners
    formula_processed_since_start = False
    while True:
        try:
            if client.token is None:
                client.login()
                log.info("worker authenticated")
            processed, formula_processed_since_start, formula_drained = _process_runners_once(
                runner, math_runner, symbolic_runner, paper_formula_runner,
                formula_model_lifecycle, formula_processed_since_start,
            )
            if formula_drained:
                # 即使 Python 引用已释放，Paddle/oneDNN
                # 仍可能保留原生分配器的内存区域。只有正常
                # 退出进程才能可靠地归还这些内存。先处理完
                # 这一批任务，让队列中的考试复用一次模型加载，
                # 无需设置任意的空闲超时。
                log.info("paper formula queue drained; per_job lifecycle is releasing model memory")
                return
            if processed == 0:
                time.sleep(settings.poll_interval)
        except AuthenticationError:
            client.token = None
            log.warning("worker authentication expired; login will be retried")
            time.sleep(settings.poll_interval)
        except APIError as exc:
            log.warning("worker API request failed; polling will continue", extra={"error_type": type(exc).__name__})
            time.sleep(settings.poll_interval)
        except Exception:
            log.exception("unexpected OCR worker error; polling will continue")
            time.sleep(settings.poll_interval)


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    log = logging.getLogger("edugrade.ocr_worker")
    settings = load_settings()
    # OCR 引擎初始化失败仍在认证和领取之前终止启动，不交给轮询重试处理。
    engine = _initialize_ocr_engine(settings, log)
    client = EduGradeClient(
        base_url=settings.api_base_url,
        tenant_code=settings.tenant_code,
        username=settings.username,
        password=settings.password,
    )
    worker_config = _build_worker_config(settings)
    runner = None
    if getattr(settings, "ocr_runtime_enabled", True):
        runner = OCRRunner(api=client, engine=engine, config=worker_config)
    math_runners = _build_math_runners(settings, client, engine, worker_config, log)
    paper_formula_runner, formula_model_lifecycle = _build_paper_formula_runner(settings, client, worker_config, log)
    _poll_runners(settings, client, runner, math_runners, paper_formula_runner, formula_model_lifecycle, log)


if __name__ == "__main__":
    main()
