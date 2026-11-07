"""按职责保留现有断言与固定响应，不改变模型协议或运行输入。"""

import copy
import json

import pytest
from grading_agent.template_analysis_repair import (
    _repair_tasks,
)
from test_template_analysis_repair import (
    ReplayModel,
    code_hint,
    code_region,
    model_row,
    normalized_and_reviewed,
    payload,
    region,
    run,
)


def test_false_identity_inside_verified_answer_replaces_entire_connected_context_once():
    wrong = region("false-identity", role="uncertain", label=None, x=.36, y=.115,
                   width=.278, height=.075, supported=False, reasons=["MODEL_PROPOSED_IDENTITY"])
    code = region("false-code", label="条码", x=.36, y=.115, width=.278, height=.075,
                  reasons=["MODEL_PROPOSED_IDENTITY"])
    answer = region("actual-15", role="answer", label=None, question="15", x=.36, y=.08,
                    width=.278, height=.83, reasons=["PRINTED_FRAME_VERIFIED"])
    answer["evidence_refs"] = ["cv-frame:actual-15"]
    before = copy.deepcopy([wrong, code, answer])
    tasks = _repair_tasks(payload(), [wrong, code, answer])
    assert len(tasks) == 1
    assert set(tasks[0]["replace_candidate_ids"]) == {"false-identity", "false-code", "actual-15"}
    assert tasks[0]["required_answer_labels"] == ["15"]
    assert tasks[0]["bbox"]["y"] < .08
    assert tasks[0]["bbox"]["y"] + tasks[0]["bbox"]["height"] > .91
    assert [wrong, code, answer] == before
    model = ReplayModel([{"candidates": [model_row(question="15")]}])
    batch = run(model, [wrong, code, answer])["batches"][0]
    assert batch["required_answer_labels"] == ["15"]


@pytest.mark.parametrize("missing", ["frame", "supported", "independent_identity", "ambiguous_answer"])
def test_containing_answer_does_not_expand_repair_without_unique_independent_frame(missing):
    wrong = region("false-identity", role="uncertain", label=None, x=.36, y=.115,
                   width=.278, height=.075, supported=False, reasons=["MODEL_PROPOSED_IDENTITY"])
    answer = region("actual-15", role="answer", label=None, question="15", x=.36, y=.08,
                    width=.278, height=.83, reasons=["PRINTED_FRAME_VERIFIED"])
    answer["evidence_refs"] = ["cv-frame:actual-15"]
    rows = [wrong, answer]
    if missing == "frame":
        answer["evidence_refs"] = []
    elif missing == "supported":
        answer["semantic_check"] = "needs_review"
    elif missing == "independent_identity":
        wrong["reason_codes"].append("SEMANTIC_IDENTITY_LABEL_VERIFIED")
    else:
        duplicate = copy.deepcopy(answer)
        duplicate["candidate_id"] = "another-answer"
        rows.append(duplicate)
    task = _repair_tasks(payload(), rows)[0]
    assert task["replace_candidate_ids"] == ["false-identity"]
    assert "required_answer_labels" not in task


def test_adjacent_identity_hypotheses_are_one_neutral_crop_and_restore_source_coordinates():
    wrong = [region(), region("wrong-number", label="准考证号", y=.42, height=.08)]
    original = copy.deepcopy(wrong)
    answer = model_row()
    model = ReplayModel([{"candidates": [answer]}])
    result = run(model, wrong)
    assert len(model.calls) == len(result["batches"]) == 1
    batch = result["batches"][0]
    assert batch["replace_candidate_ids"] == ["wrong-name", "wrong-number"]
    crop, restored = batch["crop_bbox"], batch["candidates"][0]
    assert crop["x"] < .06 and crop["x"] + crop["width"] < .4
    assert crop["y"] < .33 and crop["y"] + crop["height"] > .5
    assert restored["proposed_bbox"]["x"] == pytest.approx(crop["x"] + .1 * crop["width"])
    assert restored["proposed_bbox"]["height"] == pytest.approx(.3 * crop["height"])
    assert restored["candidate_id"] is None
    assert restored["evidence_refs"] == ["source-page:" + "a" * 64 + ":1"]
    attempt = result["metadata"]["attempts"][0]
    assert attempt["crop_width"] > 0 and attempt["crop_height"] > 0
    assert len(attempt["crop_sha256"]) == 64 and "data_base64" not in attempt
    manifest = json.loads(model.calls[0]["messages"][1]["content"][0]["text"])
    assert "identity_label" not in manifest and "proposed_bbox" not in manifest
    assert manifest["coordinate_space"] == "crop_normalized"
    assert "不根据任务名称或整场题号清单预设类别" in manifest["instruction"]
    assert wrong == original


