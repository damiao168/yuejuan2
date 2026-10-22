"""真实选项像素经过局部替换流水线；合成图和固定 OCR 不代表真实模型识别率。"""

import copy

import cv2
import numpy as np
import pytest
from grading_agent.template_analysis import _apply_repair_batches, _normalize
from grading_agent.template_analysis_objective_items import review_objective_items
from grading_agent.template_analysis_option_alignment import OPTION_ALIGNMENT_VERSION
from grading_agent.template_analysis_semantics import LocalContentEvidence


def box(x, y, width, height):
    return {"x": x, "y": y, "width": width, "height": height}


def fixture(*, blank=False):
    image = np.full((1000, 1000, 3), 255, np.uint8)
    if not blank:
        for top in (300, 400):
            for left in (200, 250, 300, 350):
                cv2.rectangle(image, (left, top), (left + 28, top + 18), (0, 0, 0), 2)
    page = {"page_no": 1, "width": 1000, "height": 1000, "sha256": "a" * 64}
    payload = {"request_id": "option-pixel-flow-fixture", "page": page, "writing_state": "blank",
               "question_labels": ["1", "2", "12"], "objective_question_labels": ["1", "2"],
               "fill_blank_question_labels": ["12"]}
    tokens = [{"text": "选择题", **box(.15, .22, .1, .02)},
              {"text": "1", **box(.16, .3, .02, .02)},
              {"text": "2", **box(.16, .4, .02, .02)}]
    return image, payload, tokens


def candidate(role, bbox, *, label=None):
    return {"candidate_id": None, "page_no": 1, "role": role, "proposed_bbox": bbox,
            "printed_question_label": label, "identity_label": None,
            "association_status": "associated" if label else "unassociated", "reason_codes": [],
            "evidence_refs": [], "observed_question_labels": [label] if label else [], "objective_items": [],
            "visual_evidence": {"static_label": "填空题" if label else None, "option_pattern": "none",
                                "identity_pattern": "none", "barcode_pattern": "none",
                                "boundary_basis": "printed_frame", "anchor_bbox": None}}


def model_choice():
    group = candidate("objective_group", box(.12, .2, .4, .3))
    group["observed_question_labels"] = ["1", "2"]
    group["visual_evidence"].update(static_label="选择题", option_pattern="letter_options")
    # 模型题号及行拓扑正确，但各题父框漏掉后两项；不能靠裁父框留白修复。
    group["objective_items"] = [
        {"printed_question_label": label, "bbox": box(.15, top - .01, .17, .05),
         "options": [{"label": letter, "bbox": box(.24 + index * .05, top + .003, .028, .018)}
                     for index, letter in enumerate("ABCD")]}
        for label, top in (("1", .3), ("2", .4))]
    return group


def apply(image, payload, tokens, raw):
    evidence = LocalContentEvidence(image, payload["page"], ocr=lambda _image: (copy.deepcopy(tokens), None))
    existing = _normalize([candidate("uncertain", box(.12, .2, .4, .3)),
                           candidate("answer", box(.62, .2, .25, .5), label="12")], {}, payload)
    evidence.review(existing, payload)
    before = copy.deepcopy(existing)
    batch = {"task": "objective_region", "replace_candidate_ids": [existing[0]["candidate_id"]],
             "crop_bbox": box(.1, .18, .5, .36), "candidates": [raw]}
    original_batch = copy.deepcopy(batch)
    applications = _apply_repair_batches(existing, [batch], payload, [], payload["page"], evidence)
    assert batch == original_batch
    return existing, before, applications


