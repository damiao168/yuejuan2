"""最终合并只验证边界与失败分类，不模拟真实供应商识别质量。"""

import copy

import cv2
import numpy as np
import pytest
from grading_agent.errors import AgentError
from grading_agent.template_analysis import (
    _align_existing_discovered_barcodes,
    _align_verified_barcode_boundaries,
    _barcode_repair_hints,
    _normalize,
    _review_region_conflicts,
    _semantic_failure_reason,
)
from grading_agent.template_analysis_barcode_discovery import discover_barcode_regions
from grading_agent.template_analysis_semantics import LocalContentEvidence


@pytest.mark.parametrize(("code", "reason"), [
    ("model_timeout", "SEMANTIC_MODEL_TIMEOUT"),
    ("model_unavailable", "SEMANTIC_MODEL_UNAVAILABLE"),
    ("model_output_invalid", "MODEL_STRUCTURED_OUTPUT_INVALID"),
])
def test_transport_failure_is_not_reported_as_invalid_model_output(code, reason):
    assert _semantic_failure_reason(AgentError(code, "private supplier detail", status=502)) == reason


@pytest.mark.parametrize("boundary_kind", ["printed_frame", "context_only"])
def test_barcode_content_anchor_uses_only_independently_measured_outer_frame(boundary_kind):
    anchor = {"x": .24, "y": .16, "width": .08, "height": .025}
    outer = {"x": .21, "y": .12, "width": .14, "height": .11}
    fresh = [{"role": "identity", "identity_label": "条码", "proposed_bbox": copy.deepcopy(anchor),
              "evidence_refs": ["source-page:synthetic"], "reason_codes": []}]
    target = {"_barcode_boundary_kind": boundary_kind, "_barcode_identity_label": "条码",
              "_barcode_anchor_bbox": anchor, "proposed_bbox": outer,
              "evidence_refs": ["local-code:synthetic"]}
    _align_verified_barcode_boundaries(fresh, [target])
    assert fresh[0]["proposed_bbox"] == (outer if boundary_kind == "printed_frame" else anchor)
    assert ("LOCAL_BARCODE_OUTER_FRAME_LOCALIZED" in fresh[0]["reason_codes"]) == (boundary_kind == "printed_frame")


def test_unrelated_code_anchor_cannot_move_a_region_to_another_outer_frame():
    anchor = {"x": .24, "y": .16, "width": .08, "height": .025}
    other = {"x": .6, "y": .5, "width": .12, "height": .1}
    fresh = [{"role": "identity", "identity_label": "条码", "proposed_bbox": copy.deepcopy(other),
              "evidence_refs": [], "reason_codes": []}]
    _align_verified_barcode_boundaries(fresh, [{
        "_barcode_boundary_kind": "printed_frame", "_barcode_identity_label": "条码",
        "_barcode_anchor_bbox": anchor, "proposed_bbox": anchor, "evidence_refs": [],
    }])
    assert fresh[0]["proposed_bbox"] == other


def discovered_code_case():
    image = np.full((600, 800, 3), 255, np.uint8)
    cv2.rectangle(image, (150, 90), (450, 230), (0, 0, 0), 2)
    for x in range(180, 420, 5):
        cv2.line(image, (x, 130), (x, 175), (0, 0, 0), 2)
    class Detector:
        def detectMulti(self, _image):
            return True, np.array([[[180, 130], [420, 130], [420, 175], [180, 175]]], np.float32)
    page = {"page_no": 1, "width": 800, "height": 600, "sha256": "a" * 64}
    hints, _ = discover_barcode_regions(image, page, detectors={"条码": Detector()})
    assert hints[0]["boundary_kind"] == "printed_frame"
    existing = [{"candidate_id": "existing-model-code", "page_no": 1,
                 "role": "identity", "identity_label": "条码", "printed_question_label": None,
                 "source_bbox": copy.deepcopy(hints[0]["anchor_bbox"]),
                 "proposed_bbox": copy.deepcopy(hints[0]["anchor_bbox"]),
                 "semantic_check": "supported", "association_status": "unassociated",
                 "evidence_refs": ["source-page:synthetic"],
                 "reason_codes": ["MULTIMODAL_OBSERVATIONS_CONSISTENT"]}]
    return image, page, hints, existing


