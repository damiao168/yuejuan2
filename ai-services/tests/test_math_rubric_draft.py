from contextlib import nullcontext

import pytest
from grading_agent.errors import AgentError
from grading_agent.math_rubric_draft import MathRubricDraftGenerator


class FakeModel:
    def __init__(self, points):
        self.points = points
        self.calls = []

    def session(self, request_id):
        return nullcontext()

    def request_structured(self, request_id, messages, schema, name):
        self.calls.append((request_id, messages, schema, name))
        return {"points": self.points}


def candidates(score=4):
    ref = {"source_id": "source-1", "block_id": "solution-1"}
    question = {"candidate_id": "q1", "question_no_normalized": "15", "question_type": "calculation", "stem": "解方程", "score": score}
    answer = {"candidate_id": "a1", "standard_answer": "x=2"}
    solution = {"candidate_id": "s1", "question_no_normalized": "15", "steps": [
        {"step_no": 1, "content": "移项得2x=4"},
        {"step_no": 2, "content": "解得x=2"},
    ], "source_refs": [ref]}
    return question, answer, solution


def test_ai_draft_is_separate_from_extracted_rubric_and_requires_review():
    model = FakeModel([
        {"description": "正确移项", "suggested_score": 2, "evidence_step_ids": ["step-1"], "review_note": ""},
        {"description": "求得正确结果", "suggested_score": 2, "evidence_step_ids": ["step-2"], "review_note": "核对其他解法"},
    ])
    result = MathRubricDraftGenerator(model).generate("job-1", *candidates())

    assert "rubric_candidates" not in result
    draft = result["suggested_rubric_candidates"][0]
    assert draft["origin"] == "ai_suggestion_from_solution"
    assert draft["status"] == "review_required"
    assert draft["max_score"] == 4
    assert [point["suggested_score"] for point in draft["points"]] == [2, 2]
    assert draft["points"][0]["source_refs"] == [{"source_id": "source-1", "block_id": "solution-1"}]
    assert model.calls[0][3] == "math_rubric_draft"


def test_no_reliable_maximum_forces_all_suggested_scores_to_null():
    model = FakeModel([{"description": "正确移项", "suggested_score": 2, "evidence_step_ids": ["step-1"], "review_note": ""}])
    result = MathRubricDraftGenerator(model).generate("job-2", *candidates(None))

    draft = result["suggested_rubric_candidates"][0]
    assert draft["max_score"] is None
    assert draft["points"][0]["suggested_score"] is None
    assert "满分" in draft["issues"][0]


def test_model_cannot_attach_invented_solution_step():
    model = FakeModel([{"description": "正确移项", "suggested_score": 2, "evidence_step_ids": ["step-99"], "review_note": ""}])

    with pytest.raises(AgentError) as raised:
        MathRubricDraftGenerator(model).generate("job-3", *candidates())

    assert raised.value.code == "model_output_invalid"


def test_draft_with_nonmatching_total_is_flagged():
    model = FakeModel([{"description": "正确移项", "suggested_score": 1, "evidence_step_ids": ["step-1"], "review_note": ""}])
    result = MathRubricDraftGenerator(model).generate("job-4", *candidates())

    assert "不一致" in result["suggested_rubric_candidates"][0]["issues"][0]


def test_objective_question_does_not_generate_math_steps():
    question, answer, solution = candidates()
    question["question_type"] = "single_choice"
    model = FakeModel([])

    with pytest.raises(AgentError) as raised:
        MathRubricDraftGenerator(model).generate("job-5", question, answer, solution)

    assert raised.value.code == "invalid_request"
    assert model.calls == []
