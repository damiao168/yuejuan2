import base64
import copy
import hashlib
import io
import json
import socket
import threading
from contextlib import nullcontext
from types import SimpleNamespace
from unittest.mock import Mock, patch
from urllib import error as urlerror
from urllib import request as urlrequest

import cv2
import numpy as np
import pytest
from grading_agent.answer_sheet_vision import (
    VisionInputError,
    decode_display_page,
    detect_printed_frames,
    snap_to_printed_frame,
)
from grading_agent.app import GradingAgentApplication
from grading_agent.errors import AgentError
from grading_agent.managed_model import paper_model, public_json_transport
from grading_agent.server import GradingAgentHTTPServer
from grading_agent.template_analysis import TemplateAnalyzer
from grading_agent.template_analysis_coarse import coarse_model_output_schema
from grading_agent.template_analysis_model import prepare_template_model
from grading_agent.template_analysis_schema import SCHEMA_VERSION
from helpers import FakeModel, settings
from PIL import Image


def test_compatible_coarse_response_does_not_gain_legacy_nullable_fields():
    # 真实粗阶段响应曾被兼容层补入旧编号/关联字段，继而被 additionalProperties 整页拒绝。
    supplied = {"candidates": [{"role": "answer", "bbox": {"x": .1, "y": .2, "width": .3, "height": .4},
        "labels": ["15"], "identity_label": None, "static_label": None, "option_pattern": "none",
        "identity_pattern": "none", "barcode_pattern": "none", "boundary_basis": "printed_frame", "anchor_bbox": None}]}
    response = {"choices": [{"finish_reason": "stop", "message": {
        "role": "assistant", "content": json.dumps(supplied)}}]}
    send = Mock(return_value=response)
    adapter = prepare_template_model(SimpleNamespace(settings=settings(adapter_type="openai_compatible"), transport=send))
    with adapter.session("coarse-compatible-regression"):
        output = adapter.request_structured("coarse-compatible-regression", [], coarse_model_output_schema(),
                                            "answer_sheet_template_analysis")
    assert output == supplied
    assert "candidate_id" not in output["candidates"][0]
    assert "printed_question_label" not in output["candidates"][0]
    assert json.loads(response["choices"][0]["message"]["content"]) == supplied


def test_schema_failure_reports_only_fixed_field_and_constraint_without_student_value():
    request = payload()
    output = output_for(request)
    output["candidates"][0]["identity_label"] = "private-student-value"
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert "MODEL_SCHEMA_IDENTITY_LABEL_ANYOF" in result["reason_codes"]
    assert "private-student-value" not in json.dumps(result)


def test_repeated_model_candidate_has_distinct_bounded_failure_reason():
    request = payload()
    output = output_for(request)
    output["candidates"].append(copy.deepcopy(output["candidates"][0]))
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert "MODEL_REPEATED_CANDIDATE" in result["reason_codes"]


def page_image(written=False):
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.rectangle(image, (60, 100), (540, 340), (0, 0, 0), 2)
    cv2.rectangle(image, (60, 400), (540, 720), (0, 0, 0), 2)
    cv2.putText(image, "17", (75, 130), cv2.FONT_HERSHEY_SIMPLEX, 0.7, (0, 0, 0), 2)
    cv2.putText(image, "18", (75, 430), cv2.FONT_HERSHEY_SIMPLEX, 0.7, (0, 0, 0), 2)
    if written:
        cv2.putText(image, "x=2", (160, 200), cv2.FONT_HERSHEY_SIMPLEX, 1.5, (0, 0, 0), 3)
        cv2.polylines(image, [np.array([[150, 500], [200, 450], [250, 520], [300, 470]])], False, (0, 0, 0), 3)
    return image


def payload(image=None, candidates=None):
    image = page_image() if image is None else image
    raw = cv2.imencode(".png", image)[1].tobytes()
    return {
        "request_id": "template-analysis-test-0001", "schema_version": SCHEMA_VERSION,
        "page": {"page_no": 1, "width": image.shape[1], "height": image.shape[0],
                 "media_type": "image/png", "data_base64": base64.b64encode(raw).decode(),
                 "sha256": hashlib.sha256(raw).hexdigest()},
        "candidates": detect_printed_frames(image, 1) if candidates is None else candidates,
        "question_labels": ["17", "18"], "writing_state": "blank",
    }


def output_for(request):
    return {"candidates": [{
        "page_no": request["page"]["page_no"], "candidate_id": candidate["candidate_id"],
        "printed_question_label": str(index + 17), "role": "answer", "proposed_bbox": dict(candidate["bbox"]),
        "identity_label": None,
        "association_status": "associated", "evidence_refs": list(candidate["evidence_refs"]), "reason_codes": [],
    } for index, candidate in enumerate(request["candidates"])]}


class StructuredModel:
    def __init__(self, result=None, error=None):
        self.result = result
        self.error = error
        self.calls = []
        self.settings = settings(model_version="test-vision-v1", model_name="test-vision", provider_key="test-provider")

    def session(self, request_id):
        return nullcontext()

    def request_structured(self, request_id, messages, schema, name):
        self.calls.append((request_id, messages, schema, name))
        if self.error:
            raise self.error
        return copy.deepcopy(self.result)

    def last_usage(self):
        return {"input_tokens": 100, "output_tokens": 20}


