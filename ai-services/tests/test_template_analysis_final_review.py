"""末轮整页查漏的独立门禁测试。

这些测试只覆盖 coverage 建议如何进入主流水线，不代表供应商模型在真实答题卡上的
识别率；真实响应仍需经过受控运行和页面级验收。
"""

import copy

import pytest
from grading_agent.template_analysis_coarse import expand_coarse_output
from grading_agent.template_analysis_final_review import apply_page_coverage

PAGE = {"page_no": 1, "width": 1000, "height": 1000, "sha256": "a" * 64}
PAYLOAD = {
    "request_id": "final-review-fixture-0001",
    "page": PAGE,
    "question_labels": ["12", "15"],
    "objective_question_labels": [],
    "writing_state": "blank",
}


def box(x, y, width, height):
    return {"x": x, "y": y, "width": width, "height": height}


def row(role, bbox, *, label=None, identity=None, status="supported", candidate_id=None):
    return {
        "candidate_id": candidate_id or f"old-{role}-{label or identity or 'x'}-{bbox['x']}",
        "page_no": 1,
        "role": role,
        "printed_question_label": label,
        "identity_label": identity,
        "proposed_bbox": copy.deepcopy(bbox),
        "source_bbox": copy.deepcopy(bbox),
        "association_status": "associated" if role == "answer" and label else "unassociated",
        "evidence_refs": ["source-page:aaaaaaaaaaaaaaaa:1"],
        "reason_codes": [],
        "semantic_check": status,
    }


def coarse_candidate(role, bbox, *, label=None, identity=None):
    """走真实粗响应展开，防止测试漏掉内部阶段标记而绕过生产故障。"""
    supplied = {
        "identity_label": identity,
        "role": role,
        "bbox": copy.deepcopy(bbox),
        "labels": [label] if label else [],
        "static_label": "填空题" if role == "answer" else None,
        "option_pattern": "none",
        "identity_pattern": "none",
        "barcode_pattern": "none",
        "boundary_basis": "unknown",
        "anchor_bbox": None,
    }
    return expand_coarse_output({"candidates": [supplied]}, PAYLOAD)["candidates"][0]


class GateEvidence:
    """记录查漏候选确实经过本地内容门禁，并给测试候选一个可用结果。"""

    def __init__(self, *, status="supported"):
        self.calls = 0
        self.status = status

    def review(self, rows, _payload):
        self.calls += 1
        for candidate in rows:
            candidate["semantic_check"] = self.status
            if self.status == "supported" and "LOCAL_CONTENT_TEST_EVIDENCE" not in candidate["reason_codes"]:
                candidate["reason_codes"].append("LOCAL_CONTENT_TEST_EVIDENCE")


def apply(existing, coverage, *, evidence=None):
    evidence = evidence or GateEvidence()
    result = apply_page_coverage(existing, coverage, PAYLOAD, [], PAGE, evidence)
    return result, evidence


def proposed(*, observations=(), uncovered=()):
    return {"metadata": {"status": "proposed"}, "observations": list(observations),
            "uncovered": list(uncovered)}


def observation(index, role="answer", labels=None, identity=None):
    return {"index": index, "role": role, "labels": [] if labels is None else labels,
            "identity_label": identity}


def test_observation_indexes_are_stable_and_unobserved_rows_are_untouched():
    first = row("answer", box(.05, .05, .2, .2), label="12")
    second = row("answer", box(.5, .05, .2, .2), label="15")
    before_second = copy.deepcopy(second)
    result, _ = apply([first, second], proposed(observations=[observation(0, labels=["15"])]))

    assert result["observed_conflict_count"] == 1
    assert first["semantic_check"] == "needs_review"
    assert "PAGE_COVERAGE_QUESTION_CONFLICT" in first["reason_codes"]
    # coverage 没有返回 index=1 时不能把未观察的第二个候选顺带降级或重编号。
    assert second == before_second


def test_explicit_role_or_question_conflict_downgrades_but_never_confirms():
    existing = [row("answer", box(.05, .05, .2, .2), label="12")]
    result, _ = apply(existing, proposed(observations=[observation(0, role="identity", labels=[])]))

    assert result["observed_conflict_count"] == 1
    assert existing[0]["semantic_check"] == "needs_review"
    assert existing[0]["role"] == "answer"
    assert "PAGE_COVERAGE_ROLE_CONFLICT" in existing[0]["reason_codes"]
    assert existing[0].get("status") != "confirmed"


