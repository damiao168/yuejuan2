"""固定响应只验证版本/清理/隐私边界，不代表 v7 的真实模型识别质量。"""

import base64
import copy
import hashlib
import json
import logging
from contextlib import nullcontext
from types import SimpleNamespace
from unittest.mock import Mock

import cv2
import numpy as np
import pytest
from grading_agent.errors import AgentError
from grading_agent.template_analysis import TemplateAnalyzer
from grading_agent.template_analysis_schema import SCHEMA_VERSION
from grading_agent.template_analysis_semantics import LocalContentEvidence

V6_SYSTEM_SHA256 = "088644693ed68bc951f1dfdee34351a8b55ef73f61cc99341be846873262a5ee"
PRIVATE_VALUE = "PRIVATE_STUDENT_946281"
PUBLIC_CANDIDATE_FIELDS = {
    "candidate_id", "page_no", "printed_question_label", "identity_label", "role", "origin",
    "source_bbox", "proposed_bbox", "association_status", "evidence_refs", "reason_codes", "semantic_check",
    "objective_items",
}


class ReplayModel:
    def __init__(self, result=None, error=None):
        self.result, self.error = result, error
        self.calls = []
        self.settings = SimpleNamespace(provider_key="synthetic", model_name="contract-replay", model_version="fixed-v1")

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, _request_id, messages, schema, _name):
        self.calls.append({"messages": copy.deepcopy(messages), "schema": copy.deepcopy(schema)})
        if self.error:
            raise self.error
        return copy.deepcopy(self.result)

    def last_usage(self):
        return {}


def request():
    # 不含姓名、笔迹或真实模板像素；几何提示只给契约重放使用。
    image = np.full((300, 400, 3), 255, np.uint8)
    raw = cv2.imencode(".png", image)[1].tobytes()
    return {
        "request_id": "v7-contract-replay-0001", "schema_version": SCHEMA_VERSION,
        "page": {"page_no": 1, "width": 400, "height": 300, "media_type": "image/png",
                 "data_base64": base64.b64encode(raw).decode(), "sha256": hashlib.sha256(raw).hexdigest()},
        "candidates": [{"candidate_id": "synthetic-hint-1", "bbox": {
            "x": .1, "y": .1, "width": .7, "height": .6}, "evidence_refs": ["synthetic-frame:1"]}],
        "question_labels": ["1", "2", "12", "15", "16"], "objective_question_labels": ["1", "2"],
        "fill_blank_question_labels": ["12"], "writing_state": "blank",
    }


def response(role="objective_group", label=None):
    supplied = request()["candidates"][0]
    return {"candidates": [{
        "candidate_id": supplied["candidate_id"], "page_no": 1, "role": role,
        "printed_question_label": label, "identity_label": "姓名" if role == "identity" else None,
        "proposed_bbox": copy.deepcopy(supplied["bbox"]),
        "association_status": "associated" if role == "answer" and label else "unassociated",
        "evidence_refs": copy.deepcopy(supplied["evidence_refs"]), "reason_codes": [],
    }]}


def with_observations(supplied=None, *, labels=None, static="选择题", options="bubble_options",
                      identity="none", barcode="none", anchor=None):
    supplied = copy.deepcopy(supplied if supplied is not None else response())
    supplied["candidates"][0].update(observed_question_labels=labels if labels is not None else ["1", "2"],
                                     visual_evidence={
                                         "static_label": static, "option_pattern": options,
                                         "identity_pattern": identity, "barcode_pattern": barcode,
                                         "boundary_basis": "printed_frame", "anchor_bbox": anchor,
                                     })
    observed = supplied["candidates"][0]["observed_question_labels"]
    supplied["candidates"][0]["objective_items"] = ([{
        "printed_question_label": label,
        "bbox": {"x": .12, "y": .15 + index * .15, "width": .6, "height": .1},
        "options": [{"label": name, "bbox": {"x": .2 + position * .1, "y": .17 + index * .15,
                                                "width": .06, "height": .05}}
                    for position, name in enumerate("ABCD")],
    } for index, label in enumerate(observed)] if supplied["candidates"][0]["role"] == "objective_group" else [])
    return supplied


