"""身份字段像素校准经过真实局部替换门禁；固定标签读取不代表真实 OCR 准确率。"""

import copy
import time

import cv2
import numpy as np
import pytest
from grading_agent.template_analysis import _apply_repair_batches, _normalize
from grading_agent.template_analysis_identity_alignment import (
    IDENTITY_ALIGNMENT_VERSION,
)
from grading_agent.template_analysis_semantics import LocalContentEvidence


def box(x, y, width, height):
    return {"x": x, "y": y, "width": width, "height": height}


def candidate(role, bbox, *, identity=None, question=None):
    return {"candidate_id": None, "page_no": 1, "role": role, "proposed_bbox": copy.deepcopy(bbox),
            "printed_question_label": question, "identity_label": identity,
            "association_status": "associated" if question else "unassociated", "reason_codes": [],
            "evidence_refs": [], "observed_question_labels": [question] if question else [], "objective_items": [],
            "visual_evidence": {"static_label": identity or ("解答题" if question else None),
                                "option_pattern": "none", "identity_pattern": {
                                    "姓名": "name_line", "准考证号": "number_grid"}.get(identity, "none"),
                                "barcode_pattern": "none", "boundary_basis": "printed_frame", "anchor_bbox": None}}


class RecordingEvidence(LocalContentEvidence):
    def __init__(self, image, page):
        super().__init__(image, page, ocr=lambda _image: ([], None), barcode=lambda _image: None)
        self.reviewed_geometry = []

    def review(self, rows, payload):
        self.reviewed_geometry.append([copy.deepcopy(row["proposed_bbox"]) for row in rows])
        return super().review(rows, payload)


def apply(monkeypatch, *, missing=None, conflict=False, expired=False):
    image = np.full((500, 800, 3), 255, np.uint8)
    cv2.line(image, (100, 150), (300, 150), (0, 0, 0), 2)
    if missing != "number_grid":
        cv2.rectangle(image, (100, 190), (316, 214), (0, 0, 0), 1)
        for index in range(1, 9):
            if missing != "grid_bar" or index != 3:
                cv2.line(image, (100 + index * 24, 190), (100 + index * 24, 214), (0, 0, 0), 1)
    # 文字块只负责触发真实窄带裁图；独立 reader 注入静态标签，不尝试识别学生填写内容。
    cv2.putText(image, "NAME", (105, 124), cv2.FONT_HERSHEY_SIMPLEX, .5, (0, 0, 0), 1)
    cv2.putText(image, "IDNO", (105, 184), cv2.FONT_HERSHEY_SIMPLEX, .5, (0, 0, 0), 1)
    reads = []

    def reader(crop, *, timeout):
        reads.append({"shape": crop.shape, "timeout": timeout})
        if missing == "labels":
            return [], None
        label = "姓名" if len(reads) == 1 else "准考证号"
        return [{"text": label, "confidence": 99, "x": 1, "y": 1,
                 "width": crop.shape[1] - 2, "height": crop.shape[0] - 2}], None

    monkeypatch.setattr("grading_agent.template_analysis_identity_alignment._read_static_labels", reader)
    page = {"page_no": 1, "width": 800, "height": 500, "sha256": "a" * 64}
    payload = {"request_id": "identity-pixel-flow-fixture", "page": page, "writing_state": "blank",
               "question_labels": ["15"], "objective_question_labels": [], "fill_blank_question_labels": []}
    fields = [candidate("identity", box(.12, .2, .26, .045), identity="姓名"),
              candidate("identity", box(.12, .28, .27, .045), identity="准考证号")]
    # 这条已接受的区域在旧字段下方；只有校准后的姓名框会与它发生新冲突。
    kept_box = box(.14, .252, .2, .02) if conflict else box(.62, .2, .3, .5)
    evidence = RecordingEvidence(image, page)
    existing = _normalize([*fields, candidate("answer", kept_box, question="15")], {}, payload)
    evidence.review(existing, payload)
    before = copy.deepcopy(existing)
    batch = {"task": "identity_cluster", "replace_candidate_ids": [row["candidate_id"] for row in existing[:2]],
             "crop_bbox": box(.08, .1, .4, .4), "candidates": copy.deepcopy(fields)}
    original_batch = copy.deepcopy(batch)
    deadline = time.monotonic() - 1 if expired else time.monotonic() + 2
    applications = _apply_repair_batches(existing, [batch], payload, [], page, evidence, deadline=deadline)
    assert batch == original_batch
    return existing, before, applications, reads, evidence.reviewed_geometry