def test_answer_observation_cannot_hide_another_formal_question_by_including_the_old_label():
    answer = row("answer", box(.05, .05, .3, .3), label="15")
    result, _ = apply([answer], proposed(observations=[observation(0, labels=["15", "12"])]))

    assert result["observed_conflict_count"] == 1
    assert answer["semantic_check"] == "needs_review"
    assert answer["printed_question_label"] == "15"
    assert answer["association_status"] == "ambiguous"
    assert "PAGE_COVERAGE_QUESTION_CONFLICT" in answer["reason_codes"]


@pytest.mark.parametrize("labels", [["15.1", "15.2"], ["15", "15(1)", "15（2）"]])
def test_answer_parent_and_continuous_child_labels_remain_one_question(labels):
    answer = row("answer", box(.05, .05, .3, .3), label="15")
    before = copy.deepcopy(answer)
    result, _ = apply([answer], proposed(observations=[observation(0, labels=labels)]))

    assert result["observed_conflict_count"] == 0
    assert answer == before


def test_formally_configured_child_question_is_not_folded_into_its_parent():
    payload = {**PAYLOAD, "question_labels": ["15", "15.1"]}
    answer = row("answer", box(.05, .05, .3, .3), label="15")
    result = apply_page_coverage([answer], proposed(observations=[observation(0, labels=["15", "15.1"])]),
                                 payload, [], PAGE, GateEvidence())

    assert result["observed_conflict_count"] == 1
    assert "PAGE_COVERAGE_QUESTION_CONFLICT" in answer["reason_codes"]


@pytest.mark.parametrize("observations", [[], [observation(0, role="uncertain", labels=["12"])],
                                          [observation(0)], [observation(0, labels=["77"])]])
def test_uncertain_empty_or_nonformal_observation_is_not_new_question_evidence(observations):
    answer = row("answer", box(.05, .05, .3, .3), label="15")
    before = copy.deepcopy(answer)
    result, _ = apply([answer], proposed(observations=observations))

    assert result["observed_conflict_count"] == 0
    assert answer == before


@pytest.mark.parametrize("labels,expected", [
    (["1", "12"], {"PAGE_COVERAGE_QUESTION_TYPE_CONFLICT", "PAGE_COVERAGE_OBJECTIVE_ITEMS_CONFLICT"}),
    (["1"], {"PAGE_COVERAGE_OBJECTIVE_ITEMS_CONFLICT"}),
    (["2", "1"], set()), ([], set()), (["77"], set()),
])
def test_objective_observation_checks_formal_type_and_existing_item_coverage(labels, expected):
    payload = {**PAYLOAD, "question_labels": ["1", "2", "12", "15"], "objective_question_labels": ["1", "2"]}
    objective = row("objective_group", box(.05, .05, .3, .3))
    objective["objective_items"] = [{"printed_question_label": "1"}, {"printed_question_label": "2"}]
    before = copy.deepcopy(objective)
    result = apply_page_coverage([objective], proposed(observations=[observation(0, role="objective_group", labels=labels)]),
                                 payload, [], PAGE, GateEvidence())

    assert result["observed_conflict_count"] == bool(expected)
    assert set(objective["reason_codes"]) == expected
    if expected:
        assert objective["semantic_check"] == "needs_review"
        assert objective["objective_items"] == []
    else:
        assert objective == before


def test_uncertain_observation_does_not_invalidate_existing_objective_mapping():
    payload = {**PAYLOAD, "question_labels": ["1", "12"], "objective_question_labels": ["1"]}
    objective = row("objective_group", box(.05, .05, .3, .3))
    objective["objective_items"] = [{"printed_question_label": "1"}]
    before = copy.deepcopy(objective)
    result = apply_page_coverage([objective], proposed(observations=[observation(0, role="uncertain", labels=["12"])]),
                                 payload, [], PAGE, GateEvidence())

    assert result["observed_conflict_count"] == 0
    assert objective == before


