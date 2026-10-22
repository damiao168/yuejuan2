"""重放真实故障的类别与修复顺序；固定响应和合成像素不代表真实模型验收。"""

import base64
import copy
import hashlib
import json
from contextlib import nullcontext

import cv2
import numpy as np
import pytest

from grading_agent.config import Settings
from grading_agent.errors import AgentError
from grading_agent.template_analysis import (
    TemplateAnalyzer,
    _apply_repair_batches,
    _normalize,
)
from grading_agent.template_analysis_coverage import review_template_page_coverage
from grading_agent.template_analysis_schema import SCHEMA_VERSION
from grading_agent.template_analysis_semantics import LocalContentEvidence


def box(x, y, width, height):
    return {"x": x, "y": y, "width": width, "height": height}


def request():
    image = np.full((1000, 1600, 3), 255, np.uint8)
    raw = cv2.imencode(".png", image)[1].tobytes()
    return {"request_id": "v8-flow-fixture-0001", "schema_version": SCHEMA_VERSION,
            "page": {"page_no": 1, "width": 1600, "height": 1000, "media_type": "image/png",
                     "data_base64": base64.b64encode(raw).decode(), "sha256": hashlib.sha256(raw).hexdigest()},
            "question_labels": ["1", "2", "12", "13", "15"], "objective_question_labels": ["1", "2"],
            "fill_blank_question_labels": ["12", "13"], "writing_state": "blank", "candidates": []}


def row(role, bbox, *, question=None, identity=None, static=None, options="none", pattern="none",
        barcode="none", labels=None, anchor=None, items=None):
    return {"candidate_id": None, "page_no": 1, "role": role, "proposed_bbox": bbox,
            "printed_question_label": question, "identity_label": identity,
            "association_status": "associated" if question and role == "answer" else "unassociated",
            "reason_codes": [], "evidence_refs": [],
            "observed_question_labels": labels if labels is not None else [question] if question else [],
            "objective_items": items or [], "visual_evidence": {
                "static_label": static, "option_pattern": options, "identity_pattern": pattern,
                "barcode_pattern": barcode, "boundary_basis": "printed_frame", "anchor_bbox": anchor}}


def answer(label, bbox):
    return row("answer", bbox, question=label, static="填空题" if label in {"12", "13"} else "解答题")


def choice(bbox, *, precise=True):
    items = [{"printed_question_label": label, "bbox": box(.1, .1 + index * .4, .8, .3),
              "options": [{"label": option, "bbox": box(.15 + offset * .18, .15 + index * .4, .1, .12)}
                          for offset, option in enumerate("ABCD")]}
             for index, label in enumerate(("1", "2"))] if precise else []
    return row("objective_group", bbox, static="选择题", options="bubble_options", labels=["1", "2"], items=items)


def identity_pair():
    # 原始模型同时捏造类别和证据，且把整条选择题行复制成姓名/考号标签锚点。
    return [row("identity", bbox, identity=label, static=label, pattern=pattern, anchor=copy.deepcopy(bbox))
            for bbox, label, pattern in [(box(.05, .2, .28, .1), "姓名", "name_line"),
                                         (box(.05, .31, .28, .1), "准考证号", "number_grid")]]


def healthy_answer():
    return answer("15", box(.55, .3, .38, .6))


def no_external_evidence(image, page):
    return LocalContentEvidence(image, page, ocr=lambda _image: ([], "SEMANTIC_OCR_UNAVAILABLE"),
                                barcode=lambda _image: None)


class ReplayModel:
    def __init__(self, outputs):
        self.outputs = list(outputs)
        self.calls = []
        self.settings = Settings()

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, _request_id, messages, schema, name):
        self.calls.append({"messages": copy.deepcopy(messages), "schema": copy.deepcopy(schema),
                           "name": name, "settings": self.settings})
        output = self.outputs.pop(0)
        if isinstance(output, Exception):
            raise output
        output = copy.deepcopy(output)
        if name == "answer_sheet_template_page_coverage" and isinstance(output, dict):
            # 流程用例描述本轮正确绑定的固定观察；专门的错位用例由 coverage 测试覆盖。
            manifest = json.loads(messages[1]["content"][0]["text"])
            for observation in output.get("observations", []):
                index = observation.get("index")
                if isinstance(index, int) and 0 <= index < len(manifest["regions"]):
                    observation.setdefault("bbox", copy.deepcopy(manifest["regions"][index]["bbox"]))
        if (name == "answer_sheet_template_local_repair" and isinstance(output, dict)
                and isinstance(output.get("candidates"), list)):
            # 用例仍描述服务端详细区域；局部模型只负责十项视觉观察和选项坐标，来源与关联由服务端推导。
            # 空对象等畸形 fixture 保持原样，继续覆盖真实 schema 拒绝路径。
            output["candidates"] = [
                {**coarse_candidate(candidate), "objective_items": candidate["objective_items"]}
                if isinstance(candidate, dict) and "proposed_bbox" in candidate and "visual_evidence" in candidate
                else candidate for candidate in output["candidates"]
            ]
        return output

    def last_usage(self):
        return {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}


