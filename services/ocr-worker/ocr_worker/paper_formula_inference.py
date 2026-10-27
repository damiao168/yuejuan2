from __future__ import annotations

# 模型实例仍由进程装配和 Runner 持有；本阶段只执行 M→统一校验→按需 L，不接管租约终态。
import math
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from .formula_validation import FormulaAction, FormulaValidationResult, FormulaValidator
from .paper_formula_projection import _progress
from .runner import _LeaseHeartbeat


@dataclass(frozen=True)
class FormulaInferenceStage:
    primary: Any
    fallback: Any
    validator: FormulaValidator
    runtime_plan: dict[str, Any]

    def recognize_primary_crops(
        self,
        crops: list[dict[str, Any]],
        heartbeat: _LeaseHeartbeat,
        formula_batch_size: int,
        detector_accept_score: float,
        render_threshold: float,
        roi_total: int,
        *,
        recognize_many: Callable[[Any, list[bytes], int], list[Any]],
    ) -> tuple[list[int], dict[int, Any], dict[int, FormulaValidationResult], list[int]]:
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
            batch_results = recognize_many(
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
            # 大模型仅重试结构或回渲染失败；低检测分、裁剪缺失及校验器不可用转人工。
            if validation.action is FormulaAction.RETRY_L:
                fallback_indices.append(index)
        return eligible, primary_results, primary_validations, fallback_indices

    def recognize_fallback_crops(
        self,
        crops: list[dict[str, Any]],
        fallback_indices: list[int],
        heartbeat: _LeaseHeartbeat,
        formula_batch_size: int,
        detector_accept_score: float,
        render_threshold: float,
        *,
        recognize_many: Callable[[Any, list[bytes], int], list[Any]],
    ) -> tuple[dict[int, Any], dict[int, FormulaValidationResult]]:
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
            batch_results = recognize_many(
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
        return fallback_results, fallback_validations


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