def test_different_columns_do_not_merge_and_call_limit_is_hard():
    rows = [region(f"identity-{index}", x=.05 + index * .22, width=.18) for index in range(4)]
    model = ReplayModel([{"candidates": []}] * 3)
    result = run(model, rows, max_calls=100)
    assert len(model.calls) == result["metadata"]["request_count"] == 3
    assert result["batches"] == []
    assert "MULTIMODAL_REPAIR_CALL_LIMIT_REACHED" in result["metadata"]["reason_codes"]


def test_real_overlapping_identity_panel_leaves_calls_for_mixed_and_objective_regions():
    # Trial13 中姓名框压到右侧码框，旧调度重复读身份面板两次，使选择题没有局部调用机会。
    rows = [region("name", x=.0625, y=.115, width=.19, height=.075),
            region("number", label="准考证号", x=.0625, y=.19, width=.19, height=.035),
            region("code", label="条码", x=.225, y=.115, width=.135, height=.075,
                   supported=False, reasons=["MODEL_PROPOSED_IDENTITY"]),
            region("mixed", role="uncertain", label=None, x=.0625, y=.48, width=.19, height=.12,
                   supported=False, reasons=["MODEL_OBS_MULTIPLE_QUESTION_LABELS"]),
            region("choice-upper", role="objective_group", label=None, x=.0625, y=.33, width=.19, height=.09,
                   supported=False, reasons=["OBJECTIVE_ITEMS_DEFERRED_TO_LOCAL"]),
            region("choice-lower", role="objective_group", label=None, x=.0625, y=.42, width=.19, height=.06,
                   supported=False, reasons=["OBJECTIVE_ITEMS_DEFERRED_TO_LOCAL"])]
    original = copy.deepcopy(rows)
    tasks = _repair_tasks(payload(), rows)
    assert [task["task"] for task in tasks] == ["identity_reclassify", "mixed_region", "objective_region"]
    assert tasks[0]["replace_candidate_ids"] == ["name", "number", "code"]
    assert tasks[0]["bbox"]["x"] + tasks[0]["bbox"]["width"] > .36
    model = ReplayModel([{"candidates": []}] * 3)
    result = run(model, rows)
    assert [attempt["task"] for attempt in result["metadata"]["attempts"]] == [
        "identity_reclassify", "mixed_region", "objective_region"]
    assert result["metadata"]["request_count"] == 3
    assert "MULTIMODAL_REPAIR_CALL_LIMIT_REACHED" not in result["metadata"]["reason_codes"]
    assert rows == original


def test_model_code_and_unique_detector_hint_share_one_identity_crop_and_all_replacement_targets():
    old, hint = code_region("model-code"), code_hint("code-hint", x=.219)
    hint["_extra_replace_candidate_ids"] = ["stale-code", "model-code"]
    tasks = _repair_tasks(payload(), [old, hint])
    assert len(tasks) == 1
    assert tasks[0]["task"] == "identity_reclassify"
    assert tasks[0]["replace_candidate_ids"] == ["model-code", "code-hint", "stale-code"]
    assert tasks[0]["bbox"]["x"] < .219
    assert tasks[0]["bbox"]["x"] + tasks[0]["bbox"]["width"] > .35


def test_identity_clustering_revisits_earlier_rows_when_a_later_overlap_connects_them():
    first = region("first", x=.05, y=.1, width=.2, height=.04, supported=False)
    last = region("last", x=.05, y=.28, width=.2, height=.04, supported=False)
    middle = region("middle", x=.05, y=.19, width=.2, height=.04, supported=False)
    tasks = _repair_tasks(payload(), [first, last, middle])
    assert len(tasks) == 1
    assert set(tasks[0]["replace_candidate_ids"]) == {"first", "last", "middle"}