def no_external_evidence(image, page):
    # 缺少 OCR 不等于模型没看清；保留实际自动采用逻辑，并区分模型支持与独立 OCR 支持。
    return LocalContentEvidence(image, page, ocr=lambda _image: ([], "SEMANTIC_OCR_UNAVAILABLE"), barcode=lambda _image: None)


@pytest.fixture(autouse=True)
def isolate_external_tools(monkeypatch):
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT", raising=False)
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", raising=False)
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", raising=False)
    monkeypatch.setattr("grading_agent.template_analysis.detect_printed_frames", lambda *_args: [])
    monkeypatch.setattr("grading_agent.template_analysis.review_forbidden_regions", lambda *_args: [])
    monkeypatch.setattr("grading_agent.template_analysis.discover_fill_regions", lambda *_args, **_kwargs: ([], []))


@pytest.mark.parametrize("variant", ["v6", "v7-text"])
def test_legacy_response_replays_under_legacy_output_contract_without_internal_public_fields(variant):
    model = ReplayModel(response())
    result = TemplateAnalyzer(model, no_external_evidence, prompt_variant=variant).analyze(request())
    assert result["semantic_status"] == "completed" and result["status"] == "review_required"
    assert len(result["candidates"]) == 1
    assert set(result["candidates"][0]) <= PUBLIC_CANDIDATE_FIELDS
    assert result["candidates"][0]["semantic_check"] != "supported"
    assert result["candidates"][0]["origin"] == "model"


def test_explicit_v6_overrides_environment_and_preserves_original_system_bytes(monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT", "v7-evidence")
    model = ReplayModel(response())
    result = TemplateAnalyzer(model, no_external_evidence, prompt_variant="v6").analyze(request())
    system = model.calls[0]["messages"][0]["content"]
    assert hashlib.sha256(system.encode()).hexdigest() == V6_SYSTEM_SHA256
    assert result["versions"]["prompt"] == "answer-sheet-full-page-layout-v6"
    assert result["semantic_status"] == "completed"


@pytest.mark.parametrize("variant", ["v6", "v7-text", "v7-evidence"])
def test_supplier_failure_retains_geometry_without_private_error_text(variant, caplog):
    model = ReplayModel(error=AgentError("model_unavailable", PRIVATE_VALUE, status=503, retryable=True))
    with caplog.at_level(logging.INFO):
        result = TemplateAnalyzer(model, no_external_evidence, prompt_variant=variant).analyze(request())
    assert result["semantic_status"] == "failed"
    assert len(result["candidates"]) == 1
    assert result["candidates"][0]["proposed_bbox"] == request()["candidates"][0]["bbox"]
    assert result["candidates"][0]["association_status"] == "unassociated"
    assert PRIVATE_VALUE not in json.dumps(result) and PRIVATE_VALUE not in caplog.text


@pytest.mark.parametrize("variant", ["v6", "v7-text"])
def test_personal_value_as_question_label_cannot_escape_public_or_default_diagnostics(variant, tmp_path, monkeypatch, caplog):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    model = ReplayModel(response("identity", PRIVATE_VALUE))
    with caplog.at_level(logging.INFO):
        result = TemplateAnalyzer(model, no_external_evidence, prompt_variant=variant).analyze(request())
    assert result["semantic_status"] == "completed"
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert PRIVATE_VALUE not in json.dumps(result) and PRIVATE_VALUE not in json.dumps(record)
    assert PRIVATE_VALUE not in caplog.text and "responses" not in record


@pytest.mark.parametrize("variant", ["v6", "v7-text"])
def test_self_reported_verified_reason_cannot_mark_candidate_supported(variant):
    supplied = response("answer", "15")
    supplied["candidates"][0]["reason_codes"] = ["SEMANTIC_QUESTION_LABEL_VERIFIED"]
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence, prompt_variant=variant).analyze(request())
    assert result["semantic_status"] == "completed"
    assert result["candidates"][0]["semantic_check"] != "supported"


