"""固定响应只验证局部修复的预算、坐标与失败边界，不代表真实模型识别准确率。"""

import copy
import json
from contextlib import nullcontext
from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pytest
from grading_agent.config import Settings
from grading_agent.errors import AgentError
from grading_agent.template_analysis import _normalize
from grading_agent.template_analysis_model import prepare_template_model
from grading_agent.template_analysis_repair import (
    _bounded_model,
    _repair_schema,
    _repair_tasks,
    _restore_rows,
    bounded_initial_model,
    repair_model_output_schema,
    repair_template_candidates,
)
from grading_agent.template_analysis_semantics import LocalContentEvidence
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


def payload():
    return {"request_id": "local-repair-fixture", "page": {"page_no": 1, "sha256": "a" * 64},
            "question_labels": ["1", "2", "12", "13", "15"],
            "objective_question_labels": ["1", "2"], "fill_blank_question_labels": ["12", "13"],
            "writing_state": "blank", "candidates": []}


def region(candidate_id="wrong-name", *, role="identity", label="姓名", question=None,
           x=.06, y=.33, width=.28, height=.09, supported=True, reasons=None, origin="model"):
    return {"candidate_id": candidate_id, "page_no": 1, "role": role, "identity_label": label,
            "printed_question_label": question, "association_status": "unassociated",
            "proposed_bbox": {"x": x, "y": y, "width": width, "height": height},
            "semantic_check": "supported" if supported else "needs_review", "origin": origin,
            "reason_codes": reasons or [], "evidence_refs": [], "objective_items": []}


def model_row(*, role="answer", question="12", label=None, box=None):
    return {"role": role, "identity_label": label, "labels": [question] if question else [],
            "bbox": box or {"x": .1, "y": .2, "width": .7, "height": .3},
            "objective_items": [], "static_label": "填空题" if role == "answer" else None,
            "option_pattern": "none", "identity_pattern": "none", "barcode_pattern": "none",
            "boundary_basis": "printed_answer_line", "anchor_bbox": None}


class Clock:
    def __init__(self):
        self.value = 100.0

    def __call__(self):
        return self.value


class ReplayModel:
    def __init__(self, results=None, *, clock=None, elapsed=0):
        self.settings = Settings()
        self.results = results or []
        self.calls = []
        self.clock, self.elapsed = clock, elapsed
        self.transport = lambda *args: {"timeout": args[-1]}

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, _request_id, messages, schema, name):
        self.calls.append({"messages": messages, "schema": schema, "name": name, "settings": self.settings})
        if self.clock:
            self.clock.value += self.elapsed
        result = self.results.pop(0)
        if isinstance(result, Exception):
            raise result
        return copy.deepcopy(result)

    def last_usage(self):
        return {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}


def run(model, rows=None, **kwargs):
    return repair_template_candidates(model, payload(), np.full((1000, 1600, 3), 255, np.uint8),
                                      rows if rows is not None else [region()], **kwargs)


def normalized_and_reviewed(rows):
    normalized = _normalize(rows, {}, payload())
    image = np.full((1000, 1600, 3), 255, np.uint8)
    LocalContentEvidence(image, payload()["page"], ocr=lambda _: ([], "SEMANTIC_OCR_UNAVAILABLE")).review(normalized, payload())
    return normalized


def code_region(candidate_id, *, x=.22, y=.1, anchor_x=None):
    row = region(candidate_id, label="条码", x=x, y=y, width=.13, height=.1,
                 supported=False, reasons=["MODEL_PROPOSED_IDENTITY"])
    row["visual_evidence"] = {"anchor_bbox": {
        "x": x + .02 if anchor_x is None else anchor_x, "y": y + .03, "width": .07, "height": .04}}
    return row


