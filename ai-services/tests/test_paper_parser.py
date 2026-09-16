import pytest
from grading_agent.errors import AgentError
from grading_agent.paper_parser import PaperParser


def ref(source_id="source-1"):
    return {"source_id": source_id, "file_asset_id": "file-1", "document_index": 0, "page_no": None, "block_id": None, "bbox": None, "text_start": 0, "text_end": 1, "ocr_confidence": None}


class FakeStructuredModel:
    class _Session:
        def __enter__(self): return None
        def __exit__(self, *_args): return False

    def __init__(self, output): self.output = output
    def session(self, _request_id): return self._Session()
    def request_structured(self, *_args): return self.output


def output(role, questions=None, answers=None, solutions=None, rubrics=None):
    return {"documents": [{"source_id": "source-1", "detected_role": role, "role_confidence": .98}], "question_candidates": questions or [], "answer_candidates": answers or [], "solution_candidates": solutions or [], "rubric_candidates": rubrics or [], "issues": []}


def payload(content="1.A"):
    return {"request_id": "job-1", "subject": "数学", "documents": [{"source_id": "source-1", "file_asset_id": "file-1", "document_index": 0, "role_hint": "auto", "content": content, "blocks": []}]}


def test_answer_only_is_valid_and_does_not_hallucinate_question():
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [ref()], "issues": []}
    result = PaperParser(FakeStructuredModel(output("answer", answers=[answer]))).parse(payload())
    assert result["question_candidates"] == []
    assert result["answer_candidates"][0]["standard_answer"] == "A"


def test_full_model_progress_reports_real_route_and_completed_request():
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [ref()], "issues": []}
    events = []

    PaperParser(FakeStructuredModel(output("answer", answers=[answer]))).parse(
        payload(), progress=events.append
    )

    assert events[0] == {
        "unit": "parse_chunk",
        "phase": "routing",
        "completed": 0,
        "total": 0,
        "route": "pending",
        "message": "正在根据版面锚点选择解析路径",
    }
    assert [(event["route"], event["completed"], event["total"]) for event in events[1:]] == [
        ("full_model", 0, 1),
        ("full_model", 1, 1),
    ]


def test_question_only_allows_unknown_score_answer_and_rubric():
    question = {"candidate_id": "q1", "question_no_raw": "第1题", "question_no_normalized": "1", "parent_question_no": None, "subquestion_no": None, "section_hint": None, "stem": "计算", "options": [], "question_type": "calculation", "score": None, "knowledge_point_hints": [], "confidence": .9, "source_refs": [ref()], "issues": []}
    result = PaperParser(FakeStructuredModel(output("question", questions=[question]))).parse(payload("第1题 计算"))
    assert result["question_candidates"][0]["score"] is None
    assert result["answer_candidates"] == []
    assert "rubric" not in result["question_candidates"][0]


def test_solution_only_is_valid():
    solution = {"candidate_id": "s1", "question_no_hint": "18(1)", "question_no_normalized": "18(1)", "subquestion_no_hint": "1", "raw_text": "因为…所以…", "steps": [{"step_no": 1, "content": "因为…"}], "confidence": .88, "source_refs": [ref()], "issues": []}
    result = PaperParser(FakeStructuredModel(output("solution", solutions=[solution]))).parse(payload("第18题解析"))
    assert result["solution_candidates"][0]["question_no_normalized"] == "18(1)"


def test_obvious_meeting_screenshot_is_rejected_without_running_full_model():
    class ModelMustNotRun(FakeStructuredModel):
        def request_structured(self, *_args):
            raise AssertionError("full extraction model must not run for an obvious meeting screenshot")

    request = payload("北师保研分享会\n会议号：120732118\n发起人：任辰红\n最近入会\n参会时长\n回放")
    result = PaperParser(ModelMustNotRun(output("unknown"))).parse(request)

    assert result["documents"][0]["detected_role"] == "unknown"
    assert result["question_candidates"] == []
    assert result["answer_candidates"] == []


