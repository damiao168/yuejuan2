import copy
import json

import pytest
from grading_agent.template_analysis_observations import (
    exclude_isolated_model_warning,
    review_model_observations,
)
from grading_agent.template_analysis_schema import model_output_schema
from jsonschema import Draft202012Validator


def row(role="answer", label="12", static=None, **evidence):
    return {
        "page_no": 1, "candidate_id": None, "printed_question_label": label, "identity_label": None,
        "role": role, "proposed_bbox": {"x": .1, "y": .2, "width": .3, "height": .2},
        "association_status": "unassociated", "evidence_refs": [], "reason_codes": [],
        "objective_items": [],
        "observed_question_labels": [label] if label else [],
        "visual_evidence": {"static_label": static, "option_pattern": "none", "identity_pattern": "none",
                            "barcode_pattern": "none", "boundary_basis": "printed_frame", "anchor_bbox": None,
                            **evidence},
    }


def payload():
    return {"question_labels": [str(number) for number in range(1, 20)],
            "objective_question_labels": [str(number) for number in range(1, 12)],
            "fill_blank_question_labels": ["12", "13", "14"]}


def review(value, request=None):
    before = copy.deepcopy(value)
    review_model_observations(value, payload() if request is None else request)
    # 观察复核只追加状态和固定码；原类别、题号、观察及大混合框均留给后续流程处理。
    assert {key: item for key, item in value.items() if key not in {"reason_codes", "semantic_check"}} == {
        key: item for key, item in before.items() if key not in {"reason_codes", "semantic_check"}}
    return value


def test_legacy_schema_stays_compatible_and_observation_fields_are_opt_in():
    value = row()
    legacy = {key: item for key, item in value.items() if key not in {"observed_question_labels", "visual_evidence", "objective_items"}}
    Draft202012Validator(model_output_schema([], 1)).validate({"candidates": [legacy]})
    assert list(Draft202012Validator(model_output_schema([], 1)).iter_errors({"candidates": [value]}))
    assert list(Draft202012Validator(model_output_schema([], 1, True)).iter_errors({"candidates": [legacy]}))


def test_observation_schema_keeps_nonobjective_question_numbers_for_contradiction_review():
    value = row("objective_group", None, option_pattern="bubble_options")
    value["observed_question_labels"] = ["15", "16"]
    Draft202012Validator(model_output_schema([], 1, include_observations=True)).validate({"candidates": [value]})
    assert "MODEL_OBS_OBJECTIVE_QUESTION_TYPE_CONFLICT" in review(value)["reason_codes"]
    assert value["semantic_check"] == "needs_review"


@pytest.mark.parametrize("label", ["private-student-value", "123456789012", "姓名", "12\n13", ""])
def test_schema_rejects_private_or_unbounded_observation_text(label):
    value = row()
    value["observed_question_labels"] = [label]
    assert list(Draft202012Validator(model_output_schema([], 1, True)).iter_errors({"candidates": [value]}))


@pytest.mark.parametrize("label", ["1", "123", "12.1", "12-1", "12(1)", "12（1）"])
def test_schema_accepts_bounded_question_and_subquestion_labels(label):
    value = row()
    value["observed_question_labels"] = [label]
    Draft202012Validator(model_output_schema([], 1, True)).validate({"candidates": [value]})


def test_schema_has_no_free_text_evidence_or_model_reported_confidence():
    value = row(static="private-student-value")
    validator = Draft202012Validator(model_output_schema([], 1, True))
    assert list(validator.iter_errors({"candidates": [value]}))
    value = row()
    value["visual_evidence"]["ocr_text"] = "private-student-value"
    assert list(validator.iter_errors({"candidates": [value]}))
    value = row()
    value["classification_confidence"] = .99
    assert list(validator.iter_errors({"candidates": [value]}))


@pytest.mark.parametrize("identity", ["姓名", "准考证号"])
def test_identity_without_reported_label_or_matching_structure_needs_review(identity):
    value = row("identity", None)
    value["identity_label"] = identity
    assert "MODEL_OBS_IDENTITY_EVIDENCE_MISSING" in review(value)["reason_codes"]