@pytest.fixture(autouse=True)
def isolate_external_tools(monkeypatch):
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", raising=False)
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", raising=False)
    monkeypatch.setattr("grading_agent.template_analysis.detect_printed_frames", lambda *_args: [])
    monkeypatch.setattr("grading_agent.template_analysis.review_forbidden_regions", lambda *_args: [])
    monkeypatch.setattr("grading_agent.template_analysis.discover_fill_regions", lambda *_args, **_kwargs: ([], []))
    monkeypatch.setattr("grading_agent.template_analysis.discover_barcode_regions", lambda *_args, **_kwargs: ([], []))
    # 本文件的全白合成图只验证模型与阶段时序；轮廓识别由像素专测覆盖，此处不做重复全图扫描。
    monkeypatch.setattr("grading_agent.template_analysis_semantics.detect_option_groups", lambda *_args: ([], []))
    # 局部修复用例独立于末轮查漏；下面标明 coverage 的流程才恢复真实 helper 并提供该阶段响应。
    monkeypatch.setattr("grading_agent.template_analysis.review_template_page_coverage", lambda *_args, **_kwargs: {
        "observations": [], "uncovered": [], "usage": {},
        "metadata": {"status": "not_run", "request_count": 0, "reason_codes": []}}, raising=False)


def analyze(outputs, payload=None):
    outputs = [output if isinstance(output, Exception) else copy.deepcopy(output) for output in outputs]
    first = outputs[0]
    if not isinstance(first, Exception):
        # 首轮 fixtures 使用轻量粗契约；局部复判仍须返回完整选项坐标及既有观察契约。
        first["candidates"] = [{"role": candidate["role"], "bbox": candidate["proposed_bbox"],
                                "labels": candidate["observed_question_labels"],
                                "identity_label": candidate["identity_label"], **candidate["visual_evidence"]}
                               for candidate in first["candidates"]]
    model = ReplayModel(outputs)
    result = TemplateAnalyzer(model, no_external_evidence, prompt_variant="v8-layout").analyze(payload or request())
    return result, model


def test_wrong_identity_rows_are_reclassified_from_neutral_crop_and_normal_results_automatically_map():
    healthy = healthy_answer()
    result, model = analyze([{"candidates": [*identity_pair(), healthy]},
                             {"candidates": [choice(box(.05, .05, .9, .9))]}])
    assert len(model.calls) == result["model_usage"]["request_count"] == 2
    assert result["model_usage"]["total_tokens"] == 24
    assert result["semantic_status"] == "completed"
    assert result["status"] == "review_required"  # 识别草稿不等于发布、锁定或教师确认。
    rows = result["candidates"]
    assert {candidate["role"] for candidate in rows} == {"objective_group", "answer"}
    assert all(candidate["semantic_check"] == "supported" for candidate in rows)
    assert not any(candidate.get("identity_label") in {"姓名", "准考证号"} for candidate in rows)
    mapped = next(candidate for candidate in rows if candidate["role"] == "answer")
    assert mapped["printed_question_label"] == "15" and mapped["association_status"] == "associated"
    assert mapped["proposed_bbox"] == healthy["proposed_bbox"]
    group = next(candidate for candidate in rows if candidate["role"] == "objective_group")
    assert [item["printed_question_label"] for item in group["objective_items"]] == ["1", "2"]
    assert "LOCAL_MODEL_REPAIR_APPLIED" in group["reason_codes"]
    assert all(candidate.get("status") != "confirmed" for candidate in rows)
    assert model.settings.model_timeout_seconds == 230
    assert model.calls[0]["settings"].model_timeout_seconds <= 30
    assert model.calls[1]["settings"].model_timeout_seconds <= 20


