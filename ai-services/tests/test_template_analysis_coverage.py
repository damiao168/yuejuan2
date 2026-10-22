"""最终整页检查只验证独立输入、预算与建议边界，不能代替真实模型视觉验收。"""

import base64
import copy
import hashlib
import json
from contextlib import nullcontext
from pathlib import Path

import cv2
import numpy as np
import pytest
from grading_agent.config import Settings
from grading_agent.errors import AgentError
from grading_agent.model import LocalLlamaCppAdapter
from grading_agent.template_analysis_coverage import (
    COVERAGE_VERSION,
    coverage_model_output_schema,
    review_template_page_coverage,
)
from grading_agent.template_analysis_final_review import apply_page_coverage
from grading_agent.template_analysis_schema import model_output_schema
from jsonschema import Draft202012Validator


def image_and_payload():
    image = np.full((300, 400, 3), 255, np.uint8)
    raw = cv2.imencode(".png", image)[1].tobytes()
    return image, {"request_id": "page-coverage-fixture", "page": {
        "page_no": 1, "width": 400, "height": 300, "sha256": hashlib.sha256(raw).hexdigest(),
        "media_type": "image/png", "data_base64": base64.b64encode(raw).decode()},
        "question_labels": ["1", "2", "12", "13", "15"], "objective_question_labels": ["1", "2"],
        "fill_blank_question_labels": ["12", "13"], "writing_state": "blank"}


def candidate(role="identity", question=None):
    return {"candidate_id": "private-internal-candidate", "role": role, "identity_label": "姓名",
            "printed_question_label": question, "proposed_bbox": {"x": .1, "y": .2, "width": .3, "height": .2},
            "semantic_check": "supported", "reason_codes": ["PRIOR_MODEL_EVIDENCE"],
            "evidence_refs": ["private-prior-proof"]}


def observation(index=0, role="objective_group", labels=None, identity=None, bbox=None):
    return {"index": index, "role": role, "labels": ["1", "2"] if labels is None else labels,
            "identity_label": identity, "bbox": copy.deepcopy(candidate()["proposed_bbox"] if bbox is None else bbox)}


def uncovered(label="12"):
    return {"role": "answer", "bbox": {"x": .1, "y": .5, "width": .3, "height": .1},
            "labels": [label], "identity_label": None, "static_label": "填空题", "option_pattern": "none",
            "identity_pattern": "none", "barcode_pattern": "none", "boundary_basis": "printed_answer_line",
            "anchor_bbox": None}


class Clock:
    def __init__(self):
        self.value = 100.0

    def __call__(self):
        return self.value


class ReplayModel:
    def __init__(self, output, *, clock=None, elapsed=0):
        self.output, self.calls = output, []
        self.settings = Settings()
        self.clock, self.elapsed = clock, elapsed

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, _request_id, messages, schema, name):
        self.calls.append({"messages": messages, "schema": schema, "name": name, "settings": self.settings})
        if self.clock:
            self.clock.value += self.elapsed
        if isinstance(self.output, Exception):
            raise self.output
        return copy.deepcopy(self.output)

    def last_usage(self):
        return {"input_tokens": 8, "output_tokens": 4, "total_tokens": 12}


def run(model, candidates=None, *, deadline=130, clock=None):
    image, payload = image_and_payload()
    return review_template_page_coverage(model, payload, image,
                                         [candidate()] if candidates is None else candidates,
                                         deadline=deadline, clock=clock or Clock())


