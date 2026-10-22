"""内部粗阶段契约只减少模型工作量，不声称固定响应能证明视觉识别效果。"""

import copy
import json

import pytest
from jsonschema import Draft202012Validator, ValidationError

from grading_agent.template_analysis import _finalize_candidate_reasons, _normalize
from grading_agent.template_analysis_coarse import (
    coarse_message_payload,
    coarse_model_output_schema,
    expand_coarse_output,
)
from grading_agent.template_analysis_schema import model_output_schema


def payload():
    return {"request_id": "coarse-fixture-0001", "page": {"page_no": 2, "sha256": "b" * 64},
            "question_labels": ["1", "2", "12", "13", "15"], "objective_question_labels": ["1", "2"],
            "fill_blank_question_labels": ["12", "13"], "writing_state": "blank", "candidates": []}


def row(role="answer", labels=None, **updates):
    supplied = {"role": role, "bbox": {"x": .1, "y": .2, "width": .25, "height": .15},
                "labels": ["12"] if labels is None else labels, "identity_label": None,
                "static_label": "填空题", "option_pattern": "none", "identity_pattern": "none",
                "barcode_pattern": "none", "boundary_basis": "printed_answer_line", "anchor_bbox": None}
    return {**supplied, **updates}


def expand(rows, supplied=None):
    output = {"candidates": rows}
    Draft202012Validator(coarse_model_output_schema()).validate(output)
    return expand_coarse_output(output, supplied or payload())


def test_coarse_schema_has_no_fine_option_subtree_or_redundant_server_fields():
    schema = coarse_model_output_schema()
    props = schema["properties"]["candidates"]["items"]["properties"]
    assert len(props) == 10
    assert not {"candidate_id", "page_no", "evidence_refs", "reason_codes", "association_status",
                "objective_items", "visual_evidence"} & props.keys()
    assert '"options"' not in json.dumps(schema)
    detailed = model_output_schema([], 2, include_observations=True)
    assert len(json.dumps(schema)) < len(json.dumps(detailed)) * .65


def test_expansion_preserves_observations_and_satisfies_existing_raw_candidate_contract_without_mutation():
    original = {"candidates": [row()]}
    before = copy.deepcopy(original)
    expanded = expand_coarse_output(original, payload())
    # 扩展器的阶段标记来自服务端，模型原始契约中没有该字段。
    raw_contract = copy.deepcopy(expanded)
    assert raw_contract["candidates"][0].pop("_coarse_layout") is True
    Draft202012Validator(model_output_schema([], 2, include_observations=True)).validate(raw_contract)
    candidate = expanded["candidates"][0]
    assert candidate["candidate_id"] is None and candidate["page_no"] == 2
    assert candidate["objective_items"] == []
    assert candidate["evidence_refs"] == ["source-page:" + "b" * 64 + ":2"]
    assert candidate["association_status"] == "associated"
    assert candidate["printed_question_label"] == "12"
    assert candidate["observed_question_labels"] == ["12"]
    assert candidate["visual_evidence"]["boundary_basis"] == "printed_answer_line"
    assert original == before


@pytest.mark.parametrize("role,labels", [("answer", []), ("answer", ["12", "13"]),
    ("answer", ["99"]), ("objective_group", ["1", "2"]), ("uncertain", ["12"])])
def test_only_single_visible_formal_answer_label_can_propose_association(role, labels):
    candidate = expand([row(role, labels)])["candidates"][0]
    assert candidate["printed_question_label"] is None
    assert candidate["association_status"] == "unassociated"
    assert candidate["observed_question_labels"] == labels


def test_coarse_choice_reaches_local_refinement_as_missing_fine_mapping_instead_of_claiming_usable_grid():
    output = expand([row("objective_group", ["1", "2"], static_label="选择题", option_pattern="bubble_options")])
    normalized = _normalize(output["candidates"], {}, payload())
    assert normalized[0]["role"] == "objective_group"
    assert "OBJECTIVE_ITEMS_DEFERRED_TO_LOCAL" in normalized[0]["reason_codes"]
    _finalize_candidate_reasons(normalized, payload()["request_id"])
    assert normalized[0]["semantic_check"] == "needs_review"
    assert "MODEL_OBS_OBJECTIVE_ITEMS_MISSING" in normalized[0]["reason_codes"]
    assert "_coarse_layout" not in normalized[0]


def test_false_identity_features_and_real_nonobjective_label_survive_expansion_for_semantic_rejection():
    output = expand([row("identity", ["15"], identity_label="姓名", static_label="Name",
                         identity_pattern="name_line", option_pattern="bubble_options")])
    normalized = _normalize(output["candidates"], {}, payload())
    assert normalized[0]["role"] == "uncertain"
    assert "MODEL_OBS_IDENTITY_OPTION_CONFLICT" in normalized[0]["reason_codes"]
    assert "MODEL_OBS_IDENTITY_QUESTION_CONFLICT" in normalized[0]["reason_codes"]


def test_no_barcode_or_no_regions_is_legal_in_coarse_stage():
    assert expand([]) == {"candidates": []}
    assert expand([row()])["candidates"][0]["visual_evidence"]["barcode_pattern"] == "none"


def test_coarse_schema_rejects_private_free_text_and_fine_grid_output():
    for supplied in [row(student_name="PRIVATE_STUDENT"), row(labels=["PRIVATE_STUDENT"]),
                     row(static_label="PRIVATE_STUDENT"), row(objective_items=[])]:
        with pytest.raises(ValidationError):
            expand([supplied])


def test_lightweight_manifest_withholds_exam_lists_without_copied_candidate_metadata():
    supplied = payload()
    supplied.update(diagnostic_context={"template_id": "PRIVATE_INTERNAL_ID"})
    supplied["candidates"] = [{"candidate_id": "private-frame", "bbox": row()["bbox"],
                               "evidence_refs": ["private-long-evidence"]}] * 70
    before = copy.deepcopy(supplied)
    manifest = coarse_message_payload(supplied)
    # 全场清单由服务端保存并在返回后核对；放进视觉请求会诱导模型按清单补框。
    assert "question_labels" not in manifest
    assert "objective_question_labels" not in manifest
    assert "fill_blank_question_labels" not in manifest
    assert manifest["question_labels_scope"] == "server_side_only_not_provided"
    assert manifest["page_no"] == 2
    assert manifest["geometry_hints"] == [row()["bbox"]]
    assert "PRIVATE_INTERNAL_ID" not in json.dumps(manifest)
    assert "private-frame" not in json.dumps(manifest)
    assert "evidence_refs" not in json.dumps(manifest)
    assert supplied == before


def test_lightweight_manifest_bounds_hint_count_without_leaking_exam_question_lists():
    supplied = payload()
    supplied["candidates"] = [{"bbox": {"x": index / 1000, "y": .1, "width": .1, "height": .1}}
                               for index in range(80)]
    manifest = coarse_message_payload(supplied)
    assert len(manifest["geometry_hints"]) == 64
    assert "question_labels" not in manifest
    assert "objective_question_labels" not in manifest
    assert "fill_blank_question_labels" not in manifest


def test_optional_question_type_lists_are_withheld_even_when_known_empty():
    supplied = payload()
    supplied.pop("objective_question_labels")
    supplied["fill_blank_question_labels"] = []
    manifest = coarse_message_payload(supplied)
    assert "objective_question_labels" not in manifest
    assert "fill_blank_question_labels" not in manifest
    assert manifest["question_labels_scope"] == "server_side_only_not_provided"
