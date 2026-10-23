"""局部只恢复12/13时，完整模型复判的14不能被同批旧子题挡住。"""

import copy

import numpy as np
import pytest
from grading_agent.template_analysis import _apply_repair_batches, _normalize
from test_template_analysis_v8_flow import box, no_external_evidence, request, row


def replay(*, variant=None):
    payload = request()
    payload["question_labels"].append("14")
    payload["fill_blank_question_labels"].append("14")
    parent = row("answer", box(.05, .35, .35, .35), static="填空题", labels=["12", "13", "14"])
    evidence = no_external_evidence(np.full((1000, 1600, 3), 255, np.uint8), payload["page"])
    existing = _normalize([parent], {}, payload)
    evidence.review(existing, payload)
    existing[0]["_fill_parent_labels"] = ["12", "13", "14"]
    children = []
    for label, bbox in [("12", box(.06, .36, .13, .1)), ("13", box(.23, .36, .14, .1))]:
        children.append({"candidate_id": "local-" + label, "origin": "local_detector", "role": "answer",
            "page_no": 1, "identity_label": None, "printed_question_label": label,
            "proposed_bbox": bbox, "source_bbox": copy.deepcopy(bbox), "objective_items": [],
            "semantic_check": "needs_review", "association_status": "unassociated",
            "evidence_refs": ["local-fill:printed-marker-and-line:" + label],
            "reason_codes": ["LOCAL_FILL_HEADER_VERIFIED", "LOCAL_FILL_PRINTED_MARKER_VERIFIED",
                             "LOCAL_FILL_UNDERLINE_VERIFIED", "LOCAL_FILL_PARENT_REGION_REQUIRES_REVIEW"]})
    if variant == "user":
        children[0]["origin"] = "user"
    elif variant == "other_column":
        children[0]["proposed_bbox"]["x"] = .6
    elif variant == "missing_marker":
        children[0]["reason_codes"].remove("LOCAL_FILL_PRINTED_MARKER_VERIFIED")
    elif variant == "duplicate_local":
        children.append({**copy.deepcopy(children[0]), "candidate_id": "duplicate-12"})
    existing.extend(children)
    fresh = [row("answer", bbox, question=label, static="填空题") for label, bbox in (
        ("12", box(.055, .355, .15, .12)), ("13", box(.225, .355, .15, .12)),
        ("14", box(.055, .49, .15, .1)))]
    if variant == "missing_14":
        fresh.pop()
    before = copy.deepcopy(existing)
    batch = {"task": "mixed_region", "replace_candidate_ids": [existing[0]["candidate_id"]],
             "crop_bbox": box(.04, .34, .37, .37), "candidates": fresh}
    original_batch = copy.deepcopy(batch)
    applied = _apply_repair_batches(existing, [batch], payload, [], payload["page"], evidence)
    assert batch == original_batch
    return existing, before, applied


def test_complete_fill_repair_absorbs_same_location_local_children_and_keeps_their_measured_geometry():
    existing, before, applied = replay()
    assert applied[0]["applied"] is True
    assert applied[0]["fill_reconciliation"] == {"preserved_local_fields": 2}
    assert sorted(item["printed_question_label"] for item in existing) == ["12", "13", "14"]
    assert all(item["semantic_check"] == "supported" and item["association_status"] == "associated" for item in existing)
    for old in before[1:]:
        current = next(item for item in existing if item["printed_question_label"] == old["printed_question_label"])
        assert current["proposed_bbox"] == old["proposed_bbox"]
        assert set(old["evidence_refs"]) <= set(current["evidence_refs"])


@pytest.mark.parametrize("variant", ["user", "other_column", "missing_marker", "duplicate_local", "missing_14"])
def test_fill_repair_does_not_consume_human_ambiguous_or_incomplete_children(variant):
    existing, before, applied = replay(variant=variant)
    assert applied[0]["applied"] is False
    assert existing == before
