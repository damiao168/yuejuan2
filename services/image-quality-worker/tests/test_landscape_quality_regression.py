import io

import pytest
from PIL import Image, ImageDraw, ImageFilter

from image_quality.engine import analyze_and_normalize


def _landscape_answer_sheet(*, margin_rules: bool = False) -> Image.Image:
    image = Image.new("RGB", (3304, 2338), "white")
    draw = ImageDraw.Draw(image)
    for column in range(3):
        left = 90 + column * 1090
        for row in range(18):
            top = 130 + row * 115
            draw.line((left, top, left + 900, top), fill="black", width=5)
            draw.line((left, top + 36, left + 450, top + 36), fill="black", width=4)
            draw.rectangle((left + 930, top, left + 955, top + 25), outline="black", width=4)
    if margin_rules:
        # 三段不相连的印刷线能抬高纸边置信度，却没有提供纸张外框或答题区缺失证据。
        draw.line((750, 70, 2550, 70), fill="black", width=5)
        draw.line((60, 350, 60, 1950), fill="black", width=5)
        draw.line((3240, 350, 3240, 1950), fill="black", width=5)
    return image


def _analyze(image: Image.Image):
    data = io.BytesIO()
    image.save(data, format="PNG")
    return analyze_and_normalize(data.getvalue())


@pytest.mark.parametrize("margin_rules", [False, True])
def test_clear_landscape_columns_continue_to_registration_without_manual_release(margin_rules):
    with _landscape_answer_sheet(margin_rules=margin_rules) as image:
        result = _analyze(image)

    report = result.quality_report
    assert result.quality_status == "passed"
    assert report["decision"] == "PASS"
    assert report["geometry"]["coarse_orientation"] == "landscape"
    assert report["geometry"]["detected_skew_angle"] == 0.0
    assert report["metrics"]["effective_short_edge_px"] == 2338
    assert result.quality_issues == []
    border = next(gate for gate in report["hard_gates"] if gate["code"] == "incomplete_page")
    assert border["status"] == "not_evaluated"
    assert border["observed"] == report["metrics"]["border_completeness_score"]
    assert border["parameters"]["reason"] == "template_registration_not_available_at_quality_stage"
    assert report["dimensions"]["completeness"]["status"] == "not_evaluated"
    assert report["roi_completeness"]["required_roi_visible_ratio"] is None
    if margin_rules:
        assert report["geometry"]["page_border_confidence"] >= 0.7
        assert border["observed"] < 0.75


@pytest.mark.parametrize("degradation,gate_code", [("blur", "severe_blur"), ("resolution", "low_effective_resolution")])
def test_landscape_still_rejects_unreadable_detail(degradation, gate_code):
    with _landscape_answer_sheet() as source:
        image = source.filter(ImageFilter.GaussianBlur(30)) if degradation == "blur" else source.resize((1000, 708))
    with image:
        result = _analyze(image)

    assert result.quality_status == "failed"
    assert result.quality_report["decision"] == "REJECT"
    assert any(gate["code"] == gate_code and gate["status"] == "failed" for gate in result.quality_report["hard_gates"])