def test_two_actual_code_anchors_stay_separate_even_when_their_outer_boxes_and_identity_context_overlap():
    # 不同实测码图共享一个宽外框时，不能通过身份框的传递连接吞并第二个码。
    first = code_region("code-one", x=.1, anchor_x=.12)
    second = code_region("code-two", x=.1, anchor_x=.25)
    second["proposed_bbox"]["width"] = .25
    first["proposed_bbox"]["width"] = .25
    bridge = region("name", x=.05, y=.1, width=.31, height=.1, supported=False)
    tasks = _repair_tasks(payload(), [bridge, first, second])
    assert len(tasks) == 2
    assert set(tasks[0]["replace_candidate_ids"]) == {"name", "code-one"}
    assert tasks[1]["replace_candidate_ids"] == ["code-two"]


def test_distant_identity_fields_do_not_merge_and_nearby_distinct_codes_keep_their_own_hints():
    rows = [region("upper", x=.05, y=.05, width=.12, height=.03, supported=False),
            region("lower", x=.05, y=.7, width=.12, height=.03, supported=False),
            code_region("code-one", x=.3), code_region("code-two", x=.3, y=.23),
            code_hint("hint-one", x=.3), code_hint("hint-two", x=.3, y=.23)]
    tasks = _repair_tasks(payload(), rows)
    assert len(tasks) == 4
    assert [set(task["replace_candidate_ids"]) for task in tasks] == [
        {"upper"}, {"lower"}, {"code-one", "hint-one"}, {"code-two", "hint-two"}]


