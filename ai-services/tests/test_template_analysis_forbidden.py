import copy
import json
from types import SimpleNamespace

import cv2
import numpy as np
import pytest
from grading_agent.template_analysis_forbidden import (
    FORBIDDEN_REGION_VERSION,
    _label_verified,
    review_forbidden_regions,
)


def candidate(**updates):
    row = {"role": "uncertain", "printed_question_label": None, "identity_label": None,
           "association_status": "unassociated", "semantic_check": "needs_review", "origin": "model",
           "proposed_bbox": {"x": .1, "y": .2, "width": .12, "height": .16},
           "reason_codes": ["MODEL_PROPOSED_IDENTITY", "SEMANTIC_EVIDENCE_INSUFFICIENT"], "evidence_refs": []}
    return {**row, **updates}


def evidence(layout="square"):
    # 合成单字框保留实测标牌的“禁作 / 止答”双列结构；它们不是从块框均分的真实 OCR 数据。
    if layout == "square":
        points, size = [(.1, .1), (.6, .1), (.1, .6), (.6, .6)], (.3, .3)
    elif layout == "horizontal":
        points, size = [(.05 + index * .24, .2) for index in range(4)], (.18, .6)
    else:
        points, size = [(.2, .05 + index * .24) for index in range(4)], (.6, .18)
    chars = [{"character": char, "bbox": {"x": x, "y": y, "width": size[0], "height": size[1]},
              "confidence": .99834, "confidence_scope": "block"} for char, (x, y) in zip("禁止作答", points)]
    # 输入数组顺序故意与阅读顺序不同；分类必须按真实字框定位。
    return {"status": "ok", "reason_code": None, "characters": [chars[0], chars[2], chars[1], chars[3]],
            "recognized_character_count": 4, "recognized_block_count": 2}


def run(rows=None, result=None, *, reader=None, clock=None):
    rows = [candidate()] if rows is None else rows
    seen = []
    def analyze(data, *, timeout):
        seen.append(timeout)
        assert data.startswith(b"\x89PNG") and len(data) <= 512 * 1024
        crop = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR)
        assert max(crop.shape[:2]) <= 1000
        return copy.deepcopy(evidence() if result is None else result)
    reasons = review_forbidden_regions(np.full((1000, 1400, 3), 255, np.uint8),
                                       {"sha256": "a" * 64, "page_no": 1}, rows,
                                       {"question_labels": ["12", "13", "14", "15"]},
                                       ocr=reader or analyze, clock=clock)
    return rows, reasons, seen


def test_true_twocolumn_label_becomes_instruction_without_deleting_candidate():
    rows, reasons, calls = run()
    assert len(rows) == 1 and reasons == [] and calls == [3]
    row = rows[0]
    assert row["role"] == "instruction" and row["semantic_check"] == "supported"
    assert row["association_status"] == "unassociated" and row["printed_question_label"] is None
    assert row["origin"] == "model" and row["identity_label"] is None
    assert row["reason_codes"][-1] == "LOCAL_FORBIDDEN_LABEL_VERIFIED"
    assert FORBIDDEN_REGION_VERSION in row["evidence_refs"][0]


@pytest.mark.parametrize(("layout", "shape"), [("square", (200, 200, 3)),
                                             ("horizontal", (80, 320, 3)), ("vertical", (320, 80, 3))])
def test_supported_layouts_use_pixel_aspect_for_true_character_geometry(layout, shape):
    assert _label_verified(evidence(layout), shape)


def test_two_column_block_concatenation_cannot_override_wrong_character_locations():
    result = evidence()
    first, second = result["characters"][1], result["characters"][2]
    first["bbox"], second["bbox"] = second["bbox"], first["bbox"]
    rows, reasons, _ = run(result=result)
    assert rows[0]["role"] == "uncertain" and reasons == []


@pytest.mark.parametrize("mutation", [
    lambda result: result["characters"].pop(),
    lambda result: result["characters"][1].update(character="禁"),
    lambda result: result["characters"][1].update(character=None),
    lambda result: result["characters"][1].update(character=["作"]),
    lambda result: result["characters"][1].update(confidence=.94),
    lambda result: result["characters"][1].update(confidence=float("nan")),
    lambda result: result["characters"][1].update(confidence_scope="character"),
    lambda result: result.update(recognized_character_count=5),
    lambda result: result.update(recognized_character_count=4.0),
    lambda result: result.update(recognized_block_count=0),
    lambda result: result.update(recognized_block_count=True),
    lambda result: result["characters"][1]["bbox"].update(x=-.1),
    lambda result: result["characters"][1]["bbox"].update(x=.9),
    lambda result: result["characters"][1]["bbox"].update(width=0),
    lambda result: result["characters"][1]["bbox"].update(height=float("inf")),
])
def test_incomplete_low_quality_extra_body_and_malformed_evidence_keep_unknown(mutation):
    result = evidence()
    mutation(result)
    before = candidate()
    rows, reasons, _ = run([copy.deepcopy(before)], result)
    assert rows == [before] and reasons == []