def test_default_selection_uses_evidence_prompt_and_automatically_accepts_consistent_content():
    model = ReplayModel(with_observations())
    result = TemplateAnalyzer(model, no_external_evidence).analyze(request())
    assert result["versions"]["prompt"] == "answer-sheet-full-page-layout-v7-evidence-auto-map"
    assert result["candidates"][0]["semantic_check"] == "supported"
    assert len(result["candidates"][0]["objective_items"]) == 2


def test_invalid_operator_prompt_selection_fails_with_fixed_503_before_model_call(monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT", PRIVATE_VALUE)
    model = ReplayModel(response())
    with pytest.raises(AgentError) as rejected:
        TemplateAnalyzer(model, no_external_evidence)
    assert rejected.value.code == "model_unavailable" and rejected.value.status == 503
    assert PRIVATE_VALUE not in json.dumps(rejected.value.payload())
    assert model.calls == []


@pytest.mark.parametrize("labels", [["15"], ["15", "16"]])
def test_observed_nonobjective_labels_survive_null_single_label_cleanup_as_conflict(labels):
    supplied = with_observations(labels=labels)
    assert supplied["candidates"][0]["printed_question_label"] is None
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    assert result["semantic_status"] == "completed"
    row = result["candidates"][0]
    assert "MODEL_OBS_OBJECTIVE_QUESTION_TYPE_CONFLICT" in row["reason_codes"]
    assert row["role"] == "uncertain" and row["semantic_check"] == "needs_review"
    assert row["association_status"] == "unassociated" and row["identity_label"] is None
    assert set(row) <= PUBLIC_CANDIDATE_FIELDS


@pytest.mark.parametrize("case", ["choice_as_name", "warning_as_barcode"])
def test_self_reported_category_contradictions_cannot_be_repromoted_by_local_stage(case):
    if case == "choice_as_name":
        supplied = with_observations(response("identity"), labels=[], static="姓名", identity="name_line")
        expected = "MODEL_OBS_IDENTITY_OPTION_CONFLICT"
    else:
        supplied = response("identity")
        supplied["candidates"][0]["identity_label"] = "条码"
        supplied = with_observations(supplied, labels=[], static="禁止作答", options="none")
        expected = "MODEL_OBS_WARNING_CATEGORY_CONFLICT"
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    row = result["candidates"][0]
    assert expected in row["reason_codes"]
    assert row["semantic_check"] == "needs_review" and row["role"] == "uncertain"
    assert row["proposed_bbox"] == request()["candidates"][0]["bbox"]
    assert set(row) <= PUBLIC_CANDIDATE_FIELDS


def test_conflicted_observations_do_not_trigger_local_positive_identity_reclassification():
    supplied = with_observations(response("identity"), labels=[], static="姓名", identity="name_line")
    ocr = Mock(return_value=([{"text": "姓名", "x": .2, "y": .2, "width": .1, "height": .1}], None))

    def evidence(image, page):
        return LocalContentEvidence(image, page, ocr=ocr, barcode=lambda _image: None)

    result = TemplateAnalyzer(ReplayModel(supplied), evidence, prompt_variant="v7-evidence").analyze(request())
    assert result["candidates"][0]["role"] == "uncertain"
    assert result["candidates"][0]["semantic_check"] == "needs_review"
    ocr.assert_not_called()


@pytest.mark.parametrize("anchor,reason", [
    ({"x": 1.01, "y": .1, "width": .01, "height": .01}, "MODEL_OBS_ANCHOR_OUT_OF_PAGE"),
    ({"x": .9, "y": .9, "width": .05, "height": .05}, "MODEL_OBS_ANCHOR_OUTSIDE_REGION"),
])
def test_observation_anchor_problems_mark_region_for_review_without_hiding_geometry(anchor, reason):
    supplied = with_observations(anchor=anchor)
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    assert result["semantic_status"] == "completed" and len(result["candidates"]) == 1
    row = result["candidates"][0]
    assert row["semantic_check"] == "needs_review" and reason in row["reason_codes"]
    assert row["proposed_bbox"] == request()["candidates"][0]["bbox"]


def test_consistent_model_observations_are_usable_without_claiming_independent_ocr_support():
    model = ReplayModel(with_observations())
    result = TemplateAnalyzer(model, no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    assert result["semantic_status"] == "completed"
    assert result["candidates"][0]["semantic_check"] == "supported"
    assert "MULTIMODAL_CONTENT_CONSISTENT" in result["candidates"][0]["reason_codes"]
    assert not any(ref.startswith("local-semantic:") for ref in result["candidates"][0]["evidence_refs"])
    assert set(result["candidates"][0]) <= PUBLIC_CANDIDATE_FIELDS


@pytest.mark.parametrize("reason", [
    "CONTENT_FORBIDDEN_WRITING", "SEMANTIC_FORBIDDEN_AREA_DETECTED", "LOCAL_FORBIDDEN_LABEL_VERIFIED",
    "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED", "REGION_CONFLICT", "SEMANTIC_MULTIPART_UNSUPPORTED",
    "MODEL_OBS_MULTIPLE_QUESTION_LABELS", "OBJECTIVE_QUESTION_REQUIRES_GROUP",
    "SEMANTIC_CONTENT_CONFLICT", "MODEL_OBS_IDENTITY_OPTION_CONFLICT",
    "MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT",
])
@pytest.mark.parametrize("position", [0, 16, 31])
def test_model_counterevidence_survives_reason_budget_before_automatic_acceptance(reason, position):
    supplied = with_observations(response("answer", "15"), labels=["15"], static="解答题", options="none")
    # 模型允许返回 32 项，公开结果仅保留前 16 项自报码；尾部反证也必须先阻止自动采用。
    codes = [f"MODEL_NOTE_{index:02d}" for index in range(31)]
    codes.insert(position, reason)
    supplied["candidates"][0]["reason_codes"] = codes

    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())
    row = result["candidates"][0]
    assert result["semantic_status"] == "completed"
    assert row["semantic_check"] == "needs_review" and row["association_status"] != "associated"
    assert "MODEL_CAUTION_REQUIRES_REVIEW" in row["reason_codes"]
    assert "MODEL_REASON_CODES_TRUNCATED" in row["reason_codes"]
    assert "MULTIMODAL_CONTENT_CONSISTENT" not in row["reason_codes"]


@pytest.mark.parametrize("reason", [
    "SEMANTIC_OCR_UNAVAILABLE", "SEMANTIC_OCR_FAILED", "SEMANTIC_EVIDENCE_INSUFFICIENT",
    "SEMANTIC_CHECK_BUDGET_EXCEEDED", "MODEL_OBS_OBJECTIVE_ITEMS_MISSING", "MODEL_OBS_ANCHOR_MISSING",
])
def test_model_diagnostic_without_counterevidence_keeps_automatic_question_association(reason):
    supplied = with_observations(response("answer", "15"), labels=["15"], static="解答题", options="none")
    supplied["candidates"][0]["reason_codes"] = [reason]

    row = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())["candidates"][0]
    assert (row["role"], row["association_status"], row["semantic_check"]) == ("answer", "associated", "supported")
    assert "MODEL_CAUTION_REQUIRES_REVIEW" not in row["reason_codes"]


