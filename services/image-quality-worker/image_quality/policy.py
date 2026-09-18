"""Thresholds, hard gates, issue rules, and quality decisions."""

from typing import Any

from image_quality.geometry import (
    MAX_DESKEW_DEGREES,
    MIN_DESKEW_CONFIDENCE,
    PERSPECTIVE_SUSPECTED_SCORE,
    _clamp,
)
from image_quality.metrics import MIN_EFFECTIVE_SHORT_EDGE

SEVERE_BLUR_SHARPNESS_THRESHOLD = 0.12


def _issues(
    metrics: dict[str, float | int],
    geometry: dict[str, Any],
    hard_gates: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    issues: list[dict[str, Any]] = []
    failed_gate_codes = {
        str(gate["code"]) for gate in hard_gates if gate["status"] == "failed"
    }
    if "low_effective_resolution" in failed_gate_codes:
        issues.append(
            _issue(
                "low_effective_resolution",
                "failed",
                "effective_short_edge_px",
                metrics["effective_short_edge_px"],
                MIN_EFFECTIVE_SHORT_EDGE,
                "recapture",
            )
        )
    elif metrics["effective_short_edge_px"] < 1600:
        issues.append(
            _issue(
                "low_effective_resolution",
                "review",
                "effective_short_edge_px",
                metrics["effective_short_edge_px"],
                1600,
                "manual_review",
            )
        )
    if "severe_blur" in failed_gate_codes:
        issues.append(
            _issue(
                "low_sharpness",
                "failed",
                "sharpness_score",
                metrics["sharpness_score"],
                SEVERE_BLUR_SHARPNESS_THRESHOLD,
                "recapture",
                {"window": "local_focus_map", "blur_pattern": metrics["blur_pattern"]},
            )
        )
    elif metrics["sharpness_score"] < 0.42 or metrics["bad_focus_patch_ratio"] > 0.15:
        issues.append(
            _issue(
                "low_sharpness",
                "review",
                "bad_focus_patch_ratio",
                metrics["bad_focus_patch_ratio"],
                0.15,
                "manual_review",
                {"window": "local_focus_map", "blur_pattern": metrics["blur_pattern"]},
            )
        )
    if metrics["blank_probability"] > 0.97:
        issues.append(
            _issue(
                "blank_page",
                "review",
                "blank_probability",
                metrics["blank_probability"],
                0.97,
                "manual_review",
            )
        )
    if "incomplete_page" in failed_gate_codes:
        issues.append(
            _issue(
                "incomplete_page_border",
                "failed",
                "border_completeness_score",
                metrics["border_completeness_score"],
                0.75,
                "recapture",
            )
        )
    elif (
        geometry["page_border_confidence"] >= 0.5
        and metrics["border_completeness_score"] < 0.98
    ):
        issues.append(
            _issue(
                "incomplete_page_border",
                "review",
                "border_completeness_score",
                metrics["border_completeness_score"],
                0.98,
                "manual_review",
            )
        )
    if (
        geometry["perspective_confidence"] >= 0.4
        and metrics["perspective_risk_score"] > PERSPECTIVE_SUSPECTED_SCORE
    ):
        issues.append(
            _issue(
                "perspective_risk",
                "review",
                "perspective_risk_score",
                metrics["perspective_risk_score"],
                PERSPECTIVE_SUSPECTED_SCORE,
                "manual_review",
            )
        )
    if (
        abs(float(geometry["detected_skew_angle"])) > MAX_DESKEW_DEGREES
        and geometry["skew_confidence"] >= MIN_DESKEW_CONFIDENCE
    ):
        issues.append(
            _issue(
                "residual_skew",
                "review",
                "detected_skew_angle",
                geometry["detected_skew_angle"],
                MAX_DESKEW_DEGREES,
                "manual_review",
            )
        )
    if geometry["coarse_orientation"] == "landscape":
        issues.append(
            _issue(
                "large_rotation_risk",
                "review",
                "coarse_orientation",
                geometry["coarse_orientation"],
                "portrait_or_square",
                "manual_review",
            )
        )
    if metrics["exposure_quality_score"] < 0.45:
        issues.append(
            _issue(
                "bad_exposure",
                "review",
                "exposure_quality_score",
                metrics["exposure_quality_score"],
                0.45,
                "manual_review",
            )
        )
    if metrics["shadow_risk_score"] > 0.48:
        issues.append(
            _issue(
                "shadow_risk",
                "review",
                "shadow_risk_score",
                metrics["shadow_risk_score"],
                0.48,
                "manual_review",
            )
        )
    if metrics["low_contrast_area_ratio"] > 0.15:
        issues.append(
            _issue(
                "low_local_contrast",
                "review",
                "low_contrast_area_ratio",
                metrics["low_contrast_area_ratio"],
                0.15,
                "manual_review",
            )
        )
    if metrics["glare_risk_score"] > 0.4:
        issues.append(
            _issue(
                "glare_risk",
                "review",
                "glare_risk_score",
                metrics["glare_risk_score"],
                0.4,
                "manual_review",
            )
        )
    if (
        metrics["occlusion_confidence"] >= 0.6
        and metrics["occlusion_risk_score"] > 0.55
    ):
        issues.append(
            _issue(
                "occlusion_risk",
                "review",
                "occlusion_risk_score",
                metrics["occlusion_risk_score"],
                0.55,
                "manual_review",
            )
        )
    if (
        max(
            float(metrics["noise_risk_score"]), float(metrics["compression_risk_score"])
        )
        > 0.72
    ):
        issues.append(
            _issue(
                "noise_or_compression",
                "review",
                "noise_risk_score",
                metrics["noise_risk_score"],
                0.72,
                "manual_review",
            )
        )
    return issues



def _hard_gates(
    metrics: dict[str, float | int], geometry: dict[str, Any]
) -> list[dict[str, Any]]:
    severe_blur = (
        float(metrics["sharpness_score"]) < SEVERE_BLUR_SHARPNESS_THRESHOLD
        and float(metrics["focus_laplacian_median"]) < 30.0
    ) or (
        float(metrics["bad_focus_patch_ratio"]) > 0.5
        and float(metrics["focus_tenengrad_median"]) < 8.0
    )
    incomplete = (
        geometry["page_border_confidence"] >= 0.7
        and float(metrics["border_completeness_score"]) < 0.75
    )
    return [
        _gate("page_decode", "passed", True, True),
        _gate(
            "low_effective_resolution",
            "failed"
            if int(metrics["effective_short_edge_px"]) < MIN_EFFECTIVE_SHORT_EDGE
            else "passed",
            metrics["effective_short_edge_px"],
            MIN_EFFECTIVE_SHORT_EDGE,
        ),
        _gate(
            "severe_blur",
            "failed" if severe_blur else "passed",
            metrics["sharpness_score"],
            SEVERE_BLUR_SHARPNESS_THRESHOLD,
        ),
        _gate(
            "incomplete_page",
            "failed" if incomplete else "passed",
            metrics["border_completeness_score"],
            0.75,
        ),
        _gate(
            "required_roi_completeness",
            "not_evaluated",
            None,
            0.98,
            {"reason": "template_registration_not_available"},
        ),
        _gate(
            "critical_roi_occlusion",
            "not_evaluated",
            None,
            0.0,
            {"reason": "template_registration_not_available"},
        ),
    ]


def _quality_score(metrics: dict[str, float | int]) -> float:
    illumination = _clamp(
        0.55 * float(metrics["exposure_quality_score"])
        + 0.45 * (1.0 - float(metrics["shadow_risk_score"]))
    )
    geometry = _clamp(1.0 - float(metrics["perspective_risk_score"]))
    occlusion = _clamp(
        1.0
        - max(
            float(metrics["occlusion_risk_score"])
            * float(metrics["occlusion_confidence"]),
            float(metrics["glare_risk_score"]),
        )
    )
    noise = _clamp(
        1.0
        - max(
            float(metrics["noise_risk_score"]), float(metrics["compression_risk_score"])
        )
    )
    score = 100.0 * (
        0.25 * float(metrics["sharpness_score"])
        + 0.15 * float(metrics["effective_resolution_score"])
        + 0.15 * geometry
        + 0.15 * illumination
        + 0.15 * float(metrics["contrast_score"])
        + 0.10 * occlusion
        + 0.05 * noise
    )
    return round(score, 1)


def _decision(
    score: float,
    issues: list[dict[str, Any]],
    hard_gates: list[dict[str, Any]],
    enhancements: list[str],
) -> tuple[str, str]:
    if any(gate["status"] == "failed" for gate in hard_gates):
        return "REJECT", "failed"
    if issues:
        return ("REJECT", "failed") if score < 50.0 else ("LOW_QUALITY", "review")
    if enhancements:
        return "PASS_WITH_ENHANCEMENT", "passed"
    return ("PASS", "passed") if score >= 78.0 else ("LOW_QUALITY", "review")


def _issue(
    code: str,
    severity: str,
    metric: str,
    observed: Any,
    threshold: Any,
    action: str,
    parameters: dict[str, Any] | None = None,
) -> dict[str, Any]:
    return {
        "code": code,
        "severity": severity,
        "metric": metric,
        "observed": observed,
        "threshold": threshold,
        "rule_id": f"{code}-v2",
        "action": action,
        "parameters": parameters or {},
    }

def _gate(
    code: str,
    status: str,
    observed: Any,
    threshold: Any,
    parameters: dict[str, Any] | None = None,
) -> dict[str, Any]:
    return {
        "code": code,
        "status": status,
        "observed": observed,
        "threshold": threshold,
        "parameters": parameters or {},
    }
