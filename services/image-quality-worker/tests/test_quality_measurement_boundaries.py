from __future__ import annotations

import numpy as np
import pytest
from image_quality import geometry, metrics, policy


def test_equal_border_scores_keep_first_contour(monkeypatch) -> None:
    first = np.asarray(
        [[[50, 150]], [[750, 150]], [[750, 850]], [[50, 850]]], dtype=np.int32
    )
    second = first + np.asarray([200, 0])
    monkeypatch.setattr(
        geometry.cv2, "findContours", lambda *_args: ([first, second], None)
    )

    status, confidence, completeness, quad = geometry._detect_page_border(
        np.full((1000, 1000), 255, dtype=np.uint8)
    )

    assert status == "partial"
    assert confidence > 0.8
    assert completeness == pytest.approx(0.49 / 0.78)
    np.testing.assert_array_equal(quad, first.reshape(4, 2))


def test_border_support_keeps_strongest_line_for_each_side(monkeypatch) -> None:
    lines = np.asarray(
        [
            [[10, 5, 80, 5]],
            [[5, 5, 95, 5]],
            [[5, 90, 75, 90]],
            [[95, 10, 95, 90]],
            [[5, 20, 5, 90]],
            [[30, 30, 60, 60]],
        ],
        dtype=np.int32,
    )
    monkeypatch.setattr(geometry.cv2, "HoughLinesP", lambda *_args, **_kwargs: lines)

    assert geometry._border_side_support(
        np.zeros((100, 100), dtype=np.uint8)
    ) == pytest.approx([0.9, 0.8, 0.7, 0.7])


@pytest.mark.parametrize("brightness", [0, 100, 255])
def test_no_usable_page_evidence_keeps_unknown_border_and_empty_focus(
    brightness,
) -> None:
    gray = np.full((12, 8), brightness, dtype=np.uint8)

    assert geometry._detect_page_border(gray)[:3] == (
        ("unknown", 0.25, 0.75) if brightness >= 210 else ("unknown", 0.0, 0.0)
    )
    focus = metrics._focus_report(gray)
    assert focus["map"] == []
    assert focus["ink_patch_count"] == 0
    assert focus["ink_coverage"] == 0.0
    assert focus["sharpness_score"] == 0.0
    assert focus["bad_patch_ratio"] == 1.0
    assert focus["blur_pattern"] == "defocus_or_mixed"
    illumination = metrics._illumination_report(gray)
    assert illumination["background_p10"] == float(brightness)
    assert illumination["background_p90"] == float(brightness)
    assert illumination["contrast_score"] == 0.0
    assert illumination["low_contrast_area_ratio"] == 1.0


def test_sparse_ink_counts_toward_blank_evidence_without_a_focus_patch() -> None:
    # 未达到局部清晰度的墨迹比例仍计入总墨迹；丢弃该像素会改变空白页证据。
    gray = np.full((1200, 800), 255, dtype=np.uint8)
    gray[0, 0] = 0

    focus = metrics._focus_report(gray)

    assert focus["map"] == []
    assert focus["ink_coverage"] == 0.000001


def _threshold_metrics() -> dict:
    return {
        "effective_short_edge_px": 1600,
        "sharpness_score": 0.42,
        "bad_focus_patch_ratio": 0.15,
        "blur_pattern": "not_detected",
        "blank_probability": 0.97,
        "border_completeness_score": 0.98,
        "perspective_risk_score": 0.15,
        "exposure_quality_score": 0.45,
        "shadow_risk_score": 0.48,
        "low_contrast_area_ratio": 0.15,
        "glare_risk_score": 0.4,
        "occlusion_confidence": 0.6,
        "occlusion_risk_score": 0.55,
        "noise_risk_score": 0.72,
        "compression_risk_score": 0.72,
    }


def _threshold_geometry() -> dict:
    return {
        "page_border_confidence": 0.5,
        "perspective_confidence": 0.4,
        "detected_skew_angle": 7.0,
        "skew_confidence": 0.45,
        "coarse_orientation": "portrait_or_square",
    }


def test_review_threshold_equalities_do_not_create_issues() -> None:
    assert policy._issues(_threshold_metrics(), _threshold_geometry(), []) == []


def test_multiple_quality_issues_keep_order_and_hard_gate_severity() -> None:
    values = _threshold_metrics() | {
        "effective_short_edge_px": 1199,
        "sharpness_score": 0.1,
        "bad_focus_patch_ratio": 0.6,
        "blank_probability": 0.98,
        "border_completeness_score": 0.5,
        "perspective_risk_score": 0.16,
        "exposure_quality_score": 0.44,
        "shadow_risk_score": 0.49,
        "low_contrast_area_ratio": 0.16,
        "glare_risk_score": 0.41,
        "occlusion_risk_score": 0.56,
        "compression_risk_score": 0.73,
    }
    shape = _threshold_geometry() | {
        "detected_skew_angle": -7.1,
        "coarse_orientation": "landscape",
    }
    gates = [
        {"code": code, "status": "failed"}
        for code in ("low_effective_resolution", "severe_blur", "incomplete_page")
    ]

    issues = policy._issues(values, shape, gates)

    assert [item["code"] for item in issues] == [
        "low_effective_resolution",
        "low_sharpness",
        "blank_page",
        "incomplete_page_border",
        "perspective_risk",
        "residual_skew",
        "large_rotation_risk",
        "bad_exposure",
        "shadow_risk",
        "low_local_contrast",
        "glare_risk",
        "occlusion_risk",
        "noise_or_compression",
    ]
    assert [item["severity"] for item in issues[:4]] == [
        "failed",
        "failed",
        "review",
        "failed",
    ]
    assert issues[1]["metric"] == "sharpness_score"
    assert issues[1]["parameters"] == {
        "window": "local_focus_map",
        "blur_pattern": "not_detected",
    }
    assert all(item["severity"] == "review" for item in issues[4:])
    assert policy._decision(100.0, issues, gates, ["limited_deskew"]) == (
        "REJECT",
        "failed",
    )