def test_objective_group_covers_choice_rows_without_associating_a_single_question():
    request = payload(unframed_page(), candidates=[])
    request["question_labels"] = ["1", "2", "3", "17"]
    request["objective_question_labels"] = ["1", "2", "3"]
    block = {"x": .08, "y": .15, "width": .4, "height": .2}
    group = discovered_row("1", block, "objective_group")
    row = discovered_row("1", {"x": .08, "y": .18, "width": .4, "height": .03})
    result = TemplateAnalyzer(StructuredModel({"candidates": [group, row]}), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert len(result["candidates"]) == 1
    candidate = result["candidates"][0]
    assert candidate["role"] == "uncertain"
    assert candidate["semantic_check"] == "needs_review"
    assert candidate["printed_question_label"] is None
    assert candidate["identity_label"] is None
    assert candidate["association_status"] == "unassociated"
    assert candidate["proposed_bbox"] == block
    assert "REGION_CONFLICT" not in candidate["reason_codes"]


def test_choice_row_cannot_become_a_single_question_without_grid_review():
    request = payload(unframed_page(), candidates=[])
    request["question_labels"] = ["1", "2"]
    request["objective_question_labels"] = ["1", "2"]
    row = discovered_row("1")
    result = TemplateAnalyzer(StructuredModel({"candidates": [row]}), prompt_variant="v6").analyze(request)
    candidate = result["candidates"][0]
    assert candidate["role"] == "uncertain"
    assert candidate["association_status"] == "unassociated"
    assert "OBJECTIVE_QUESTION_REQUIRES_GROUP" in candidate["reason_codes"]


def test_objective_group_can_correct_a_candidate_that_only_covers_one_row():
    request = payload(unframed_page(), candidates=[{
        "candidate_id": "choice-row", "bbox": {"x": .08, "y": .2, "width": .4, "height": .03},
        "evidence_refs": ["local-frame:choice-row"],
    }])
    block = {"x": .08, "y": .15, "width": .4, "height": .2}
    group = discovered_row(None, block, "objective_group")
    group.update(candidate_id="choice-row", evidence_refs=["local-frame:choice-row"])
    result = TemplateAnalyzer(StructuredModel({"candidates": [group]}), prompt_variant="v6").analyze(request)
    assert result["candidates"][0]["proposed_bbox"] == block


def test_prompt_requires_whole_choice_area_and_passes_server_question_types():
    request = payload()
    request["objective_question_labels"] = ["17"]
    model = StructuredModel(output_for(request))
    TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    system = model.calls[0][1][0]["content"]
    manifest = json.loads(model.calls[0][1][1]["content"][0]["text"])
    assert "objective_group" in system
    assert "不把整行多道题误作一道题" in system
    assert manifest["objective_question_labels"] == ["17"]


@pytest.mark.parametrize("written", [False, True])
def test_geometry_detects_complete_frames_including_blank_answer_space(written):
    frames = detect_printed_frames(page_image(written), 1)
    assert len(frames) == 2
    assert frames[0]["bbox"]["x"] == pytest.approx(59 / 600, abs=2 / 600)
    assert frames[0]["bbox"]["width"] > 0.79
    assert frames[0]["bbox"]["height"] > 0.29
    assert frames == detect_printed_frames(page_image(written), 1)


def test_geometry_does_not_create_blank_space_or_handwriting_boxes_without_frames():
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.putText(image, "17 x=2", (60, 100), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    assert detect_printed_frames(image, 1) == []
    result = TemplateAnalyzer(prompt_variant="v6").analyze(payload(image, candidates=[]))
    assert result["candidates"] == []
    assert "NO_GEOMETRY_CANDIDATES" in result["reason_codes"]
    assert result["semantic_status"] == "not_configured"
    assert result["model_usage"]["request_count"] == 0


def test_no_model_returns_geometry_for_review_without_guessing_question_numbers():
    result = TemplateAnalyzer(prompt_variant="v6").analyze(payload(candidates=[]))
    assert result["status"] == "review_required"
    assert result["semantic_status"] == "not_configured"
    assert len(result["candidates"]) == 2
    assert all(row["printed_question_label"] is None for row in result["candidates"])
    assert all(row["association_status"] == "unassociated" for row in result["candidates"])


def test_semantics_gets_original_image_and_only_generates_review_candidates():
    request = payload()
    original = copy.deepcopy(request)
    model = StructuredModel(output_for(request))
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert request == original
    assert result["semantic_status"] == "completed"
    assert result["status"] == "review_required"
    assert [row["printed_question_label"] for row in result["candidates"]] == ["17", "18"]
    assert result["source_sha256"] == request["page"]["sha256"]
    assert result["coordinate_space"] == "source_display_normalized"
    assert result["model_usage"]["total_tokens"] == 120
    image = model.calls[0][1][1]["content"][1]["image_url"]
    assert image["url"].endswith(request["page"]["data_base64"])
    assert image["detail"] == "high"


@pytest.mark.parametrize("change", ["unknown_candidate", "unknown_page", "unknown_evidence", "nonfinite", "overflow", "duplicate", "extra_field", "invalid_role"])
def test_invalid_model_evidence_is_discarded_and_geometry_survives(change):
    request = payload()
    output = output_for(request)
    row = output["candidates"][0]
    if change == "unknown_candidate":
        row["candidate_id"] = "invented"
    elif change == "unknown_page":
        row["page_no"] = 2
    elif change == "unknown_evidence":
        row["evidence_refs"] = ["invented-evidence"]
    elif change == "nonfinite":
        row["proposed_bbox"]["x"] = float("nan")
    elif change == "overflow":
        row["proposed_bbox"]["width"] = 1
    elif change == "duplicate":
        output["candidates"][1] = copy.deepcopy(row)
    elif change == "extra_field":
        row["question_id"] = "invented-question-id"
    else:
        row["role"] = "official_answer"
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert result["semantic_error"]["code"] == "model_output_invalid"
    assert len(result["candidates"]) == len(request["candidates"])
    assert all(row["printed_question_label"] is None for row in result["candidates"])


def test_model_failure_keeps_geometry_and_attempt_usage_without_error_text():
    error = AgentError("model_unavailable", "secret-provider-response", status=503, retryable=True)
    result = TemplateAnalyzer(StructuredModel(error=error), prompt_variant="v6").analyze(payload())
    assert result["semantic_status"] == "failed"
    assert result["semantic_error"] == {"code": "model_unavailable", "retryable": True}
    assert result["model_usage"]["request_count"] == 1
    assert "secret-provider-response" not in json.dumps(result)


@pytest.mark.parametrize("provider_code", ["model_not_found", "private-provider-error-echo"])
def test_semantic_failure_keeps_only_safe_upstream_diagnostics(provider_code):
    error = AgentError("model_request_rejected", "private-provider-response", status=502,
                       upstream_http_status=400, upstream_error_code=provider_code)
    result = TemplateAnalyzer(StructuredModel(error=error), prompt_variant="v6").analyze(payload())
    assert result["semantic_status"] == "failed"
    expected = {"code": "model_request_rejected", "retryable": False, "upstream_http_status": 400}
    if provider_code == "model_not_found":
        expected["upstream_error_code"] = provider_code
    assert result["semantic_error"] == expected
    assert "private-provider" not in json.dumps(result)
    assert all(row["association_status"] == "unassociated" for row in result["candidates"])


def test_model_session_queue_failure_preserves_geometry_without_a_provider_attempt():
    model = StructuredModel()

    def unavailable_session(_request_id):
        raise AgentError("model_queue_timeout", "private-queue-detail", status=503, retryable=True)

    model.session = unavailable_session
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(payload())
    assert result["semantic_status"] == "failed"
    assert result["model_usage"]["request_count"] == 0
    assert len(result["candidates"]) == 2


def test_duplicate_question_regions_are_retained_as_ambiguous():
    request = payload()
    output = output_for(request)
    output["candidates"][1]["printed_question_label"] = "17"
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    assert len(result["candidates"]) == 2
    assert all(row["association_status"] == "ambiguous" for row in result["candidates"])
    assert all("MULTIPLE_REGIONS_FOR_QUESTION" in row["reason_codes"] for row in result["candidates"])


def test_identity_regions_and_unknown_labels_cannot_become_question_associations():
    request = payload()
    output = output_for(request)
    output["candidates"][0]["role"] = "identity"
    output["candidates"][1]["printed_question_label"] = "999"
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    assert all(row["association_status"] == "unassociated" for row in result["candidates"])
    assert "QUESTION_LABEL_NOT_IN_LIST" in result["candidates"][1]["reason_codes"]


def test_identity_fields_keep_static_labels_and_do_not_echo_student_values_as_question_labels():
    request = payload(unframed_page(written=True), candidates=[])
    request["writing_state"] = "written"
    name = discovered_row("测试学生甲", {"x": .05, "y": .02, "width": .35, "height": .05}, "identity")
    name["identity_label"] = "姓名"
    number = discovered_row("2026001234", {"x": .45, "y": .02, "width": .45, "height": .05}, "identity")
    number["identity_label"] = "准考证号"
    model = StructuredModel({"candidates": [name, number]})
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert all(row["identity_label"] is None for row in result["candidates"])
    assert all(row["semantic_check"] == "needs_review" for row in result["candidates"])
    assert all(row["printed_question_label"] is None for row in result["candidates"])
    assert all(row["association_status"] == "unassociated" for row in result["candidates"])
    assert all("IDENTITY_LAYOUT_REQUIRES_REVIEW" in row["reason_codes"] for row in result["candidates"])
    assert "测试学生甲" not in json.dumps(result, ensure_ascii=False)
    assert "2026001234" not in json.dumps(result, ensure_ascii=False)
    system = model.calls[0][1][0]["content"]
    assert "所有编号格" in system and "不要抄录学生实际姓名" in system


def test_identity_null_category_remains_generic_and_answer_cannot_carry_identity_label():
    request = payload(unframed_page(), candidates=[])
    identity = discovered_row(None, {"x": .05, "y": .02, "width": .7, "height": .07}, "identity")
    answer = discovered_row()
    answer["identity_label"] = "学号"
    result = TemplateAnalyzer(StructuredModel({"candidates": [identity, answer]}), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert result["candidates"][0]["identity_label"] is None
    assert result["candidates"][0]["role"] == "uncertain"
    assert result["candidates"][1]["identity_label"] is None


@pytest.mark.parametrize("student_value", ["测试学生甲", "2026001234", "学号：2026001234"])
def test_identity_field_category_rejects_personal_values(student_value):
    request = payload()
    output = output_for(request)
    output["candidates"][0].update(role="identity", identity_label=student_value)
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert result["semantic_error"]["code"] == "model_output_invalid"
    assert student_value not in json.dumps(result, ensure_ascii=False)


def test_full_page_semantics_can_ignore_false_positive_geometry_hints():
    request = payload()
    output = output_for(request)
    output["candidates"].pop()
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert len(result["candidates"]) == 1
    assert result["candidates"][0]["candidate_id"] == request["candidates"][0]["candidate_id"]


def test_full_exam_labels_do_not_require_missing_questions_on_this_page():
    request = payload()
    request["question_labels"] = [str(number) for number in range(1, 20)]
    model = StructuredModel(output_for(request))
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert [row["printed_question_label"] for row in result["candidates"]] == ["17", "18"]
    assert len(model.calls) == 1
    system = model.calls[0][1][0]["content"]
    manifest = json.loads(model.calls[0][1][1]["content"][0]["text"])
    assert manifest["question_labels_scope"] == "entire_exam_allowlist_only"
    assert "严禁为覆盖题号清单而均分页面" in system
    assert "不是本页必须出现的题号集合" in system


@pytest.mark.parametrize("claimed_verified", [False, True])
def test_uniform_full_exam_partition_without_printed_frames_requires_review(claimed_verified):
    # 复现整场 1～19 题被模型均分到一页的形状；假模型只验证保护规则，不证明识图准确率。
    request = payload(unframed_page(), candidates=[])
    request["question_labels"] = [str(number) for number in range(1, 20)]
    rows = [discovered_row(str(number), {"x": .05, "y": round(number * .05, 4), "width": .9, "height": .05})
            for number in range(1, 20)]
    if claimed_verified:
        for row in rows:
            row["reason_codes"] = ["PRINTED_FRAME_VERIFIED"]
    model = StructuredModel({"candidates": rows})
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert result["model_usage"]["request_count"] == 1
    assert len(result["candidates"]) == 19
    assert "SUSPECTED_SYNTHETIC_PAGE_PARTITION" in result["reason_codes"]
    assert all(row["association_status"] == "ambiguous" for row in result["candidates"])
    assert all("SUSPECTED_SYNTHETIC_PAGE_PARTITION" in row["reason_codes"] for row in result["candidates"])
    assert [row["proposed_bbox"] for row in result["candidates"]] == [row["proposed_bbox"] for row in rows]


def test_real_nineteen_row_printed_table_keeps_supported_associations():
    image = np.full((800, 600, 3), 255, np.uint8)
    for number in range(19):
        cv2.rectangle(image, (30, 40 + number * 38), (570, 40 + (number + 1) * 38), (0, 0, 0), 2)
    request = payload(image, candidates=[])
    request["question_labels"] = [str(number) for number in range(1, 20)]
    rows = [discovered_row(str(number + 1), {"x": .05, "y": (40 + number * 38) / 800,
                                           "width": .9, "height": 38 / 800}) for number in range(19)]
    result = TemplateAnalyzer(StructuredModel({"candidates": rows}), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert len(result["candidates"]) == 19
    assert all("PRINTED_FRAME_VERIFIED" in row["reason_codes"] for row in result["candidates"])
    assert all(row["association_status"] == "associated" for row in result["candidates"])
    assert "SUSPECTED_SYNTHETIC_PAGE_PARTITION" not in result["reason_codes"]


@pytest.mark.parametrize("layout", ["three_columns", "ten_unequal_rows", "short_uniform_rows"])
def test_ordinary_unframed_layouts_are_not_rejected_as_full_page_partition(layout):
    if layout == "three_columns":
        boxes = [{"x": .04 + number * .32, "y": .1, "width": .28, "height": .8} for number in range(3)]
        labels = ["17", "18", "19"]
    else:
        heights = [.05, .11] * 5 if layout == "ten_unequal_rows" else [.04] * 10
        boxes, top = [], .05
        for height in heights:
            boxes.append({"x": .05, "y": round(top, 4), "width": .9, "height": height})
            top += height
        labels = [str(number + 1) for number in range(10)]
    request = payload(unframed_page(), candidates=[])
    request["question_labels"] = labels
    rows = [discovered_row(label, box) for label, box in zip(labels, boxes)]
    result = TemplateAnalyzer(StructuredModel({"candidates": rows}), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert len(result["candidates"]) == len(boxes)
    assert all(row["association_status"] == "associated" for row in result["candidates"])
    assert "SUSPECTED_SYNTHETIC_PAGE_PARTITION" not in result["reason_codes"]


def unframed_page(written=False):
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.putText(image, "Name: ______", (40, 45), cv2.FONT_HERSHEY_SIMPLEX, 0.6, (0, 0, 0), 1)
    cv2.putText(image, "17", (60, 120), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (0, 0, 0), 2)
    cv2.putText(image, "18", (60, 410), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (0, 0, 0), 2)
    if written:
        cv2.putText(image, "x=2", (100, 180), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (0, 0, 0), 2)
    return image


def discovered_row(label="17", bbox=None, role="answer"):
    return {
        "page_no": 1, "candidate_id": None, "printed_question_label": label, "role": role,
        "identity_label": None,
        "proposed_bbox": bbox or {"x": 0.08, "y": 0.13, "width": 0.84, "height": 0.35},
        "association_status": "associated" if role == "answer" else "unassociated", "evidence_refs": [], "reason_codes": [],
    }


@pytest.mark.parametrize("written", [False, True])
def test_unframed_page_is_discovered_by_one_full_page_multimodal_call(written):
    # 实际无闭合框图像触发过去完全跳过模型的路径；结构化假模型只验证调度和信任边界。
    request = payload(unframed_page(written), candidates=[])
    request["writing_state"] = "written" if written else "blank"
    model = StructuredModel({"candidates": [
        discovered_row(role="identity", label=None, bbox={"x": 0.05, "y": 0.02, "width": 0.7, "height": 0.07}),
        discovered_row(),
        discovered_row("18", {"x": 0.08, "y": 0.5, "width": 0.84, "height": 0.44}),
    ]})
    assert detect_printed_frames(unframed_page(written), 1) == []
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert result["status"] == "review_required"
    assert result["model_usage"]["request_count"] == 1
    assert len(model.calls) == 1
    assert len(result["candidates"]) == 3
    identity, first, second = result["candidates"]
    assert identity["role"] == "uncertain" and identity["association_status"] == "unassociated"
    assert [first["printed_question_label"], second["printed_question_label"]] == ["17", "18"]
    assert first["association_status"] == "associated"
    # 留白作答空间由模型建议保留，不能重新被 CV 缺框或单张手写范围覆盖。
    assert first["proposed_bbox"]["height"] == 0.35
    assert first["source_bbox"] == first["proposed_bbox"]
    assert "MODEL_DISCOVERED_REGION" in first["reason_codes"]
    assert "NO_PRINTED_FRAME_REQUIRES_REVIEW" in first["reason_codes"]
    assert any(ref.startswith("source-page:") for ref in first["evidence_refs"])
    if written:
        assert "HANDWRITING_LAYOUT_REQUIRES_REVIEW" in first["reason_codes"]
    manifest = json.loads(model.calls[0][1][1]["content"][0]["text"])
    assert manifest["candidates"] == []
    assert manifest["page_evidence_ref"] in first["evidence_refs"]


def test_model_discovers_missing_frame_and_keeps_geometry_as_optional_hint():
    request = payload()
    partial_frames = [copy.deepcopy(request["candidates"][0])]
    request["candidates"] = partial_frames
    output = output_for(request)
    output["candidates"].append(discovered_row("18", {"x": 0.1, "y": 0.5, "width": 0.8, "height": 0.4}))
    model = StructuredModel(output)
    with patch("grading_agent.template_analysis.detect_printed_frames", return_value=partial_frames):
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert len(result["candidates"]) == 2
    assert result["candidates"][1]["printed_question_label"] == "18"
    assert "MODEL_DISCOVERED_REGION" in result["candidates"][1]["reason_codes"]
    assert len(model.calls) == 1


def test_model_can_replace_false_geometry_without_reintroducing_it_as_a_conflict():
    request = payload(unframed_page(), candidates=[{
        "candidate_id": "bad-full-page-frame", "bbox": {"x": 0.01, "y": 0.01, "width": 0.98, "height": 0.98},
        "evidence_refs": ["local-frame:bad"],
    }])
    result = TemplateAnalyzer(StructuredModel({"candidates": [discovered_row()]}), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert len(result["candidates"]) == 1
    assert result["candidates"][0]["candidate_id"].startswith("model-1-")
    assert "REGION_CONFLICT" not in result["candidates"][0]["reason_codes"]


def test_identity_correction_is_not_reset_to_the_original_large_geometry_hint():
    request = payload(unframed_page(), candidates=[{
        "candidate_id": "broad-header", "bbox": {"x": .02, "y": .01, "width": .95, "height": .1},
        "evidence_refs": ["local-frame:header"],
    }])
    box = {"x": .05, "y": .02, "width": .3, "height": .05}
    row = discovered_row(None, box, "identity")
    row.update(candidate_id="broad-header", identity_label="姓名", evidence_refs=["local-frame:header"])
    result = TemplateAnalyzer(StructuredModel({"candidates": [row]}), prompt_variant="v6").analyze(request)
    candidate = result["candidates"][0]
    assert result["semantic_status"] == "completed"
    assert candidate["proposed_bbox"] == box
    assert candidate["source_bbox"] == request["candidates"][0]["bbox"]
    assert "GEOMETRY_NOT_VERIFIED" in candidate["reason_codes"]


def test_small_identity_field_does_not_snap_to_a_larger_nearby_header_frame():
    # 原吸附容差按整页计算；高度只占 5% 的身份栏会被扩展到相邻边界，现按字段自身尺寸收紧。
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.rectangle(image, (60, 80), (540, 128), (0, 0, 0), 2)
    request = payload(image, candidates=[])
    box = {"x": .1, "y": .1, "width": .8, "height": .05}
    frames = detect_printed_frames(image, 1)
    assert snap_to_printed_frame(box, frames)[2] == "PRINTED_FRAME_VERIFIED"
    row = discovered_row(None, box, "identity")
    row["identity_label"] = "学号"
    result = TemplateAnalyzer(StructuredModel({"candidates": [row]}), prompt_variant="v6").analyze(request)
    assert result["candidates"][0]["proposed_bbox"] == box
    assert "NO_PRINTED_FRAME_REQUIRES_REVIEW" in result["candidates"][0]["reason_codes"]


def test_identity_field_with_matching_printed_frame_keeps_cv_evidence():
    request = payload()
    output = output_for(request)
    output["candidates"] = output["candidates"][:1]
    output["candidates"][0].update(role="identity", identity_label="身份信息")
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    candidate = result["candidates"][0]
    assert result["semantic_status"] == "completed"
    assert candidate["proposed_bbox"] == request["candidates"][0]["bbox"]
    assert "PRINTED_FRAME_VERIFIED" in candidate["reason_codes"]


def test_server_assigns_stable_discovery_ids_independent_of_model_order():
    request = payload(unframed_page(), candidates=[])
    rows = [discovered_row(), discovered_row("18", {"x": 0.08, "y": 0.5, "width": 0.84, "height": 0.44})]
    first = TemplateAnalyzer(StructuredModel({"candidates": rows}), prompt_variant="v6").analyze(request)
    second = TemplateAnalyzer(StructuredModel({"candidates": rows[::-1]}), prompt_variant="v6").analyze(request)
    assert {row["printed_question_label"]: row["candidate_id"] for row in first["candidates"]} == {
        row["printed_question_label"]: row["candidate_id"] for row in second["candidates"]
    }


@pytest.mark.parametrize("change", ["nonfinite", "overflow", "zero_width", "negative", "unknown_evidence", "invented_id", "duplicate"])
def test_untrusted_discoveries_fail_closed_instead_of_creating_template_regions(change):
    request = payload(unframed_page(), candidates=[])
    output = {"candidates": [discovered_row()]}
    row = output["candidates"][0]
    if change == "nonfinite":
        row["proposed_bbox"]["x"] = float("nan")
    elif change == "overflow":
        row["proposed_bbox"]["width"] = 1
    elif change == "zero_width":
        row["proposed_bbox"]["width"] = 0
    elif change == "negative":
        row["proposed_bbox"]["y"] = -0.1
    elif change == "unknown_evidence":
        row["evidence_refs"] = ["model-invented-proof"]
    elif change == "invented_id":
        row["candidate_id"] = "model-invented-id"
    elif change == "duplicate":
        output["candidates"].append(copy.deepcopy(row))
    else:
        output["candidates"] = []
    model = StructuredModel(output)
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert result["semantic_error"]["code"] == "model_output_invalid"
    assert result["candidates"] == []
    assert result["model_usage"]["request_count"] == 1


def test_unframed_unknown_question_and_conflicting_discoveries_require_review():
    request = payload(unframed_page(), candidates=[])
    result = TemplateAnalyzer(StructuredModel({"candidates": [
        discovered_row("999"), discovered_row("18", {"x": 0.1, "y": 0.3, "width": 0.8, "height": 0.4}),
    ]}), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    first, second = result["candidates"]
    assert "QUESTION_LABEL_NOT_IN_LIST" in first["reason_codes"]
    assert first["association_status"] != "associated"
    assert second["association_status"] == "unassociated"
    assert second["role"] == "uncertain"
    assert second["semantic_check"] == "needs_review"
    assert all("REGION_CONFLICT" in row["reason_codes"] for row in result["candidates"])


def test_unframed_supplier_failure_is_visible_without_a_local_grading_model_fallback():
    error = AgentError("model_unavailable", "private-provider-error", status=503, retryable=True)
    request = payload(unframed_page(), candidates=[])
    result = TemplateAnalyzer(StructuredModel(error=error), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert result["semantic_error"] == {"code": "model_unavailable", "retryable": True}
    assert result["candidates"] == []
    assert "private-provider-error" not in json.dumps(result)
    assert result["model_usage"]["request_count"] == 1


def test_handwriting_sized_model_bbox_cannot_shrink_the_printed_template_frame():
    request = payload(page_image(written=True))
    output = output_for(request)
    output["candidates"][0]["proposed_bbox"] = {"x": 0.25, "y": 0.2, "width": 0.2, "height": 0.05}
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    row = result["candidates"][0]
    assert row["proposed_bbox"] == row["source_bbox"]
    assert "GEOMETRY_NOT_VERIFIED" in row["reason_codes"]
    assert "MODEL_GEOMETRY_REQUIRES_REVIEW" in row["reason_codes"]


def test_model_cannot_move_a_candidate_to_another_genuine_frame():
    request = payload()
    output = output_for(request)
    output["candidates"][0]["proposed_bbox"] = dict(request["candidates"][1]["bbox"])
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    row = result["candidates"][0]
    assert row["proposed_bbox"] == row["source_bbox"]
    assert "MODEL_GEOMETRY_DISAGREEMENT" in row["reason_codes"]


def test_supplied_geometry_snaps_only_with_nearby_printed_frame_evidence():
    frames = detect_printed_frames(page_image(), 1)
    bbox = dict(frames[0]["bbox"])
    bbox["x"] += 0.005
    refined, refs, reason = snap_to_printed_frame(bbox, frames)
    assert refined == frames[0]["bbox"]
    assert refs == frames[0]["evidence_refs"]
    assert reason == "PRINTED_FRAME_VERIFIED"


@pytest.mark.parametrize("change", ["checksum", "dimensions", "not_image", "duplicate", "nonfinite", "out_of_bounds", "unexpected_field"])
def test_invalid_inputs_fail_before_calling_the_model(change):
    request = payload()
    if change == "checksum":
        request["page"]["sha256"] = "0" * 64
    elif change == "dimensions":
        request["page"]["width"] += 1
    elif change == "not_image":
        raw = b"not an image"
        request["page"].update(data_base64=base64.b64encode(raw).decode(), sha256=hashlib.sha256(raw).hexdigest())
    elif change == "duplicate":
        request["candidates"].append(copy.deepcopy(request["candidates"][0]))
    elif change == "nonfinite":
        request["candidates"][0]["bbox"]["x"] = float("nan")
    elif change == "out_of_bounds":
        request["candidates"][0]["bbox"]["width"] = 1
    else:
        request["publish"] = True
    model = StructuredModel()
    with pytest.raises(AgentError) as caught:
        TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert caught.value.status == 400
    assert model.calls == []


def test_exif_orientation_is_rejected_instead_of_silently_changing_coordinates():
    image = Image.new("RGB", (100, 200), "white")
    exif = Image.Exif()
    exif[274] = 6
    buffer = io.BytesIO()
    image.save(buffer, format="JPEG", exif=exif)
    with pytest.raises(VisionInputError):
        decode_display_page(buffer.getvalue(), {"width": 100, "height": 200, "media_type": "image/jpeg"})


def test_managed_document_model_reuses_image_transport_without_exposing_credentials():
    request = payload()
    config = {"adapter_type": "openai_compatible", "base_url": "https://vision.example.test/v1",
              "api_key": "school-model-api-key-at-least-16", "model_name": "vision-model",
              "model_version": "vision-test-v1", "provider_key": "vision-provider"}
    request["managed_model"] = config
    app = SimpleNamespace(settings=settings(), model=object())
    model = prepare_template_model(paper_model(app, request))
    response = {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps(output_for(request))}}],
                "usage": {"prompt_tokens": 20, "completion_tokens": 10}}
    with patch("grading_agent.managed_model.public_json_transport", return_value=response) as send:
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert result["model_identity"]["model_version"] == "vision-test-v1"
    assert config["api_key"] not in json.dumps(result)
    transmitted = send.call_args.args[1]
    assert transmitted["messages"][1]["content"][1]["type"] == "image_url"
    assert transmitted["messages"][1]["content"][1]["image_url"]["detail"] == "high"
    assert transmitted["max_tokens"] >= 8192
    assert send.call_count == 1
    assert config["api_key"] not in json.dumps(transmitted)


@pytest.mark.parametrize("field", ["candidate_id", "printed_question_label", "identity_label"])
def test_compatible_identity_region_accepts_omitted_nullable_metadata_without_guessing(field):
    request = payload()
    row = output_for(request)["candidates"][0]
    row.update(candidate_id=None, printed_question_label=None, identity_label="姓名", role="identity", association_status="unassociated")
    row["evidence_refs"] = [f"source-page:{request['page']['sha256']}:{request['page']['page_no']}"]
    row.pop(field)
    response = {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps({"candidates": [row]})}}]}
    source = SimpleNamespace(settings=settings(adapter_type="openai_compatible"), transport=Mock(return_value=response))
    result = TemplateAnalyzer(prepare_template_model(source), prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert result["candidates"][0]["printed_question_label"] is None
    assert result["candidates"][0]["association_status"] == "unassociated"
    # 空值补齐只解决协议兼容；缺少本地图像证据时仍不能把模型类别当成已核验事实。
    assert result["candidates"][0]["identity_label"] is None
    assert result["candidates"][0]["semantic_check"] == "needs_review"
    assert field not in row


@pytest.mark.parametrize("field", ["role", "proposed_bbox"])
def test_compatible_identity_metadata_repair_cannot_invent_role_or_coordinates(field):
    request = payload()
    row = output_for(request)["candidates"][0]
    row.pop(field)
    response = {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps({"candidates": [row]})}}]}
    source = SimpleNamespace(settings=settings(adapter_type="openai_compatible"), transport=Mock(return_value=response))
    assert TemplateAnalyzer(prepare_template_model(source), prompt_variant="v6").analyze(request)["semantic_status"] == "failed"