@pytest.mark.parametrize("identity", ["姓名", "准考证号"])
def test_option_array_cannot_pass_as_identity_even_with_a_claimed_matching_label(identity):
    value = row("identity", None, static=identity, option_pattern="letter_options")
    value["identity_label"] = identity
    assert "MODEL_OBS_IDENTITY_OPTION_CONFLICT" in review(value)["reason_codes"]


def test_identity_static_label_disagreement_is_preserved():
    value = row("identity", None, static="姓名")
    value["identity_label"] = "准考证号"
    assert "MODEL_OBS_IDENTITY_LABEL_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("role,identity", [("identity", "条码"), ("answer", None), ("objective_group", None)])
def test_warning_observation_flags_wrong_category_without_deleting_mixed_answer_frame(role, identity):
    value = row(role, None, static="禁止作答")
    value.update(identity_label=identity, proposed_bbox={"x": .05, "y": .1, "width": .8, "height": .8})
    assert "MODEL_OBS_WARNING_CATEGORY_CONFLICT" in review(value)["reason_codes"]


def test_empty_rectangle_cannot_be_reported_as_a_barcode():
    value = row("identity", None)
    value["identity_label"] = "条码"
    assert "MODEL_OBS_BARCODE_EVIDENCE_MISSING" in review(value)["reason_codes"]


@pytest.mark.parametrize("static,identity", [("条形码粘贴区", "条码"), ("二维码粘贴区", "二维码")])
def test_explicit_blank_code_placeholder_does_not_require_decoding(static, identity):
    value = row("identity", None, static=static, barcode_pattern="labelled_placeholder")
    value["identity_label"] = identity
    assert not review(value)["reason_codes"]
    assert "semantic_check" not in value


def test_unlabelled_placeholder_is_not_positive_barcode_evidence():
    value = row("identity", None, barcode_pattern="labelled_placeholder")
    value["identity_label"] = "条码"
    assert "MODEL_OBS_BARCODE_EVIDENCE_MISSING" in review(value)["reason_codes"]


def test_fill_heading_with_official_calculation_number_flags_type_conflict():
    value = row(label="15", static="填空题", boundary_basis="printed_answer_line")
    assert "MODEL_OBS_FILL_QUESTION_TYPE_CONFLICT" in review(value)["reason_codes"]


def test_fill_question_with_option_array_flags_conflict():
    value = row(label="12", option_pattern="bubble_options")
    assert "MODEL_OBS_FILL_OPTION_CONFLICT" in review(value)["reason_codes"]


def test_ordinary_answer_line_does_not_by_itself_claim_a_fill_question():
    value = row(label="15", boundary_basis="printed_answer_line")
    assert not review(value)["reason_codes"]
    assert "semantic_check" not in value


@pytest.mark.parametrize("anchor", [
    {"x": -.01, "y": .2, "width": .05, "height": .05},
    {"x": .95, "y": .2, "width": .1, "height": .05},
    {"x": .1, "y": .2, "width": -.1, "height": .05},
])
def test_numeric_out_of_page_anchor_is_region_review_instead_of_whole_page_schema_failure(anchor):
    value = row(anchor_bbox=anchor)
    Draft202012Validator(model_output_schema([], 1, True)).validate({"candidates": [value]})
    assert "MODEL_OBS_ANCHOR_OUT_OF_PAGE" in review(value)["reason_codes"]


def test_anchor_from_another_column_flags_review():
    value = row(anchor_bbox={"x": .8, "y": .2, "width": .05, "height": .05})
    assert "MODEL_OBS_ANCHOR_OUTSIDE_REGION" in review(value)["reason_codes"]


def test_anchor_can_touch_or_sit_just_outside_its_candidate_label_boundary():
    value = row(anchor_bbox={"x": .08, "y": .23, "width": .015, "height": .03})
    assert not review(value)["reason_codes"]