def test_valid_choice_mapping_keeps_each_formal_question_and_option_coordinates_for_automatic_apply():
    supplied = with_observations()
    model = ReplayModel(supplied)
    result = TemplateAnalyzer(model, no_external_evidence).analyze(request())
    row = result["candidates"][0]
    assert result["versions"]["prompt"].endswith("v7-evidence-auto-map")
    assert row["role"] == "objective_group" and row["semantic_check"] == "supported"
    assert row["objective_items"] == supplied["candidates"][0]["objective_items"]
    assert [item["printed_question_label"] for item in row["objective_items"]] == ["1", "2"]
    assert len(model.calls) == 1


@pytest.mark.parametrize("label,static", [("12", "填空题"), ("15", "解答题")])
def test_formal_nonchoice_question_is_automatically_associated_when_local_ocr_is_missing(label, static):
    supplied = with_observations(response("answer", label), labels=[label], static=static, options="none")
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())
    row = result["candidates"][0]
    assert (row["role"], row["association_status"], row["semantic_check"]) == ("answer", "associated", "supported")
    assert row["printed_question_label"] == label
    assert "SEMANTIC_OCR_UNAVAILABLE" in row["reason_codes"]
    assert row["objective_items"] == []


@pytest.mark.parametrize("identity,static", [("班级", "Class"), ("学校", "School")])
def test_actual_class_or_school_label_remains_automatically_usable_when_ocr_is_unavailable(identity, static):
    supplied = with_observations(response("identity"), labels=[], static=static, options="none", identity="identity_fields")
    supplied["candidates"][0]["identity_label"] = identity
    row = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())["candidates"][0]
    assert (row["role"], row["identity_label"], row["semantic_check"]) == ("identity", identity, "supported")
    assert "MULTIMODAL_CONTENT_CONSISTENT" in row["reason_codes"]
    assert "SEMANTIC_OCR_UNAVAILABLE" in row["reason_codes"]
    assert not any(ref.startswith("local-semantic:") for ref in row["evidence_refs"])