def code_hint(candidate_id, *, x=.22, y=.1, anchor_x=None):
    row = code_region(candidate_id, x=x, y=y, anchor_x=anchor_x)
    row.update(role="uncertain", identity_label=None, reason_codes=["BARCODE_DISCOVERY_CANDIDATE"],
               _barcode_discovery_hint=True, _barcode_identity_label="条码")
    row["_barcode_anchor_bbox"] = row.pop("visual_evidence")["anchor_bbox"]
    return row


def test_repair_raw_rows_pass_existing_normalizer_and_keep_observation_semantics():
    model = ReplayModel([{"candidates": [model_row()]}])
    batch = run(model)["batches"][0]
    normalized = normalized_and_reviewed(batch["candidates"])
    assert len(normalized) == 1
    assert normalized[0]["printed_question_label"] == "12"
    assert normalized[0]["semantic_check"] == "supported"
    assert normalized[0]["origin"] == "model"


def test_single_visible_answer_label_derives_association_and_request_owned_fields_once():
    supplied = model_row()
    original = copy.deepcopy(supplied)
    result = run(ReplayModel([{"candidates": [supplied]}]))
    row = result["batches"][0]["candidates"][0]
    assert row["printed_question_label"] == "12"
    assert row["association_status"] == "associated"
    assert row["observed_question_labels"] == ["12"]
    assert row["candidate_id"] is None and row["page_no"] == 1
    assert row["evidence_refs"] == ["source-page:" + "a" * 64 + ":1"]
    assert "_coarse_layout" not in row
    assert supplied == original


def test_multiple_visible_answer_labels_remain_unassociated_and_keep_contradiction():
    supplied = model_row()
    supplied["labels"] = ["12", "13"]
    result = run(ReplayModel([{"candidates": [supplied]}]))
    row = result["batches"][0]["candidates"][0]
    assert row["printed_question_label"] is None
    assert row["association_status"] == "unassociated"
    assert row["observed_question_labels"] == ["12", "13"]
    reviewed = normalized_and_reviewed([row])[0]
    assert "MODEL_OBS_MULTIPLE_QUESTION_LABELS" in reviewed["reason_codes"]
    assert reviewed["semantic_check"] == "needs_review"


@pytest.mark.parametrize("field,value", [("association_status", "associated"),
    ("printed_question_label", "12"), ("candidate_id", None), ("unexpected", "extra"),
    ("_requires_option_alignment", True), ("_requires_option_alignment", False)])
def test_legacy_or_unknown_fields_are_rejected_before_observation_expansion(field, value):
    supplied = model_row()
    supplied[field] = value
    result = run(ReplayModel([{"candidates": [supplied]}]))
    assert result["batches"] == []
    assert result["metadata"]["attempts"][0]["error_code"] == "model_output_invalid"


def test_local_objective_cannot_inherit_coarse_marker_to_skip_missing_item_validation():
    supplied = model_row(role="objective_group", question="1")
    supplied.update(static_label="选择题", option_pattern="letter_options")
    result = run(ReplayModel([{"candidates": [supplied]}]))
    row = result["batches"][0]["candidates"][0]
    assert "_coarse_layout" not in row
    assert row["printed_question_label"] is None and row["association_status"] == "unassociated"
    reviewed = normalized_and_reviewed([row])[0]
    assert "MODEL_OBS_OBJECTIVE_ITEMS_MISSING" in reviewed["reason_codes"]
    assert reviewed["semantic_check"] == "needs_review"


@pytest.mark.parametrize("output", [{"candidates": []}, {"candidates": [{}]},
    AgentError("model_request_rejected", "private supplier content", status=502)])
def test_empty_invalid_or_failed_repair_never_returns_replacement(output):
    model = ReplayModel([output])
    result = run(model)
    assert result["batches"] == []
    assert result["usage"]["total_tokens"] == 12
    assert "private supplier content" not in json.dumps(result)
    assert result["metadata"]["request_count"] == 1


