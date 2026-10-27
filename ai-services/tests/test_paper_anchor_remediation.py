from contextlib import nullcontext
from types import SimpleNamespace

import pytest
from grading_agent.paper_compact import anchored_paper_result
from grading_agent.paper_parser import PaperParser


def request(lines):
    return {"request_id": "synthetic-anchor", "subject": "math", "documents": [{
        "source_id": "source-1", "file_asset_id": "file-1", "document_index": 0,
        "role_hint": "auto", "content": "\n".join(lines), "blocks": [],
    }]}


class FallbackModel:
    settings = SimpleNamespace(provider_key="fixture", model_name="synthetic")

    def __init__(self):
        self.calls = []

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, *args):
        self.calls.append(args)
        return {"documents": [{"source_id": "source-1", "detected_role": "unknown", "role_confidence": .5}],
                "question_candidates": [], "answer_candidates": [], "solution_candidates": [], "rubric_candidates": [], "issues": []}


@pytest.mark.parametrize("middle", [
    ["【解析】1. 2x=4", "2. x=2", "2. Calculate 1+1", "【答案】2"],
    ["【解析】", "1. 2x=4", "2. x=2", "2. Calculate 1+1", "【答案】2"],
    ["【解析】1. 2x=4", "2. x=2"],
])
def test_numbered_solution_ambiguity_uses_model_without_publishing_mixed_stem(middle):
    payload = request(["1. Solve x", "【答案】x=2", *middle])
    assert anchored_paper_result(payload["documents"]) is None
    model = FallbackModel()
    events = []
    result = PaperParser(model).parse(payload, progress=events.append)
    assert model.calls
    assert any(event["route"] == "full_model" for event in events)
    assert result["question_candidates"] == []
    assert "2. x=2" in str(model.calls[0])


@pytest.mark.parametrize("answer_lines,expected", [
    (["【答案】(1) x=2", "(2) y=3"], "(1) x=2\n(2) y=3"),
    (["【答案】", "(1) a", "(2) b", "(3) c", "(4) d", "(5) e"], "(1) a\n(2) b\n(3) c\n(4) d\n(5) e"),
])
def test_multiline_answer_is_preserved_to_next_section_boundary(answer_lines, expected):
    payload = request(["1. Solve both", *answer_lines, "【解析】完整解析", "2. Calculate 1+1", "【答案】2"])
    # None 保证此对照确实走无模型锚点路径，题号仍能正常分开。
    result = PaperParser(None).parse(payload)
    assert [q["question_no_normalized"] for q in result["question_candidates"]] == ["1", "2"]
    assert result["answer_candidates"][0]["standard_answer"] == expected
    assert result["answer_candidates"][1]["standard_answer"] == "2"
    assert result["solution_candidates"][0]["raw_text"] == "完整解析"