def test_mixed_fill_and_warning_region_is_split_without_hiding_the_answer_area():
    mixed = choice(box(.05, .48, .28, .42), precise=False)
    local = [answer("12", box(.05, .1, .9, .2)), answer("13", box(.05, .4, .9, .2)),
             row("instruction", box(.05, .76, .9, .1), static="禁止作答")]
    result, model = analyze([{"candidates": [mixed, healthy_answer()]}, {"candidates": local}])
    assert len(model.calls) == 2
    assert sorted(candidate["printed_question_label"] for candidate in result["candidates"]
                  if candidate["role"] == "answer") == ["12", "13", "15"]
    assert not any(candidate["role"] in {"uncertain", "objective_group"} for candidate in result["candidates"])
    warning = next(candidate for candidate in result["candidates"] if candidate["role"] == "instruction")
    assert "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED" in warning["reason_codes"]
    assert warning["proposed_bbox"]["y"] > .7


@pytest.mark.parametrize("repair", [{"candidates": []}, {"candidates": [{}]},
    AgentError("model_timeout", "private supplier details", status=504)])
def test_empty_invalid_or_failed_crop_preserves_old_issue_and_good_regions_without_fake_success(repair):
    baseline, _model = analyze([{"candidates": [*identity_pair(), healthy_answer()]}, {"candidates": []}])
    result, model = analyze([{"candidates": [*identity_pair(), healthy_answer()]}, repair])
    assert len(model.calls) == 2
    assert result["candidates"] == baseline["candidates"]
    assert sum(candidate["semantic_check"] == "needs_review" for candidate in result["candidates"]) == 2
    assert all("LOCAL_MODEL_REPAIR_APPLIED" not in candidate["reason_codes"] for candidate in result["candidates"])
    assert "private supplier details" not in str(result)


def test_warning_only_repair_cannot_delete_a_mixed_answer_region():
    mixed = choice(box(.05, .48, .28, .42), precise=False)
    result, _model = analyze([{"candidates": [mixed, healthy_answer()]},
                              {"candidates": [row("instruction", box(.05, .75, .9, .1), static="禁止作答")]}])
    assert len(result["candidates"]) == 2
    assert any(candidate["semantic_check"] == "needs_review" for candidate in result["candidates"])
    assert not any(candidate["role"] == "instruction" for candidate in result["candidates"])


def barcode_hint():
    return {"candidate_id": "local-code-fixture", "bbox": box(.36, .05, .13, .1), "identity_label": "条码",
            "reason_codes": ["LOCAL_BARCODE_DETECTOR_LOCALIZED"], "evidence_refs": ["local-code:fixture"]}


def test_missing_barcode_hint_is_verified_as_outer_paste_frame_before_becoming_a_region(monkeypatch):
    hint = barcode_hint()
    monkeypatch.setattr("grading_agent.template_analysis.discover_barcode_regions",
                        lambda *_args, **_kwargs: ([hint], []))
    code = row("identity", box(.05, .05, .9, .9), identity="条码", static="条形码粘贴区",
               barcode="linear_bars", anchor=box(.3, .3, .4, .2))
    result, model = analyze([{"candidates": [healthy_answer()]}, {"candidates": [code]}])
    assert len(model.calls) == 2
    code_row = next(candidate for candidate in result["candidates"] if candidate.get("identity_label") == "条码")
    assert code_row["semantic_check"] == "supported"
    assert "LOCAL_MODEL_REPAIR_APPLIED" in code_row["reason_codes"]
    assert not any(candidate["candidate_id"] == hint["candidate_id"] for candidate in result["candidates"])
    assert code_row["proposed_bbox"]["width"] > .1
    assert "完整实线或虚线粘贴框" in model.calls[-1]["messages"][0]["content"]


def test_no_barcode_is_valid_and_no_missing_barcode_region_is_fabricated():
    result, model = analyze([{"candidates": [healthy_answer()]}])
    assert len(model.calls) == 1
    assert len(result["candidates"]) == 1
    assert not any("BARCODE" in reason and "MISSING" in reason for reason in result["reason_codes"])


