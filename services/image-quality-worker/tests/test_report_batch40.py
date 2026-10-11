import pytest
from image_quality.report import (
    METRIC_SCHEMA_VERSION,
    REPORT_SCHEMA_VERSION,
    QualityMeasurements,
    QualityReportDecision,
    build_quality_report,
)


@pytest.fixture
def report_values():
    measurements = QualityMeasurements(
        metrics={"sharpness_score": 0.42},
        focus={"map": [0.3]},
        illumination={"contrast_score": 0.8},
        occlusion={"status": "unknown"},
        degradation={"noise_risk_score": 0.1},
        dimensions={"resolution": "review"},
    )
    assessment = QualityReportDecision([], 72.5, "REVIEW", ["limited_deskew"])
    return {
        "pixel_width": 1600,
        "pixel_height": 2400,
        "source_format": "JPEG",
        "geometry": {"page_border_status": "partial"},
        "measurements": measurements,
        "assessment": assessment,
    }


@pytest.mark.parametrize("source_dpi", [None, 0, 300.0])
def test_report_preserves_measurement_references_and_dpi_presence(report_values, source_dpi):
    report = build_quality_report(source_dpi=source_dpi, **report_values)

    assert report["metric_schema_version"] == METRIC_SCHEMA_VERSION
    assert report["report_schema_version"] == REPORT_SCHEMA_VERSION
    assert (report["pixel_width"], report["pixel_height"]) == (1600, 2400)
    assert report["source_dpi"] == source_dpi
    assert report["source_dpi_method"] == (
        "missing_metadata" if source_dpi is None else "embedded_metadata"
    )
    assert report["render_dpi"] is None
    assert report["source_format"] == "JPEG"
    assert report["output_format"] == "image/png"
    assert report["normalized_color_mode"] == "RGB"
    assert report["geometry"] is report_values["geometry"]
    measurements = report_values["measurements"]
    for key, field in (
        ("metrics", "metrics"),
        ("focus", "focus"),
        ("illumination", "illumination"),
        ("occlusion_reflection", "occlusion"),
        ("noise_compression", "degradation"),
        ("dimensions", "dimensions"),
    ):
        assert report[key] is getattr(measurements, field)
    assert report["roi_completeness"]["status"] == "unavailable"
    assert report["roi_completeness"]["required_roi_visible_ratio"] is None
    assert report["predictor"]["calibration_status"] == "awaiting_real_answer_sheet_samples"


@pytest.mark.parametrize(
    "hard_gates,fast_gate_status",
    [([], "passed"), ([{"status": "passed"}, {"status": "failed"}], "failed")],
)
def test_report_fast_gate_does_not_replace_existing_quality_decision(
    report_values, hard_gates, fast_gate_status
):
    # 门槛汇总只改变 fast_gate 状态，不能在报告层重算已给出的 REVIEW 决策。
    assessment = QualityReportDecision(hard_gates, 72.5, "REVIEW", ["limited_deskew"])
    report_values["assessment"] = assessment

    report = build_quality_report(source_dpi=None, **report_values)

    assert report["hard_gates"] is hard_gates
    assert report["enhancements_applied"] is assessment.enhancements
    assert report["quality_score"] == 72.5
    assert report["decision"] == "REVIEW"
    assert report["stages"]["fast_gate"]["status"] == fast_gate_status
    assert report["stages"]["post_registration_roi_assessment"]["status"] == "awaiting_template_registration"
