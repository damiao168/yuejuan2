from __future__ import annotations

import io

import cv2
import numpy as np
import pytest
from PIL import Image

from page_processing.omr import OMRProfile, extract_marks
from page_processing.omr_acceptance import OPTION_REGIONS, SyntheticOMRCase, render_synthetic_case


def printed_sheet(marked: tuple[str, ...] = (), *, scale: int = 1, gray_mark: str = "", red_annotation: bool = False) -> tuple[bytes, list[dict]]:
    image = np.full((54, 270, 3), 255, dtype=np.uint8)
    regions = []
    for label, x in zip("ABCD", (15, 75, 135, 195), strict=True):
        regions.append({"label": label, "x": x, "y": 16, "width": 46, "height": 22})
        cv2.putText(image, f"[{label}]", (x + 2, 33), cv2.FONT_HERSHEY_SIMPLEX, 0.6, (0, 0, 0), 1, cv2.LINE_AA)
        if label in marked or label == gray_mark:
            ink = 200 if label == gray_mark else 0
            cv2.rectangle(image, (x + 2, 18), (x + 43, 35), (ink, ink, ink), -1)
    if red_annotation:
        cv2.putText(image, "A", (203, 36), cv2.FONT_HERSHEY_SIMPLEX, 0.9, (255, 0, 0), 3, cv2.LINE_AA)
    output = io.BytesIO()
    Image.fromarray(image).resize((270 * scale, 54 * scale), Image.Resampling.NEAREST).save(output, "PNG")
    return output.getvalue(), [
        {key: value * scale if key != "label" else value for key, value in region.items()}
        for region in regions
    ]


@pytest.mark.parametrize("scale", [1, 2, 3])
@pytest.mark.parametrize("marked,multiple", [((), False), (("C",), False), (("A", "D"), True)])
def test_printed_letters_and_brackets_are_not_marks_at_different_sizes(scale: int, marked: tuple[str, ...], multiple: bool) -> None:
    source, regions = printed_sheet(marked, scale=scale)
    result = extract_marks(source, regions, multiple=multiple)
    assert result["selected"] == list(marked)
    assert result["decision"] == ("selected" if marked else "blank")
    assert result["needs_human_review"] is False
    assert result["thresholds"]["measurement_method"] == "solid-core-v1"
    # 总墨量仍展示印刷信息；筛选依赖实心内芯，不能靠把旧阈值从 0.18 一律调高。
    printed = [item for item in result["measurements"] if item["label"] not in marked]
    assert all(item["source_fill_ratio"] > item["decision_ratio"] for item in printed)
    assert all(item["decision_ratio"] < 0.10 for item in printed)


def test_printed_blank_reproduces_old_total_ink_false_positive() -> None:
    source, regions = printed_sheet()
    result = extract_marks(source, regions)
    assert all(item["fill_ratio"] >= 0.18 for item in result["measurements"])
    assert result["decision"] == "blank"


def test_small_compact_mark_with_printed_letter_routes_to_review() -> None:
    case = SyntheticOMRCase("printed-light", "single_choice", ("A",), False, True, "light")
    result = extract_marks(render_synthetic_case(case), OPTION_REGIONS)
    assert result["decision"] == "ambiguous"
    assert result["needs_human_review"] is True
    assert result["measurements"][0]["state"] == "uncertain"


def test_broad_gray_erasure_with_other_selected_mark_routes_to_review() -> None:
    source, regions = printed_sheet(("C",), gray_mark="A")
    result = extract_marks(source, regions, multiple=True)
    assert result["selected"] == ["C"]
    assert result["needs_human_review"] is True
    assert result["thresholds"]["quality"]["classification"] == "possible_erasure"
    assert result["measurements"][0]["gray_core_ratio"] > 0.12


def test_red_annotation_is_excluded_without_forcing_review_of_clear_black_marks() -> None:
    source, regions = printed_sheet(("B", "C"), red_annotation=True)
    result = extract_marks(source, regions, multiple=True)
    assert result["selected"] == ["B", "C"]
    assert result["needs_human_review"] is False
    assert result["thresholds"]["quality"]["classification"] == "clear"
    assert result["thresholds"]["ink_color_policy"] == "black-only-v1"
    assert result["measurements"][3]["red_annotation_ratio"] > 0.01


@pytest.mark.parametrize("ink", [(255, 0, 0), (0, 0, 200), (0, 160, 0), (160, 0, 160), (100, 5, 5)])
def test_colored_solid_fills_do_not_become_black_choices(ink: tuple[int, int, int]) -> None:
    source, regions = printed_sheet(("B",))
    image = np.array(Image.open(io.BytesIO(source)).convert("RGB"))
    cv2.rectangle(image, (137, 18), (178, 35), ink, -1)
    output = io.BytesIO()
    Image.fromarray(image).save(output, "PNG")
    result = extract_marks(output.getvalue(), regions, multiple=True)
    assert result["selected"] == ["B"]
    assert result["needs_human_review"] is False
    assert result["measurements"][2]["state"] == "clear"


def test_neutral_dark_pencil_remains_a_valid_black_mark() -> None:
    source, regions = printed_sheet()
    image = np.array(Image.open(io.BytesIO(source)).convert("RGB"))
    cv2.rectangle(image, (137, 18), (178, 35), (70, 68, 66), -1)
    output = io.BytesIO()
    Image.fromarray(image).save(output, "PNG")
    result = extract_marks(output.getvalue(), regions)
    assert result["selected"] == ["C"]
    assert result["needs_human_review"] is False


def test_template_difference_also_ignores_red_annotations() -> None:
    source, regions = printed_sheet(("B", "C"), red_annotation=True)
    reference, _ = printed_sheet()
    result = extract_marks(source, regions, multiple=True,
        profile=OMRProfile(mode="template_difference", version="opencv-template-difference-bubble-v1"),
        reference_image_bytes=reference, reference_content_type="image/png",
        reference_page_width=270, reference_page_height=54,
        reference_question_region={"x": 0, "y": 0, "width": 1, "height": 1})
    assert result["selected"] == ["B", "C"]
    assert result["needs_human_review"] is False
    assert result["thresholds"]["ink_color_policy"] == "black-only-v1"