def test_schema_failure_records_only_fixed_path_and_keyword_without_private_instance():
    row = model_row()
    row["role"] = "private student content"
    result = run(ReplayModel([{"candidates": [row]}]))
    attempt = result["metadata"]["attempts"][0]
    assert attempt["failure_stage"] == "schema"
    assert attempt["validation_path"] == "$.candidates[0].role"
    assert attempt["validation_keyword"] == "enum"
    assert "private student content" not in json.dumps(result)
    assert result["batches"] == []


def test_adapter_wrapped_schema_failure_preserves_safe_location_in_repair_metadata():
    # 真正适配器会把 schema 异常包成 AgentError；仍须区别于 HTTP/JSON 读取失败。
    value = {"candidates": [model_row()]}
    value["candidates"][0]["objective_items"] = "private supplier content"
    with pytest.raises(ValidationError) as captured:
        Draft202012Validator(repair_model_output_schema()).validate(value)
    wrapped = AgentError("model_output_invalid", "private error message", status=502)
    wrapped.__cause__ = captured.value
    result = run(ReplayModel([wrapped]))
    attempt = result["metadata"]["attempts"][0]
    assert attempt["failure_stage"] == "schema"
    assert attempt["validation_path"] == "$.candidates[0].objective_items"
    assert attempt["validation_keyword"] == "type"
    assert "private" not in json.dumps(result)


def test_provider_failure_is_separate_from_schema_and_records_no_error_body():
    result = run(ReplayModel([AgentError("model_unavailable", "private transport body", status=503)]))
    attempt = result["metadata"]["attempts"][0]
    assert attempt["failure_stage"] == "provider"
    assert "validation_path" not in attempt
    assert "validation_keyword" not in attempt
    assert "private" not in json.dumps(result)


def test_model_error_does_not_prevent_independent_next_crop_from_succeeding():
    rows = [region(), region("other-column", x=.55, width=.25)]
    model = ReplayModel([AgentError("model_timeout", "private timeout", status=504), {"candidates": [model_row()]}])
    result = run(model, rows)
    assert result["metadata"]["attempts"][0]["error_code"] == "model_timeout"
    assert result["batches"][0]["replace_candidate_ids"] == ["other-column"]
    assert result["usage"]["total_tokens"] == 24


def test_total_deadline_discards_late_response_and_starts_no_later_call():
    clock = Clock()
    model = ReplayModel([{"candidates": [model_row()]}], clock=clock, elapsed=6)
    result = run(model, [region(), region("other", x=.55)], deadline=105, clock=clock)
    assert result["batches"] == [] and len(model.calls) == 1
    assert result["usage"]["total_tokens"] == 12
    assert "MULTIMODAL_REPAIR_BUDGET_EXCEEDED" in result["metadata"]["reason_codes"]
    assert model.settings.model_timeout_seconds == 230
    assert model.calls[0]["settings"].model_timeout_seconds < 5


def test_single_crop_queue_and_model_wait_share_twenty_second_limit_and_late_output_is_not_applied():
    clock = Clock()
    model = ReplayModel([{"candidates": [model_row()]}], clock=clock, elapsed=21)
    result = run(model, clock=clock)
    assert result["batches"] == []
    assert "MULTIMODAL_REPAIR_CALL_TIMEOUT" in result["metadata"]["reason_codes"]
    settings = model.calls[0]["settings"]
    assert settings.model_timeout_seconds + settings.model_queue_timeout_seconds <= 20
    assert result["usage"]["total_tokens"] == 12


def test_exhausted_deadline_does_not_call_model():
    clock, model = Clock(), ReplayModel()
    result = run(model, deadline=99, clock=clock)
    assert model.calls == []
    assert "MULTIMODAL_REPAIR_BUDGET_EXCEEDED" in result["metadata"]["reason_codes"]


def test_clone_preserves_wrapped_adapter_transport_and_original_settings():
    clock, model = Clock(), ReplayModel()
    bounded = _bounded_model(model, 104, clock)
    assert bounded is not model and bounded.settings is not model.settings
    assert bounded.calls is model.calls
    assert bounded.transport("url", {}, {}, 100)["timeout"] == 4
    clock.value = 104
    with pytest.raises(TimeoutError):
        bounded.transport("url", {}, {}, 100)
    assert model.transport("url", {}, {}, 100)["timeout"] == 100
    assert model.settings.model_timeout_seconds == 230