def test_consistent_model_observation_never_becomes_independent_supported_or_confirmed():
    value = row("objective_group", None, option_pattern="bubble_options")
    value["observed_question_labels"] = ["1", "2"]
    assert not review(value)["reason_codes"]
    assert "semantic_check" not in value
    assert value["association_status"] == "unassociated"


def test_old_result_without_observations_keeps_existing_review_state():
    value = {key: item for key, item in row().items() if key not in {"observed_question_labels", "visual_evidence"}}
    value["semantic_check"] = "needs_review"
    value["reason_codes"] = ["PRIOR_REVIEW_REASON"]
    assert review(value)["reason_codes"] == ["PRIOR_REVIEW_REASON"]


def test_missing_type_whitelist_is_unknown_instead_of_proof_of_wrong_type():
    value = row("objective_group", None, option_pattern="bubble_options")
    value["observed_question_labels"] = ["15", "16"]
    assert not review(value, {"question_labels": ["15", "16"]})["reason_codes"]


def test_invalid_observation_does_not_echo_its_value_in_fixed_reason():
    value = row()
    value["observed_question_labels"] = ["private-student-value"]
    assert review(value)["reason_codes"] == ["MODEL_OBSERVATIONS_INVALID"]
    assert "private-student-value" not in json.dumps(value["reason_codes"])


def test_repeat_review_is_idempotent_and_does_not_clear_prior_reasons():
    value = row("identity", None)
    value["identity_label"] = "姓名"
    value["reason_codes"] = ["PRIOR_REVIEW_REASON"]
    review(value)
    before = copy.deepcopy(value)
    review(value)
    assert value == before


@pytest.mark.parametrize("anchor", [
    {"x": float("nan"), "y": .2, "width": .05, "height": .05},
    {"x": True, "y": .2, "width": .05, "height": .05},
])
def test_invalid_anchor_numbers_remain_a_local_review_issue(anchor):
    assert "MODEL_OBS_ANCHOR_OUT_OF_PAGE" in review(row(anchor_bbox=anchor))["reason_codes"]


def test_answer_association_cannot_disagree_with_its_nonempty_observed_question_number():
    value = row(label="15")
    value["observed_question_labels"] = ["16"]
    assert "MODEL_OBS_ANSWER_LABEL_CONFLICT" in review(value)["reason_codes"]


def test_answer_cannot_silently_cover_another_formal_question_beside_its_associated_number():
    value = row(label="15")
    value["observed_question_labels"] = ["15", "16"]
    assert "MODEL_OBS_MULTIPLE_QUESTION_LABELS" in review(value)["reason_codes"]


@pytest.mark.parametrize("observed", [
    ["15(1)", "15(2)"], ["15（1）", "15（2）"], ["15.1", "15.2"],
    ["15．1", "15．2"], ["15-1", "15-2"], ["15", "15(1)", "15(2)"],
])
def test_answer_with_explicit_formal_parent_keeps_its_contiguous_subquestions(observed):
    value = row(label="15")
    value["association_status"] = "associated"
    value["observed_question_labels"] = observed
    assert not review(value)["reason_codes"]
    assert "semantic_check" not in value


@pytest.mark.parametrize("observed", [["15(1)", "16"], ["15(1)", "16(1)"], ["15(1)", "99"]])
def test_parent_association_cannot_fold_cross_parent_or_unknown_question_observations(observed):
    value = row(label="15")
    value["observed_question_labels"] = observed
    assert "MODEL_OBS_QUESTION_LABEL_NOT_IN_LIST" in review(value)["reason_codes"]
    assert value["semantic_check"] == "needs_review"


@pytest.mark.parametrize("observed", [["15.1"], ["15", "15.1"], ["15.1", "15(2)"]])
def test_exact_formal_subquestion_is_never_folded_into_another_formal_parent(observed):
    request = payload()
    request["question_labels"].append("15.1")
    value = row(label="15")
    value["observed_question_labels"] = observed
    review(value, request)
    assert value["semantic_check"] == "needs_review"
    assert {"MODEL_OBS_ANSWER_LABEL_CONFLICT", "MODEL_OBS_MULTIPLE_QUESTION_LABELS",
            "MODEL_OBS_QUESTION_LABEL_NOT_IN_LIST"} & set(value["reason_codes"])


