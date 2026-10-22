"""重试身份裁图会再次带回可靠条码；独立测量只去掉同一码的新建议。"""

import copy

import numpy as np
import pytest
from grading_agent.template_analysis import (
    _align_verified_barcode_boundaries,
    _apply_repair_batches,
    _barcode_frame_targets,
    _filter_repair_duplicate_codes,
    _finalize_candidate_reasons,
    _normalize,
    _review_region_conflicts,
)
from grading_agent.template_analysis_semantics import LocalContentEvidence

PAGE = {"page_no": 1, "width": 1600, "height": 1132, "sha256": "a" * 64}
OUTER = {"x": .219375, "y": .11307420494699646, "width": .1225, "height": .10865724381625441}
ANCHOR = {"x": .24, "y": .16, "width": .08, "height": .025}


def hint(*, anchor=None, bbox=None):
    return {"boundary_kind": "printed_frame", "identity_label": "条码",
            "anchor_bbox": copy.deepcopy(anchor or ANCHOR), "bbox": copy.deepcopy(bbox or OUTER),
            "evidence_refs": ["local-code:independently-measured"]}


def code(candidate_id):
    return {"candidate_id": candidate_id, "page_no": 1, "role": "identity", "identity_label": "条码",
            "proposed_bbox": copy.deepcopy(ANCHOR), "semantic_check": "supported", "origin": "model",
            "association_status": "unassociated", "reason_codes": [], "evidence_refs": []}


def measured_pair():
    old, fresh = code("old"), code("fresh")
    _align_verified_barcode_boundaries([old, fresh], _barcode_frame_targets([hint()]))
    return old, fresh


@pytest.mark.parametrize("old_origin", ["model", "user"])
def test_same_independent_code_anchor_and_outer_frame_keep_existing_region_unchanged(old_origin):
    old, fresh = measured_pair()
    old["origin"] = old_origin
    before = copy.deepcopy([old, fresh])

    assert _filter_repair_duplicate_codes([fresh], [old]) == ([], 1)
    assert [old, fresh] == before


@pytest.mark.parametrize("change", ["no_measurement", "different_anchor", "different_outer", "fresh_unresolved",
                                    "old_unresolved", "fresh_user", "multiple_owners", "different_category"])
def test_missing_unique_measurement_or_any_unresolved_evidence_does_not_hide_fresh_code(change):
    old, fresh = measured_pair()
    kept = [old]
    if change == "no_measurement":
        fresh.pop("_barcode_frame_key")
    elif change == "different_anchor":
        other = {"x": .24, "y": .195, "width": .08, "height": .02}
        fresh["proposed_bbox"] = other
        _align_verified_barcode_boundaries([fresh], _barcode_frame_targets([hint(anchor=other)]))
    elif change == "different_outer":
        fresh["proposed_bbox"]["width"] += .01
    elif change == "fresh_unresolved":
        fresh.update(semantic_check="needs_review", reason_codes=["SEMANTIC_CONTENT_CONFLICT"])
    elif change == "old_unresolved":
        old["semantic_check"] = "needs_review"
    elif change == "fresh_user":
        fresh["origin"] = "user"
    elif change == "multiple_owners":
        kept.append(copy.deepcopy(old))
    else:
        fresh["identity_label"] = "二维码"
    before = copy.deepcopy([fresh, kept])

    assert _filter_repair_duplicate_codes([fresh], kept) == ([fresh], 0)
    assert [fresh, kept] == before


def raw(role, bbox, *, identity=None):
    return {"candidate_id": None, "page_no": 1, "role": role, "identity_label": identity,
            "printed_question_label": None, "proposed_bbox": copy.deepcopy(bbox),
            "association_status": "unassociated", "reason_codes": [], "evidence_refs": [],
            "observed_question_labels": [], "objective_items": [], "visual_evidence": {
                "static_label": "姓名" if identity == "姓名" else None,
                "option_pattern": "none", "identity_pattern": "name_line" if identity == "姓名" else "none",
                "barcode_pattern": "linear_bars" if identity == "条码" else "none",
                "boundary_basis": "printed_frame", "anchor_bbox": None}}