def test_initial_model_clone_limits_whole_page_call_and_queue_to_thirty_seconds():
    clock, model = Clock(), ReplayModel()
    bounded = bounded_initial_model(model, deadline=172, clock=clock)
    assert bounded is not model and bounded.settings is not model.settings
    assert bounded.settings.model_timeout_seconds + bounded.settings.model_queue_timeout_seconds <= 30
    assert bounded.transport("url", {}, {}, 100)["timeout"] == 30
    clock.value = 130
    with pytest.raises(TimeoutError):
        bounded.transport("url", {}, {}, 100)
    assert model.settings.model_timeout_seconds == 230
    assert bounded_initial_model(model, deadline=129, clock=clock) is None
    assert bounded_initial_model(SimpleNamespace(), deadline=172, clock=clock) is None


def test_objective_item_and_option_boxes_are_all_transformed_from_crop():
    row = model_row(role="objective_group", question=None)
    row["bbox"] = {"x": 0, "y": 0, "width": 1, "height": 1}
    row["labels"] = ["1"]
    row.update(static_label="选择题", option_pattern="bubble_options",
               anchor_bbox={"x": .05, "y": .05, "width": .1, "height": .1})
    row["objective_items"] = [{"printed_question_label": "1", "bbox": {"x": .1, "y": .2, "width": .8, "height": .3},
                                "options": [{"label": label, "bbox": {"x": .15 + index * .2, "y": .25, "width": .1, "height": .1}}
                                            for index, label in enumerate("ABCD")]}]
    result = run(ReplayModel([{"candidates": [row]}]))
    batch = result["batches"][0]
    normalized = normalized_and_reviewed(batch["candidates"])
    assert normalized[0]["role"] == "objective_group"
    assert normalized[0]["semantic_check"] == "supported"
    option = normalized[0]["objective_items"][0]["options"][0]["bbox"]
    assert option["x"] == pytest.approx(batch["crop_bbox"]["x"] + .15 * batch["crop_bbox"]["width"])


def test_repeated_option_box_rejects_whole_batch_without_losing_existing_region():
    row = model_row(role="objective_group", question=None)
    row["objective_items"] = [{"printed_question_label": "1", "bbox": {"x": .1, "y": .2, "width": .8, "height": .3},
                                "options": [{"label": label, "bbox": {"x": .2, "y": .2, "width": .1, "height": .1}} for label in "AB"]}]
    original = region()
    result = run(ReplayModel([{"candidates": [row]}]), [original])
    assert result["batches"] == []
    assert result["metadata"]["attempts"][0]["error_code"] == "model_output_invalid"
    assert result["metadata"]["attempts"][0]["failure_stage"] == "geometry"
    assert original == region()


def overflowing_objective_response():
    row = model_row(role="objective_group", question="1")
    row["objective_items"] = [{"printed_question_label": "1", "bbox": {"x": .1, "y": .2, "width": .8, "height": .3},
                               "options": [{"label": "A", "bbox": {"x": .2, "y": .25, "width": .04, "height": .06}},
                                           {"label": "B", "bbox": {"x": .97, "y": .25, "width": .04, "height": .06}}]}]
    return row


