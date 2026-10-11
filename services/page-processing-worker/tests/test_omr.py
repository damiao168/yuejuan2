from __future__ import annotations

import io

import pytest
from page_processing.omr import OMRExtractionError, OMRProfile, extract_marks
from PIL import Image, ImageDraw

REGIONS = [
    {"label": "A", "x": 20, "y": 20, "width": 30, "height": 30},
    {"label": "B", "x": 70, "y": 20, "width": 30, "height": 30},
    {"label": "C", "x": 120, "y": 20, "width": 30, "height": 30},
]


def sheet(*marked: str) -> bytes:
    image = Image.new("RGB", (180, 70), "white")
    draw = ImageDraw.Draw(image)
    for region in REGIONS:
        box = (region["x"], region["y"], region["x"] + region["width"], region["y"] + region["height"])
        draw.rectangle(box, outline="black", width=2)
        if region["label"] in marked:
            draw.ellipse((box[0] + 6, box[1] + 6, box[2] - 6, box[3] - 6), fill="black")
    output = io.BytesIO()
    image.save(output, format="PNG")
    return output.getvalue()


def test_extracts_one_clear_mark() -> None:
    result = extract_marks(sheet("B"), REGIONS)
    assert result["decision"] == "selected"
    assert result["selected"] == ["B"]
    assert result["needs_human_review"] is False
    assert result["confidence"] >= 0.8
    assert result["overlay_png"].startswith(b"\x89PNG")


def test_blank_and_multiple_route_to_review() -> None:
    blank = extract_marks(sheet(), REGIONS)
    assert blank["decision"] == "blank"
    assert blank["needs_human_review"] is True

    multiple = extract_marks(sheet("A", "C"), REGIONS)
    assert multiple["decision"] == "multiple"
    assert multiple["needs_human_review"] is True


def test_multiple_choice_preserves_clear_set() -> None:
    result = extract_marks(sheet("A", "C"), REGIONS, multiple=True)
    assert result["decision"] == "selected"
    assert result["selected"] == ["A", "C"]


def test_rejects_missing_or_unsafe_regions() -> None:
    try:
        extract_marks(sheet("A"), [])
        raise AssertionError("missing regions must fail")
    except OMRExtractionError as exc:
        assert str(exc) == "omr_option_regions_missing"

    try:
        extract_marks(sheet("A"), [{"label": "A", "x": -1, "y": 0, "width": 5, "height": 5}])
        raise AssertionError("unsafe region must fail")
    except OMRExtractionError as exc:
        assert str(exc) == "omr_option_region_out_of_bounds"


def test_rejects_non_finite_regions_and_profile_thresholds() -> None:
    image_bytes = sheet('A')
    invalid_value = float('nan')
    with pytest.raises(OMRExtractionError, match='omr_option_region_invalid'):
        extract_marks(image_bytes, [{'label': 'A', 'x': invalid_value, 'y': 0, 'width': 10, 'height': 10}])

    image_bytes = sheet('A')
    profile = OMRProfile(marked_threshold=float('nan'))
    with pytest.raises(OMRExtractionError, match='omr_profile_threshold_invalid'):
        extract_marks(image_bytes, REGIONS, profile=profile)

    image_bytes = sheet('A')
    profile = OMRProfile(border_fraction=0.5)
    with pytest.raises(OMRExtractionError, match='omr_profile_threshold_invalid'):
        extract_marks(image_bytes, REGIONS, profile=profile)

    image_bytes = sheet('A')
    with pytest.raises(OMRExtractionError, match='omr_multiple_flag_invalid'):
        extract_marks(image_bytes, REGIONS, multiple='false')

    image_bytes = sheet('A')
    with pytest.raises(OMRExtractionError, match='omr_option_region_invalid'):
        extract_marks(image_bytes, [{'label': 'A', 'x': True, 'y': 0, 'width': 10, 'height': 10}])


def test_rejects_unsafe_reference_dimensions_before_resize() -> None:
    profile = OMRProfile(
        mode="template_difference",
        version="opencv-template-difference-bubble-v1",
    )
    reference_region = {"x": 0, "y": 0, "width": 1, "height": 1}
    image_bytes = sheet('A')
    image_bytes_2 = sheet()
    with pytest.raises(OMRExtractionError, match='omr_reference_dimensions_invalid'):
        extract_marks(image_bytes, REGIONS, profile=profile, reference_image_bytes=image_bytes_2, reference_content_type='image/png', reference_page_width=100000, reference_page_height=100000, reference_question_region=reference_region)
