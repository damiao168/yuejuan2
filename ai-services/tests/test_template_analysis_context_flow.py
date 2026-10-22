"""裁图说明去重经过真实合并门禁；固定 OCR 只验证流程，不代表模型识别率。"""

import copy

import numpy as np
import pytest
from grading_agent.template_analysis import _apply_repair_batches, _normalize
from grading_agent.template_analysis_context import CONTEXT_FILTER_VERSION
from grading_agent.template_analysis_semantics import LocalContentEvidence

PAGE = {"page_no": 1, "width": 1600, "height": 1132, "sha256": "a" * 64}
CROP = {"x": .048125, "y": .0980565371024735, "width": .308125, "height": .14752650176678445}
FRAME = {"x": .115, "y": .2252650176678445, "width": .235625, "height": .09982332155477032}
FRAGMENT = {"x": .122075, "y": .21902826855123675, "width": .160225, "height": .026554770318021198}
KNOWN = {"x": .0625, "y": .235, "width": .29, "height": .09}


def candidate(role, bbox, *, identity=None, question=None):
    static = identity if identity in {"姓名", "准考证号"} else "注意事项" if role == "instruction" else None
    pattern = {"姓名": "name_line", "准考证号": "number_grid"}.get(identity, "none")
    return {"candidate_id": None, "page_no": 1, "role": role, "proposed_bbox": copy.deepcopy(bbox),
            "printed_question_label": question, "identity_label": identity,
            "association_status": "associated" if question else "unassociated", "reason_codes": [],
            "evidence_refs": [], "observed_question_labels": [question] if question else [], "objective_items": [],
            "visual_evidence": {"static_label": static, "option_pattern": "none", "identity_pattern": pattern,
                                "barcode_pattern": "linear_bars" if identity == "条码" else "none",
                                "boundary_basis": "printed_frame", "anchor_bbox": None}}


def apply(*, missing=None, visible=False, fragment_conflict=False, replace_instruction=False):
    payload = {"request_id": "instruction-context-flow", "page": PAGE, "writing_state": "blank",
               "question_labels": ["15"], "objective_question_labels": [], "fill_blank_question_labels": []}
    image = np.full((PAGE["height"], PAGE["width"], 3), 255, np.uint8)
    evidence = LocalContentEvidence(image, PAGE, ocr=lambda _image: ([], None), barcode=lambda _image: "barcode")
    raw = [candidate("uncertain", CROP), candidate("instruction", KNOWN)]
    if visible:
        raw.append(candidate("answer", {"x": .2, "y": .27, "width": .1, "height": .03}, question="15"))
    existing = _normalize(raw, {}, payload)
    evidence.review(existing, payload)
    if missing == "instruction_evidence":
        existing[1]["reason_codes"].remove("MULTIMODAL_FORBIDDEN_REGION_EXCLUDED")
    frames = [] if missing == "frame" else [{"bbox": FRAME, "evidence_refs": ["cv-frame:measured"]}]
    fragment = candidate("instruction", FRAGMENT)
    if fragment_conflict:
        # 裁边候选自报含真实身份结构时必须失败，不能借“说明片段去重”把矛盾藏起来。
        fragment["visual_evidence"]["identity_pattern"] = "name_line"
    batch = {"task": "identity_reclassify", "replace_candidate_ids": [existing[0]["candidate_id"]],
             "crop_bbox": CROP, "candidates": [
                 candidate("identity", {"x": .06, "y": .1, "width": .08, "height": .03}, identity="姓名"),
                 candidate("identity", {"x": .06, "y": .15, "width": .12, "height": .05}, identity="准考证号"),
                 candidate("identity", {"x": .219375, "y": .11307420494699646,
                                        "width": .1225, "height": .10865724381625441}, identity="条码"),
                 fragment]}
    if replace_instruction:
        batch["replace_candidate_ids"].append(existing[1]["candidate_id"])
    before, original_batch = copy.deepcopy(existing), copy.deepcopy(batch)
    applications = _apply_repair_batches(existing, [batch], payload, frames, PAGE, evidence)
    assert batch == original_batch
    return existing, before, applications


def test_identity_repair_removes_only_measured_crop_edge_context_and_keeps_complete_instruction():
    existing, before, applications = apply()

    assert applications == [{"task": "identity_reclassify", "applied": True, "candidate_count": 3,
                             "context_filter": {"version": CONTEXT_FILTER_VERSION, "removed_count": 1,
                                                "reason_codes": ["LOCAL_REPAIR_INSTRUCTION_CONTEXT_DEDUPLICATED"]}}]
    assert existing[0] == before[1]
    assert [row["identity_label"] for row in existing[1:]] == ["姓名", "准考证号", "条码"]
    assert all(row["semantic_check"] == "supported" for row in existing[1:])
    assert all("LOCAL_MODEL_REPAIR_APPLIED" in row["reason_codes"] for row in existing[1:])
    assert not any(row["proposed_bbox"] == FRAGMENT for row in existing)


@pytest.mark.parametrize("missing", ["frame", "instruction_evidence"])
def test_missing_independent_context_evidence_retains_fragment_instead_of_hiding_it(missing):
    existing, before, applications = apply(missing=missing)

    assert applications == [{"task": "identity_reclassify", "applied": True, "candidate_count": 4}]
    assert existing[0] == before[1]
    assert existing[-1]["role"] == "instruction" and existing[-1]["proposed_bbox"] == FRAGMENT
    assert len([row for row in existing if row["role"] == "identity"]) == 3


def test_measured_frame_with_visible_answer_retains_answer_and_fragment():
    existing, before, applications = apply(visible=True)

    assert applications == [{"task": "identity_reclassify", "applied": True, "candidate_count": 4}]
    assert existing[:2] == before[1:]
    assert existing[1]["role"] == "answer" and existing[1]["printed_question_label"] == "15"
    assert existing[-1]["role"] == "instruction" and existing[-1]["proposed_bbox"] == FRAGMENT


def test_fragment_reporting_visible_identity_content_keeps_original_rejection_and_regions():
    existing, before, applications = apply(fragment_conflict=True)

    assert existing == before
    assert applications[0]["applied"] is False
    assert applications[0]["reason"] == "REPAIR_CONTENT_UNRESOLVED"
    assert "MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT" in applications[0]["content_issues"]
    assert "context_filter" not in applications[0]


def test_instruction_selected_for_replacement_cannot_prove_its_own_context():
    existing, before, applications = apply(replace_instruction=True)

    assert applications == [{"task": "identity_reclassify", "applied": True, "candidate_count": 4}]
    assert before[1] not in existing
    assert existing[-1]["role"] == "instruction" and existing[-1]["proposed_bbox"] == FRAGMENT
