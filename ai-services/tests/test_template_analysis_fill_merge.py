import copy

import pytest
from grading_agent.template_analysis import (
    _merge_local_fill_regions,
    _replace_verified_fill_parents,
)

PAGE = {"width": 1000, "height": 1000}
VERIFIED = [
    "LOCAL_FILL_HEADER_VERIFIED",
    "LOCAL_FILL_PRINTED_MARKER_VERIFIED",
    "LOCAL_FILL_UNDERLINE_VERIFIED",
]


def fill_layout():
    parent = {
        "candidate_id": "model-mixed-fill",
        "page_no": 1,
        "origin": "model",
        "role": "uncertain",
        "printed_question_label": None,
        "association_status": "unassociated",
        "semantic_check": "needs_review",
        "proposed_bbox": {"x": .08, "y": .25, "width": .5, "height": .3},
        "_fill_parent_labels": ["12", "13", "14"],
        "reason_codes": ["MODEL_OBS_MULTIPLE_QUESTION_LABELS"],
    }
    local = []
    for label, x, y in [("12", .1, .3), ("13", .35, .3), ("14", .1, .4)]:
        local.append({
            "candidate_id": f"local-fill-{label}",
            "page_no": 1,
            "origin": "local_detector",
            "role": "answer",
            "printed_question_label": label,
            "association_status": "unassociated",
            "semantic_check": "needs_review",
            "proposed_bbox": {"x": x, "y": y, "width": .2, "height": .08},
            "reason_codes": [*VERIFIED, "LOCAL_FILL_REQUIRES_REVIEW"],
        })
    return parent, local


def test_complete_independent_fill_evidence_atomically_replaces_parent_without_duplicate_append():
    parent, local = fill_layout()
    before = copy.deepcopy(local)
    existing = [parent]

    _merge_local_fill_regions(existing, local, PAGE)

    assert [row["printed_question_label"] for row in existing] == ["12", "13", "14"]
    assert all(row["semantic_check"] == "supported" and row["association_status"] == "associated" for row in existing)
    assert all("LOCAL_FILL_LAYOUT_VERIFIED" in row["reason_codes"] for row in existing)
    assert [row["proposed_bbox"] for row in existing] == [row["proposed_bbox"] for row in before]
    assert local == before


def test_supported_model_fill_is_aligned_to_unique_printed_marker_and_underline_instead_of_skipped():
    _parent, local = fill_layout()
    measured = local[-1]
    measured["evidence_refs"] = ["local-fill:actual-marker-and-line"]
    model = {**copy.deepcopy(measured), "candidate_id": "coarse-14", "origin": "model",
             "semantic_check": "supported", "association_status": "associated", "reason_codes": [],
             "evidence_refs": ["source-page:actual"],
             "proposed_bbox": {"x": .08, "y": .5, "width": .4, "height": .04}}
    model["visual_evidence"] = {"boundary_basis": "printed_frame",
                                "anchor_bbox": {"x": .1, "y": .51, "width": .02, "height": .02}}
    existing = [model]
    _merge_local_fill_regions(existing, [measured], PAGE)
    assert existing == [model]
    assert model["proposed_bbox"] == measured["proposed_bbox"]
    assert model["semantic_check"] == "supported"
    assert model["evidence_refs"] == ["source-page:actual", "local-fill:actual-marker-and-line"]
    assert "LOCAL_FILL_LAYOUT_VERIFIED" in model["reason_codes"]
    assert model["visual_evidence"] == {"boundary_basis": "printed_answer_line", "anchor_bbox": None}


@pytest.mark.parametrize("blocked", ["user", "missing_evidence", "other_column", "distant", "other_question"])
def test_local_fill_alignment_preserves_human_geometry_and_rejects_unproven_or_conflicting_moves(blocked):
    _parent, local = fill_layout()
    measured = local[-1]
    model = {**copy.deepcopy(measured), "candidate_id": "coarse-14", "origin": "model",
             "semantic_check": "supported", "association_status": "associated", "reason_codes": [],
             "proposed_bbox": {"x": .08, "y": .5, "width": .4, "height": .04}}
    existing = [model]
    if blocked == "user":
        model["origin"] = "user"
    elif blocked == "missing_evidence":
        measured["reason_codes"].remove("LOCAL_FILL_PRINTED_MARKER_VERIFIED")
    elif blocked == "other_column":
        model["proposed_bbox"]["x"] = .6
    elif blocked == "distant":
        model["proposed_bbox"]["y"] = .8
    else:
        existing.append({**copy.deepcopy(measured), "candidate_id": "blocking-15", "printed_question_label": "15"})
    before = copy.deepcopy(model["proposed_bbox"])
    _merge_local_fill_regions(existing, [measured], PAGE)
    assert model["proposed_bbox"] == before
    assert "LOCAL_FILL_LAYOUT_VERIFIED" not in model["reason_codes"]


def test_missing_one_fill_label_preserves_parent_and_all_original_evidence():
    parent, local = fill_layout()
    existing = [parent]
    before = copy.deepcopy(existing)

    _replace_verified_fill_parents(existing, local[:2], PAGE)

    assert existing == before


@pytest.mark.parametrize("origin,status", [("user", "confirmed"), ("model", "supported")])
def test_overlap_with_human_or_usable_region_preserves_both_original_regions(origin, status):
    parent, local = fill_layout()
    blocker = {
        **copy.deepcopy(local[0]),
        "candidate_id": "existing-answer",
        "origin": origin,
        "semantic_check": status,
        "association_status": "associated",
        "printed_question_label": "15",
    }
    existing = [parent, blocker]
    before = copy.deepcopy(existing)

    _replace_verified_fill_parents(existing, local, PAGE)

    assert existing == before


def test_model_claiming_local_reason_codes_cannot_replace_parent():
    parent, local = fill_layout()
    for row in local:
        row["origin"] = "model"
    existing = [parent]
    before = copy.deepcopy(existing)

    _replace_verified_fill_parents(existing, local, PAGE)

    assert existing == before


@pytest.mark.parametrize("missing", VERIFIED)
def test_incomplete_independent_evidence_cannot_replace_parent(missing):
    parent, local = fill_layout()
    local[1]["reason_codes"].remove(missing)
    existing = [parent]
    before = copy.deepcopy(existing)

    _replace_verified_fill_parents(existing, local, PAGE)

    assert existing == before


def test_same_question_numbers_in_another_column_cannot_replace_parent():
    parent, local = fill_layout()
    for row in local:
        row["proposed_bbox"]["x"] += .55
    existing = [parent]
    before = copy.deepcopy(existing)

    _replace_verified_fill_parents(existing, local, PAGE)

    assert existing == before


def test_one_misplaced_child_cannot_use_union_enclosing_parent_to_pass_position_check():
    parent, local = fill_layout()
    # 并集矩形会跨过空白并包住父框，不能证明右栏的子题也属于左栏合题框。
    local[-1]["proposed_bbox"]["x"] = .75
    existing = [parent]
    before = copy.deepcopy(existing)

    _replace_verified_fill_parents(existing, local, PAGE)

    assert existing == before


@pytest.mark.parametrize("origin,status", [("user", "needs_review"), ("model", "supported")])
def test_human_or_supported_parent_is_never_replaced(origin, status):
    parent, local = fill_layout()
    parent.update(origin=origin, semantic_check=status)
    existing = [parent]
    before = copy.deepcopy(existing)

    _replace_verified_fill_parents(existing, local, PAGE)

    assert existing == before