def test_real_incident_geometry_selects_identity_pair_and_failed_mixed_region_only():
    rows = [region(), region("wrong-number", label="准考证号", y=.42, height=.08),
            region("mixed", role="uncertain", label=None, supported=False, y=.508, height=.407,
                   reasons=["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]),
            region("correct-15", role="answer", label=None, question="15", x=.36, y=.08, height=.83)]
    tasks = _repair_tasks(payload(), rows)
    assert [task["task"] for task in tasks] == ["identity_reclassify", "mixed_region"]
    assert set().union(*(set(task["replace_candidate_ids"]) for task in tasks)) == {"wrong-name", "wrong-number", "mixed"}


def test_clean_upper_identity_and_complete_objective_mapping_are_not_rechecked():
    choice = region("objective", role="objective_group", label=None, y=.2)
    choice["objective_items"] = [{"printed_question_label": "1"}, {"printed_question_label": "2"}]
    assert _repair_tasks(payload(), [region(y=.05, height=.04), choice]) == []


@pytest.mark.parametrize("y,height", [(.01, .03), (.72, .04)])
def test_missing_choice_with_only_self_reported_identity_triggers_independent_crop_without_page_position_assumption(y, height):
    candidate = region(y=y, height=height, reasons=["MULTIMODAL_CONTENT_CONSISTENT"])
    assert _repair_tasks(payload(), [candidate])[0]["replace_candidate_ids"] == ["wrong-name"]


def test_independently_verified_identity_is_not_rechecked_only_because_exam_choices_are_elsewhere():
    candidate = region(reasons=["SEMANTIC_IDENTITY_LABEL_VERIFIED"])
    assert _repair_tasks(payload(), [candidate]) == []


def test_barcode_discovery_is_a_neutral_crop_task_before_lower_priority_missing_fill():
    barcode = region("code-hint", role="uncertain", label=None, supported=False,
                     reasons=["BARCODE_DISCOVERY_CANDIDATE"], origin="local_detector")
    fill = region("fill-12", role="uncertain", label=None, question="12", supported=False)
    tasks = _repair_tasks(payload(), [fill, barcode])
    assert [task["task"] for task in tasks] == ["barcode_region", "fill_region"]
    model = ReplayModel([{"candidates": []}])
    result = run(model, [barcode])
    assert result["batches"] == []
    manifest = json.loads(model.calls[0]["messages"][1]["content"][0]["text"])
    assert manifest["task"] == "inspect_visible_region_types"
    assert "图中没有条码就不输出条码" in model.calls[0]["messages"][0]["content"]
    assert "完整实线或虚线粘贴框" in model.calls[0]["messages"][0]["content"]


@pytest.mark.parametrize("reason", ["PAGE_COVERAGE_ROLE_CONFLICT", "PAGE_COVERAGE_QUESTION_CONFLICT",
    "PAGE_COVERAGE_IDENTITY_CONFLICT", "PAGE_COVERAGE_DISCOVERED_REGION"])
def test_independent_coverage_issue_is_a_generic_crop_before_previous_identity_hypotheses(reason):
    coverage = region("new-page-evidence", role="uncertain", label=None, supported=False, x=.6,
                      reasons=[reason])
    tasks = _repair_tasks(payload(), [region(), coverage])
    assert tasks[0]["task"] == "coverage_region"
    assert tasks[0]["replace_candidate_ids"] == ["new-page-evidence"]
    assert tasks[1]["task"] == "identity_reclassify"


def test_coverage_hint_replaces_old_code_atomically_and_deferred_old_box_is_never_requested_again():
    old = region("old-code", role="uncertain", label=None, supported=False,
                 reasons=["PAGE_COVERAGE_IDENTITY_CONFLICT"])
    old["_repair_deferred_to_hint"] = True
    hint = region("new-code-location", role="uncertain", label=None, supported=False, x=.6,
                  reasons=["PAGE_COVERAGE_DISCOVERED_REGION", "BARCODE_DISCOVERY_CANDIDATE"])
    hint["_extra_replace_candidate_ids"] = ["old-code", "old-code"]
    tasks = _repair_tasks(payload(), [old, hint])
    assert len(tasks) == 1 and tasks[0]["task"] == "coverage_region"
    assert tasks[0]["replace_candidate_ids"] == ["new-code-location", "old-code"]
    assert tasks[0]["bbox"]["x"] > .5
    model = ReplayModel([{"candidates": [model_row()]}])
    batch = run(model, [old, hint], max_calls=1)["batches"][0]
    assert batch["replace_candidate_ids"] == ["new-code-location", "old-code"]


def test_coverage_extra_target_is_claimed_once_even_if_old_row_was_not_explicitly_deferred():
    old = region("old-name", supported=False)
    hint = region("new-location", role="uncertain", label=None, supported=False, x=.6,
                  reasons=["PAGE_COVERAGE_DISCOVERED_REGION"])
    hint["_extra_replace_candidate_ids"] = ["old-name"]
    tasks = _repair_tasks(payload(), [old, hint])
    assert len(tasks) == 1 and tasks[0]["replace_candidate_ids"] == ["new-location", "old-name"]


def test_new_coverage_location_claims_old_conflict_before_single_available_crop():
    # 独立整页观察已否定旧位置；只剩一次调用时必须看新位置，而非按 existing 顺序再看旧框。
    old = region("old-code", role="uncertain", label=None, supported=False,
                 reasons=["PAGE_COVERAGE_IDENTITY_CONFLICT"])
    hint = region("new-code-location", role="uncertain", label=None, supported=False, x=.6,
                  reasons=["PAGE_COVERAGE_DISCOVERED_REGION"])
    hint["_extra_replace_candidate_ids"] = ["old-code"]
    tasks = _repair_tasks(payload(), [old, hint])
    assert len(tasks) == 1
    assert tasks[0]["replace_candidate_ids"] == ["new-code-location", "old-code"]
    model = ReplayModel([{"candidates": [model_row()]}])
    result = run(model, [old, hint], max_calls=1)
    assert result["metadata"]["request_count"] == 1
    assert result["batches"][0]["crop_bbox"]["x"] > .5


def test_missing_exam_fill_numbers_without_visible_page_evidence_do_not_create_tasks():
    assert _repair_tasks(payload(), [region("correct", role="answer", label=None, question="15", y=.08)]) == []


def test_visible_local_fill_candidate_gets_neutral_crop_without_exam_allowlists_or_forced_category():
    local = region("fill-12", role="uncertain", label=None, question="12", y=.6, height=.08,
                   supported=False, origin="local_detector", reasons=["LOCAL_FILL_REQUIRES_REVIEW"])
    model = ReplayModel([{"candidates": [model_row()]}])
    result = run(model, [local])
    task = result["batches"][0]
    assert task["task"] == "fill_region" and task["replace_candidate_ids"] == ["fill-12"]
    props = model.calls[0]["schema"]["properties"]["candidates"]["items"]["properties"]
    assert "printed_question_label" not in props
    assert {"identity", "objective_group", "answer"} <= set(props["role"]["enum"])
    assert task["candidates"][0]["printed_question_label"] == "12"
    manifest = json.loads(model.calls[0]["messages"][1]["content"][0]["text"])
    assert manifest["task"] == "inspect_visible_region_types"
    assert manifest["question_labels_scope"] == "server_side_only_not_provided"
    assert "question_labels" not in manifest
    assert "objective_question_labels" not in manifest
    assert "fill_blank_question_labels" not in manifest


def test_mislocated_fill_hint_can_propose_actual_identity_instead_of_inventing_answer():
    local = region("fill-hint", role="uncertain", label=None, question="12", supported=False,
                   reasons=["LOCAL_FILL_REQUIRES_REVIEW"])
    identity = model_row(role="identity", question=None, label="姓名")
    identity.update(static_label="姓名", identity_pattern="name_line", boundary_basis="label_and_field",
                    anchor_bbox={"x": .1, "y": .2, "width": .05, "height": .06})
    result = run(ReplayModel([{"candidates": [identity]}]), [local])
    reviewed = normalized_and_reviewed(result["batches"][0]["candidates"])
    assert reviewed[0]["role"] == "identity"
    assert reviewed[0]["identity_label"] == "姓名"
    assert reviewed[0]["printed_question_label"] is None
    assert reviewed[0]["semantic_check"] == "supported"


def test_uncertain_identity_after_original_fields_are_removed_remains_repairable():
    uncertain = region("lost-identity", role="uncertain", label=None, supported=False,
                       reasons=["MODEL_PROPOSED_IDENTITY", "MODEL_OBS_IDENTITY_OPTION_CONFLICT"])
    assert _repair_tasks(payload(), [uncertain])[0]["replace_candidate_ids"] == ["lost-identity"]


def test_multiple_page_question_observation_triggers_mixed_crop_without_global_fill_guess():
    # normalize 会清掉 12/13/14 中无法唯一关联的 printed label；这个原因码是本页
    # 仍可追溯的证据，必须直接驱动局部复判，而不是用全场缺题清单猜框内内容。
    coarse = region("multi-question", role="uncertain", label=None, question=None,
                    x=.05, y=.45, width=.86, height=.28, supported=False,
                    reasons=["MODEL_OBS_MULTIPLE_QUESTION_LABELS"])
    tasks = _repair_tasks(payload(), [coarse])
    assert len(tasks) == 1
    assert tasks[0]["task"] == "mixed_region"
    assert tasks[0]["fill_question_labels"] == []
    assert tasks[0]["replace_candidate_ids"] == ["multi-question"]


def test_identity_task_does_not_claim_supported_identity_that_contains_barcode_hint():
    identity = region("supported-identity", label="姓名", x=.05, y=.1, width=.5, height=.3,
                      supported=True)
    hint = region("barcode-hint", role="uncertain", label=None, x=.2, y=.18, width=.1,
                  height=.08, supported=False, reasons=["BARCODE_DISCOVERY_CANDIDATE"])
    hint["_barcode_discovery_hint"] = True
    tasks = _repair_tasks(payload(), [identity, hint])
    assert [task["task"] for task in tasks] == ["barcode_region"]
    assert tasks[0]["replace_candidate_ids"] == ["barcode-hint"]


def test_adjacent_bad_objective_regions_are_one_task_but_reliable_answer_region_breaks_cluster():
    first = region("choice-1-8", role="objective_group", label=None, x=.1, y=.1,
                   width=.7, height=.12, supported=False,
                   reasons=["MODEL_OBS_OBJECTIVE_ITEMS_MISSING"])
    second = region("choice-9-11", role="objective_group", label=None, x=.1, y=.26,
                    width=.7, height=.12, supported=False,
                    reasons=["MODEL_OBS_OBJECTIVE_ITEMS_MISSING"])
    tasks = _repair_tasks(payload(), [first, second])
    assert len(tasks) == 1
    assert tasks[0]["replace_candidate_ids"] == ["choice-1-8", "choice-9-11"]

    reliable = region("answer-15", role="answer", label=None, question="15", x=.1, y=.225,
                      width=.7, height=.02, supported=True)
    tasks = _repair_tasks(payload(), [first, reliable, second])
    assert len(tasks) == 2
    assert [task["replace_candidate_ids"] for task in tasks] == [["choice-1-8"], ["choice-9-11"]]


def test_layout_first_objective_without_item_coordinates_triggers_local_refinement():
    uncertain = region("large-choice", role="uncertain", label=None, supported=False,
                       reasons=["MODEL_PROPOSED_OBJECTIVE_GROUP", "MODEL_OBS_OBJECTIVE_ITEMS_MISSING"])
    assert _repair_tasks(payload(), [uncertain])[0]["task"] == "objective_region"