@pytest.mark.parametrize("role,label", [("answer", None), ("answer", "99"), ("objective_group", None)])
def test_unassociated_unknown_or_objective_regions_cannot_infer_a_parent_from_subquestion_prefix(role, label):
    value = row(role, label, option_pattern="bubble_options" if role == "objective_group" else "none")
    value["observed_question_labels"] = ["15(1)", "15(2)"]
    assert "MODEL_OBS_QUESTION_LABEL_NOT_IN_LIST" in review(value)["reason_codes"]


def test_unassociated_answer_with_multiple_visible_formal_numbers_still_needs_review():
    value = row(label=None)
    value["observed_question_labels"] = ["15", "16"]
    assert "MODEL_OBS_MULTIPLE_QUESTION_LABELS" in review(value)["reason_codes"]


def test_empty_observed_question_list_does_not_prove_the_associated_number_is_absent():
    value = row(label="15")
    value["observed_question_labels"] = []
    assert "MODEL_OBS_ANSWER_LABEL_CONFLICT" not in review(value)["reason_codes"]


@pytest.mark.parametrize("static", ["禁止作答", "注意事项"])
def test_instruction_with_formal_question_number_is_kept_for_review_even_with_warning_label(static):
    value = row("instruction", "15", static=static)
    assert "MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT" in review(value)["reason_codes"]


def test_instruction_with_only_the_printed_association_number_also_needs_review():
    value = row("instruction", "15", static="注意事项")
    value["observed_question_labels"] = []
    assert "MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("evidence", [
    {"option_pattern": "letter_options"}, {"option_pattern": "bubble_options"},
    {"identity_pattern": "name_line"}, {"identity_pattern": "number_grid"},
    {"identity_pattern": "identity_fields"}, {"barcode_pattern": "linear_bars"},
    {"barcode_pattern": "qr_modules"}, {"barcode_pattern": "labelled_placeholder"},
])
def test_instruction_cannot_hide_reported_answer_identity_or_code_structure(evidence):
    value = row("instruction", None, static="禁止作答", **evidence)
    assert "MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("static", [None, "选择题", "填空题", "姓名"])
def test_instruction_without_static_explanatory_label_is_not_silently_hidden(static):
    value = row("instruction", None, static=static)
    assert "MODEL_OBS_INSTRUCTION_EVIDENCE_MISSING" in review(value)["reason_codes"]


def test_isolated_instruction_observation_is_retained_without_claiming_independent_support():
    value = row("instruction", None, static="禁止作答")
    assert not review(value)["reason_codes"]
    assert "semantic_check" not in value


@pytest.mark.parametrize("identity,pattern", [("姓名", "name_line"), ("准考证号", "number_grid")])
def test_identity_structure_without_a_reported_label_requests_local_recheck(identity, pattern):
    value = row("identity", None, identity_pattern=pattern)
    value["identity_label"] = identity
    assert "MODEL_OBS_IDENTITY_LABEL_MISSING" in review(value)["reason_codes"]
    assert value["semantic_check"] == "needs_review"


@pytest.mark.parametrize("identity", ["班级", "学校"])
def test_unlocalized_english_class_or_school_structure_requests_local_recheck(identity):
    value = row("identity", None, identity_pattern="identity_fields", boundary_basis="label_and_field")
    value["identity_label"] = identity
    assert "MODEL_OBS_IDENTITY_LABEL_MISSING" in review(value)["reason_codes"]
    assert value["visual_evidence"]["static_label"] is None


@pytest.mark.parametrize("identity,pattern", [("姓名", "name_line"), ("准考证号", "number_grid")])
def test_live_identity_claim_cannot_reuse_the_entire_region_as_its_label_anchor(identity, pattern):
    # 真实响应把两条选择题行自报为身份字段，且把整行框原样复制为标签锚点。
    value = row("identity", None, static=identity, identity_pattern=pattern, boundary_basis="label_and_field")
    value["identity_label"] = identity
    value["visual_evidence"]["anchor_bbox"] = copy.deepcopy(value["proposed_bbox"])
    assert "MODEL_OBS_IDENTITY_LABEL_ANCHOR_NOT_LOCALIZED" in review(value)["reason_codes"]
    assert value["semantic_check"] == "needs_review"