def test_overflowing_option_is_preserved_for_required_pixel_alignment_without_clipping():
    # Trial 16 的 schema 合法，但两个选项 x+width=1.01；整组必须先通过独立像素重测才能应用。
    supplied = overflowing_objective_response()
    original = copy.deepcopy(supplied)
    result = run(ReplayModel([{"candidates": [supplied]}]))
    batch = result["batches"][0]
    row = batch["candidates"][0]
    assert supplied == original
    assert row["_requires_option_alignment"] is True
    option = row["objective_items"][0]["options"][1]["bbox"]
    assert option["x"] == pytest.approx(batch["crop_bbox"]["x"] + .97 * batch["crop_bbox"]["width"])
    assert option["width"] == pytest.approx(.04 * batch["crop_bbox"]["width"])
    crop_right = batch["crop_bbox"]["x"] + batch["crop_bbox"]["width"]
    assert option["x"] + option["width"] > crop_right
    assert result["metadata"]["attempts"][0]["status"] == "proposed"


def test_restore_rows_default_rejects_overflowing_options():
    supplied = overflowing_objective_response()
    row = {"role": supplied["role"], "proposed_bbox": supplied["bbox"], "visual_evidence": {"anchor_bbox": None},
           "objective_items": supplied["objective_items"]}
    with pytest.raises(ValueError, match="outside crop"):
        _restore_rows([row], {"x": 0, "y": 0, "width": 1, "height": 1}, payload())


@pytest.mark.parametrize("failure", ["group", "item", "duplicate"])
def test_required_option_alignment_does_not_defer_other_geometry_failures(failure):
    supplied = overflowing_objective_response()
    if failure == "group":
        supplied["bbox"] = {"x": .8, "y": .1, "width": .3, "height": .2}
    elif failure == "item":
        supplied["objective_items"][0]["bbox"] = {"x": .8, "y": .1, "width": .3, "height": .2}
    else:
        supplied["objective_items"][0]["options"][0]["bbox"] = copy.deepcopy(supplied["objective_items"][0]["options"][1]["bbox"])
    result = run(ReplayModel([{"candidates": [supplied]}]))
    assert result["batches"] == []
    assert result["metadata"]["attempts"][0]["failure_stage"] == "geometry"


def test_task_schema_has_only_eleven_observation_fields_and_does_not_require_barcode():
    task = _repair_tasks(payload(), [region()])[0]
    schema = _repair_schema(payload(), task)
    props = schema["properties"]["candidates"]["items"]["properties"]
    assert set(props) == {"role", "bbox", "labels", "identity_label", "static_label", "option_pattern",
                          "identity_pattern", "barcode_pattern", "boundary_basis", "anchor_bbox", "objective_items"}
    assert set(schema["properties"]["candidates"]["items"]["required"]) == set(props)
    assert "none" in props["barcode_pattern"]["enum"]
    assert schema == repair_model_output_schema()


def test_local_prompt_explains_real_incident_field_contract_without_forcing_category():
    model = ReplayModel([{"candidates": []}])
    run(model)
    system = model.calls[0]["messages"][0]["content"]
    properties = model.calls[0]["schema"]["properties"]["candidates"]["items"]["properties"]
    assert "单题关联或其他重复字段" in system
    assert "条码的 identity_pattern=none" in system
    assert "禁止把 bbox 原样复制为 anchor_bbox" in system
    assert "不逐行返回大框" in system
    assert "不能把“填空题”标题当成禁止作答" in system
    assert "完整实线或虚线粘贴框" in system
    assert "association_status" not in properties
    assert "不是 bbox 的副本" in properties["anchor_bbox"]["description"]


def test_local_prompt_withholds_exam_question_lists_to_prevent_hypothesis_self_confirmation():
    model = ReplayModel([{"candidates": []}])
    run(model)
    manifest = json.loads(model.calls[0]["messages"][1]["content"][0]["text"])
    assert manifest["question_labels_scope"] == "server_side_only_not_provided"
    assert "question_labels" not in manifest
    assert "objective_question_labels" not in manifest
    assert "fill_blank_question_labels" not in manifest
    assert "不根据任务名称或整场题号清单预设类别" in manifest["instruction"]