def test_final_check_reads_original_full_image_and_only_box_indices_without_prior_category_or_number():
    rows = [candidate()]
    before = copy.deepcopy(rows)
    model = ReplayModel({"observations": [observation()], "uncovered": [uncovered()]})
    result = run(model, rows)
    call = model.calls[0]
    manifest = json.loads(call["messages"][1]["content"][0]["text"])
    assert manifest == {"page_no": 1, "coordinate_space": "source_display_normalized", "regions": [
        {"index": 0, "bbox": rows[0]["proposed_bbox"]}]}
    assert "question_labels" not in manifest and "identity_label" not in str(manifest)
    assert "private" not in str(manifest) and "PRIOR_MODEL_EVIDENCE" not in str(manifest)
    _image, payload = image_and_payload()
    assert call["messages"][1]["content"][1]["image_url"]["url"] == (
        "data:image/png;base64," + payload["page"]["data_base64"])
    assert result["observations"] == [{key: value for key, value in observation().items() if key != "bbox"}]
    assert result["metadata"]["status"] == "proposed"
    assert result["metadata"]["request_count"] == 1
    contract = result["metadata"]["request_contract"]
    assert contract["source_page_sha256"] == payload["page"]["sha256"]
    assert len(contract["system_prompt_sha256"]) == len(contract["model_output_schema_sha256"]) == 64
    assert rows == before


def test_coverage_prompt_separates_fill_examples_from_real_numbered_choices_and_keeps_continuous_group():
    model = ReplayModel({"observations": [observation(role="instruction", labels=[])], "uncovered": []})
    result = run(model)
    system = model.calls[0]["messages"][0]["content"]
    assert "正确填涂、错误填涂、填涂示例" in system
    assert "没有实际题号排列时不能因演示符号认成objective_group" in system
    assert "不能把真实作答区跟示例一起隐藏" in system
    assert "不能按小标题拆成多组" in system
    assert result["observations"][0]["role"] == "instruction"


def test_second_image_marks_region_indices_while_preserving_original_pixels_and_first_image():
    image, payload = image_and_payload()
    original = image.copy()
    model = ReplayModel({"observations": [observation()], "uncovered": []})
    result = review_template_page_coverage(model, payload, image, [candidate()], deadline=130, clock=Clock())
    images = [part["image_url"]["url"] for part in model.calls[0]["messages"][1]["content"]
              if part["type"] == "image_url"]
    assert len(images) == 2
    original_raw, marked_raw = (base64.b64decode(url.split(",", 1)[1]) for url in images)
    assert hashlib.sha256(original_raw).hexdigest() == payload["page"]["sha256"]
    assert hashlib.sha256(marked_raw).hexdigest() != payload["page"]["sha256"]
    marked = cv2.imdecode(np.frombuffer(marked_raw, np.uint8), cv2.IMREAD_COLOR)
    assert marked.shape == image.shape and not np.array_equal(marked, image)
    assert np.array_equal(image, original)
    assert result["metadata"]["version"] == COVERAGE_VERSION == "independent-page-coverage-v3"


def test_misaligned_bbox_discards_only_that_observation_and_keeps_independent_discovery():
    first, second = candidate(), candidate(role="answer", question="15")
    second["proposed_bbox"] = {"x": .5, "y": .6, "width": .2, "height": .1}
    invalid = observation(bbox=second["proposed_bbox"])
    valid = observation(index=1, role="answer", labels=["15"], bbox=second["proposed_bbox"])
    model = ReplayModel({"observations": [invalid, valid], "uncovered": [uncovered()]})
    result = run(model, [first, second])
    assert result["observations"] == [{key: value for key, value in valid.items() if key != "bbox"}]
    assert result["metadata"]["status"] == "proposed" and result["metadata"]["observed_count"] == 1
    assert len(result["uncovered"]) == 1 and result["uncovered"][0]["printed_question_label"] == "12"
    assert result["metadata"]["reason_codes"] == [
        "PAGE_COVERAGE_OBSERVATION_BBOX_MISMATCH", "PAGE_COVERAGE_OBSERVATIONS_INCOMPLETE"]


