from __future__ import annotations

import hashlib
import math
import time
from typing import Any

from .formula_validation import (
    FormulaAction,
    FormulaValidationResult,
    FormulaValidator,
    validate_latex_structure,
)
from .math_layout import (
    FORMULA_DETECTOR_ACCEPT_SCORE,
    FORMULA_EDGE_INK_THRESHOLD,
    PaddleFormulaLayoutDetector,
    _adaptive_crop_formula,
)
from .math_layout import (
    open_image as _open_image,
)
from .recognition_router import PaddleFormulaNetEngine, canonicalize_latex
from .runner import WorkerConfig, _LeaseHeartbeat

FORMULA_VALIDATION_VERSION = "latex-structure-render-v2"


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
        import_id, tenant_id = str(task["source_id"]), str(task["tenant_id"])
        self.api.activate_task(task, self.config.worker_id)
        payload = task.get("payload") or {}
        policy = payload.get("recognition_policy") or {}
        pages = payload.get("pages")
        if payload.get("subject_code") != "mathematics" or not policy.get("formula_enabled") or not isinstance(pages, list):
            self.api.fail_paper_import(import_id, task_id, lease, "invalid_formula_route", False, tenant_id)
            return
        with _LeaseHeartbeat(
            api=self.api,
            runtime_task_id=task_id,
            lease_token=lease,
            worker_id=self.config.worker_id,
            interval=self.config.heartbeat_interval,
            lease_seconds=self.config.lease_seconds,
            request_timeout=self.config.heartbeat_timeout,
            tenant_id=tenant_id,
        ) as heartbeat:
            regions: list[dict[str, Any]] = []
            crops: list[dict[str, Any]] = []
            try:
                page_total = len(pages)
                detector_accept_score = float(policy.get("accept_detector_score", FORMULA_DETECTOR_ACCEPT_SCORE))
                edge_threshold = float(policy.get("max_edge_ink_ratio", FORMULA_EDGE_INK_THRESHOLD))
                formula_batch_size = max(1, int(policy.get("formula_batch_size", self.batch_size)))
                max_padding = max(self.padding, int(policy.get("max_roi_padding_pixels", self.max_padding)))
                render_threshold = float(policy.get("render_similarity_threshold", self.validator.render_similarity_threshold))
                if not self.detector.initialized:
                    heartbeat.set_progress(
                        _progress(
                            "formula_detection",
                            "model_loading",
                            0,
                            page_total,
                            "page",
                            self.detector.model_version,
                            "正在从本地模型缓存加载公式区域检测模型",
                            cold_start=True,
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )
                    self.detector.initialize()
                heartbeat.set_progress(
                    _progress(
                        "formula_detection",
                        "detecting",
                        0,
                        page_total,
                        "page",
                        self.detector.model_version,
                        "公式区域检测模型已就绪，正在检测页面",
                        cold_start=False,
                        **self.runtime_plan,
                    ),
                    flush=True,
                )
                for page_index, page in enumerate(pages, 1):
                    heartbeat.raise_if_failed()
                    image_bytes = self.api.download(str(page["download_url"]), tenant_id)
                    image = _open_image(image_bytes)
                    min_score = float(policy.get("min_detector_score", 0.45))
                    padding = int(policy.get("roi_padding_pixels", self.padding))
                    for index, (bbox, detector_score) in enumerate(self.detector.detect(image_bytes, min_score), 1):
                        crop, padded_bbox, edge_ink, recrop_count, crop_complete = _adaptive_crop_formula(
                            image,
                            bbox,
                            padding,
                            max_padding=max(max_padding, padding),
                            max_padding_height_ratio=self.max_padding_height_ratio,
                            edge_threshold=edge_threshold,
                        )
                        crops.append(
                            {
                                "page": page,
                                "index": index,
                                "crop": crop,
                                "bbox": padded_bbox,
                                "detector_score": detector_score,
                                "edge_ink": edge_ink,
                                "recrop_count": recrop_count,
                                "crop_complete": crop_complete,
                                "aspect_ratio": padded_bbox[2] / max(padded_bbox[3], 1.0),
                            }
                        )
                    image.close()
                    heartbeat.set_progress(
                        _progress(
                            "formula_detection",
                            "detecting",
                            page_index,
                            page_total,
                            "page",
                            self.detector.model_version,
                            f"已检测 {page_index}/{page_total} 页，发现 {len(crops)} 个公式区域",
                            page_no=page_index,
                            page_total=page_total,
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )

                roi_total = len(crops)
                eligible = [
                    index
                    for index, item in enumerate(crops)
                    if float(item["detector_score"]) >= detector_accept_score and bool(item["crop_complete"])
                ]
                skipped = roi_total - len(eligible)
                heartbeat.set_progress(
                    _progress(
                        "formula_recognition",
                        "primary_batch",
                        skipped,
                        roi_total,
                        "formula_region",
                        self.primary.model_version,
                        f"{len(eligible)} 个有效 ROI 进入 plus-M 批量识别，{skipped} 个检测/裁剪异常保留 OCR 并复核",
                        batch_size=formula_batch_size,
                        **self.runtime_plan,
                    ),
                    flush=True,
                )

                primary_results: dict[int, Any] = {}
                primary_batches = _bucketed_batches(crops, eligible, formula_batch_size)
                completed = skipped
                if eligible and not self.primary.initialized:
                    heartbeat.set_progress(
                        _progress(
                            "formula_recognition",
                            "model_loading",
                            completed,
                            roi_total,
                            "formula_region",
                            self.primary.model_version,
                            "正在从本地模型缓存加载 plus-M；完成后开始批量识别",
                            cold_start=True,
                            batch_size=formula_batch_size,
                            batch_total=len(primary_batches),
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )
                    self.primary.initialize()
                for batch_number, batch_indices in enumerate(primary_batches, 1):
                    heartbeat.raise_if_failed()
                    heartbeat.set_progress(
                        _progress(
                            "formula_recognition",
                            "primary_batch",
                            completed,
                            roi_total,
                            "formula_region",
                            self.primary.model_version,
                            f"plus-M 批次 {batch_number}/{len(primary_batches)}，本批 {len(batch_indices)} 个 ROI",
                            batch_no=batch_number,
                            batch_total=len(primary_batches),
                            batch_size=formula_batch_size,
                            cold_start=False,
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )
                    batch_results = _recognize_many(
                        self.primary, [crops[index]["crop"] for index in batch_indices], formula_batch_size,
                    )
                    for index, result in zip(batch_indices, batch_results, strict=True):
                        primary_results[index] = result
                    completed += len(batch_indices)
                    heartbeat.set_progress(
                        _progress(
                            "formula_recognition",
                            "primary_batch",
                            completed,
                            roi_total,
                            "formula_region",
                            self.primary.model_version,
                            f"plus-M 已完成 {completed - skipped}/{len(eligible)} 个有效 ROI",
                            batch_no=batch_number,
                            batch_total=len(primary_batches),
                            batch_size=formula_batch_size,
                            cold_start=False,
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )

                primary_validations: dict[int, FormulaValidationResult] = {}
                fallback_indices: list[int] = []
                for index in eligible:
                    item, result = crops[index], primary_results[index]
                    validation = self.validator.validate(
                        result.canonical_latex,
                        item["crop"],
                        detector_score=float(item["detector_score"]),
                        crop_complete=bool(item["crop_complete"]),
                        detector_accept_score=detector_accept_score,
                        render_similarity_threshold=render_threshold,
                    )
                    primary_validations[index] = validation
                    if validation.action is FormulaAction.RETRY_L:
                        fallback_indices.append(index)

                fallback_results: dict[int, Any] = {}
                fallback_validations: dict[int, FormulaValidationResult] = {}
                fallback_batches = _bucketed_batches(crops, fallback_indices, formula_batch_size)
                fallback_completed = 0
                if fallback_indices and not self.fallback.initialized:
                    heartbeat.set_progress(
                        _progress(
                            "formula_recognition",
                            "model_loading",
                            0,
                            len(fallback_indices),
                            "formula_region",
                            self.fallback.model_version,
                            "正在从本地模型缓存加载 plus-L，仅处理已确认的疑难 ROI",
                            cold_start=True,
                            batch_size=formula_batch_size,
                            batch_total=len(fallback_batches),
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )
                    self.fallback.initialize()
                for batch_number, batch_indices in enumerate(fallback_batches, 1):
                    heartbeat.raise_if_failed()
                    heartbeat.set_progress(
                        _progress(
                            "formula_recognition",
                            "fallback_batch",
                            fallback_completed,
                            len(fallback_indices),
                            "formula_region",
                            self.fallback.model_version,
                            f"仅 {len(fallback_indices)} 个结构/渲染异常 ROI 进入 plus-L；批次 {batch_number}/{len(fallback_batches)}",
                            batch_no=batch_number,
                            batch_total=len(fallback_batches),
                            batch_size=formula_batch_size,
                            cold_start=False,
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )
                    batch_results = _recognize_many(
                        self.fallback, [crops[index]["crop"] for index in batch_indices], formula_batch_size,
                    )
                    for index, result in zip(batch_indices, batch_results, strict=True):
                        fallback_results[index] = result
                        item = crops[index]
                        fallback_validations[index] = self.validator.validate(
                            result.canonical_latex,
                            item["crop"],
                            detector_score=float(item["detector_score"]),
                            crop_complete=bool(item["crop_complete"]),
                            detector_accept_score=detector_accept_score,
                            render_similarity_threshold=render_threshold,
                        )
                    fallback_completed += len(batch_indices)
                    heartbeat.set_progress(
                        _progress(
                            "formula_recognition",
                            "fallback_batch",
                            fallback_completed,
                            len(fallback_indices),
                            "formula_region",
                            self.fallback.model_version,
                            f"plus-L 已完成 {fallback_completed}/{len(fallback_indices)} 个异常 ROI",
                            batch_no=batch_number,
                            batch_total=len(fallback_batches),
                            batch_size=formula_batch_size,
                            cold_start=False,
                            **self.runtime_plan,
                        ),
                        flush=True,
                    )

                for item_index, item in enumerate(crops):
                    detector_score = float(item["detector_score"])
                    primary = primary_results.get(item_index)
                    fallback = fallback_results.get(item_index)
                    primary_validation = primary_validations.get(item_index)
                    fallback_validation = fallback_validations.get(item_index)
                    candidates = []
                    if primary is not None and primary_validation is not None:
                        candidates.append(_candidate(primary, primary_validation))
                    if fallback is not None and fallback_validation is not None:
                        candidates.append(_candidate(fallback, fallback_validation))

                    if primary is None:
                        selected = None
                        status = "review_required"
                        reasons = []
                        if detector_score < detector_accept_score:
                            reasons.append("low_detector_confidence")
                        if not item["crop_complete"]:
                            reasons.append("roi_incomplete_after_recrop")
                    else:
                        selected, status, reasons = _select_validated(
                            primary,
                            primary_validation,
                            fallback,
                            fallback_validation,
                        )
                    if int(item["recrop_count"]) > 0:
                        reasons.append("roi_recropped")

                    page = item["page"]
                    index = int(item["index"])
                    source_id, page_no = str(page["source_id"]), int(page["page_no"])
                    regions.append(
                        {
                            "source_id": source_id,
                            "document_index": int(page["document_index"]),
                            "page_no": page_no,
                            "region_id": f"formula:{source_id}:{page_no}:{index}",
                            "bbox": item["bbox"],
                            "detector_model": self.detector.model_version,
                            "detector_confidence": detector_score,
                            "crop_sha256": hashlib.sha256(item["crop"]).hexdigest(),
                            "edge_ink_ratio": float(item["edge_ink"]),
                            "crop_complete": bool(item["crop_complete"]),
                            "recrop_count": int(item["recrop_count"]),
                            "validation_version": FORMULA_VALIDATION_VERSION,
                            "candidates": candidates,
                            "selected_latex": selected.canonical_latex if selected else "",
                            "selected_model": selected.engine_version if selected else "",
                            "status": status,
                            "reason_codes": list(dict.fromkeys(reasons)),
                        }
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
            except Exception as exc:  # noqa: BLE001 - task boundary must persist any backend failure.
                heartbeat.stop()
                self.api.fail_paper_import(
                    import_id,
                    task_id,
                    lease,
                    f"paper_formula_failed:{type(exc).__name__}"[:80],
                    True,
                    tenant_id,
                    {"error_type": type(exc).__name__, "message": str(exc)[:500]},
                    int((time.monotonic() - started) * 1000),
                )
                return
            heartbeat.stop()
            self.api.complete_paper_import_formula(
                import_id,
                {
                    "task_id": task_id,
                    "lease_token": lease,
                    "duration_ms": int((time.monotonic() - started) * 1000),
                    "regions": regions,
                },
                tenant_id,
            )


def _progress(
    stage: str,
    phase: str,
    completed: int,
    total: int,
    unit: str,
    model: str,
    message: str,
    **extra: Any,
) -> dict[str, Any]:
    return {
        "stage": stage,
        "phase": phase,
        "completed": completed,
        "total": total,
        "unit": unit,
        "model": model,
        "message": message,
        **extra,
    }


def _candidate(result: Any, validation: FormulaValidationResult | None = None) -> dict[str, Any]:
    if validation is None:
        syntax_valid, structure_valid, reasons = validate_latex_structure(result.canonical_latex)
        validation = FormulaValidationResult(
            syntax_valid,
            structure_valid,
            None,
            True,
            None,
            FormulaAction.ACCEPT if structure_valid else FormulaAction.RETRY_L,
            reasons,
        )
    return {
        "model_version": result.engine_version,
        "raw_latex": result.raw_latex,
        "canonical_latex": result.canonical_latex,
        "confidence": result.confidence,
        "valid": validation.valid,
        "syntax_valid": validation.syntax_valid,
        "structure_valid": validation.structure_valid,
        "render_valid": validation.render_valid,
        "render_similarity": validation.render_similarity,
        "validation_action": validation.action.value,
        "reason_codes": list(validation.reason_codes),
    }


def _needs_fallback(primary: Any, detector_score: float, edge_ink: float) -> bool:
    # A low detector score is a classification problem and edge ink is a crop
    # problem. Neither can be repaired by a larger recognition model.
    return (
        detector_score >= FORMULA_DETECTOR_ACCEPT_SCORE
        and edge_ink <= FORMULA_EDGE_INK_THRESHOLD
        and not _valid_formula(primary.canonical_latex)
    )


def _valid_formula(value: str) -> bool:
    _, structure_valid, _ = validate_latex_structure(canonicalize_latex(value))
    return structure_valid


def _select_formula(
    primary: Any,
    fallback: Any,
    detector_score: float,
    edge_ink: float,
) -> tuple[Any | None, str, list[str]]:
    reasons: list[str] = []
    if detector_score < FORMULA_DETECTOR_ACCEPT_SCORE:
        reasons.append("low_detector_confidence")
    if edge_ink > FORMULA_EDGE_INK_THRESHOLD:
        reasons.append("roi_edge_ink")
    p_valid = _valid_formula(primary.canonical_latex)
    if not p_valid:
        reasons.append("primary_invalid_latex")
    if fallback is not None:
        f_valid = _valid_formula(fallback.canonical_latex)
        if not f_valid:
            reasons.append("fallback_invalid_latex")
        if p_valid and f_valid and canonicalize_latex(primary.canonical_latex) != canonicalize_latex(fallback.canonical_latex):
            reasons.append("model_disagreement")
        chosen = primary if p_valid else (fallback if f_valid else None)
    else:
        chosen = primary if p_valid else None
    safe = (
        chosen is not None
        and detector_score >= FORMULA_DETECTOR_ACCEPT_SCORE
        and edge_ink <= FORMULA_EDGE_INK_THRESHOLD
        and "model_disagreement" not in reasons
    )
    return chosen, "accepted" if safe else "review_required", reasons


def _select_validated(
    primary: Any,
    primary_validation: FormulaValidationResult | None,
    fallback: Any,
    fallback_validation: FormulaValidationResult | None,
) -> tuple[Any | None, str, list[str]]:
    if primary_validation is None:
        return None, "review_required", ["primary_validation_missing"]
    reasons = list(primary_validation.reason_codes)
    if primary_validation.valid:
        return primary, "accepted", reasons
    if fallback is not None and fallback_validation is not None:
        if fallback_validation.valid:
            reasons.extend(("fallback_used", *fallback_validation.reason_codes))
            return fallback, "accepted", list(dict.fromkeys(reasons))
        reasons.extend(fallback_validation.reason_codes)
        reasons.append("fallback_validation_failed")
    else:
        reasons.append("fallback_unavailable")
    selected = None
    if fallback is not None and fallback_validation is not None and fallback_validation.structure_valid:
        selected = fallback
    elif primary_validation.structure_valid:
        selected = primary
    return selected, "review_required", list(dict.fromkeys(reasons))


def _recognize_many(engine: Any, crops: list[bytes], batch_size: int) -> list[Any]:
    recognize_many = getattr(engine, "recognize_formulas", None)
    if callable(recognize_many):
        results = list(recognize_many(crops, batch_size=batch_size))
    else:
        results = [engine.recognize_formula(crop) for crop in crops]
    if len(results) != len(crops):
        raise RuntimeError(f"{engine.model_version} returned {len(results)} results for {len(crops)} crops")
    return results


def _bucketed_batches(crops: list[dict[str, Any]], indices: list[int], batch_size: int) -> list[list[int]]:
    buckets: dict[int, list[int]] = {}
    for index in indices:
        ratio = max(0.01, float(crops[index].get("aspect_ratio", 1.0)))
        bucket = max(-3, min(5, math.floor(math.log2(ratio))))
        buckets.setdefault(bucket, []).append(index)
    batches: list[list[int]] = []
    for bucket in sorted(buckets):
        items = buckets[bucket]
        for offset in range(0, len(items), batch_size):
            batches.append(items[offset : offset + batch_size])
    return batches