def test_live_false_identity_rows_with_entire_frame_anchors_do_not_become_supported_without_ocr():
    # 重放真实故障的静态响应结构；图片仍为合成白页，不把本测试当作真实识别质量评测。
    requested = request()
    requested["candidates"] = []
    supplied = {"candidates": []}
    for index, (identity, pattern, y, height) in enumerate([
        ("姓名", "name_line", .33127208480565373, .08745583038869258),
        ("准考证号", "number_grid", .41872791519434627, .08303886925795052),
    ], 1):
        box = {"x": .066875, "y": y, "width": .28125, "height": height}
        candidate = with_observations(response("identity"), labels=[], static=identity,
                                      options="none", identity=pattern, anchor=copy.deepcopy(box))["candidates"][0]
        candidate.update(candidate_id=f"page-1-frame-{index}", identity_label=identity, proposed_bbox=copy.deepcopy(box))
        requested["candidates"].append({"candidate_id": candidate["candidate_id"], "bbox": copy.deepcopy(box),
                                         "evidence_refs": copy.deepcopy(candidate["evidence_refs"])})
        supplied["candidates"].append(candidate)
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(requested)
    assert len(result["candidates"]) == 2
    for candidate in result["candidates"]:
        assert candidate["role"] == "uncertain"
        assert candidate["semantic_check"] == "needs_review"
        assert "MODEL_OBS_IDENTITY_LABEL_ANCHOR_NOT_LOCALIZED" in candidate["reason_codes"]
        assert "MULTIMODAL_CONTENT_CONSISTENT" not in candidate["reason_codes"]


@pytest.mark.parametrize("frame_kind", ["none", "original_large", "matching", "neighbor"])
def test_consistent_answer_uses_multimodal_geometry_instead_of_the_original_large_hint(frame_kind, monkeypatch):
    supplied = with_observations(response("answer", "12"), labels=["12"], static="填空题", options="none")
    proposal = {"x": .12, "y": .45, "width": .6, "height": .1}
    matching = {"x": .121, "y": .451, "width": .599, "height": .099}
    neighbor = {**proposal, "y": .461}
    supplied["candidates"][0]["proposed_bbox"] = proposal
    boxes = {"none": [], "original_large": [request()["candidates"][0]["bbox"]],
             "matching": [matching], "neighbor": [neighbor]}[frame_kind]
    frames = [{"candidate_id": f"printed-{index}", "bbox": box, "evidence_refs": [f"cv-frame:{index}"]}
              for index, box in enumerate(boxes)]
    monkeypatch.setattr("grading_agent.template_analysis.detect_printed_frames", lambda *_args: frames)

    row = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())["candidates"][0]
    assert row["source_bbox"] == request()["candidates"][0]["bbox"]
    assert row["proposed_bbox"] == (matching if frame_kind == "matching" else proposal)
    assert row["semantic_check"] == "supported" and row["association_status"] == "associated"
    assert ("PRINTED_FRAME_VERIFIED" in row["reason_codes"]) is (frame_kind == "matching")
    assert ("cv-frame:0" in row["evidence_refs"]) is (frame_kind == "matching")


