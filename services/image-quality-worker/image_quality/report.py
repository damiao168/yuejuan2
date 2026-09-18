"""Assemble stable quality-report and normalization-transform schemas."""

from typing import Any

import numpy as np

from image_quality.geometry import _matrix_list

METRIC_SCHEMA_VERSION = "image-quality-metrics-v2"
REPORT_SCHEMA_VERSION = "image-quality-report-v2"


def build_quality_report(
    *,
    pixel_width: int,
    pixel_height: int,
    source_dpi: float | None,
    source_format: str,
    metrics: dict[str, Any],
    geometry: dict[str, Any],
    focus: dict[str, Any],
    illumination: dict[str, Any],
    occlusion: dict[str, Any],
    degradation: dict[str, Any],
    dimensions: dict[str, Any],
    hard_gates: list[dict[str, Any]],
    quality_score: float,
    decision: str,
    enhancements: list[str],
) -> dict[str, Any]:
    return {
        "metric_schema_version": METRIC_SCHEMA_VERSION,
        "report_schema_version": REPORT_SCHEMA_VERSION,
        "pixel_width": pixel_width,
        "pixel_height": pixel_height,
        "source_dpi": source_dpi,
        "source_dpi_method": "embedded_metadata"
        if source_dpi is not None
        else "missing_metadata",
        "render_dpi": None,
        "source_format": source_format,
        "output_format": "image/png",
        "normalized_color_mode": "RGB",
        "metrics": metrics,
        "geometry": geometry,
        "focus": focus,
        "illumination": illumination,
        "occlusion_reflection": occlusion,
        "noise_compression": degradation,
        "dimensions": dimensions,
        "hard_gates": hard_gates,
        "quality_score": quality_score,
        "decision": decision,
        "enhancements_applied": enhancements,
        "roi_completeness": {
            "status": "unavailable",
            "required_roi_visible_ratio": None,
            "reason": "template_registration_not_available_at_quality_stage",
        },
        "predictor": {
            "kind": "weighted_rules",
            "version": "weighted-rules-v2",
            "calibration_status": "awaiting_real_answer_sheet_samples",
            "future_model": "xgboost_ocr_failure_probability",
        },
        "stages": {
            "fast_gate": {
                "status": "failed"
                if any(gate["status"] == "failed" for gate in hard_gates)
                else "passed",
                "checks": [
                    "decode",
                    "effective_resolution",
                    "severe_blur",
                    "blank_probability",
                ],
            },
            "page_registration": {"status": "external_stage"},
            "detailed_assessment": {"status": "completed"},
            "post_registration_roi_assessment": {
                "status": "awaiting_template_registration"
            },
        },
    }

def build_normalization_transform(
    *,
    source_width: int,
    source_height: int,
    pixel_width: int,
    pixel_height: int,
    exif_rotation: int,
    content_rotation: float,
    skew_angle: float,
    deskew_applied: bool,
    deskew_matrix: np.ndarray,
    exif_matrix: np.ndarray,
) -> dict[str, Any]:
    return {
        "source_pixel_width": source_width,
        "source_pixel_height": source_height,
        "normalized_pixel_width": pixel_width,
        "normalized_pixel_height": pixel_height,
        "exif_rotation_degrees": exif_rotation,
        "content_rotation_degrees": round(content_rotation, 4),
        "detected_skew_angle": round(skew_angle, 4),
        "skew_correction_applied": deskew_applied,
        "crop_box_source_pixels": [0, 0, source_width, source_height],
        "source_to_normalized_matrix": _matrix_list(deskew_matrix @ exif_matrix),
    }