def test_false_or_failed_barcode_hint_does_not_become_an_unusable_teacher_region(monkeypatch):
    monkeypatch.setattr("grading_agent.template_analysis.discover_barcode_regions",
                        lambda *_args, **_kwargs: ([barcode_hint()], []))
    result, _model = analyze([{"candidates": [healthy_answer()]}, {"candidates": []}])
    assert len(result["candidates"]) == 1
    assert result["candidates"][0]["printed_question_label"] == "15"
    assert result["candidates"][0]["semantic_check"] == "supported"


def test_incident_sequence_repairs_choice_barcode_and_fill_in_three_local_calls(monkeypatch):
    monkeypatch.setattr("grading_agent.template_analysis.discover_barcode_regions",
                        lambda *_args, **_kwargs: ([barcode_hint()], []))
    initial = [*identity_pair(), choice(box(.05, .48, .28, .42), precise=False), healthy_answer()]
    local_code = row("identity", box(.05, .05, .9, .9), identity="条码", static="条形码粘贴区",
                     barcode="linear_bars", anchor=box(.3, .3, .4, .2))
    local_fill = [answer("12", box(.05, .1, .9, .2)), answer("13", box(.05, .4, .9, .2)),
                  row("instruction", box(.05, .76, .9, .1), static="禁止作答")]
    result, model = analyze([{"candidates": initial}, {"candidates": [choice(box(.05, .05, .9, .9))]},
                             {"candidates": [local_code]}, {"candidates": local_fill}])
    assert len(model.calls) == result["model_usage"]["request_count"] == 4
    assert result["model_usage"]["total_tokens"] == 48
    assert sorted(candidate["printed_question_label"] for candidate in result["candidates"]
                  if candidate["role"] == "answer") == ["12", "13", "15"]
    assert sum(candidate["role"] == "objective_group" for candidate in result["candidates"]) == 1
    assert sum(candidate.get("identity_label") == "条码" for candidate in result["candidates"]) == 1
    assert all(candidate["semantic_check"] == "supported" for candidate in result["candidates"]
               if candidate["role"] not in {"instruction", "other"})
    assert not any(candidate["role"] == "uncertain" for candidate in result["candidates"])


def enable_coverage(monkeypatch):
    monkeypatch.setattr("grading_agent.template_analysis.review_template_page_coverage", review_template_page_coverage)


def coverage_observation(index, role="answer", labels=None, identity=None):
    return {"index": index, "role": role, "labels": ["15"] if labels is None else labels,
            "identity_label": identity}


def coarse_candidate(supplied):
    return {"role": supplied["role"], "bbox": supplied["proposed_bbox"],
            "labels": supplied["observed_question_labels"], "identity_label": supplied["identity_label"],
            **supplied["visual_evidence"]}


def test_final_whole_page_discovers_missing_fill_then_one_crop_verifies_it_without_fabricating_other_exam_questions(monkeypatch):
    enable_coverage(monkeypatch)
    missed = answer("12", box(.05, .55, .28, .12))
    result, model = analyze([{"candidates": [healthy_answer()]},
        {"observations": [coverage_observation(0)], "uncovered": [coarse_candidate(missed)]},
        {"candidates": [answer("12", box(.05, .1, .9, .8))]}])
    assert [call["name"] for call in model.calls] == ["answer_sheet_template_analysis",
        "answer_sheet_template_page_coverage", "answer_sheet_template_local_repair"]
    assert result["model_usage"]["request_count"] == 3 and result["model_usage"]["total_tokens"] == 36
    assert sorted(candidate["printed_question_label"] for candidate in result["candidates"]
                  if candidate["role"] == "answer") == ["12", "15"]
    found = next(candidate for candidate in result["candidates"] if candidate["printed_question_label"] == "12")
    assert found["semantic_check"] == "supported" and "LOCAL_MODEL_REPAIR_APPLIED" in found["reason_codes"]
    assert all(candidate.get("status") != "confirmed" for candidate in result["candidates"])