@pytest.mark.parametrize("counterevidence", [False, True])
def test_identity_crop_repeating_kept_barcode_applies_other_fields_only_after_content_passes(monkeypatch, counterevidence):
    payload = {"request_id": "retry-code-context-fixture", "page": PAGE, "writing_state": "written",
               "question_labels": [], "objective_question_labels": [], "fill_blank_question_labels": []}
    identity_box = {"x": .06, "y": .12, "width": .1, "height": .04}
    image = np.full((1132, 1600, 3), 255, np.uint8)
    evidence = LocalContentEvidence(image, PAGE, ocr=lambda _image: ([], None), barcode=lambda _image: "barcode")
    existing = _normalize([raw("uncertain", identity_box), raw("identity", ANCHOR, identity="条码")], {}, payload)
    evidence.review(existing, payload)
    _align_verified_barcode_boundaries([existing[1]], _barcode_frame_targets([hint()]))
    before = copy.deepcopy(existing)
    # 真正故障的两个条码 ID 按校准前几何计算；外框校准后坐标相同、ID 仍不同。
    different_inner = {**ANCHOR, "x": ANCHOR["x"] + .001}
    repeated = raw("identity", different_inner, identity="条码")
    if counterevidence:
        repeated["reason_codes"] = ["SEMANTIC_CONTENT_CONFLICT"]
    monkeypatch.setattr("grading_agent.template_analysis.align_identity_fields", lambda *_args, **_kwargs: {"aligned_indices": []})
    batch = {"task": "identity_reclassify", "replace_candidate_ids": [existing[0]["candidate_id"]],
             "candidates": [raw("identity", identity_box, identity="姓名"), repeated]}
    original_batch = copy.deepcopy(batch)

    applications = _apply_repair_batches(existing, [batch], payload, [], PAGE, evidence, barcode_hints=[hint()])

    assert batch == original_batch
    if counterevidence:
        assert existing == before
        assert applications[0]["applied"] is False
        assert applications[0]["reason"] == "REPAIR_CONTENT_UNRESOLVED"
        assert "duplicate_code_context" not in applications[0]
    else:
        assert applications == [{"task": "identity_reclassify", "applied": True, "candidate_count": 1,
                                 "duplicate_code_context": {"removed_count": 1}}]
        assert existing[0] == before[1]
        assert [row["identity_label"] for row in existing] == ["条码", "姓名"]
        assert all(row["semantic_check"] == "supported" for row in existing)
        _finalize_candidate_reasons(existing, payload["request_id"])
        assert all("_barcode_frame_key" not in row for row in existing)


def test_same_code_outer_frame_without_unique_dedup_evidence_remains_a_visible_conflict():
    old, fresh = measured_pair()
    fresh.pop("_barcode_frame_key")
    _review_region_conflicts([old, fresh], PAGE, semantic_completed=True)
    assert all(row["semantic_check"] == "needs_review" for row in (old, fresh))
    assert all("SEMANTIC_REGION_OVERLAP_CONFLICT" in row["reason_codes"] for row in (old, fresh))


def test_two_distinct_actual_codes_are_not_a_singleton_or_spatial_conflict():
    old, fresh = measured_pair()
    other_outer = {**OUTER, "x": .6}
    other_anchor = {**ANCHOR, "x": .62}
    fresh["proposed_bbox"] = other_anchor
    _align_verified_barcode_boundaries([fresh], _barcode_frame_targets([hint(anchor=other_anchor, bbox=other_outer)]))
    assert _filter_repair_duplicate_codes([fresh], [old]) == ([fresh], 0)
    _review_region_conflicts([old, fresh], PAGE, semantic_completed=True)
    assert all(row["semantic_check"] == "supported" for row in (old, fresh))