def measured_omr_objective():
    labels = [str(number) for number in range(1, 12)]
    payload = {**PAYLOAD, "question_labels": [*labels, "12", "15"], "objective_question_labels": labels}
    objective = row("objective_group", box(.05, .05, .7, .8))
    objective["objective_items"] = [
        {"printed_question_label": label, "bbox": box(.08, .08 + index * .065, .5, .05),
         "options": [{"label": letter, "bbox": box(.12 + option * .1, .09 + index * .065, .06, .03)}
                     for option, letter in enumerate("ABCD")]}
        for index, label in enumerate(labels)
    ]
    objective["reason_codes"] = ["LOCAL_OBJECTIVE_OMR_LAYOUT_VERIFIED"]
    objective["evidence_refs"].append("local-omr:coarse-semantic-measured-omr-layout-v2:" + PAGE["sha256"][:16] + ":1")
    return payload, objective


@pytest.mark.parametrize("labels", [[str(number) for number in range(1, 9)], ["2"], ["9", "10", "11"]])
def test_coverage_subset_cannot_erase_complete_source_bound_measured_omr_mapping(labels):
    # 真实故障：OMR 已测得 11 题 44 格，末轮只读到 1–8，旧逻辑会清空整组。
    payload, objective = measured_omr_objective()
    original = copy.deepcopy(objective)
    result = apply_page_coverage([objective], proposed(observations=[observation(0, role="objective_group", labels=labels)]),
                                 payload, [], PAGE, GateEvidence())
    assert result["observed_conflict_count"] == 0
    assert objective == original
    assert sum(len(item["options"]) for item in objective["objective_items"]) == 44


@pytest.mark.parametrize("fault", ["no_reason", "no_reference", "other_source", "other_page", "not_supported",
                                   "overlapping_options", "missing_item_geometry", "wrong_official_type"])
def test_subset_exception_requires_current_valid_mapping_and_bound_omr_evidence(fault):
    payload, objective = measured_omr_objective()
    if fault == "no_reason":
        objective["reason_codes"] = []
    elif fault == "no_reference":
        objective["evidence_refs"] = []
    elif fault == "other_source":
        objective["evidence_refs"][-1] = "local-omr:coarse-semantic-measured-omr-layout-v2:" + "b" * 16 + ":1"
    elif fault == "other_page":
        objective["evidence_refs"][-1] = objective["evidence_refs"][-1][:-1] + "2"
    elif fault == "not_supported":
        objective["semantic_check"] = "needs_review"
    elif fault == "overlapping_options":
        objective["objective_items"][0]["options"][1]["bbox"] = objective["objective_items"][0]["options"][0]["bbox"]
    elif fault == "missing_item_geometry":
        objective["objective_items"][0].pop("bbox")
    elif fault == "wrong_official_type":
        payload["objective_question_labels"] = payload["objective_question_labels"][:-1]
    result = apply_page_coverage([objective], proposed(observations=[observation(0, role="objective_group", labels=["1"])]),
                                 payload, [], PAGE, GateEvidence())
    assert result["observed_conflict_count"] == 1
    assert "PAGE_COVERAGE_OBJECTIVE_ITEMS_CONFLICT" in objective["reason_codes"]
    assert objective["objective_items"] == []


@pytest.mark.parametrize("observed_role,labels,identity,code", [
    ("objective_group", ["1", "12"], None, "PAGE_COVERAGE_QUESTION_TYPE_CONFLICT"),
    ("identity", [], "姓名", "PAGE_COVERAGE_ROLE_CONFLICT"),
    ("instruction", [], None, "PAGE_COVERAGE_ROLE_CONFLICT"),
])
def test_independent_omr_does_not_override_positive_type_identity_or_instruction_counterevidence(observed_role, labels, identity, code):
    payload, objective = measured_omr_objective()
    result = apply_page_coverage([objective], proposed(observations=[observation(0, role=observed_role, labels=labels, identity=identity)]),
                                 payload, [], PAGE, GateEvidence())
    assert result["observed_conflict_count"] == 1
    assert code in objective["reason_codes"]
    assert objective["semantic_check"] == "needs_review"
    assert objective["objective_items"] == []


