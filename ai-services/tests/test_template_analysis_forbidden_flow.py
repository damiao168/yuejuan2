import copy
import json
from unittest.mock import Mock

import grading_agent.template_analysis as analysis
import numpy as np
import pytest
from grading_agent.errors import AgentError
from grading_agent.template_analysis_forbidden import (
    FORBIDDEN_REGION_VERSION,
    review_forbidden_regions,
)
from grading_agent.template_analysis_semantics import LocalContentEvidence
from test_template_analysis import StructuredModel, discovered_row, payload
from test_template_analysis_forbidden import evidence


def request_and_bad_barcode():
    request = payload(np.full((800, 600, 3), 255, np.uint8), candidates=[])
    request.update(question_labels=["12", "15"], fill_blank_question_labels=["12"])
    row = discovered_row(None, {"x": .1, "y": .2, "width": .2, "height": .15}, "identity")
    row["identity_label"] = "条码"
    return request, row


def empty_content(image, page):
    return LocalContentEvidence(image, page, ocr=lambda image: ([], None), barcode=lambda image: None)


def test_forbidden_review_runs_after_barcode_downgrade_and_partition_before_local_fill(monkeypatch):
    request, row = request_and_bad_barcode()
    stages = []
    class Content(LocalContentEvidence):
        def review(self, rows, payload):
            assert rows[0]["role"] == "identity" and rows[0]["identity_label"] == "条码"
            super().review(rows, payload)
            assert rows[0]["role"] == "uncertain" and rows[0]["identity_label"] is None
            stages.append("content")
    def content(image, page):
        return Content(image, page, ocr=lambda image: ([], None), barcode=lambda image: None)
    def partition(rows, labels, frames):
        assert rows[0]["role"] == "uncertain"
        stages.append("partition")
        return False
    def forbidden(image, page, rows, payload):
        assert rows[0]["role"] == "uncertain"
        stages.append("forbidden")
        return review_forbidden_regions(image, page, rows, payload, ocr=lambda png, timeout: evidence())
    def fill(image, page, frames, labels, **_kwargs):
        stages.append("fill")
        assert labels == ["12"]
        return [], []
    monkeypatch.setattr(analysis, "review_synthetic_page_partition", partition)
    monkeypatch.setattr(analysis, "review_forbidden_regions", forbidden)
    monkeypatch.setattr(analysis, "discover_fill_regions", fill)
    result = analysis.TemplateAnalyzer(StructuredModel({"candidates": [row]}), content, prompt_variant="v6").analyze(request)
    assert stages == ["content", "partition", "forbidden", "fill"]
    assert result["semantic_status"] == "completed" and result["status"] == "review_required"
    # API 保留说明区及证据，画布再按 role 过滤；后端不能把已核验禁区重新降回可关联未知框。
    assert len(result["candidates"]) == 1
    candidate = result["candidates"][0]
    assert candidate["role"] == "instruction" and candidate["semantic_check"] == "supported"
    assert candidate["printed_question_label"] is None and candidate["association_status"] == "unassociated"
    assert "LOCAL_FORBIDDEN_LABEL_VERIFIED" in candidate["reason_codes"]
    assert candidate["proposed_bbox"] == row["proposed_bbox"]


