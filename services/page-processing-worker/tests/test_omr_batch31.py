import io

import numpy as np
import pytest
from page_processing import omr
from PIL import Image, ImageDraw


def controlled_marks(monkeypatch: pytest.MonkeyPatch, counts: dict[str, int], profile: omr.OMRProfile, *, order: tuple[str, ...] = ("A", "B"), multiple: bool = False) -> dict:
    with Image.new("L", (120, 60), "white") as image:
        output = io.BytesIO()
        image.save(output, "PNG")
    mask = np.zeros((60, 120), dtype=np.uint8)
    positions = {"A": 0, "B": 60}
    for label, x in positions.items():
        mask[1:49, x + 1:x + 49].flat[:counts.get(label, 0)] = 255
    regions = [{"label": label, "x": positions[label], "y": 0, "width": 50, "height": 50} for label in order]
    # 固定前景掩码只隔离阈值化噪声；公开入口仍执行原坐标裁剪、测量和判定，保护取整边界。
    monkeypatch.setattr(omr, "_foreground_mask", lambda _image: mask.copy())
    return omr.extract_marks(output.getvalue(), regions, profile=profile, multiple=multiple)


def test_marked_threshold_uses_unrounded_fill_ratio(monkeypatch: pytest.MonkeyPatch) -> None:
    profile = omr.OMRProfile(marked_threshold=0.1002602, ambiguous_threshold=0.04, minimum_margin=0.01, border_fraction=0)
    result = controlled_marks(monkeypatch, {"A": 231}, profile)
    assert result["measurements"][0]["fill_ratio"] == 0.10026
    assert result["measurements"][0]["marked"] is True
    assert result["decision"] == "selected"
    assert result["selected"] == ["A"]


def test_single_choice_margin_uses_rounded_measurements(monkeypatch: pytest.MonkeyPatch) -> None:
    profile = omr.OMRProfile(marked_threshold=0.2, ambiguous_threshold=0.1, minimum_margin=0.0434029, border_fraction=0)
    result = controlled_marks(monkeypatch, {"A": 500, "B": 400}, profile)
    assert [item["fill_ratio"] for item in result["measurements"]] == [0.217014, 0.173611]
    assert result["decision"] == "selected"
    assert result["selected"] == ["A"]


def test_multiple_choice_preserves_region_order_despite_strength_sorting(monkeypatch: pytest.MonkeyPatch) -> None:
    profile = omr.OMRProfile(marked_threshold=0.1, ambiguous_threshold=0.04, border_fraction=0)
    result = controlled_marks(monkeypatch, {"A": 500, "B": 400}, profile, order=("B", "A"), multiple=True)
    assert [item["label"] for item in result["measurements"]] == ["B", "A"]
    assert result["selected"] == ["B", "A"]
    assert result["decision"] == "selected"
    assert result["overlay_png"].startswith(b"\x89PNG")


def reference_sheet(marked: str | None) -> bytes:
    with Image.new("RGB", (160, 70), "white") as image:
        draw = ImageDraw.Draw(image)
        for label, x in (("A", 20), ("B", 70), ("C", 120)):
            draw.rectangle((x, 20, x + 28, 48), outline="black", width=2)
            if label == "A":
                draw.rectangle((x + 7, 27, x + 21, 41), fill="black")
            if label == marked:
                draw.ellipse((x + 6, 26, x + 22, 42), fill="black")
        output = io.BytesIO()
        image.save(output, "PNG")
    return output.getvalue()


def test_template_difference_keeps_measurement_fields_and_overlay() -> None:
    regions = [{"label": label, "x": x, "y": 20, "width": 28, "height": 28} for label, x in (("A", 20), ("B", 70), ("C", 120))]
    profile = omr.OMRProfile(mode="template_difference", version="opencv-template-difference-bubble-v1", marked_threshold=0.12, ambiguous_threshold=0.04, minimum_margin=0.04, reference_mask_dilation_pixels=1)
    result = omr.extract_marks(reference_sheet("B"), regions, profile=profile, reference_image_bytes=reference_sheet(None), reference_content_type="image/png", reference_question_region={"x": 0, "y": 0, "width": 1, "height": 1})
    assert result["selected"] == ["B"]
    assert result["decision"] == "selected"
    assert result["needs_human_review"] is False
    assert result["measurements"][0]["source_fill_ratio"] > 0
    assert result["measurements"][0]["foreground_delta"] == 0
    assert result["measurements"][0]["reference_fill_ratio"] > 0
    assert result["thresholds"]["reference_mask_dilation_pixels"] == 1
    with Image.open(io.BytesIO(result["overlay_png"])) as overlay:
        assert overlay.size == (160, 70)