def test_new_formal_objective_number_still_contradicts_measured_mapping():
    payload, objective = measured_omr_objective()
    payload["question_labels"] = [*payload["question_labels"], "20"]
    payload["objective_question_labels"] = [*payload["objective_question_labels"], "20"]
    result = apply_page_coverage([objective], proposed(observations=[observation(0, role="objective_group", labels=["1", "20"])]),
                                 payload, [], PAGE, GateEvidence())
    assert result["observed_conflict_count"] == 1
    assert "PAGE_COVERAGE_OBJECTIVE_ITEMS_CONFLICT" in objective["reason_codes"]
    assert objective["objective_items"] == []


def test_instruction_with_observed_answer_is_exposed_for_repair():
    warning = row("instruction", box(.05, .45, .4, .08), identity=None)
    result, _ = apply([warning], proposed(observations=[observation(0, role="answer", labels=["12"])]))

    assert result["observed_conflict_count"] == 1
    assert warning["role"] == "uncertain"
    assert warning["semantic_check"] == "needs_review"
    assert warning["role"] not in {"instruction", "other"}


def test_uncovered_candidate_runs_schema_normalize_refine_content_and_global_gate():
    existing = [row("answer", box(.65, .05, .2, .2), label="15")]
    evidence = GateEvidence()
    fresh = coarse_candidate("answer", box(.05, .5, .35, .2), label="12")
    result, evidence = apply(existing, proposed(uncovered=[fresh]), evidence=evidence)

    assert result["discoveries_added"] == 1
    assert result["rejected_count"] == 0
    assert evidence.calls == 1
    discovered = existing[-1]
    assert discovered["printed_question_label"] == "12"
    assert discovered["semantic_check"] == "needs_review"
    assert discovered["association_status"] == "ambiguous"
    assert "LOCAL_CONTENT_TEST_EVIDENCE" in discovered["reason_codes"]
    assert "PAGE_COVERAGE_DISCOVERED_REGION" in discovered["reason_codes"]
    assert discovered["_coarse_layout"] is True
    assert discovered.get("status") != "confirmed"


@pytest.mark.parametrize("role", ["answer", "identity"])
def test_new_visible_coarse_region_cannot_be_auto_adopted_without_a_successful_crop_replacement(role):
    from grading_agent.template_analysis import _finalize_candidate_reasons

    fresh = coarse_candidate(role, box(.05, .5, .3, .15), label="12" if role == "answer" else None,
                             identity="姓名" if role == "identity" else None)
    if role == "identity":
        fresh["visual_evidence"].update(static_label="姓名", identity_pattern="name_line")
    existing = []
    result, _ = apply(existing, proposed(uncovered=[fresh]))
    # 裁图为空、失败或预算不足时不会生成替换批次；最终整理也不能重新升级这个粗候选。
    _finalize_candidate_reasons(existing, PAYLOAD["request_id"])

    assert result["discoveries_added"] == 1
    assert existing[0]["semantic_check"] == "needs_review"
    assert "PAGE_COVERAGE_DISCOVERED_REGION" in existing[0]["reason_codes"]
    assert "LOCAL_MODEL_REPAIR_APPLIED" not in existing[0]["reason_codes"]
    assert existing[0].get("_coarse_layout") is None


def test_expanded_objective_keeps_coarse_stage_marker_through_public_schema_validation():
    payload = {**PAYLOAD, "question_labels": ["1", "2"], "objective_question_labels": ["1", "2"]}
    supplied = {"role": "objective_group", "bbox": box(.05, .1, .4, .3), "labels": ["1", "2"],
                "identity_label": None, "static_label": "选择题", "option_pattern": "bubble_options",
                "identity_pattern": "none", "barcode_pattern": "none", "boundary_basis": "printed_frame",
                "anchor_bbox": None}
    raw = expand_coarse_output({"candidates": [supplied]}, payload)["candidates"][0]
    before = copy.deepcopy(raw)
    existing = []
    result = apply_page_coverage(existing, proposed(uncovered=[raw]), payload, [], PAGE, GateEvidence())

    assert result["discoveries_added"] == 1 and result["rejected_count"] == 0
    assert existing[0]["role"] == "objective_group"
    assert existing[0]["_coarse_layout"] is True
    assert "OBJECTIVE_ITEMS_DEFERRED_TO_LOCAL" in existing[0]["reason_codes"]
    assert "PAGE_COVERAGE_DISCOVERED_REGION" in existing[0]["reason_codes"]
    assert existing[0]["semantic_check"] == "needs_review"
    assert raw == before