@pytest.mark.parametrize("identity,pattern", [("姓名", "name_line"), ("准考证号", "number_grid")])
def test_localized_identity_label_still_passes_model_observation_review(identity, pattern):
    value = row("identity", None, static=identity, identity_pattern=pattern,
                anchor_bbox={"x": .1, "y": .2, "width": .04, "height": .02})
    value["identity_label"] = identity
    assert not review(value)["reason_codes"]


@pytest.mark.parametrize("static,identity,pattern", [
    ("Name", "姓名", "name_line"), ("Student ID", "学号", "number_grid"),
    ("Candidate No.", "准考证号", "number_grid"),
    ("Class", "班级", "identity_fields"), ("School", "学校", "identity_fields"),
])
def test_actual_english_identity_labels_are_compared_without_fabricating_chinese_evidence(static, identity, pattern):
    value = row("identity", None, static=static, identity_pattern=pattern,
                anchor_bbox={"x": .1, "y": .2, "width": .04, "height": .02})
    value["identity_label"] = identity
    assert not review(value)["reason_codes"]
    assert value["visual_evidence"]["static_label"] == static
    Draft202012Validator(model_output_schema([], 1, include_observations=True)).validate({"candidates": [value]})


@pytest.mark.parametrize("static", ["条形码", "条码粘贴区", "条形码区", "Barcode"])
def test_actual_barcode_alias_labels_support_an_empty_placeholder(static):
    value = row("identity", None, static=static, barcode_pattern="labelled_placeholder")
    value["identity_label"] = "条码"
    assert not review(value)["reason_codes"]
    Draft202012Validator(model_output_schema([], 1, include_observations=True)).validate({"candidates": [value]})


@pytest.mark.parametrize("role,label", [("answer", "12"), ("objective_group", None)])
def test_english_identity_labels_remain_cross_category_counterevidence(role, label):
    value = row(role, label, static="Name", option_pattern="bubble_options" if role == "objective_group" else "none")
    if role == "objective_group":
        value["observed_question_labels"] = ["1"]
    assert f"MODEL_OBS_{'ANSWER' if role == 'answer' else 'OBJECTIVE'}_STATIC_LABEL_CONFLICT" in review(value)["reason_codes"]


def test_english_qr_placeholder_cannot_pass_as_linear_barcode():
    value = row("identity", None, static="QR Code", barcode_pattern="labelled_placeholder")
    value["identity_label"] = "条码"
    assert "MODEL_OBS_BARCODE_TYPE_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("identity", ["班级", "学校"])
@pytest.mark.parametrize("pattern", ["none", "unknown", "name_line", "number_grid"])
def test_unlabelled_class_or_school_cannot_rely_on_empty_or_other_field_structure(identity, pattern):
    value = row("identity", None, identity_pattern=pattern)
    value["identity_label"] = identity
    assert "MODEL_OBS_IDENTITY_EVIDENCE_MISSING" in review(value)["reason_codes"]


@pytest.mark.parametrize("identity,conflict", [("班级", "学校"), ("学校", "班级")])
def test_general_identity_structure_does_not_override_a_conflicting_class_or_school_label(identity, conflict):
    value = row("identity", None, static=conflict, identity_pattern="identity_fields")
    value["identity_label"] = identity
    assert "MODEL_OBS_IDENTITY_LABEL_CONFLICT" in review(value)["reason_codes"]


def test_positive_identity_structure_does_not_erase_a_conflicting_question_section_label():
    value = row("identity", None, static="选择题", identity_pattern="name_line")
    value["identity_label"] = "姓名"
    assert "MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT" in review(value)["reason_codes"]