def test_shifted_model_identity_boxes_are_aligned_before_content_review_and_applied(monkeypatch):
    existing, before, applications, reads, reviewed = apply(monkeypatch)

    assert applications == [{"task": "identity_cluster", "applied": True, "candidate_count": 2,
                             "identity_alignment": {"aligned_fields": 2}}]
    assert existing[0] == before[2]
    name, number = existing[1:]
    for row in (name, number):
        assert row["semantic_check"] == "supported"
        assert "LOCAL_IDENTITY_FIELD_STRUCTURE_ALIGNED" in row["reason_codes"]
        assert "LOCAL_MODEL_REPAIR_APPLIED" in row["reason_codes"]
        assert any(ref.startswith(f"local-identity:{IDENTITY_ALIGNMENT_VERSION}:") for ref in row["evidence_refs"])
    assert name["proposed_bbox"]["y"] <= 112 / 500
    assert name["proposed_bbox"]["y"] + name["proposed_bbox"]["height"] >= 152 / 500
    assert number["proposed_bbox"]["x"] <= 100 / 800
    assert number["proposed_bbox"]["x"] + number["proposed_bbox"]["width"] >= 317 / 800
    assert number["proposed_bbox"]["y"] + number["proposed_bbox"]["height"] >= 215 / 500
    assert reviewed[-1] == [name["proposed_bbox"], number["proposed_bbox"]]
    assert len(reads) == 2 and all(0 < read["timeout"] <= 2 for read in reads)


@pytest.mark.parametrize("missing", ["labels", "number_grid", "grid_bar"])
def test_missing_label_or_complete_grid_keeps_affected_model_geometry_without_fabrication(monkeypatch, missing):
    existing, before, applications, _reads, _reviewed = apply(monkeypatch, missing=missing)

    assert applications[0]["applied"] is True
    assert existing[0] == before[2]
    assert len(existing) == len(before)
    affected = existing[1:] if missing == "labels" else existing[2:]
    originals = before[:2] if missing == "labels" else before[1:2]
    for row, original in zip(affected, originals, strict=True):
        assert row["proposed_bbox"] == original["proposed_bbox"]
        assert "LOCAL_IDENTITY_FIELD_STRUCTURE_ALIGNED" not in row["reason_codes"]
        assert not any(ref.startswith("local-identity:") for ref in row["evidence_refs"])
    if missing == "labels":
        assert "identity_alignment" not in applications[0]
    else:
        assert applications[0]["identity_alignment"] == {"aligned_fields": 1}


def test_new_identity_geometry_conflicting_with_kept_answer_rejects_entire_batch(monkeypatch):
    existing, before, applications, reads, _reviewed = apply(monkeypatch, conflict=True)

    assert len(reads) == 2
    assert applications == [{"task": "identity_cluster", "applied": False,
                             "identity_alignment": {"aligned_fields": 2},
                             "reason": "REPAIR_COMBINED_LAYOUT_CONFLICT"}]
    assert existing == before


def test_shared_expired_analysis_deadline_prevents_additional_label_reads_in_repair(monkeypatch):
    existing, before, applications, reads, _reviewed = apply(monkeypatch, expired=True)

    assert reads == []
    assert applications == [{"task": "identity_cluster", "applied": True, "candidate_count": 2}]
    assert [row["proposed_bbox"] for row in existing[1:]] == [row["proposed_bbox"] for row in before[:2]]


def test_expanded_identity_repair_cannot_drop_previously_verified_whole_question():
    page = {"page_no": 1, "width": 800, "height": 500, "sha256": "a" * 64}
    payload = {"request_id": "identity-whole-question", "page": page, "writing_state": "blank",
               "question_labels": ["15"], "objective_question_labels": [], "fill_blank_question_labels": []}
    image = np.full((500, 800, 3), 255, np.uint8)
    evidence = RecordingEvidence(image, page)
    existing = _normalize([candidate("answer", box(.4, .1, .3, .8), question="15")], {}, payload)
    evidence.review(existing, payload)
    before = copy.deepcopy(existing)
    batch = {"task": "identity_reclassify", "replace_candidate_ids": [existing[0]["candidate_id"]],
             "required_answer_labels": ["15"], "crop_bbox": box(.35, .05, .4, .9),
             "candidates": [candidate("identity", box(.4, .2, .15, .1), identity="姓名")]}
    applications = _apply_repair_batches(existing, [batch], payload, [], page, evidence)
    assert applications == [{"task": "identity_reclassify", "applied": False,
                             "reason": "REPAIR_REQUIRED_ANSWER_MISSING"}]
    assert existing == before
