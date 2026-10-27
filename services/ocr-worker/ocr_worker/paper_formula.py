from __future__ import annotations

# Runner 持有任务身份、心跳与唯一终态；裁片、推理和结果投影分别在独立阶段中执行。
import time
from typing import Any

from .formula_validation import FormulaValidationResult, FormulaValidator
from .math_layout import (
    FORMULA_DETECTOR_ACCEPT_SCORE,
    FORMULA_EDGE_INK_THRESHOLD,
    PaddleFormulaLayoutDetector,
    _adaptive_crop_formula,
)
from .math_layout import open_image as _open_image
from .paper_formula_crops import FormulaCropStage
from .paper_formula_inference import (
    FormulaInferenceStage,
    _bucketed_batches,
    _recognize_many,
)

# 保留旧模块的纯 helper 入口，现有消费者无需因职责拆分改变导入路径。
from .paper_formula_projection import (
    FORMULA_VALIDATION_VERSION,
    _candidate,
    _needs_fallback,
    _progress,
    _select_formula,
    _select_formula_candidate,
    _select_validated,
    _valid_formula,
    formula_region,
)
from .recognition_router import PaddleFormulaNetEngine
from .runner import WorkerConfig, _LeaseHeartbeat

__all__ = [
    "FORMULA_VALIDATION_VERSION",
    "PaperFormulaRunner",
    "_bucketed_batches",
    "_candidate",
    "_needs_fallback",
    "_recognize_many",
    "_select_formula",
    "_select_formula_candidate",
    "_select_validated",
    "_valid_formula",
]