def test_choice_section_with_formal_fill_number_cannot_pass_as_an_answer_region():
    value = row(label="12", static="选择题")
    assert "MODEL_OBS_QUESTION_TYPE_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("static", ["单选题", "多选题", "单项选择题", "多项选择题", "判断题"])
def test_exact_choice_heading_aliases_keep_original_evidence_and_question_type_checks(static):
    choice = row("objective_group", None, static=static, option_pattern="letter_options")
    choice["observed_question_labels"] = ["1"]
    Draft202012Validator(model_output_schema([], 1, True)).validate({"candidates": [choice]})
    assert not review(choice)["reason_codes"]
    assert choice["visual_evidence"]["static_label"] == static

    identity = row("identity", None, static=static, identity_pattern="name_line")
    identity["identity_label"] = "姓名"
    assert "MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT" in review(identity)["reason_codes"]
    assert "MODEL_OBS_QUESTION_TYPE_CONFLICT" in review(row(label="12", static=static))["reason_codes"]
    assert "MODEL_OBS_INSTRUCTION_EVIDENCE_MISSING" in review(row("instruction", None, static=static))["reason_codes"]


@pytest.mark.parametrize("static", ["正确填涂", "错误填涂", "填涂示例"])
def test_fill_examples_are_bounded_instruction_labels_without_forbidden_auto_exclusion(static):
    instruction = row("instruction", None, static=static)
    Draft202012Validator(model_output_schema([], 1, True)).validate({"candidates": [instruction]})
    assert not review(instruction)["reason_codes"]
    candidate = row("answer", None, static=static)
    candidate["proposed_bbox"] = {"x": .1, "y": .2, "width": .1, "height": .05}
    before = copy.deepcopy(candidate)
    assert not exclude_isolated_model_warning(candidate)
    assert candidate == before


@pytest.mark.parametrize("static", ["正确填涂", "错误填涂", "填涂示例"])
@pytest.mark.parametrize("content", ["question", "choice", "identity", "barcode"])
def test_fill_example_instruction_cannot_hide_observed_answer_or_identity_content(static, content):
    value = row("instruction", "1" if content == "question" else None, static=static)
    if content == "choice":
        value["visual_evidence"]["option_pattern"] = "letter_options"
    elif content == "identity":
        value["visual_evidence"]["identity_pattern"] = "name_line"
    elif content == "barcode":
        value["visual_evidence"]["barcode_pattern"] = "linear_bars"
    assert "MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("role,label", [("identity", None), ("answer", "12"), ("objective_group", "1")])
def test_fill_example_static_label_remains_counterevidence_for_actual_region_roles(role, label):
    value = row(role, label, static="正确填涂", option_pattern="letter_options" if role == "objective_group" else "none")
    if role == "identity":
        value["identity_label"] = "姓名"
        value["visual_evidence"]["identity_pattern"] = "name_line"
    assert "MODEL_OBS_WARNING_CATEGORY_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("static", ["单选题学生甲", "正确填涂学生甲", "一、单选题", "未登记静态标题"])
def test_heading_aliases_do_not_turn_static_label_into_free_text(static):
    value = row(static=static)
    assert list(Draft202012Validator(model_output_schema([], 1, True)).iter_errors({"candidates": [value]}))


def test_answer_with_static_identity_or_barcode_label_keeps_the_conflict():
    value = row(label="15", static="姓名")
    assert "MODEL_OBS_ANSWER_STATIC_LABEL_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("role,prefix", [("objective_group", "OBJECTIVE"), ("answer", "ANSWER")])
@pytest.mark.parametrize("evidence,category", [
    ({"identity_pattern": "name_line"}, "IDENTITY"),
    ({"identity_pattern": "number_grid"}, "IDENTITY"),
    ({"identity_pattern": "identity_fields"}, "IDENTITY"),
    ({"barcode_pattern": "linear_bars"}, "BARCODE"),
    ({"barcode_pattern": "qr_modules"}, "BARCODE"),
    ({"barcode_pattern": "labelled_placeholder"}, "BARCODE"),
])
def test_answer_or_choice_region_cannot_silently_include_reported_identity_or_barcode_structure(role, prefix, evidence,
                                                                                             category):
    value = row(role, "1" if role == "objective_group" else "15", **evidence)
    if role == "objective_group":
        value["visual_evidence"]["option_pattern"] = "bubble_options"
    value["proposed_bbox"] = {"x": .02, "y": .05, "width": .9, "height": .8}
    assert f"MODEL_OBS_{prefix}_{category}_CONFLICT" in review(value)["reason_codes"]
    assert value["semantic_check"] == "needs_review"


