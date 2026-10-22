from contextlib import nullcontext

import pytest

from grading_agent.errors import AgentError
from grading_agent.math_rubric_draft import MathRubricDraftGenerator


class Model:
    def __init__(self):
        self.calls = 0

    def session(self, request_id):
        return nullcontext()

    def request_structured(self, request_id, messages, schema, name):
        self.calls += 1
        return {"points": [{"description": "正确移项", "suggested_score": 4,
                            "evidence_step_ids": ["step-1"], "review_note": "待复核"}]}


def formal_input():
    question = {"candidate_id": "question-1", "question_no_normalized": "15",
                "question_type": "calculation", "score": 4}
    solution = {"candidate_id": "question-solution-question-1", "question_no_normalized": "15",
                "steps": [{"content": "移项"}], "source_refs": []}
    reference = {"question_id": "question-1", "exam_id": "exam-1", "source_type": "question_solution",
                 "source_id": "solution-1", "source_version": "v1", "snapshot_sha256": "a" * 64}
    return question, solution, reference


def test_import_rubric_without_sources_cannot_use_formal_question_exception():
    question, solution, _ = formal_input()
    model = Model()
    with pytest.raises(AgentError, match="solution provenance is required"):
        MathRubricDraftGenerator(model).generate("import-request", question, None, solution)
    assert model.calls == 0


@pytest.mark.parametrize("source_type", ["question_solution", "answer_key"])
def test_formal_question_reference_preserves_empty_file_sources_and_explicit_origin(source_type):
    question, solution, reference = formal_input()
    reference["source_type"] = source_type
    model = Model()
    result = MathRubricDraftGenerator(model).generate(
        "formal-request", question, None, solution, question_reference=reference
    )
    suggestion = result["suggested_rubric_candidates"][0]
    assert suggestion["points"][0]["source_refs"] == []
    assert suggestion["provenance"]["question_reference"] == reference
    assert suggestion["status"] == "review_required"
    assert model.calls == 1


@pytest.mark.parametrize("field,value", [
    ("question_id", "other-question"), ("exam_id", ""), ("source_type", "import_file"),
    ("source_id", ""), ("source_version", ""), ("snapshot_sha256", "not-a-hash"),
    ("snapshot_sha256", "A" * 64), ("extra", "not-accepted"),
])
def test_formal_reference_tampering_is_rejected_before_the_model(field, value):
    question, solution, reference = formal_input()
    reference[field] = value
    model = Model()
    with pytest.raises(AgentError, match="formal question reference is invalid"):
        MathRubricDraftGenerator(model).generate(
            "formal-request", question, None, solution, question_reference=reference
        )
    assert model.calls == 0


def test_formal_reference_also_requires_the_bound_solution_candidate():
    question, solution, reference = formal_input()
    solution["candidate_id"] = "other-solution"
    model = Model()
    with pytest.raises(AgentError, match="formal question reference is invalid"):
        MathRubricDraftGenerator(model).generate(
            "formal-request", question, None, solution, question_reference=reference
        )
    assert model.calls == 0


def test_partitioned_steps_keep_whole_solution_context_without_widening_evidence_ids():
    import json

    class ContextModel(Model):
        def request_structured(self, request_id, messages, schema, name):
            supplied = json.loads(messages[-1]["content"])
            assert supplied["solution_context"] == "完整公式上下文"
            assert supplied["solution_steps"] == [{"id": "step-1", "content": "公式前半"},
                                                   {"id": "step-2", "content": "公式后半"}]
            allowed = schema["properties"]["points"]["items"]["properties"]["evidence_step_ids"]["items"]["enum"]
            assert allowed == ["step-1", "step-2"]
            return super().request_structured(request_id, messages, schema, name)

    question, solution, reference = formal_input()
    solution["raw_text"] = "完整公式上下文"
    solution["steps"] = [{"content": "公式前半"}, {"content": "公式后半"}]
    model = ContextModel()
    result = MathRubricDraftGenerator(model).generate(
        "formal-partition", question, None, solution, question_reference=reference
    )
    assert result["suggested_rubric_candidates"][0]["points"][0]["evidence_step_ids"] == ["step-1"]
    assert model.calls == 1


@pytest.mark.parametrize("scenario,expected_status", [("valid", 200), ("tampered", 400), ("unauthenticated", 401)])
def test_http_route_carries_formal_reference_and_keeps_service_auth(scenario, expected_status):
    import json
    import http.client
    from unittest.mock import patch
    from urllib import error, request

    import test_http

    # 复用真实 HTTP 服务夹具；只替换模型，协议解码、服务认证与生成器校验全部运行。
    fixture = test_http.HTTPTests("test_math_rubric_draft_is_authenticated_and_review_only")
    fixture.setUp()
    try:
        question, solution, reference = formal_input()
        if scenario == "tampered":
            reference["question_id"] = "another-question"
        body = {"request_id": "formal-http-001", "subject": "mathematics", "question_candidate": question,
                "answer_candidate": None, "solution_candidate": solution, "question_reference": reference}
        headers = {"Content-Type": "application/json"}
        if scenario != "unauthenticated":
            headers["Authorization"] = "Bearer " + fixture.settings.service_token
        req = request.Request(fixture.base_url + "/paper/rubric-draft", data=json.dumps(body).encode(),
                              headers=headers, method="POST")
        model = Model()
        with patch("grading_agent.server.paper_model", return_value=model):
            if scenario == "unauthenticated":
                # 先发无凭据的请求头；鉴权应先于读取 JSON，避免 Windows 关闭未读请求体时重置连接。
                connection = http.client.HTTPConnection("127.0.0.1", fixture.server.server_port, timeout=2)
                try:
                    connection.request("POST", "/paper/rubric-draft", headers={"Content-Type": "application/json"})
                    response = connection.getresponse()
                    assert response.status == expected_status
                    result = json.loads(response.read())
                finally:
                    connection.close()
            else:
                try:
                    response = request.urlopen(req, timeout=2)
                except error.HTTPError as exc:
                    response = exc
                with response:
                    assert response.status == expected_status
                    result = json.loads(response.read())
        if expected_status == 200:
            assert result["suggested_rubric_candidates"][0]["provenance"]["question_reference"] == reference
            assert model.calls == 1
        else:
            assert model.calls == 0
    finally:
        fixture.tearDown()