def test_compatible_template_model_rejects_truncated_json_with_known_usage():
    request = payload()
    config = {"adapter_type": "openai_compatible", "base_url": "https://vision.example.test/v1",
              "api_key": "school-model-api-key-at-least-16", "model_name": "vision-model", "model_version": "v1"}
    request["managed_model"] = config
    app = SimpleNamespace(settings=settings(), model=object())
    response = {"choices": [{"finish_reason": "length", "message": {
        "role": "assistant", "content": json.dumps(output_for(request)),
    }}], "usage": {"prompt_tokens": 20, "completion_tokens": 10}}
    with patch("grading_agent.managed_model.public_json_transport", return_value=response) as send:
        model = prepare_template_model(paper_model(app, request))
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "failed"
    assert result["model_usage"]["total_tokens"] == 30
    assert result["model_usage"]["request_count"] == 1
    assert send.call_count == 1
    assert send.call_args.args[1]["max_tokens"] >= 8192
    assert all(row["printed_question_label"] is None for row in result["candidates"])


def test_compatible_template_budget_is_local_to_the_single_template_request():
    request = payload()
    response = {"choices": [{"finish_reason": "stop", "message": {
        "role": "assistant", "content": json.dumps(output_for(request)),
    }}], "usage": {"prompt_tokens": 10, "completion_tokens": 20}}
    transport = Mock(return_value=response)
    original_settings = settings(adapter_type="openai_compatible", model_max_output_tokens=512)
    source = SimpleNamespace(settings=original_settings, transport=transport)
    model = prepare_template_model(source)
    result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert transport.call_count == 1
    assert transport.call_args.args[1]["max_tokens"] == 8192
    assert source.settings.model_max_output_tokens == 512
    assert model.settings.model_max_output_tokens == 512