@pytest.mark.parametrize("identity,static,pattern", [
    ("条码", "姓名", "linear_bars"), ("二维码", "准考证号", "qr_modules"),
    ("姓名", "条码", "none"), ("准考证号", "二维码粘贴区", "none"),
])
def test_identity_static_label_of_another_field_is_not_overridden_by_matching_structure(identity, static, pattern):
    value = row("identity", None, static=static, barcode_pattern=pattern,
                identity_pattern="name_line" if identity == "姓名" else "number_grid")
    value["identity_label"] = identity
    assert "MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("identity,static", [("条码", "二维码粘贴区"), ("二维码", "条形码粘贴区")])
def test_labelled_blank_code_placeholder_must_match_reported_code_type(identity, static):
    value = row("identity", None, static=static, barcode_pattern="labelled_placeholder")
    value["identity_label"] = identity
    assert "MODEL_OBS_BARCODE_TYPE_CONFLICT" in review(value)["reason_codes"]


def test_barcode_next_to_number_field_is_kept_for_review_instead_of_discarded():
    value = row("identity", None, static="条形码粘贴区", barcode_pattern="labelled_placeholder",
                identity_pattern="number_grid")
    value["identity_label"] = "条码"
    assert "MODEL_OBS_BARCODE_IDENTITY_CONFLICT" in review(value)["reason_codes"]


def test_name_region_with_barcode_structure_keeps_cross_category_conflict():
    value = row("identity", None, static="姓名", identity_pattern="name_line", barcode_pattern="qr_modules")
    value["identity_label"] = "姓名"
    assert "MODEL_OBS_IDENTITY_BARCODE_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("evidence", [
    {"option_pattern": "letter_options"}, {"option_pattern": "bubble_options"},
    {"identity_pattern": "name_line"}, {"identity_pattern": "number_grid"},
    {"identity_pattern": "identity_fields"}, {"barcode_pattern": "linear_bars"},
    {"barcode_pattern": "qr_modules"}, {"barcode_pattern": "labelled_placeholder"},
])
def test_other_role_cannot_hide_reported_answer_identity_or_code_structure(evidence):
    value = row("other", None, **evidence)
    assert "MODEL_OBS_OTHER_CONTENT_CONFLICT" in review(value)["reason_codes"]


@pytest.mark.parametrize("observed,printed", [(["15"], None), ([], "15")])
def test_other_role_with_a_formal_question_number_is_kept_for_review(observed, printed):
    value = row("other", printed)
    value["observed_question_labels"] = observed
    assert "MODEL_OBS_OTHER_CONTENT_CONFLICT" in review(value)["reason_codes"]


def test_other_role_with_only_an_unrelated_number_does_not_invent_an_answer_region():
    value = row("other", None)
    value["observed_question_labels"] = ["99"]
    assert "MODEL_OBS_OTHER_CONTENT_CONFLICT" not in review(value)["reason_codes"]
    assert "MODEL_OBS_QUESTION_LABEL_NOT_IN_LIST" in value["reason_codes"]


def test_other_role_without_positive_content_does_not_require_a_warning_label():
    value = row("other", None)
    assert not review(value)["reason_codes"]


@pytest.mark.parametrize("role", ["answer", "objective_group"])
def test_unknown_identity_and_barcode_observations_are_not_positive_cross_category_evidence(role):
    value = row(role, "1" if role == "objective_group" else "15",
                identity_pattern="unknown", barcode_pattern="unknown",
                option_pattern="bubble_options" if role == "objective_group" else "none")
    assert not review(value)["reason_codes"]