def test_supplier_missing_required_bbox_preserves_original_results_and_rejects_whole_check():
    # 真实适配器先校验 schema；缺少绑定不能补成原框坐标，否则会掩盖模型的错编号。
    rows = [candidate()]
    before = copy.deepcopy(rows)
    invalid = observation()
    invalid.pop("bbox")
    output = {"observations": [invalid], "uncovered": [uncovered()]}
    calls = []

    def transport(_url, body, _headers, _timeout):
        calls.append(body)
        return {"choices": [{"finish_reason": "stop", "message": {
            "role": "assistant", "content": json.dumps(output)}}],
            "usage": {"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12}}

    settings = Settings(prompt_root=str(Path(__file__).resolve().parents[1] / "prompts"))
    result = run(LocalLlamaCppAdapter(settings, transport=transport), rows)
    sent_schema = calls[0]["response_format"]["json_schema"]["schema"]
    observation_schema = sent_schema["properties"]["observations"]["items"]
    assert {"index", "bbox"} <= set(observation_schema["required"])
    assert "逐字复制" in observation_schema["properties"]["bbox"]["description"]
    assert '"index":2,"bbox"' in calls[0]["messages"][0]["content"]
    assert result["metadata"]["status"] == "failed"
    assert result["metadata"]["error_code"] == "model_output_invalid"
    assert result["observations"] == [] and result["uncovered"] == []
    assert result["usage"]["total_tokens"] == 12
    _, payload = image_and_payload()
    apply_page_coverage(rows, result, payload, [], payload["page"], None)
    assert rows == before


def test_reading_order_index_with_another_regions_bbox_cannot_downgrade_the_wrong_candidate():
    # 模型把第二个框的用途写到 index=0；绑定失败后不能让这个回答反驳第一个身份框。
    rows = [candidate(), candidate(role="answer", question="15")]
    rows[1]["proposed_bbox"] = {"x": .5, "y": .6, "width": .2, "height": .1}
    before = copy.deepcopy(rows)
    model = ReplayModel({"observations": [observation(bbox=rows[1]["proposed_bbox"]),
                         observation(index=1, role="answer", labels=["15"], bbox=rows[1]["proposed_bbox"])],
                         "uncovered": []})
    result = run(model, rows)
    _, payload = image_and_payload()
    applied = apply_page_coverage(rows, result, payload, [], payload["page"], None)
    assert applied["observed_conflict_count"] == 0
    assert rows == before


@pytest.mark.parametrize("difference, accepted", [(5e-7, True), (1.1e-6, False)])
def test_bbox_binding_allows_only_fixed_small_numeric_rounding_tolerance(difference, accepted):
    observed = observation()
    observed["bbox"]["x"] += difference
    result = run(ReplayModel({"observations": [observed], "uncovered": []}))
    assert bool(result["observations"]) is accepted
    if accepted:
        assert "bbox" not in result["observations"][0]
    else:
        assert "PAGE_COVERAGE_OBSERVATION_BBOX_MISMATCH" in result["metadata"]["reason_codes"]


def test_uncovered_is_raw_candidate_that_still_requires_normalization_and_global_checks():
    result = run(ReplayModel({"observations": [observation()], "uncovered": [uncovered()]}))
    raw = result["uncovered"][0]
    Draft202012Validator(model_output_schema([], 1, include_observations=True)).validate({"candidates": [raw]})
    assert raw["printed_question_label"] == "12" and raw["association_status"] == "associated"
    assert raw["candidate_id"] is None and raw["objective_items"] == []
    assert "semantic_check" not in raw and raw.get("status") != "confirmed"
    assert result["usage"]["total_tokens"] == 12


def test_independent_visible_unknown_number_is_retained_without_inventing_formal_association():
    result = run(ReplayModel({"observations": [observation(labels=["99"])], "uncovered": [uncovered("99")]}))
    assert result["observations"][0]["labels"] == ["99"]
    assert result["uncovered"][0]["observed_question_labels"] == ["99"]
    assert result["uncovered"][0]["printed_question_label"] is None
    assert result["uncovered"][0]["association_status"] == "unassociated"


def test_completely_missed_area_can_be_found_when_initial_layout_has_no_regions():
    result = run(ReplayModel({"observations": [], "uncovered": [uncovered()]}), [])
    assert result["metadata"]["status"] == "proposed"
    assert len(result["uncovered"]) == 1 and result["uncovered"][0]["printed_question_label"] == "12"


def test_no_barcode_is_legal_and_missing_exam_numbers_do_not_create_suggestions():
    result = run(ReplayModel({"observations": [observation(role="answer", labels=["15"])], "uncovered": []}),
                 [candidate(role="answer", question="15")])
    assert result["uncovered"] == []
    assert result["metadata"]["reason_codes"] == []
    assert not any("MISSING" in reason or "BARCODE" in reason for reason in result["metadata"]["reason_codes"])


def test_incomplete_observations_are_explicit_and_do_not_claim_every_region_was_checked():
    result = run(ReplayModel({"observations": [], "uncovered": []}))
    assert result["metadata"]["reason_codes"] == ["PAGE_COVERAGE_OBSERVATIONS_INCOMPLETE"]
    assert result["metadata"]["observed_count"] == 0


@pytest.mark.parametrize("output", [{}, {"observations": [observation(2)], "uncovered": []},
    {"observations": [observation(), observation()], "uncovered": []},
    {"observations": [{**observation(), "student_name": "PRIVATE_STUDENT"}], "uncovered": []},
    AgentError("model_timeout", "PRIVATE_STUDENT from supplier", status=504)])
def test_invalid_or_failed_check_discards_suggestions_keeps_usage_and_never_echoes_private_error(output):
    result = run(ReplayModel(output))
    assert result["observations"] == [] and result["uncovered"] == []
    assert result["metadata"]["status"] == "failed"
    assert result["usage"]["total_tokens"] == 12
    assert "PRIVATE_STUDENT" not in json.dumps(result)


def test_duplicate_index_is_rejected_even_when_schema_count_limit_is_not_exceeded():
    rows = [candidate(), candidate(role="answer", question="15")]
    result = run(ReplayModel({"observations": [observation(), observation()], "uncovered": []}), rows)
    assert result["metadata"]["status"] == "failed" and result["observations"] == []


def test_less_than_one_second_remaining_skips_without_requesting_or_reading_previous_usage():
    model, clock = ReplayModel({}), Clock()
    result = run(model, deadline=100.9, clock=clock)
    assert result["metadata"]["status"] == "skipped_budget"
    assert result["metadata"]["request_count"] == 0 and result["usage"] == {}
    assert model.calls == []


def test_queue_and_http_wait_share_twelve_seconds_without_mutating_shared_settings():
    model = ReplayModel({"observations": [observation()], "uncovered": []})
    result = run(model)
    call_settings = model.calls[0]["settings"]
    assert call_settings.model_timeout_seconds + call_settings.model_queue_timeout_seconds <= 12
    assert model.settings.model_timeout_seconds == 230
    assert result["metadata"]["status"] == "proposed"


def test_shorter_global_deadline_wins_and_late_response_is_not_a_coverage_proposal():
    clock = Clock()
    model = ReplayModel({"observations": [observation()], "uncovered": [uncovered()]}, clock=clock, elapsed=6)
    result = run(model, deadline=105, clock=clock)
    settings = model.calls[0]["settings"]
    assert settings.model_timeout_seconds + settings.model_queue_timeout_seconds <= 5
    assert result["metadata"]["status"] == "budget_exceeded"
    assert result["observations"] == [] and result["uncovered"] == []
    assert result["usage"]["total_tokens"] == 12


def test_whole_check_late_response_does_not_expand_beyond_twelve_seconds():
    clock = Clock()
    model = ReplayModel({"observations": [observation()], "uncovered": []}, clock=clock, elapsed=13)
    result = run(model, deadline=172, clock=clock)
    assert result["metadata"]["status"] == "budget_exceeded"
    assert result["observations"] == []


def test_invalid_pixel_binding_is_rejected_without_request():
    model = ReplayModel({})
    image, payload = image_and_payload()
    payload["page"]["width"] += 1
    result = review_template_page_coverage(model, payload, image, [candidate()], deadline=130, clock=Clock())
    assert result["metadata"]["status"] == "invalid_input" and model.calls == []


def test_coverage_schema_has_no_fine_option_boxes_or_student_text_fields():
    schema = coverage_model_output_schema(2)
    assert '"options"' not in json.dumps(schema)
    assert schema["properties"]["observations"]["maxItems"] == 2
    assert set(schema["properties"]["observations"]["items"]["properties"]["bbox"]["properties"]) == {
        "x", "y", "width", "height"}
    assert schema["properties"]["uncovered"]["maxItems"] == 64
