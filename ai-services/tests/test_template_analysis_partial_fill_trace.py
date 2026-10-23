"""仅重放真实模型响应和实录坐标，重建修复前状态；原始像素与独立 OCR 不在此验收。"""

import copy
import json
from pathlib import Path

import numpy as np
from grading_agent.template_analysis import _apply_repair_batches
from grading_agent.template_analysis_coarse import expand_coarse_output
from grading_agent.template_analysis_repair import _restore_rows
from grading_agent.template_analysis_semantics import LocalContentEvidence

FIXTURE = Path(__file__).with_name("fixtures") / "template_fill_partial_trace_5896b78c.json"


def replay_recorded_fill_geometry(*, restore_pre_repair_roles=True):
    recorded = json.loads(FIXTURE.read_text(encoding="utf-8"))
    page = {**recorded["page"], "sha256": recorded["source_sha256"]}
    payload = {"request_id": "trace-geometry-only-5896b78c", "page": page,
               "question_labels": ["12", "13", "14"], "fill_blank_question_labels": ["12", "13", "14"],
               "objective_question_labels": [], "writing_state": "unknown", "candidates": []}
    existing = copy.deepcopy(recorded["recorded_final_rows"])
    parent_id = recorded["repair_batch"]["replace_candidate_ids"][0]
    for candidate in existing:
        candidate.update(page_no=page["page_no"], objective_items=[], evidence_refs=[])
        if candidate["candidate_id"] == parent_id:
            candidate["_fill_parent_labels"] = list(recorded["parent_observed_labels"])
        elif restore_pre_repair_roles:
            # trace 是最终摘要；局部题框在首轮修复前尚未被覆盖查漏降成 uncertain。
            # 这里只恢复已知阶段状态，不修改实录题号、坐标或三项本地证据。
            candidate["role"] = "answer"
            suffix = candidate["candidate_id"].rsplit("-", 1)[1]
            candidate["evidence_refs"] = [f"local-fill:{recorded['fill_region_version']}:{suffix}"]
        if restore_pre_repair_roles:
            candidate["reason_codes"] = [reason for reason in candidate["reason_codes"] if reason not in {
                "REGION_CONFLICT", "SEMANTIC_REGION_OVERLAP_CONFLICT", "PAGE_COVERAGE_ROLE_CONFLICT"}]
    expanded = expand_coarse_output(recorded["coarse_repair_response"], payload)["candidates"]
    for candidate, supplied in zip(expanded, recorded["coarse_repair_response"]["candidates"]):
        candidate["objective_items"] = copy.deepcopy(supplied["objective_items"])
        candidate.pop("_coarse_layout", None)
    # 和真实修复路径使用同一裁图坐标还原，不把响应里的比例框直接当整页位置。
    fresh = _restore_rows(expanded, recorded["repair_batch"]["crop_bbox"], payload)
    batch = {**copy.deepcopy(recorded["repair_batch"]), "candidates": fresh}
    before, batch_before = copy.deepcopy(existing), copy.deepcopy(batch)
    placeholder = np.full((page["height"], page["width"], 3), 255, np.uint8)
    evidence = LocalContentEvidence(placeholder, page, ocr=lambda _image: ([], "SEMANTIC_OCR_UNAVAILABLE"),
                                    barcode=lambda _image: None)
    applications = _apply_repair_batches(existing, [batch], payload, [], page, evidence)
    assert batch == batch_before
    return recorded, existing, before, applications, fresh


def test_recorded_source_partial_fill_repair_keeps_measured_children_and_adds_actual_model_14():
    recorded, existing, before, applications, fresh = replay_recorded_fill_geometry()
    assert recorded["source_sha256"] == "6b961b0be6d1561bcd4e2ab66fa5914e6483bad9bd03820747630301353d8285"
    assert applications == [{"task": "mixed_region", "applied": True,
                             "fill_reconciliation": {"preserved_local_fields": 2}, "candidate_count": 3}]
    assert [row["printed_question_label"] for row in existing] == ["12", "13", "14"]
    assert all(row["semantic_check"] == "supported" and row["association_status"] == "associated" for row in existing)
    for measured in before[1:]:
        current = next(row for row in existing if row["printed_question_label"] == measured["printed_question_label"])
        assert current["proposed_bbox"] == measured["proposed_bbox"]
        assert set(measured["evidence_refs"]) <= set(current["evidence_refs"])
    assert existing[-1]["proposed_bbox"] == fresh[-1]["proposed_bbox"]


def test_final_uncertain_trace_state_is_not_silently_treated_as_pre_repair_local_evidence():
    _recorded, existing, before, applications, _fresh = replay_recorded_fill_geometry(restore_pre_repair_roles=False)
    assert applications[0]["applied"] is False
    assert applications[0]["reason"] == "REPAIR_COMBINED_LAYOUT_CONFLICT"
    assert existing == before