def test_supplier_cannot_inject_internal_coarse_marker_into_coverage_response(monkeypatch):
    enable_coverage(monkeypatch)
    supplied = coarse_candidate(answer("12", box(.05, .55, .28, .12)))
    supplied["_coarse_layout"] = True
    result, model = analyze([{"candidates": [healthy_answer()]},
                            {"observations": [coverage_observation(0)], "uncovered": [supplied]}])

    # 该标记只允许在严格校验后的内部展开步骤产生，供应商未知字段不能因后续白名单而被吞掉。
    assert [call["name"] for call in model.calls] == ["answer_sheet_template_analysis",
                                                    "answer_sheet_template_page_coverage"]
    assert len(result["candidates"]) == 1
    assert result["candidates"][0]["printed_question_label"] == "15"
    assert not any(candidate["printed_question_label"] == "12" for candidate in result["candidates"])


def test_final_disagreement_needs_local_verification_and_failed_crop_cannot_turn_observation_into_truth(monkeypatch):
    enable_coverage(monkeypatch)
    result, model = analyze([{"candidates": [healthy_answer()]},
        {"observations": [coverage_observation(0, labels=["12"])], "uncovered": []},
        {"candidates": []}])
    assert len(model.calls) == 3
    assert len(result["candidates"]) == 1
    candidate = result["candidates"][0]
    assert candidate["printed_question_label"] != "12"
    assert candidate["semantic_check"] == "needs_review"
    assert any(code.startswith("PAGE_COVERAGE_") and code.endswith("_CONFLICT")
               for code in candidate["reason_codes"])
    assert "LOCAL_MODEL_REPAIR_APPLIED" not in candidate["reason_codes"]


def test_final_check_timeout_keeps_already_usable_result_and_does_not_start_an_unfounded_repair(monkeypatch):
    enable_coverage(monkeypatch)
    result, model = analyze([{"candidates": [healthy_answer()]},
                             AgentError("model_timeout", "private supplier timeout", status=504)])
    assert len(model.calls) == result["model_usage"]["request_count"] == 2
    assert result["model_usage"]["total_tokens"] == 24
    assert len(result["candidates"]) == 1
    assert result["candidates"][0]["printed_question_label"] == "15"
    assert result["candidates"][0]["semantic_check"] == "supported"
    assert "private supplier timeout" not in str(result)


@pytest.mark.parametrize("crop_succeeds", [False, True])
def test_final_barcode_relocation_replaces_both_regions_only_after_crop_succeeds(monkeypatch, crop_succeeds):
    enable_coverage(monkeypatch)
    supplied = request()
    supplied["objective_question_labels"] = []
    old = row("identity", box(.05, .12, .18, .1), identity="条码", static="条形码粘贴区",
              barcode="linear_bars", anchor=box(.08, .14, .1, .05))
    discovered = row("identity", box(.36, .05, .13, .1), identity="条码", static="条形码粘贴区",
                     barcode="linear_bars", anchor=box(.39, .08, .07, .03))
    verified = row("identity", box(.05, .05, .9, .9), identity="条码", static="条形码粘贴区",
                   barcode="linear_bars", anchor=box(.3, .3, .4, .2))
    result, model = analyze([{"candidates": [old, healthy_answer()]},
        {"observations": [coverage_observation(0, role="other", labels=[]), coverage_observation(1)],
         "uncovered": [coarse_candidate(discovered)]}, {"candidates": [verified] if crop_succeeds else []}], supplied)
    assert len(model.calls) == 3
    assert not any("_extra_replace_candidate_ids" in candidate or "_repair_deferred_to_hint" in candidate
                   for candidate in result["candidates"])
    if crop_succeeds:
        codes = [candidate for candidate in result["candidates"] if candidate.get("identity_label") == "条码"]
        assert len(codes) == 1 and codes[0]["proposed_bbox"]["x"] > .3
        assert codes[0]["semantic_check"] == "supported"
        assert len(result["candidates"]) == 2
    else:
        assert any(candidate["proposed_bbox"]["x"] < .2 and candidate["semantic_check"] == "needs_review"
                   for candidate in result["candidates"])
        assert all("LOCAL_MODEL_REPAIR_APPLIED" not in candidate["reason_codes"] for candidate in result["candidates"])


def normalized_existing(supplied, payload):
    candidates = _normalize(copy.deepcopy(supplied), {}, payload)
    evidence = no_external_evidence(np.full((1000, 1600, 3), 255, np.uint8), payload["page"])
    evidence.review(candidates, payload)
    return candidates, evidence