def test_existing_supported_stripe_box_moves_to_outer_frame_before_deduplication_without_adding_second_barcode():
    image, page, hints, existing = discovered_code_case()
    anchor = copy.deepcopy(existing[0]["proposed_bbox"])
    aligned = _align_existing_discovered_barcodes(hints, existing)
    assert len(aligned) == 1 and aligned[0] is existing[0]
    assert existing[0]["proposed_bbox"] == hints[0]["bbox"] != anchor
    evidence = LocalContentEvidence(image, page, ocr=lambda _image: ([], None), barcode=lambda _image: "barcode")
    evidence.review(aligned, {"question_labels": []})
    assert existing[0]["semantic_check"] == "supported"
    assert _barcode_repair_hints(hints, existing, page) == []
    assert len(existing) == 1 and existing[0]["source_bbox"] == anchor


@pytest.mark.parametrize("change", ["context_only", "needs_review", "different_kind"])
def test_existing_code_alignment_does_not_promote_unverified_geometry_or_category(change):
    _image, _page, hints, existing = discovered_code_case()
    if change == "context_only":
        hints[0]["boundary_kind"] = "context_only"
    elif change == "needs_review":
        existing[0]["semantic_check"] = "needs_review"
    else:
        existing[0]["identity_label"] = "二维码"
    before = copy.deepcopy(existing)
    assert _align_existing_discovered_barcodes(hints, existing) == []
    assert existing == before


def test_model_region_downgraded_by_normalization_cannot_be_rescued_by_outer_code_geometry():
    _image, page, hints, existing = discovered_code_case()
    raw = {"candidate_id": None, "page_no": 1, "role": "identity", "identity_label": "条码",
           "printed_question_label": "15", "proposed_bbox": copy.deepcopy(existing[0]["proposed_bbox"]),
           "association_status": "unassociated", "evidence_refs": [], "reason_codes": [],
           "observed_question_labels": [], "objective_items": [], "visual_evidence": {
               "static_label": None, "identity_pattern": "none", "option_pattern": "none",
               "barcode_pattern": "linear_bars", "boundary_basis": "printed_frame", "anchor_bbox": None}}
    payload = {"request_id": "synthetic-normalization", "page": page, "question_labels": ["15"], "writing_state": "blank"}
    normalized = _normalize([raw], {}, payload)
    assert normalized[0]["role"] == "uncertain" and normalized[0]["semantic_check"] == "needs_review"
    before = copy.deepcopy(normalized)
    assert _align_existing_discovered_barcodes(hints, normalized) == []
    _align_verified_barcode_boundaries(normalized, _barcode_repair_hints(hints, [], page))
    assert normalized == before


def test_outer_code_geometry_still_runs_content_and_final_spatial_conflict_checks():
    image, page, hints, existing = discovered_code_case()
    aligned = _align_existing_discovered_barcodes(hints, existing)
    # 标签原本在条纹小框外；扩成外框后必须重新读，不能沿用小框曾经通过的内容校验。
    warning = {"text": "禁止作答", "x": .2, "y": .34, "width": .1, "height": .025}
    evidence = LocalContentEvidence(image, page, ocr=lambda _image: ([warning], None), barcode=lambda _image: "barcode")
    evidence.review(aligned, {"question_labels": []})
    assert existing[0]["semantic_check"] == "needs_review"
    assert "SEMANTIC_FORBIDDEN_AREA_DETECTED" in existing[0]["reason_codes"]
    _image, page, hints, existing = discovered_code_case()
    _align_existing_discovered_barcodes(hints, existing)
    answer = {"role": "answer", "printed_question_label": "15", "identity_label": None,
              "proposed_bbox": {"x": .18, "y": .14, "width": .1, "height": .05},
              "association_status": "associated", "semantic_check": "supported", "reason_codes": []}
    _review_region_conflicts([*existing, answer], page, semantic_completed=True)
    assert existing[0]["semantic_check"] == "needs_review" and answer["semantic_check"] == "needs_review"