@pytest.mark.parametrize("extra", [{"_provider_debug": True}, {"_coarse_layout": False}, {"unexpected": "value"}])
def test_only_trusted_coarse_stage_marker_is_exempt_from_public_schema(extra):
    fresh = coarse_candidate("answer", box(.05, .5, .35, .2), label="12")
    fresh.update(extra)
    existing = []
    evidence = GateEvidence()
    result, evidence = apply(existing, proposed(uncovered=[fresh]), evidence=evidence)

    assert result["discoveries_added"] == 0 and result["rejected_count"] == 1
    assert evidence.calls == 0 and existing == []


def test_invalid_uncovered_schema_is_rejected_before_content_gate():
    existing = [row("answer", box(.65, .05, .2, .2), label="15")]
    evidence = GateEvidence()
    result, evidence = apply(existing, proposed(uncovered=[{"role": "answer"}]), evidence=evidence)

    assert result["discoveries_added"] == 0
    assert result["rejected_count"] == 1
    assert evidence.calls == 0
    assert len(existing) == 1


@pytest.mark.parametrize("kind", ["same_candidate", "same_question", "overlap"])
def test_uncovered_duplicate_or_overlap_is_not_appended_and_good_region_stays_supported(kind):
    old = row("answer", box(.05, .05, .2, .2), label="12", candidate_id="stable-12")
    good = row("answer", box(.65, .05, .2, .2), label="15", candidate_id="stable-15")
    before = copy.deepcopy([old, good])
    if kind == "same_candidate":
        candidate = coarse_candidate("answer", box(.3, .5, .2, .2), label="12")
        candidate["candidate_id"] = "stable-12"
    elif kind == "same_question":
        candidate = coarse_candidate("answer", box(.3, .5, .2, .2), label="15")
    else:
        candidate = coarse_candidate("answer", box(.65, .05, .2, .2), label="12")
    result, _ = apply([old, good], proposed(uncovered=[candidate]))

    assert result["discoveries_added"] == 0
    assert result["rejected_count"] == 1
    assert old == before[0]
    assert good == before[1]


@pytest.mark.parametrize("status", ["supported", "needs_review"])
@pytest.mark.parametrize("observation_kind", ["absent", "uncertain", "same_question"])
def test_nonoverlapping_same_question_needs_current_positive_counterevidence(status, observation_kind):
    old = row("answer", box(.05, .05, .2, .2), label="12", status=status)
    old["reason_codes"] = ["PAGE_COVERAGE_QUESTION_CONFLICT"] if status == "needs_review" else []
    before = copy.deepcopy(old)
    observations = {"absent": [], "uncertain": [observation(0, role="uncertain", labels=["15"])],
                    "same_question": [observation(0, labels=["12"])]}[observation_kind]
    existing = [old]
    result, _ = apply(existing, proposed(observations=observations, uncovered=[
        coarse_candidate("answer", box(.05, .5, .2, .2), label="12")]))

    assert result == {"observed_conflict_count": 0, "discoveries_added": 0, "rejected_count": 1}
    assert existing == [before]


@pytest.mark.parametrize("fresh_box", [box(.05, .5, .2, .2), box(.06, .06, .2, .2)])
def test_same_question_currently_denied_old_region_is_only_a_repair_replacement(fresh_box):
    from grading_agent.template_analysis_repair import _repair_tasks

    old = row("answer", box(.05, .05, .2, .2), label="12", candidate_id="wrong-12")
    good = row("answer", box(.65, .05, .2, .2), label="15", candidate_id="good-15")
    before_good = copy.deepcopy(good)
    existing = [old, good]
    result, _ = apply(existing, proposed(observations=[observation(0, labels=["15"])], uncovered=[
        coarse_candidate("answer", fresh_box, label="12")]))

    assert result == {"observed_conflict_count": 1, "discoveries_added": 1, "rejected_count": 0}
    fresh = existing[-1]
    assert fresh["_extra_replace_candidate_ids"] == ["wrong-12"]
    assert fresh["semantic_check"] == "needs_review"
    assert "PAGE_COVERAGE_DISCOVERED_REGION" in fresh["reason_codes"]
    assert "MULTIPLE_REGIONS_FOR_QUESTION" not in fresh["reason_codes"]
    assert existing[0] is old and "PAGE_COVERAGE_QUESTION_CONFLICT" in old["reason_codes"]
    assert good == before_good
    tasks = _repair_tasks(PAYLOAD, existing)
    assert len(tasks) == 1 and tasks[0]["task"] == "coverage_region"
    assert set(tasks[0]["replace_candidate_ids"]) == {"wrong-12", fresh["candidate_id"]}