@pytest.mark.parametrize("finish_reason", ["stop", "length"])
def test_native_template_model_uses_real_multimodal_protocol_and_keeps_failed_usage(finish_reason):
    request = payload()
    config = {"adapter_type": "dashscope_native", "base_url": "https://dashscope.aliyuncs.com/api/v1",
              "api_key": "school-model-api-key-at-least-16", "model_name": "qwen3-vl-plus",
              "model_version": "vision-test-v1", "provider_key": "dashscope"}
    request["managed_model"] = config
    app = SimpleNamespace(settings=settings(), model=object())
    response = {"request_id": "native-template-test-1", "output": {"choices": [{
        "finish_reason": finish_reason, "message": {"role": "assistant", "content": [{
            "text": json.dumps(output_for(request)),
        }]},
    }]}, "usage": {"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}}
    with patch("grading_agent.managed_model.public_json_transport", return_value=response) as send:
        model = prepare_template_model(paper_model(app, request))
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    url, body, headers, _ = send.call_args.args
    assert url == "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
    assert body["input"]["messages"][0]["role"] == "system"
    assert body["input"]["messages"][1]["content"][1]["image"].endswith(request["page"]["data_base64"])
    assert "image_url" not in json.dumps(body)
    assert body["parameters"]["enable_thinking"] is False
    assert body["parameters"]["max_tokens"] >= 8192
    assert headers["Authorization"] == "Bearer " + config["api_key"]
    assert config["api_key"] not in json.dumps(body)
    assert config["api_key"] not in json.dumps(result)
    assert result["model_usage"]["total_tokens"] == 120
    assert result["semantic_status"] == ("completed" if finish_reason == "stop" else "failed")


def test_native_visual_transport_uses_image_budget_while_text_keeps_its_budget():
    response = Mock(status=200)
    connection = Mock()
    connection.getresponse.return_value = response
    resolved = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", 443))]
    image = "data:image/png;base64," + "A" * 2_100_000
    native = {"input": {"messages": [{"role": "user", "content": [{"image": image}]}]}}
    with (
        patch("socket.getaddrinfo", return_value=resolved),
        patch("http.client.HTTPSConnection", return_value=connection),
        patch("grading_agent.managed_model.read_json_response", return_value=b"{}"),
    ):
        assert public_json_transport("https://dashscope.aliyuncs.com/api/v1", native, {}, 1) == {}
        with pytest.raises(AgentError) as caught:
            public_json_transport("https://dashscope.aliyuncs.com/api/v1", {"messages": [{"role": "user", "content": image}]}, {}, 1)
    assert caught.value.status == 413
    connection.request.assert_called_once()


@pytest.fixture
def http_server():
    app = GradingAgentApplication(settings(), model=FakeModel())
    server = GradingAgentHTTPServer(("127.0.0.1", 0), app)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}", app
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


def test_internal_analysis_http_requires_service_auth_and_does_not_require_grading_idempotency(http_server):
    base_url, app = http_server
    request = urlrequest.Request(f"{base_url}/answer-sheet/analyze", data=json.dumps(payload(candidates=[])).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
    with pytest.raises(urlerror.HTTPError) as caught:
        urlrequest.urlopen(request, timeout=3)
    assert caught.value.code == 401
    request.add_header("Authorization", f"Bearer {app.settings.service_token}")
    with urlrequest.urlopen(request, timeout=3) as response:
        result = json.loads(response.read())
    assert result["status"] == "review_required"
    assert result["semantic_status"] == "not_configured"
    assert len(result["candidates"]) == 2


def test_internal_analysis_http_uses_request_scoped_managed_vision_model(http_server, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT", "v6")
    base_url, app = http_server
    body = payload()
    model = StructuredModel(output_for(body))
    body["managed_model"] = {"injected_by_gateway": True}

    def select_model(_app, request_payload):
        request_payload.pop("managed_model")
        return model

    request = urlrequest.Request(f"{base_url}/answer-sheet/analyze", data=json.dumps(body).encode(), headers={
        "Content-Type": "application/json", "Authorization": f"Bearer {app.settings.service_token}",
    }, method="POST")
    with (
        patch("grading_agent.server.paper_model", side_effect=select_model),
        urlrequest.urlopen(request, timeout=3) as response,
    ):
        result = json.loads(response.read())
    assert result["semantic_status"] == "completed"
    assert len(model.calls) == 1