@pytest.mark.parametrize("with_hint", [False, True])
def test_ordinary_barcode_repair_uses_page_outer_frame_hint_without_creating_another_region(with_hint):
    payload = request()
    outer = box(.17, .1, .17, .14)
    inner = box(.2, .14, .1, .04)
    old = row("identity", box(.18, .11, .15, .1), identity="条码", barcode="linear_bars")
    existing, evidence = normalized_existing([old, healthy_answer()], payload)
    target_id = existing[0]["candidate_id"]
    hint = {**barcode_hint(), "bbox": outer, "anchor_bbox": copy.deepcopy(inner), "boundary_kind": "printed_frame"}
    fresh = row("identity", inner, identity="条码", barcode="linear_bars", anchor=box(.22, .15, .05, .02))

    # 实际故障从普通身份栏修复进入，替换目标没有补漏提示字段；不能只在修复 hint 时才取外框。
    assert not any(key.startswith("_barcode") for key in existing[0])
    applications = _apply_repair_batches(existing, [{"task": "identity_cluster",
        "replace_candidate_ids": [target_id], "candidates": [fresh]}],
        payload, [], payload["page"], evidence, barcode_hints=[hint] if with_hint else [])

    assert applications == [{"task": "identity_cluster", "applied": True, "candidate_count": 1}]
    codes = [candidate for candidate in existing if candidate.get("identity_label") == "条码"]
    assert len(existing) == 2 and len(codes) == 1
    assert codes[0]["proposed_bbox"] == (outer if with_hint else inner)
    assert ("LOCAL_BARCODE_OUTER_FRAME_LOCALIZED" in codes[0]["reason_codes"]) == with_hint
    assert codes[0]["semantic_check"] == "supported"
    assert not any(candidate["candidate_id"] == hint["candidate_id"] for candidate in existing)


def test_ordinary_identity_cluster_matches_two_barcode_outer_frames_by_independent_anchor():
    payload = request()
    outer_boxes = [box(.07, .08, .16, .14), box(.31, .08, .16, .14)]
    inner_boxes = [box(.1, .12, .1, .04), box(.34, .12, .1, .04)]
    supplied = [row("identity", bbox, identity="条码", barcode="linear_bars") for bbox in inner_boxes]
    existing, evidence = normalized_existing([*supplied, healthy_answer()], payload)
    hints = [{**barcode_hint(), "candidate_id": f"local-code-{index}", "bbox": outer,
              "anchor_bbox": inner, "boundary_kind": "printed_frame"}
             for index, (outer, inner) in enumerate(zip(outer_boxes, inner_boxes))]

    # 两个真实码都可存在；故意倒置提示顺序，防止按类别或列表顺序把边框配错。
    applications = _apply_repair_batches(existing, [{"task": "identity_cluster",
        "replace_candidate_ids": [candidate["candidate_id"] for candidate in existing[:2]],
        "candidates": supplied}], payload, [], payload["page"], evidence, barcode_hints=list(reversed(hints)))

    assert applications == [{"task": "identity_cluster", "applied": True, "candidate_count": 2}]
    codes = [candidate for candidate in existing if candidate.get("identity_label") == "条码"]
    assert len(existing) == 3 and len(codes) == 2
    assert [candidate["proposed_bbox"] for candidate in codes] == outer_boxes
    assert all("LOCAL_BARCODE_OUTER_FRAME_LOCALIZED" in candidate["reason_codes"] for candidate in codes)
    assert all(candidate["semantic_check"] == "supported" for candidate in codes)


@pytest.mark.parametrize("duplicate", ["question", "geometry", "same_candidate"])
def test_crop_cannot_replace_good_neighbor_by_reintroducing_duplicate_question_or_overlapping_region(duplicate):
    payload = request()
    target = row("uncertain", box(.05, .2, .2, .2))
    supplied = [target, healthy_answer()]
    existing, evidence = normalized_existing(supplied, payload)
    before = copy.deepcopy(existing)
    if duplicate == "question":
        fresh = answer("15", box(.05, .2, .2, .2))
    elif duplicate == "geometry":
        fresh = answer("12", copy.deepcopy(healthy_answer()["proposed_bbox"]))
    else:
        fresh = healthy_answer()
    application = _apply_repair_batches(existing, [{"task": "mixed_region",
        "replace_candidate_ids": [existing[0]["candidate_id"]], "candidates": [fresh]}],
        payload, [], payload["page"], evidence)
    assert application[0]["applied"] is False
    assert existing == before