@pytest.mark.parametrize("repair_result", ["supported", "empty", "unresolved"])
def test_same_question_relocation_removes_both_candidates_only_after_successful_repair(repair_result):
    from grading_agent.template_analysis import _apply_repair_batches
    from grading_agent.template_analysis_repair import _repair_tasks

    # 原先只替换旧框会留下查漏追加框，最终仍有两个同题；成功须同时撤下两者，失败须保留。
    old = row("answer", box(.05, .05, .2, .2), label="12", candidate_id="wrong-12")
    existing = [old]
    fresh_box = box(.05, .5, .2, .2)
    apply(existing, proposed(observations=[observation(0, labels=["15"])], uncovered=[
        coarse_candidate("answer", fresh_box, label="12")]))
    task = _repair_tasks(PAYLOAD, existing)[0]
    before = copy.deepcopy(existing)
    repaired = coarse_candidate("answer", fresh_box, label="12")
    repaired.pop("_coarse_layout")
    batch = {**task, "candidates": [] if repair_result == "empty" else [repaired]}
    applications = _apply_repair_batches(existing, [batch], PAYLOAD, [], PAGE,
                                        GateEvidence(status="needs_review" if repair_result == "unresolved" else "supported"))

    assert applications[0]["applied"] == (repair_result == "supported")
    if repair_result == "supported":
        assert len(existing) == 1 and existing[0]["printed_question_label"] == "12"
        assert existing[0]["semantic_check"] == "supported"
        assert existing[0]["candidate_id"] != "wrong-12"
        assert "LOCAL_MODEL_REPAIR_APPLIED" in existing[0]["reason_codes"]
    else:
        assert existing == before


def test_multiple_old_same_question_regions_cannot_be_arbitrarily_relocated():
    existing = [row("answer", box(.05, .05, .2, .2), label="12", candidate_id="first-12"),
                row("answer", box(.35, .05, .2, .2), label="12", status="needs_review", candidate_id="second-12")]
    result, _ = apply(existing, proposed(observations=[observation(0, labels=["15"])], uncovered=[
        coarse_candidate("answer", box(.05, .5, .2, .2), label="12")]))

    assert result["discoveries_added"] == 0 and result["rejected_count"] == 1
    assert len(existing) == 2
    assert all("_extra_replace_candidate_ids" not in candidate for candidate in existing)


def test_different_question_discovery_does_not_replace_a_denied_old_question():
    old = row("answer", box(.05, .05, .2, .2), label="12", candidate_id="wrong-12")
    existing = [old]
    result, _ = apply(existing, proposed(observations=[observation(0, labels=["15"])], uncovered=[
        coarse_candidate("answer", box(.05, .5, .2, .2), label="15")]))

    assert result["discoveries_added"] == 1
    assert "_extra_replace_candidate_ids" not in existing[-1]
    assert existing[0] is old


def test_two_same_question_discoveries_cannot_both_plan_replacing_one_old_region():
    old = row("answer", box(.05, .05, .2, .2), label="12", candidate_id="wrong-12")
    existing = [old]
    result, _ = apply(existing, proposed(observations=[observation(0, labels=["15"])], uncovered=[
        coarse_candidate("answer", box(.05, .5, .2, .2), label="12"),
        coarse_candidate("answer", box(.4, .5, .2, .2), label="12")]))

    assert result["discoveries_added"] == 1 and result["rejected_count"] == 1
    assert len(existing) == 2
    assert existing[-1]["_extra_replace_candidate_ids"] == ["wrong-12"]