@pytest.mark.parametrize("replacement", [
    {"x": .1, "y": .1, "width": .04, "height": .04},
    {"x": .1, "y": .1, "width": .3, "height": .01},
])
def test_tiny_corner_warning_or_unreasonable_font_shape_does_not_hide_whole_region(replacement):
    result = evidence()
    for item in result["characters"]:
        item["bbox"] = copy.deepcopy(replacement)
    rows, reasons, _ = run(result=result)
    assert rows[0]["role"] == "uncertain" and reasons == []


def test_corner_warning_is_rejected_even_when_its_character_layout_is_valid():
    result = evidence()
    for item in result["characters"]:
        item["bbox"] = {key: value * .25 for key, value in item["bbox"].items()}
    assert not _label_verified(result, (200, 200, 3))


def test_different_characters_cannot_reuse_one_block_bounding_box_as_word_boxes():
    result = evidence()
    result["characters"][1]["bbox"] = copy.deepcopy(result["characters"][0]["bbox"])
    assert not _label_verified(result, (200, 200, 3))


@pytest.mark.parametrize("updates", [
    {"printed_question_label": "15"},
    {"printed_question_label": "999"},
    {"association_status": "associated"},
    {"association_status": "ambiguous"},
    {"role": "answer"},
    {"role": "objective_group"},
    {"role": "identity"},
    {"proposed_bbox": {"x": .1, "y": .1, "width": .7, "height": .7}},
    {"proposed_bbox": {"x": .1, "y": .1, "width": .5, "height": .05}},
    {"proposed_bbox": {"x": .9, "y": .1, "width": .2, "height": .1}},
    {"proposed_bbox": {"x": .1, "y": .1, "width": .1, "height": float("nan")}},
])
def test_question_regions_mixed_columns_and_invalid_boxes_are_not_sent_to_ocr(updates):
    before = candidate(**updates)
    rows, reasons, calls = run([copy.deepcopy(before)])
    assert json.dumps(rows) == json.dumps([before]) and reasons == [] and calls == []


def test_runtime_object_protocol_is_supported():
    runtime = SimpleNamespace(analyze_crop=lambda data, timeout: evidence())
    rows, reasons, _ = run(reader=runtime)
    assert rows[0]["role"] == "instruction" and reasons == []


@pytest.mark.parametrize(("code", "expected"), [
    ("TEMPLATE_STATIC_OCR_DISABLED", []),
    ("TEMPLATE_STATIC_OCR_WARMING", ["LOCAL_FORBIDDEN_OCR_WARMING"]),
    ("TEMPLATE_STATIC_OCR_TIMEOUT", ["LOCAL_FORBIDDEN_OCR_TIMEOUT"]),
    ("TEMPLATE_STATIC_OCR_UNAVAILABLE", ["LOCAL_FORBIDDEN_OCR_UNAVAILABLE"]),
    ("PRIVATE_STUDENT_TEXT", ["LOCAL_FORBIDDEN_OCR_UNAVAILABLE"]),
])
def test_runtime_unavailable_keeps_candidates_and_only_returns_fixed_reasons(code, expected):
    result = {"status": "unavailable", "reason_code": code}
    before = [candidate(), candidate()]
    rows, reasons, calls = run(copy.deepcopy(before), result)
    assert rows == before and reasons == expected and calls == [3]
    assert "PRIVATE" not in json.dumps(reasons)


def test_runtime_failure_does_not_emit_exception_text():
    def fail(data, *, timeout):
        raise OSError("PRIVATE_STUDENT_TEXT")
    rows, reasons, _ = run(reader=fail)
    assert rows[0]["role"] == "uncertain" and reasons == ["LOCAL_FORBIDDEN_OCR_FAILED"]


def test_region_count_is_bounded_even_with_nonadvancing_clock():
    rows, reasons, calls = run([candidate() for _ in range(6)], clock=lambda: 0)
    assert len(calls) == 4 and reasons == ["LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED"]
    assert [row["role"] for row in rows] == ["instruction"] * 4 + ["uncertain"] * 2


def test_page_budget_reduces_each_runtime_timeout_and_drops_late_result():
    times = iter([0, 0, 0, 2.8, 2.8, 2.8, 5.6, 5.6, 5.6, 6.01])
    rows, reasons, calls = run([candidate() for _ in range(4)], clock=times.__next__)
    assert calls == [3, 3, pytest.approx(.4)]
    assert [row["role"] for row in rows] == ["instruction", "instruction", "uncertain", "uncertain"]
    assert reasons == ["LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED"]


def test_budget_exhausted_before_first_crop_does_not_call_runtime():
    rows, reasons, calls = run(clock=iter([0, 6.01]).__next__)
    assert rows[0]["role"] == "uncertain" and calls == []
    assert reasons == ["LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED"]