def test_option_pixels_fix_model_geometry_before_normalization_and_atomically_replace_group():
    image, payload, tokens = fixture()
    raw = model_choice()
    assert review_objective_items(raw, payload) == ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]
    assert _normalize([copy.deepcopy(raw)], {}, payload)[0]["semantic_check"] == "needs_review"

    existing, before, applications = apply(image, payload, tokens, raw)

    assert applications == [{"task": "objective_region", "applied": True, "candidate_count": 1,
                             "option_alignment": {"matched_items": 2, "verified_anchors": 2}}]
    assert existing[0] == before[1]
    group = existing[1]
    assert group["role"] == "objective_group" and group["semantic_check"] == "supported"
    assert {"LOCAL_OBJECTIVE_OPTION_PIXELS_ALIGNED", "LOCAL_MODEL_REPAIR_APPLIED",
            "SEMANTIC_OPTION_PATTERN_VERIFIED"} <= set(group["reason_codes"])
    assert review_objective_items(group, payload, check_observed=False) == []
    assert f"local-options:{OPTION_ALIGNMENT_VERSION}:{payload['page']['sha256'][:16]}:1" in group["evidence_refs"]
    assert [item["printed_question_label"] for item in group["objective_items"]] == ["1", "2"]
    for item, top in zip(group["objective_items"], (.299, .399), strict=True):
        for option, left in zip(item["options"], (.199, .249, .299, .349), strict=True):
            assert option["bbox"] == pytest.approx(box(left, top, .031, .021))
    assert all(candidate["candidate_id"] != before[0]["candidate_id"] for candidate in existing)


def test_conflicting_independent_question_anchor_cannot_replace_or_split_old_group():
    image, payload, tokens = fixture()
    tokens[1]["text"], tokens[2]["text"] = tokens[2]["text"], tokens[1]["text"]

    existing, before, applications = apply(image, payload, tokens, model_choice())

    assert existing == before
    assert applications[0]["applied"] is False
    assert applications[0]["reason"] == "REPAIR_CONTENT_UNRESOLVED"
    assert "option_alignment" not in applications[0]


@pytest.mark.parametrize("missing", ["pixels", "question_anchors"])
def test_missing_pixels_or_independent_anchors_keeps_geometry_rejection_and_old_group(missing):
    image, payload, tokens = fixture(blank=missing == "pixels")
    if missing == "question_anchors":
        tokens = tokens[:1]

    existing, before, applications = apply(image, payload, tokens, model_choice())

    assert existing == before
    assert applications[0]["applied"] is False
    assert applications[0]["reason"] == "REPAIR_CONTENT_UNRESOLVED"
    assert "option_alignment" not in applications[0]


def test_deferred_crop_options_require_pixel_alignment_before_they_can_replace_old_group():
    image, payload, tokens = fixture()
    raw = model_choice()
    # 已仿射还原的选项仍在整页内，但越出本次裁图。服务端标记必须由独立测量消除。
    raw["_requires_option_alignment"] = True
    for item in raw["objective_items"]:
        item["options"][-1]["bbox"]["x"] = .61

    existing, _, applications = apply(image, payload, tokens, raw)

    assert applications[0]["applied"] is True
    assert applications[0]["option_alignment"]["matched_items"] == 2
    assert existing[1]["semantic_check"] == "supported"
    assert "_requires_option_alignment" not in existing[1]
    assert all(item["options"][-1]["bbox"]["x"] == pytest.approx(.349)
               for item in existing[1]["objective_items"])


@pytest.mark.parametrize("missing", ["pixels", "question_anchors"])
def test_deferred_crop_options_cannot_pass_without_independent_alignment(missing):
    image, payload, tokens = fixture(blank=missing == "pixels")
    if missing == "question_anchors":
        tokens = tokens[:1]
    raw = model_choice()
    raw["_requires_option_alignment"] = True
    for item in raw["objective_items"]:
        item["options"][-1]["bbox"]["x"] = .61

    existing, before, applications = apply(image, payload, tokens, raw)

    assert existing == before
    assert applications == [{"task": "objective_region", "applied": False, "reason": "REPAIR_RESULT_INVALID"}]


@pytest.mark.parametrize("invalid_kind", ["non_objective", "missing_items"])
def test_deferred_marker_cannot_skip_alignment_for_other_roles_or_missing_items(invalid_kind):
    image, payload, tokens = fixture()
    raw = model_choice()
    raw["_requires_option_alignment"] = True
    if invalid_kind == "non_objective":
        raw["role"] = "instruction"
    else:
        raw["objective_items"] = []

    existing, before, applications = apply(image, payload, tokens, raw)

    assert existing == before
    assert applications == [{"task": "objective_region", "applied": False, "reason": "REPAIR_RESULT_INVALID"}]