def test_same_question_relocation_cannot_claim_an_already_planned_replacement():
    old = row("answer", box(.05, .05, .2, .2), label="12", candidate_id="wrong-12")
    prior = row("identity", box(.65, .05, .2, .2), identity="身份信息", status="needs_review")
    prior["_extra_replace_candidate_ids"] = ["wrong-12"]
    existing = [old, prior]
    result, _ = apply(existing, proposed(observations=[observation(0, labels=["15"])], uncovered=[
        coarse_candidate("answer", box(.05, .5, .2, .2), label="12")]))

    assert result["discoveries_added"] == 0 and result["rejected_count"] == 1
    assert len(existing) == 2 and existing[1] is prior


def test_relocation_cannot_reuse_an_existing_candidate_identifier():
    old = row("answer", box(.05, .05, .2, .2), label="12", candidate_id="wrong-12")
    fresh = coarse_candidate("answer", box(.05, .5, .2, .2), label="12")
    fresh["candidate_id"] = old["candidate_id"]
    existing = [old]
    result, _ = apply(existing, proposed(observations=[observation(0, labels=["15"])], uncovered=[fresh]))

    assert result["discoveries_added"] == 0 and result["rejected_count"] == 1
    assert existing == [old]


def test_unsupported_uncovered_overlap_is_rejected_without_downgrading_supported_rows():
    old = row("answer", box(.05, .05, .3, .3), label="12")
    candidate = coarse_candidate("answer", box(.15, .15, .3, .3), label="15")
    result, _ = apply([old], proposed(uncovered=[candidate]), evidence=GateEvidence(status="needs_review"))

    assert result["discoveries_added"] == 0
    assert result["rejected_count"] == 1
    assert old["semantic_check"] == "supported"


def test_uncovered_processing_stops_at_max_256_and_does_not_infer_missing_questions():
    existing = []
    # 使用身份候选而非重复题号，避免“同题重复”门禁提前结束；这里专门覆盖数量上限。
    uncovered = [coarse_candidate("identity", box((index % 16) * .06, (index // 16) * .06, .03, .03),
                                  identity="身份信息") for index in range(257)]
    result, _ = apply(existing, proposed(uncovered=uncovered))

    assert result["discoveries_added"] == 256
    assert result["rejected_count"] == 1
    assert len(existing) == 256


def test_empty_uncovered_list_does_not_infer_regions_from_exam_question_list():
    existing = []
    result, _ = apply(existing, proposed(uncovered=[]))

    assert result["discoveries_added"] == 0
    assert result["rejected_count"] == 0
    assert existing == []


def barcode_candidate(bbox):
    candidate = coarse_candidate("identity", bbox, identity="条码")
    candidate["visual_evidence"].update(static_label="条形码粘贴区", barcode_pattern="linear_bars",
                                        anchor_bbox=box(bbox["x"] + .01, bbox["y"] + .01, .04, .03))
    return candidate


@pytest.mark.parametrize("contradiction", [False, True])
def test_new_barcode_only_replaces_unique_old_barcode_explicitly_denied_by_coverage(contradiction):
    old = row("identity", box(.05, .1, .18, .12), identity="条码", candidate_id="old-code")
    fresh = barcode_candidate(box(.45, .1, .18, .12))
    observations = [observation(0, role="other", labels=[])] if contradiction else []
    existing = [old]
    result, _ = apply(existing, proposed(observations=observations, uncovered=[fresh]))

    assert result["discoveries_added"] == 1
    if contradiction:
        assert existing[-1]["_extra_replace_candidate_ids"] == ["old-code"]
        assert old["semantic_check"] == "needs_review"
    else:
        assert "_extra_replace_candidate_ids" not in existing[-1]
        assert old["semantic_check"] == "supported"


def test_two_existing_physical_barcodes_cannot_be_treated_as_one_relocation_target():
    existing = [row("identity", box(.05, .1, .18, .12), identity="条码", candidate_id="first-code"),
                row("identity", box(.3, .1, .18, .12), identity="条码", candidate_id="second-code")]
    fresh = barcode_candidate(box(.6, .1, .18, .12))
    result, _ = apply(existing, proposed(observations=[observation(0, role="other", labels=[])], uncovered=[fresh]))

    assert result["discoveries_added"] == 1
    assert "_extra_replace_candidate_ids" not in existing[-1]
    assert len(existing) == 3