def test_preserving_multimodal_answer_geometry_does_not_suppress_a_real_cross_question_overlap():
    supplied = with_observations(response("answer", "12"), labels=["12"], static="填空题", options="none")
    proposal = {"x": .12, "y": .45, "width": .6, "height": .1}
    supplied["candidates"][0]["proposed_bbox"] = proposal
    other = with_observations(response("answer", "15"), labels=["15"], static="解答题", options="none")["candidates"][0]
    other.update(candidate_id=None, evidence_refs=[], proposed_bbox=proposal)
    supplied["candidates"].append(other)

    rows = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())["candidates"]
    assert len(rows) == 2
    assert all(row["proposed_bbox"] == proposal for row in rows)
    assert all(row["association_status"] != "associated" and row["semantic_check"] == "needs_review" for row in rows)
    assert all("REGION_CONFLICT" in row["reason_codes"] for row in rows)
    assert all("SEMANTIC_REGION_OVERLAP_CONFLICT" in row["reason_codes"] for row in rows)


def test_isolated_warning_misclassified_as_barcode_is_excluded_without_a_second_ocr_engine():
    supplied = response("identity")
    supplied["candidates"][0].update(candidate_id=None, identity_label="条码",
                                      evidence_refs=[],
                                      proposed_bbox={"x": .2, "y": .2, "width": .12, "height": .1})
    supplied = with_observations(supplied, labels=[], static="禁止作答", options="none")
    row = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())["candidates"][0]
    assert row["role"] == "instruction"
    assert "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED" in row["reason_codes"]
    assert "REGION_ROLE_UNCERTAIN" not in row["reason_codes"]


@pytest.mark.parametrize("initial_role", ["answer", "identity", "uncertain"])
def test_warning_inside_large_geometry_hint_never_expands_back_or_blocks_the_real_fill_answer(initial_role):
    supplied = response(initial_role)
    warning_box = {"x": .2, "y": .2, "width": .12, "height": .1}
    supplied["candidates"][0].update(proposed_bbox=warning_box, identity_label="条码" if initial_role == "identity" else None)
    supplied = with_observations(supplied, labels=[], static="禁止作答", options="none")
    answer = with_observations(response("answer", "12"), labels=["12"], static="填空题", options="none")["candidates"][0]
    answer["candidate_id"] = None
    answer["evidence_refs"] = []
    supplied["candidates"].append(answer)

    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())
    warning, answer = result["candidates"]
    assert warning["source_bbox"] == request()["candidates"][0]["bbox"]
    assert warning["role"] == "instruction" and warning["proposed_bbox"] == warning_box
    assert "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED" in warning["reason_codes"]
    assert answer["role"] == "answer" and answer["printed_question_label"] == "12"
    assert answer["semantic_check"] == "supported" and answer["association_status"] == "associated"
    assert all("REGION_CONFLICT" not in row["reason_codes"] for row in result["candidates"])


def test_mixed_warning_and_fill_frame_is_not_hidden_by_warning_label():
    supplied = with_observations(response("answer", "12"), labels=["12"], static="禁止作答", options="none")
    row = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())["candidates"][0]
    assert row["role"] == "uncertain" and row["semantic_check"] == "needs_review"
    assert "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED" not in row["reason_codes"]


@pytest.mark.parametrize("change,reason", [
    ("missing", "MODEL_OBS_OBJECTIVE_ITEMS_MISSING"),
    ("duplicate_question", "MODEL_OBS_OBJECTIVE_ITEMS_QUESTION_CONFLICT"),
    ("not_formal_choice", "MODEL_OBS_OBJECTIVE_ITEMS_QUESTION_CONFLICT"),
    ("duplicate_option", "MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT"),
    ("outside_parent", "MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"),
    ("missing_question", "MODEL_OBS_OBJECTIVE_ITEMS_COVERAGE_CONFLICT"),
])
def test_invalid_model_choice_mapping_is_visible_for_correction_but_never_applied(change, reason):
    supplied = with_observations()
    items = supplied["candidates"][0]["objective_items"]
    if change == "missing":
        items.clear()
    elif change == "duplicate_question":
        items[1]["printed_question_label"] = "1"
    elif change == "not_formal_choice":
        items[0]["printed_question_label"] = "15"
    elif change == "duplicate_option":
        items[0]["options"][1]["label"] = "A"
    elif change == "outside_parent":
        items[0]["options"][0]["bbox"]["x"] = .9
    else:
        items.pop()
    row = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence).analyze(request())["candidates"][0]
    assert row["role"] == "uncertain" and row["semantic_check"] == "needs_review"
    assert reason in row["reason_codes"] and row["objective_items"] == []