def test_unrelated_guard_progress_explicitly_reports_non_model_route():
    request = payload("北师保研分享会\n会议号：120732118\n发起人：任辰红\n最近入会\n参会时长\n回放")
    events = []

    PaperParser(FakeStructuredModel(output("unknown"))).parse(request, progress=events.append)

    assert events[-1]["route"] == "unrelated_guard"
    assert events[-1]["completed"] == events[-1]["total"] == 1


def test_exam_markers_take_priority_over_unrelated_ui_words():
    question = {"candidate_id": "q1", "question_no_raw": "第1题", "question_no_normalized": "1", "parent_question_no": None, "subquestion_no": None, "section_hint": None, "stem": "计算", "options": [], "question_type": "calculation", "score": None, "knowledge_point_hints": [], "confidence": .9, "source_refs": [ref()], "issues": []}
    request = payload("会议资料附件\n选择题\n第1题 计算\n会议号\n发起人\n回放")
    result = PaperParser(FakeStructuredModel(output("question", questions=[question]))).parse(request)

    assert result["question_candidates"][0]["candidate_id"] == "q1"


def test_rejects_empty_document_collection():
    with pytest.raises(AgentError): PaperParser(FakeStructuredModel(output("unknown"))).parse({"request_id": "job", "subject": "语文", "documents": []})


def test_rejects_duplicate_source_identity_or_document_order():
    request = payload()
    request["documents"].append({**request["documents"][0], "source_id": "source-2"})
    with pytest.raises(AgentError) as raised:
        PaperParser(FakeStructuredModel(output("unknown"))).parse(request)
    assert raised.value.code == "invalid_request"


def test_rejects_untrusted_provenance_from_model():
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [ref("invented")], "issues": []}
    with pytest.raises(AgentError): PaperParser(FakeStructuredModel(output("answer", answers=[answer]))).parse(payload())


def test_rejects_source_ref_with_fabricated_file_or_document_index():
    fabricated = {**ref(), "file_asset_id": "invented", "document_index": 9}
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [fabricated], "issues": []}
    with pytest.raises(AgentError): PaperParser(FakeStructuredModel(output("answer", answers=[answer]))).parse(payload())


def test_direct_text_discards_model_invented_ocr_metadata():
    model_ref = {
        **ref(),
        "page_no": 1,
        "bbox": {},
        "ocr_confidence": 1.0,
    }
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [model_ref], "issues": []}

    result = PaperParser(FakeStructuredModel(output("answer", answers=[answer]))).parse(payload())

    normalized_ref = result["answer_candidates"][0]["source_refs"][0]
    assert normalized_ref["page_no"] is None
    assert normalized_ref["bbox"] is None
    assert normalized_ref["ocr_confidence"] is None


def test_rejects_duplicate_candidate_ids_across_candidate_kinds():
    answer = {"candidate_id": "same", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [ref()], "issues": []}
    solution = {"candidate_id": "same", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "raw_text": "解析", "steps": [], "confidence": .8, "source_refs": [ref()], "issues": []}
    with pytest.raises(AgentError): PaperParser(FakeStructuredModel(output("mixed", answers=[answer], solutions=[solution]))).parse(payload())


def test_rejects_ocr_ref_with_fabricated_confidence():
    ocr_ref = {**ref(), "page_no": 1, "block_id": "b1", "bbox": [1, 2, 3, 4], "text_start": None, "text_end": None, "ocr_confidence": .99}
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [ocr_ref], "issues": []}
    request = payload()
    request["documents"][0]["blocks"] = [{"source_id": "source-1", "document_index": 0, "block_id": "b1", "page_no": 1, "text": "1.A", "bbox": [1, 2, 3, 4], "confidence": .82}]
    with pytest.raises(AgentError): PaperParser(FakeStructuredModel(output("answer", answers=[answer]))).parse(request)


