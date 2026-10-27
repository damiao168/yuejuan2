from __future__ import annotations

# 仅负责候选选择与 API 结果投影；不领取任务、不持有图像或写终态。
import hashlib
from typing import Any

from .formula_validation import (
    FormulaAction,
    FormulaValidationResult,
    validate_latex_structure,
)
from .math_layout import FORMULA_DETECTOR_ACCEPT_SCORE, FORMULA_EDGE_INK_THRESHOLD
from .recognition_router import canonicalize_latex

FORMULA_VALIDATION_VERSION = "latex-structure-render-v2"


def formula_region(
    detector_model: str,
    item_index: int,
    item: dict[str, Any],
    primary_results: dict[int, Any],
    primary_validations: dict[int, FormulaValidationResult],
    fallback_results: dict[int, Any],
    fallback_validations: dict[int, FormulaValidationResult],
    detector_accept_score: float,
) -> dict[str, Any]:
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
    return {
        "source_id": source_id,
        "document_index": int(page["document_index"]),
        "page_no": page_no,
        "region_id": f"formula:{source_id}:{page_no}:{index}",
        "bbox": item["bbox"],
        "detector_model": detector_model,
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
    # 检测分数低属于分类问题，边缘有墨迹属于裁剪
    # 问题。更大的识别模型无法修复这两类问题。
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
    chosen = _select_formula_candidate(primary, fallback, p_valid, reasons)
    safe = (
        chosen is not None
        and detector_score >= FORMULA_DETECTOR_ACCEPT_SCORE
        and edge_ink <= FORMULA_EDGE_INK_THRESHOLD
        and "model_disagreement" not in reasons
    )
    return chosen, "accepted" if safe else "review_required", reasons


def _select_formula_candidate(primary: Any, fallback: Any, p_valid: bool, reasons: list[str]) -> Any | None:
    if fallback is None:
        return primary if p_valid else None
    f_valid = _valid_formula(fallback.canonical_latex)
    if not f_valid:
        reasons.append("fallback_invalid_latex")
    if p_valid and f_valid and canonicalize_latex(primary.canonical_latex) != canonicalize_latex(fallback.canonical_latex):
        reasons.append("model_disagreement")
    if p_valid:
        return primary
    return fallback if f_valid else None


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
    # 保留结构可用的候选供复核，但没有通过完整校验时仍保持 review_required。
    selected = None
    if fallback is not None and fallback_validation is not None and fallback_validation.structure_valid:
        selected = fallback
    elif primary_validation.structure_valid:
        selected = primary
    return selected, "review_required", list(dict.fromkeys(reasons))