def test_local_prompt_treats_neighbor_heading_as_context_without_forcing_choice_role():
    # Trial 15 局部模型已返回 11 道选择题，却给裁图底部的填空标题另造了空题框。
    model = ReplayModel([{"candidates": []}])
    run(model)
    system = model.calls[0]["messages"][0]["content"]
    props = model.calls[0]["schema"]["properties"]["candidates"]["items"]["properties"]
    assert "不要求把裁图每一块都生成候选" in system
    assert "标题本身不是独立用途区" in system
    assert "不能受旧类别限制" in system
    assert "裁图边缘仅有题型标题时不单独生成候选" in props["static_label"]["description"]
    assert {"answer", "identity", "objective_group", "instruction", "uncertain"} <= set(props["role"]["enum"])


def test_local_prompt_treats_cut_off_instruction_lines_as_context_but_keeps_complete_prohibition():
    # 真实身份裁图只带入说明框上缘和首行，额外候选却反过来与条码外框冲突。
    model = ReplayModel([{"candidates": []}])
    run(model)
    system = model.calls[0]["messages"][0]["content"]
    assert "注意事项等说明区的上缘或几行截断正文" in system
    assert "完整说明区延伸到裁图外时，也只作上下文" in system
    assert "完整可见的说明区和明确禁止作答区仍按实际用途输出" in system


def test_local_choice_contract_uses_crop_coordinates_for_every_nested_box():
    model = ReplayModel([{"candidates": []}])
    run(model)
    system = model.calls[0]["messages"][0]["content"]
    props = model.calls[0]["schema"]["properties"]["candidates"]["items"]["properties"]
    item = props["objective_items"]["items"]["properties"]
    assert "子框不是相对父框的坐标" in system
    assert "同一行可有多道题" in system
    assert "x+width<=1" in props["bbox"]["description"]
    assert "完整包含在 objective_group 的 bbox 内" in item["bbox"]["description"]
    assert "完整包含在本题 bbox 内" in item["options"]["items"]["properties"]["bbox"]["description"]


def test_local_prompt_requires_one_continuous_choice_group_and_separates_demonstration_symbols():
    model = ReplayModel([{"candidates": []}])
    run(model)
    system = model.calls[0]["messages"][0]["content"]
    assert "必须合成一个整体大区" in system
    assert "不得按单选/多选标题再分成两组" in system
    assert "不相邻分栏或中间确有其他用途区时分别返回" in system
    assert "没有实际题号排列时属于说明" in system
    assert "不能借示例文字把真实作答区隐藏" in system


@pytest.mark.parametrize("static", ["单选题", "多选题", "单项选择题", "多项选择题", "判断题", "正确填涂", "错误填涂", "填涂示例"])
def test_local_schema_accepts_only_registered_exact_heading_and_example_labels(static):
    supplied = model_row(role="instruction", question=None)
    supplied["static_label"] = static
    schema = repair_model_output_schema()
    Draft202012Validator(schema).validate({"candidates": [supplied]})
    supplied["static_label"] += "private student content"
    assert list(Draft202012Validator(schema).iter_errors({"candidates": [supplied]}))


@pytest.mark.parametrize("failure,expected_reason", [
    ("identity_full_anchor", "MODEL_OBS_IDENTITY_LABEL_ANCHOR_NOT_LOCALIZED"),
    ("barcode_identity_fields", "MODEL_OBS_BARCODE_IDENTITY_CONFLICT"),
    ("fill_heading_is_instruction", "MODEL_OBS_INSTRUCTION_EVIDENCE_MISSING"),
])
def test_real_local_response_contradictions_are_preserved_for_rejection_not_rewritten_to_success(failure, expected_reason):
    # 真实模型曾返回这些字段矛盾；提示词修订不能靠服务端改字段伪装成识别正确。
    row = model_row(role="identity", question=None, label="姓名")
    row.update(static_label="姓名", identity_pattern="name_line", boundary_basis="label_and_field",
               anchor_bbox={"x": .1, "y": .2, "width": .05, "height": .06})
    if failure == "identity_full_anchor":
        row["anchor_bbox"] = copy.deepcopy(row["bbox"])
    elif failure == "barcode_identity_fields":
        row["identity_label"] = "条码"
        row.update(static_label="条码", identity_pattern="identity_fields",
                   barcode_pattern="linear_bars", boundary_basis="printed_frame")
    else:
        row.update(role="instruction", identity_label=None)
        row.update(static_label="填空题", identity_pattern="none")
    result = run(ReplayModel([{"candidates": [row]}]))
    raw = result["batches"][0]["candidates"][0]
    assert raw["association_status"] == "unassociated"
    assert raw["visual_evidence"]["identity_pattern"] == row["identity_pattern"]
    reviewed = normalized_and_reviewed([raw])[0]
    assert reviewed["semantic_check"] == "needs_review"
    assert expected_reason in reviewed["reason_codes"]


