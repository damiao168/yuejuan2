"""模型transport之外调用真实来源校验，保护大小题号与输入边界。"""
from copy import deepcopy

import pytest
from grading_agent.errors import AgentError
from grading_agent.paper_validation import validate_paper_output


def grounded_output(number, field="question_no_normalized", collection="question_candidates"):
    document = {"source_id": "source", "file_asset_id": "asset", "document_index": 0,
                "content": "第1题（1分） first 第10001题（1分） second", "blocks": []}
    ref = {"source_id": "source", "file_asset_id": "asset", "document_index": 0,
           "page_no": None, "block_id": None, "bbox": None, "ocr_confidence": None,
           "text_start": 0, "text_end": len(document["content"])}
    output = {"documents": [{"source_id": "source", "detected_role": "mixed", "role_confidence": 1.0}],
              "question_candidates": [], "answer_candidates": [], "solution_candidates": [], "rubric_candidates": [], "issues": []}
    candidate = {"candidate_id": "candidate", "question_no_normalized": "1", "confidence": 1.0, "source_refs": [ref]}
    candidate[field] = number
    output[collection].append(candidate)
    return output, document


@pytest.mark.parametrize("number", ["1", "03", "第03题", "二十三", "18（1）", "01.02", "A-1", "10001", "9223372036854775807", "a" * 64, "0" * 60 + "1"])
def test_normal_and_sparse_question_labels_are_preserved(number):
    output, document = grounded_output(number)
    assert validate_paper_output(output, "bounded", "mathematics", [document])["question_candidates"][0]["question_no_normalized"] == number


@pytest.mark.parametrize("number", ["9" * 32, "9223372036854775808", "1(" + "9" * 32 + ")", "9" * 32 + ".1", "a" * 65, "9 " * 20, 10001, True])
def test_extreme_number_is_rejected_without_echoing_input(number):
    output, document = grounded_output(number)
    with pytest.raises(AgentError) as error:
        validate_paper_output(output, "bounded", "mathematics", [document])
    assert error.value.code == "model_output_invalid"
    assert len(str(error.value)) < 160
    assert "9" * 32 not in str(error.value)


@pytest.mark.parametrize("field,collection", [
    ("question_no_raw", "question_candidates"), ("parent_question_no", "question_candidates"),
    ("subquestion_no", "question_candidates"), ("question_no_hint", "answer_candidates"),
    ("subquestion_no_hint", "answer_candidates"), ("question_no_normalized", "solution_candidates"),
    ("subquestion_no_hint", "solution_candidates"), ("question_no_hint", "rubric_candidates"),
])
def test_all_candidate_number_fields_share_the_bound(field, collection):
    output, document = grounded_output("9" * 32, field, collection)
    with pytest.raises(AgentError):
        validate_paper_output(output, "bounded", "mathematics", [document])


def test_small_and_distant_pair_stays_two_candidates():
    output, document = grounded_output("1")
    second = deepcopy(output["question_candidates"][0])
    second.update(candidate_id="second", question_no_normalized="10001")
    output["question_candidates"].append(second)
    assert len(validate_paper_output(output, "bounded", "mathematics", [document])["question_candidates"]) == 2