def test_candidate_confidence_never_exceeds_its_grounded_ocr_confidence():
    ocr_ref = {**ref(), "page_no": 1, "block_id": "b1", "bbox": [1, 2, 3, 4], "text_start": None, "text_end": None, "ocr_confidence": .52}
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "A", "equivalent_answers": [], "tolerance": None, "confidence": .99, "source_refs": [ocr_ref], "issues": []}
    request = payload()
    request["documents"][0]["blocks"] = [{"source_id": "source-1", "document_index": 0, "block_id": "b1", "page_no": 1, "text": "1.A", "bbox": [1, 2, 3, 4], "confidence": .52}]

    result = PaperParser._validate(output("answer", answers=[answer]), "job-1", "mathematics", request["documents"])

    assert result["answer_candidates"][0]["confidence"] == .52


def test_explicit_rubric_is_preserved_with_nullable_scores_and_provenance():
    rubric = {"candidate_id": "r1", "question_no_hint": "18(1)", "question_no_normalized": "18(1)", "subquestion_no_hint": None, "max_score": 6, "points": [{"id": "p1", "description": "列出关系式", "score": None, "required": True, "evidence_requirements": []}], "deductions": [], "examples": [], "confidence": .94, "source_refs": [ref()], "issues": []}
    result = PaperParser(FakeStructuredModel(output("rubric", rubrics=[rubric]))).parse(payload("评分标准：列出关系式"))
    assert result["rubric_candidates"][0]["points"][0]["score"] is None
    assert result["rubric_candidates"][0]["source_refs"][0]["source_id"] == "source-1"


def test_solution_without_explicit_rubric_does_not_generate_one():
    solution = {"candidate_id": "s1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "raw_text": "第一步，第二步", "steps": [{"step_no": 1, "content": "第一步"}], "confidence": .9, "source_refs": [ref()], "issues": []}
    result = PaperParser(FakeStructuredModel(output("solution", solutions=[solution]))).parse(payload("解析：第一步，第二步"))
    assert result["rubric_candidates"] == []


def test_rejects_rubric_with_ungrounded_provenance():
    rubric = {"candidate_id": "r1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "max_score": 2, "points": [], "deductions": [], "examples": [], "confidence": .9, "source_refs": [ref("invented")], "issues": []}
    with pytest.raises(AgentError):
        PaperParser(FakeStructuredModel(output("rubric", rubrics=[rubric]))).parse(payload("评分标准"))


def test_mixed_document_can_return_all_four_candidate_kinds():
    question = {"candidate_id": "q1", "question_no_raw": "1", "question_no_normalized": "1", "parent_question_no": None, "subquestion_no": None, "section_hint": None, "stem": "求值", "options": [], "question_type": "calculation", "score": 2, "knowledge_point_hints": [], "confidence": .95, "source_refs": [ref()], "issues": []}
    answer = {"candidate_id": "a1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "standard_answer": "2", "equivalent_answers": [], "tolerance": None, "confidence": .95, "source_refs": [ref()], "issues": []}
    solution = {"candidate_id": "s1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "raw_text": "计算得2", "steps": [{"step_no": 1, "content": "计算"}], "confidence": .9, "source_refs": [ref()], "issues": []}
    rubric = {"candidate_id": "r1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "max_score": 2, "points": [{"id": "p1", "description": "过程正确", "score": 1, "required": True, "evidence_requirements": []}, {"id": "p2", "description": "结果正确", "score": 1, "required": True, "evidence_requirements": []}], "deductions": [], "examples": [], "confidence": .93, "source_refs": [ref()], "issues": []}
    result = PaperParser(FakeStructuredModel(output("mixed", [question], [answer], [solution], [rubric]))).parse(payload("题目 答案 解析 评分标准"))
    assert [len(result[key]) for key in ("question_candidates", "answer_candidates", "solution_candidates", "rubric_candidates")] == [1, 1, 1, 1]


def test_rejects_negative_rubric_point_score():
    rubric = {"candidate_id": "r1", "question_no_hint": "1", "question_no_normalized": "1", "subquestion_no_hint": None, "max_score": 2, "points": [{"id": "p1", "description": "过程", "score": -1, "required": True, "evidence_requirements": []}], "deductions": [], "examples": [], "confidence": .9, "source_refs": [ref()], "issues": []}
    with pytest.raises(AgentError):
        PaperParser(FakeStructuredModel(output("rubric", rubrics=[rubric]))).parse(payload("评分标准"))