class PaperFormulaRunner:
    def __init__(
        self,
        *,
        api: Any,
        config: WorkerConfig,
        detector: PaddleFormulaLayoutDetector,
        primary: PaddleFormulaNetEngine,
        fallback: PaddleFormulaNetEngine,
        padding: int = 12,
        max_padding: int = 96,
        max_padding_height_ratio: float = 0.75,
        batch_size: int = 4,
        validator: FormulaValidator | None = None,
        runtime_plan: dict[str, Any] | None = None,
    ) -> None:
        self.api, self.config, self.detector = api, config, detector
        self.primary, self.fallback, self.padding = primary, fallback, padding
        self.max_padding = max(max_padding, padding)
        self.max_padding_height_ratio = max(0.25, max_padding_height_ratio)
        self.batch_size = max(1, batch_size)
        self.validator = validator or FormulaValidator()
        self.runtime_plan = dict(runtime_plan or {})

    def process_once(self) -> int:
        tasks = self.api.claim_paper_formula_tasks(self.config.worker_id, self.config.lease_seconds)
        for task in tasks:
            self._process(task)
        return len(tasks)

    def _process(self, task: dict[str, Any]) -> None:
        started = time.monotonic()
        task_id = str(task["id"])
        lease = str(task["lease_token"])
        import_id = str(task["source_id"])
        # 保留领取任务的元数据契约检查；此字段不能用于选择 HTTP 请求租户。
        if "tenant_id" not in task:
            raise KeyError("tenant_id")
        self.api.activate_task(task, self.config.worker_id)
        payload = task.get("payload") or {}
        policy = payload.get("recognition_policy") or {}
        pages = payload.get("pages")
        if payload.get("subject_code") != "mathematics" or not policy.get("formula_enabled") or not isinstance(pages, list):
            self.api.fail_paper_import(import_id, task_id, lease, "invalid_formula_route", False)
            return
        with _LeaseHeartbeat(
            api=self.api,
            runtime_task_id=task_id,
            lease_token=lease,
            worker_id=self.config.worker_id,
            interval=self.config.heartbeat_interval,
            lease_seconds=self.config.lease_seconds,
            request_timeout=self.config.heartbeat_timeout,
        ) as heartbeat:
            regions: list[dict[str, Any]] = []
            try:
                page_total = len(pages)
                detector_accept_score = float(policy.get("accept_detector_score", FORMULA_DETECTOR_ACCEPT_SCORE))
                edge_threshold = float(policy.get("max_edge_ink_ratio", FORMULA_EDGE_INK_THRESHOLD))
                formula_batch_size = max(1, int(policy.get("formula_batch_size", self.batch_size)))
                max_padding = max(self.padding, int(policy.get("max_roi_padding_pixels", self.max_padding)))
                render_threshold = float(policy.get("render_similarity_threshold", self.validator.render_similarity_threshold))
                crops = self._detect_formula_crops(
                    pages, policy, heartbeat, max_padding, edge_threshold, page_total,
                )
                roi_total = len(crops)
                # 所有 M 批次结束后才统一校验；只有 RETRY_L 的 ROI 会继续进入 L 阶段。
                eligible, primary_results, primary_validations, fallback_indices = self._recognize_primary_crops(
                    crops, heartbeat, formula_batch_size, detector_accept_score, render_threshold, roi_total,
                )
                fallback_results, fallback_validations = self._recognize_fallback_crops(
                    crops, fallback_indices, heartbeat, formula_batch_size, detector_accept_score, render_threshold,
                )
                for item_index, item in enumerate(crops):
                    regions.append(
                        self._formula_region(
                            item_index, item, primary_results, primary_validations,
                            fallback_results, fallback_validations, detector_accept_score,
                        )
                    )

                heartbeat.set_progress(
                    _progress(
                        "formula_recognition",
                        "completed",
                        roi_total,
                        roi_total,
                        "formula_region",
                        self.primary.model_version,
                        f"公式识别完成：M {len(eligible)} 个，L {len(fallback_indices)} 个，复核 {sum(region['status'] != 'accepted' for region in regions)} 个",
                        batch_size=formula_batch_size,
                        **self.runtime_plan,
                    ),
                    flush=True,
                )
            except Exception as exc:  # noqa: BLE001 - 捕获模型等后端异常，先停止心跳再上报任务失败。
                heartbeat.stop()
                self.api.fail_paper_import(
                    import_id,
                    task_id,
                    lease,
                    f"paper_formula_failed:{type(exc).__name__}"[:80],
                    True,
                    error_detail={"error_type": type(exc).__name__, "message": str(exc)[:500]},
                    duration_ms=int((time.monotonic() - started) * 1000),
                )
                return
            # 停止心跳失败仍沿原路径抛出，不能继续写入成功终态。
            heartbeat.stop()
            self.api.complete_paper_import_formula(
                import_id,
                {
                    "task_id": task_id,
                    "lease_token": lease,
                    "duration_ms": int((time.monotonic() - started) * 1000),
                    "regions": regions,
                },
            )

    def _detect_formula_crops(
        self,
        pages: list[dict[str, Any]],
        policy: dict[str, Any],
        heartbeat: _LeaseHeartbeat,
        max_padding: int,
        edge_threshold: float,
        page_total: int,
    ) -> list[dict[str, Any]]:
        stage = FormulaCropStage(self.api, self.detector, self.padding, self.max_padding_height_ratio, self.runtime_plan)
        return stage.detect_formula_crops(
            pages, policy, heartbeat, max_padding, edge_threshold, page_total,
            open_image=_open_image, adaptive_crop=_adaptive_crop_formula,
        )

    def _recognize_primary_crops(
        self,
        crops: list[dict[str, Any]],
        heartbeat: _LeaseHeartbeat,
        formula_batch_size: int,
        detector_accept_score: float,
        render_threshold: float,
        roi_total: int,
    ) -> tuple[list[int], dict[int, Any], dict[int, FormulaValidationResult], list[int]]:
        stage = FormulaInferenceStage(self.primary, self.fallback, self.validator, self.runtime_plan)
        return stage.recognize_primary_crops(
            crops, heartbeat, formula_batch_size, detector_accept_score, render_threshold, roi_total,
            recognize_many=_recognize_many,
        )

    def _recognize_fallback_crops(
        self,
        crops: list[dict[str, Any]],
        fallback_indices: list[int],
        heartbeat: _LeaseHeartbeat,
        formula_batch_size: int,
        detector_accept_score: float,
        render_threshold: float,
    ) -> tuple[dict[int, Any], dict[int, FormulaValidationResult]]:
        stage = FormulaInferenceStage(self.primary, self.fallback, self.validator, self.runtime_plan)
        return stage.recognize_fallback_crops(
            crops, fallback_indices, heartbeat, formula_batch_size, detector_accept_score, render_threshold,
            recognize_many=_recognize_many,
        )

    def _formula_region(
        self,
        item_index: int,
        item: dict[str, Any],
        primary_results: dict[int, Any],
        primary_validations: dict[int, FormulaValidationResult],
        fallback_results: dict[int, Any],
        fallback_validations: dict[int, FormulaValidationResult],
        detector_accept_score: float,
    ) -> dict[str, Any]:
        return formula_region(
            self.detector.model_version, item_index, item, primary_results, primary_validations,
            fallback_results, fallback_validations, detector_accept_score,
        )
