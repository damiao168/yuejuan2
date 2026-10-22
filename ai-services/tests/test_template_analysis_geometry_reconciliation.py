"""固定几何回归只验证父框修正与合并门禁，不代表真实模型识别验收。"""

import copy

import numpy as np
import pytest
from grading_agent.template_analysis import (
    _apply_repair_batches,
    _normalize,
    _review_region_conflicts,
)
from grading_agent.template_analysis_objective_items import (
    constrain_objective_item_parents,
    review_objective_items,
)
from grading_agent.template_analysis_semantics import LocalContentEvidence


def box(x, y, width, height):
    return {"x": x, "y": y, "width": width, "height": height}


def objective_row():
    # 脱敏真实响应中第 7、8 题父框右端为 .97，大区右端为 .945；选项右端仅为 .93。
    # 只允许去掉这段父框留白，不能把子选项一起缩放或移动。
    return {
        "role": "objective_group", "proposed_bbox": box(.055, .12, .89, .72),
        "observed_question_labels": ["7", "8"],
        "objective_items": [
            {"printed_question_label": label, "bbox": box(.7, top, .27, .06),
             "options": [{"label": option, "bbox": box(left, top, .04, .05)}
                         for option, left in zip("ABCD", (.74, .79, .84, .89))]}
            for label, top in (("7", .19), ("8", .26))
        ],
    }


def test_real_geometry_clips_only_parent_whitespace_and_preserves_every_option():
    row = objective_row()
    options = [copy.deepcopy(item["options"]) for item in row["objective_items"]]
    payload = {"objective_question_labels": ["7", "8"]}
    assert review_objective_items(row, payload) == ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]

    assert constrain_objective_item_parents(row) is True

    assert [item["options"] for item in row["objective_items"]] == options
    for item in row["objective_items"]:
        assert item["bbox"]["x"] == .7
        assert item["bbox"]["width"] == pytest.approx(.245)
        assert item["bbox"]["height"] == pytest.approx(.06)
    assert review_objective_items(row, payload) == []
    assert constrain_objective_item_parents(row) is False


@pytest.mark.parametrize("outside", ["group", "item", "page"])
def test_parent_clipping_cannot_rescue_an_option_outside_either_original_parent(outside):
    row = objective_row()
    option = row["objective_items"][0]["options"][-1]
    option["bbox"] = {
        "group": box(.93, .19, .03, .05),
        "item": box(.69, .19, .04, .05),
        "page": box(.98, .19, .04, .05),
    }[outside]
    original = copy.deepcopy(row["objective_items"][0])

    constrain_objective_item_parents(row)

    assert row["objective_items"][0] == original
    assert review_objective_items(row, {"objective_question_labels": ["7", "8"]}) == [
        "MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]


@pytest.mark.parametrize("invalid", ["duplicate_box", "intersecting_boxes", "duplicate_label"])
def test_valid_parent_intersection_does_not_make_invalid_options_acceptable(invalid):
    row = objective_row()
    options = row["objective_items"][0]["options"]
    if invalid == "duplicate_box":
        options[1]["bbox"] = copy.deepcopy(options[0]["bbox"])
    elif invalid == "intersecting_boxes":
        options[1]["bbox"]["x"] = .76
    else:
        options[1]["label"] = "A"
    before = copy.deepcopy(options)

    assert constrain_objective_item_parents(row) is True

    assert options == before
    expected = ("MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT" if invalid == "duplicate_label"
                else "MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT")
    assert review_objective_items(row, {"objective_question_labels": ["7", "8"]}) == [expected]


def raw_region(role, bbox, *, label=None, identity=None):
    return {
        "candidate_id": None, "page_no": 1, "role": role, "proposed_bbox": bbox,
        "printed_question_label": label, "identity_label": identity,
        "association_status": "associated" if label else "unassociated",
        "evidence_refs": [], "reason_codes": [], "objective_items": [],
        "observed_question_labels": [label] if label else [],
        "visual_evidence": {
            "static_label": identity or ("填空题" if label else None),
            "option_pattern": "none", "identity_pattern": "name_line" if identity else "none",
            "barcode_pattern": "none", "boundary_basis": "printed_frame",
            "anchor_bbox": box(bbox["x"] + .02, bbox["y"] + .02, .05, .03) if identity else None,
        },
    }


@pytest.fixture
def merge_context(monkeypatch):
    payload = {"request_id": "geometry-reconciliation-fixture", "writing_state": "blank",
               "page": {"page_no": 1, "width": 320, "height": 200, "sha256": "0" * 64},
               "question_labels": ["12", "13"], "objective_question_labels": [],
               "fill_blank_question_labels": ["12", "13"]}
    # 本组只验证合并前后的几何变化；关闭外部 OCR 和选项检测，仍执行真实规范化与冲突规则。
    monkeypatch.setattr("grading_agent.template_analysis_semantics.detect_option_groups", lambda *_args: ([], []))
    evidence = LocalContentEvidence(np.full((200, 320, 3), 255, np.uint8), payload["page"],
                                    ocr=lambda _image: ([], "SEMANTIC_OCR_UNAVAILABLE"),
                                    barcode=lambda _image: None)
    supplied = [raw_region("uncertain", box(.05, .1, .2, .15)),
                raw_region("answer", box(.55, .45, .4, .2), label="12"),
                raw_region("answer", box(.55, .60, .4, .2), label="13")]
    existing = _normalize(supplied, {}, payload)
    evidence.review(existing, payload)
    assert all(row["semantic_check"] == "supported" for row in existing[1:])
    return payload, evidence, existing


def repair(existing, fresh):
    return [{"task": "identity", "replace_candidate_ids": [existing[0]["candidate_id"]],
             "candidates": [fresh]}]


def test_unrelated_existing_fill_overlap_does_not_block_identity_repair(merge_context):
    payload, evidence, existing = merge_context
    kept = copy.deepcopy(existing[1:])
    fresh = raw_region("identity", box(.05, .1, .2, .15), identity="姓名")

    applications = _apply_repair_batches(existing, repair(existing, fresh), payload, [], payload["page"], evidence)

    assert applications == [{"task": "identity", "applied": True, "candidate_count": 1}]
    assert existing[:2] == kept
    assert existing[2]["role"] == "identity" and existing[2]["identity_label"] == "姓名"
    assert existing[2]["semantic_check"] == "supported"
    assert "LOCAL_MODEL_REPAIR_APPLIED" in existing[2]["reason_codes"]
    # 允许这一批无关修复，不等于清除旧冲突；后续全局检查仍须报告两个重叠填空区。
    checked = copy.deepcopy(existing)
    _review_region_conflicts(checked, payload["page"], semantic_completed=True)
    assert all(row["semantic_check"] == "needs_review" for row in checked[:2])
    assert checked[2]["semantic_check"] == "supported"


def test_fresh_identity_overlapping_kept_fill_is_rejected_even_with_baseline_overlap(merge_context):
    payload, evidence, existing = merge_context
    before = copy.deepcopy(existing)
    fresh = raw_region("identity", box(.52, .45, .3, .15), identity="姓名")

    applications = _apply_repair_batches(existing, repair(existing, fresh), payload, [], payload["page"], evidence)

    assert applications == [{"task": "identity", "applied": False,
                             "reason": "REPAIR_COMBINED_LAYOUT_CONFLICT"}]
    assert existing == before