@pytest.mark.parametrize("failure", ["unconfigured", "supplier_failed", "invalid_output"])
def test_no_successful_semantic_result_never_runs_forbidden_or_fill_recovery(monkeypatch, failure):
    request, row = request_and_bad_barcode()
    model = None
    if failure == "supplier_failed":
        model = StructuredModel(error=AgentError("model_unavailable", "PRIVATE", status=503))
    elif failure == "invalid_output":
        invalid = copy.deepcopy(row)
        invalid["role"] = "invented_role"
        model = StructuredModel({"candidates": [invalid]})
    forbidden = Mock(side_effect=AssertionError("forbidden must not run"))
    fill = Mock(side_effect=AssertionError("fill must not run"))
    monkeypatch.setattr(analysis, "review_forbidden_regions", forbidden)
    monkeypatch.setattr(analysis, "discover_fill_regions", fill)
    result = analysis.TemplateAnalyzer(model, empty_content, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == ("not_configured" if failure == "unconfigured" else "failed")
    assert result["candidates"] == []
    forbidden.assert_not_called()
    fill.assert_not_called()


@pytest.mark.parametrize("status", ["failed", "unavailable"])
def test_ocr_runtime_failure_keeps_unknown_barcode_and_supported_answer(monkeypatch, status):
    request, barcode = request_and_bad_barcode()
    answer = discovered_row("15", {"x": .45, "y": .2, "width": .5, "height": .35})
    def content(image, page):
        return LocalContentEvidence(image, page, ocr=lambda image: ([{
            "text": "15.", "x": .46, "y": .21, "width": .03, "height": .02,
        }], None), barcode=lambda image: None)
    calls = []
    def reader(data, *, timeout):
        calls.append(timeout)
        return {"status": status, "reason_code": "PRIVATE_OCR_DETAILS"}
    def forbidden(image, page, rows, payload):
        return review_forbidden_regions(image, page, rows, payload, ocr=reader)
    monkeypatch.setattr(analysis, "review_forbidden_regions", forbidden)
    monkeypatch.setattr(analysis, "discover_fill_regions", lambda *args, **kwargs: ([], []))
    result = analysis.TemplateAnalyzer(StructuredModel({"candidates": [barcode, answer]}), content, prompt_variant="v6").analyze(request)
    unknown, supported = result["candidates"]
    assert unknown["role"] == "uncertain" and unknown["semantic_check"] == "needs_review"
    assert supported["role"] == "answer" and supported["semantic_check"] == "supported"
    assert supported["printed_question_label"] == "15" and supported["association_status"] == "associated"
    assert calls == [3] and len(result["candidates"]) == 2
    code = "LOCAL_FORBIDDEN_OCR_UNAVAILABLE" if status == "unavailable" else "LOCAL_FORBIDDEN_OCR_FAILED"
    assert code in result["reason_codes"] and "PRIVATE" not in json.dumps(result)


def test_forbidden_version_is_exported_in_response_and_controlled_diagnostics(monkeypatch, tmp_path):
    request, row = request_and_bad_barcode()
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setattr(analysis, "review_forbidden_regions", lambda *args: [])
    monkeypatch.setattr(analysis, "discover_fill_regions", lambda *args, **kwargs: ([], []))
    result = analysis.TemplateAnalyzer(StructuredModel({"candidates": [row]}), empty_content, prompt_variant="v6").analyze(request)
    trace = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf8"))
    assert result["versions"]["forbidden_regions"] == FORBIDDEN_REGION_VERSION
    assert trace["metadata"]["versions"]["forbidden_regions"] == FORBIDDEN_REGION_VERSION


@pytest.mark.parametrize("fill_left", [.4, .28])
def test_forbidden_instruction_and_missing_fill_suggestion_remain_distinct(monkeypatch, fill_left):
    request, barcode = request_and_bad_barcode()
    local = {"candidate_id": "local-fill-1-test", "page_no": 1, "role": "answer", "identity_label": None,
             "printed_question_label": "12", "proposed_bbox": {"x": fill_left, "y": .2, "width": .3, "height": .1},
             "source_bbox": {"x": fill_left, "y": .2, "width": .3, "height": .1}, "origin": "local_detector",
             "association_status": "unassociated", "semantic_check": "needs_review",
             "evidence_refs": ["local-fill:test"], "reason_codes": ["LOCAL_FILL_REQUIRES_REVIEW"]}
    def forbidden(image, page, rows, payload):
        return review_forbidden_regions(image, page, rows, payload, ocr=lambda png, timeout: evidence())
    monkeypatch.setattr(analysis, "review_forbidden_regions", forbidden)
    monkeypatch.setattr(analysis, "discover_fill_regions", lambda *args, **kwargs: ([copy.deepcopy(local)], []))
    result = analysis.TemplateAnalyzer(StructuredModel({"candidates": [barcode]}), empty_content, prompt_variant="v6").analyze(request)
    instruction, fill = result["candidates"]
    # 局部填空的边缘余量可能碰到说明牌；它不能把已排除的禁区重新变成待纠错候选。
    assert instruction["role"] == "instruction" and instruction["semantic_check"] == "supported"
    assert "REGION_CONFLICT" not in instruction["reason_codes"]
    assert fill == local and fill["printed_question_label"] == "12"
    assert fill["semantic_check"] == "needs_review" and fill["association_status"] == "unassociated"


@pytest.mark.parametrize("variant", ["v6", "v7-evidence"])
@pytest.mark.parametrize("remaining_conflict", ["none", "active_answer", "unverified_warning", "model_caution"])
def test_final_visible_regions_define_conflicts_after_local_forbidden_exclusion(monkeypatch, variant, remaining_conflict):
    request, warning = request_and_bad_barcode()
    request.update(question_labels=["12", "15", "16"])
    answer = discovered_row("15", {"x": .28, "y": .2, "width": .6, "height": .3})
    if remaining_conflict == "model_caution":
        answer["reason_codes"] = ["REGION_CONFLICT"]
    supplied = [warning, answer]
    if remaining_conflict == "active_answer":
        supplied.append(discovered_row("16", {"x": .5, "y": .2, "width": .45, "height": .3}))
    if variant == "v7-evidence":
        for row in supplied:
            is_warning = row is warning
            row.update(objective_items=[], observed_question_labels=[] if is_warning else [row["printed_question_label"]],
                       visual_evidence={"static_label": None if is_warning else "解答题", "option_pattern": "none",
                                        "identity_pattern": "none", "barcode_pattern": "unknown" if is_warning else "none",
                                        "boundary_basis": "printed_frame", "anchor_bbox": None})
    def content(image, page):
        return LocalContentEvidence(image, page, ocr=lambda _: ([
            {"text": "15.", "x": .29, "y": .21, "width": .03, "height": .02},
            {"text": "16.", "x": .51, "y": .21, "width": .03, "height": .02},
        ], None), barcode=lambda _: None)
    def forbidden(image, page, rows, payload):
        result = ({"status": "unavailable", "reason_code": "LOCAL_STATIC_OCR_UNAVAILABLE"}
                  if remaining_conflict == "unverified_warning" else evidence())
        return review_forbidden_regions(image, page, rows, payload, ocr=lambda png, timeout: result)
    monkeypatch.setattr(analysis, "review_forbidden_regions", forbidden)
    monkeypatch.setattr(analysis, "discover_fill_regions", lambda *args, **kwargs: ([], []))
    result = analysis.TemplateAnalyzer(StructuredModel({"candidates": supplied}), content, prompt_variant=variant).analyze(request)
    excluded, final_answer = result["candidates"][:2]
    # 禁区排除发生在局部内容核验之后；只撤销隐藏区造成的伪冲突，不能清掉其他作答区或模型自报的冲突。
    if remaining_conflict != "unverified_warning":
        assert excluded["role"] == "instruction" and excluded["semantic_check"] == "supported"
        assert "REGION_CONFLICT" not in excluded["reason_codes"]
    if remaining_conflict == "none":
        assert (final_answer["role"], final_answer["association_status"], final_answer["semantic_check"]) == (
            "answer", "associated", "supported")
        assert "REGION_CONFLICT" not in final_answer["reason_codes"]
    else:
        assert final_answer["semantic_check"] == "needs_review"
        assert final_answer["association_status"] != "associated"
        if remaining_conflict == "model_caution":
            assert "MODEL_CAUTION_REQUIRES_REVIEW" in final_answer["reason_codes"]
        else:
            assert "REGION_CONFLICT" in final_answer["reason_codes"]
            assert "SEMANTIC_REGION_OVERLAP_CONFLICT" in final_answer["reason_codes"]