@pytest.mark.parametrize("case,reason", [
    ("choice_as_instruction", "MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT"),
    ("answer_label_disagrees", "MODEL_OBS_ANSWER_LABEL_CONFLICT"),
])
def test_observation_conflicts_keep_real_region_visible_for_review(case, reason):
    if case == "choice_as_instruction":
        supplied = with_observations(response("instruction"), static="禁止作答")
    else:
        supplied = with_observations(response("answer", "15"), labels=["16"], static="解答题", options="none")
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    row = result["candidates"][0]
    assert result["semantic_status"] == "completed"
    assert reason in row["reason_codes"]
    assert row["role"] == "uncertain" and row["semantic_check"] == "needs_review"
    assert row["association_status"] == "unassociated"


@pytest.mark.parametrize("field", ["observed_question_labels", "visual_evidence"])
def test_private_values_in_observations_fail_closed_without_public_or_log_echo(field, caplog):
    supplied = with_observations()
    if field == "observed_question_labels":
        supplied["candidates"][0][field] = [PRIVATE_VALUE]
    else:
        supplied["candidates"][0][field]["static_label"] = PRIVATE_VALUE
    with caplog.at_level(logging.INFO):
        result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    assert result["semantic_status"] == "failed" and len(result["candidates"]) == 1
    assert result["candidates"][0]["printed_question_label"] is None
    assert PRIVATE_VALUE not in json.dumps(result) and PRIVATE_VALUE not in caplog.text


def test_evidence_variant_does_not_invent_missing_observations_from_legacy_response():
    result = TemplateAnalyzer(ReplayModel(response()), no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    assert result["semantic_status"] == "failed" and len(result["candidates"]) == 1
    assert result["candidates"][0]["role"] == "uncertain"


@pytest.mark.parametrize("variant", ["v6", "v7-text", "v7-evidence"])
def test_diagnostics_hash_actual_request_contract_without_claiming_provider_strict(variant, tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    supplied = with_observations() if variant == "v7-evidence" else response()
    model = ReplayModel(supplied)
    TemplateAnalyzer(model, no_external_evidence, prompt_variant=variant).analyze(request())
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    contract = record["metadata"]["request_contract"]
    call = model.calls[0]
    system = "\n".join(message["content"] for message in call["messages"] if message["role"] == "system")
    schema_bytes = json.dumps(call["schema"], ensure_ascii=False, sort_keys=True,
                              separators=(",", ":"), allow_nan=False).encode()
    assert contract == {"prompt_variant": variant, "system_prompt_sha256": hashlib.sha256(system.encode()).hexdigest(),
                        "model_output_schema_sha256": hashlib.sha256(schema_bytes).hexdigest(),
                        "requested_output_mode": "schema_contract"}
    assert "provider_request" not in record["metadata"]


def test_opt_in_raw_response_keeps_contradiction_while_summary_is_public_only(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "true")
    supplied = with_observations(labels=["15", "16"])
    result = TemplateAnalyzer(ReplayModel(supplied), no_external_evidence, prompt_variant="v7-evidence").analyze(request())
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    raw = record["responses"][0]["content_json"]
    assert json.loads(raw) == supplied
    assert record["metadata"]["attempts"][0]["content_sha256"] == hashlib.sha256(raw.encode()).hexdigest()
    assert result["candidates"][0]["role"] == "uncertain"
    assert all("observed_question_labels" not in row and "visual_evidence" not in row for row in record["candidate_summary"])