def test_malformed_local_box_is_rejected_not_clipped_into_plausible_geometry():
    row = model_row(box={"x": .8, "y": .1, "width": .5, "height": .2})
    result = run(ReplayModel([{"candidates": [row]}]))
    assert result["batches"] == []
    assert result["metadata"]["attempts"][0]["status"] == "failed"
    assert result["metadata"]["attempts"][0]["failure_stage"] == "geometry"


def test_prepared_compatible_adapter_clone_preserves_nullable_fields_finish_validation_and_usage():
    calls = []
    row = model_row()
    row.pop("identity_label")
    def transport(url, body, headers, timeout):
        calls.append({"url": url, "timeout": timeout, "body": body})
        return {"choices": [{"message": {"role": "assistant", "content": json.dumps({"candidates": [row]})},
                             "finish_reason": "stop"}], "usage": {"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10}}
    settings = Settings(adapter_type="openai_compatible", prompt_root=str(Path(__file__).resolve().parents[1] / "prompts"))
    model = prepare_template_model(SimpleNamespace(settings=settings, transport=transport))
    original_settings = model.settings
    result = run(model)
    assert result["batches"][0]["candidates"][0]["candidate_id"] is None
    assert result["usage"]["total_tokens"] == 10
    assert calls[0]["timeout"] <= 20
    assert calls[0]["body"]["response_format"]["json_schema"]["name"] == "answer_sheet_template_local_repair"
    assert model.settings is original_settings and model.settings.model_timeout_seconds == 230


@pytest.mark.parametrize("finish_reason", ["stop", "length"])
def test_prepared_native_adapter_clone_keeps_multimodal_protocol_and_failed_usage(finish_reason):
    calls = []
    def transport(url, body, headers, timeout):
        calls.append({"url": url, "body": body, "timeout": timeout})
        return {"request_id": "native-local-repair-1", "output": {"choices": [{
            "finish_reason": finish_reason, "message": {"role": "assistant", "content": [{"text": json.dumps({"candidates": [model_row()]})}]},
        }]}, "usage": {"input_tokens": 7, "output_tokens": 3, "total_tokens": 10}}
    settings = Settings(adapter_type="dashscope_native", model_base_url="https://native.example/api/v1",
                        prompt_root=str(Path(__file__).resolve().parents[1] / "prompts"))
    model = prepare_template_model(SimpleNamespace(settings=settings, transport=transport))
    result = run(model)
    assert calls[0]["url"].endswith("/services/aigc/multimodal-generation/generation")
    assert calls[0]["body"]["input"]["messages"][1]["content"][1]["image"].startswith("data:image/png;base64,")
    assert "image_url" not in json.dumps(calls[0]["body"])
    assert calls[0]["timeout"] <= 20
    assert result["usage"]["total_tokens"] == 10
    assert bool(result["batches"]) == (finish_reason == "stop")
    assert model.settings is settings


def test_explicit_zero_call_budget_and_unconfigured_model_do_not_request():
    model = ReplayModel()
    assert run(model, max_calls=0)["metadata"]["request_count"] == 0
    assert run(None)["metadata"]["request_count"] == 0
    assert model.calls == []
