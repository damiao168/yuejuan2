import copy

import pytest

from grading_agent.errors import AgentError
from grading_agent.paper_validation import normalize_direct_text_refs, validate_paper_output, validate_refs


def _document():
    return {"source_id": "source-1", "file_asset_id": "file-1", "document_index": 0, "content": "question text"}


def _ref():
    return {
        "source_id": "source-1", "file_asset_id": "file-1", "document_index": 0,
        "page_no": None, "block_id": None, "bbox": None,
        "text_start": 0, "text_end": 1, "ocr_confidence": None,
    }


def _output():
    return {
        "documents": [{"source_id": "source-1", "detected_role": "question", "role_confidence": 0.9}],
        "question_candidates": [{"candidate_id": "q1", "confidence": 0.8, "source_refs": [_ref()], "question_type": "calculation", "score": None}],
        "answer_candidates": [], "solution_candidates": [], "rubric_candidates": [], "issues": [],
    }


def test_all_candidate_provenance_is_checked_before_rubric_scores_and_question_routes():
    result = _output()
    result["question_candidates"][0]["question_type"] = "unknown"
    result["rubric_candidates"] = [{"candidate_id": "r1", "confidence": 0.8, "source_refs": [], "max_score": -1}]
    document = _document()
    with pytest.raises(AgentError, match='candidate provenance was not grounded'):
        validate_paper_output(result, 'request-1', 'mathematics', [document])


def test_earlier_candidate_confidence_mutation_is_preserved_when_later_candidate_fails():
    document = _document()
    document["blocks"] = [{"block_id": "b1", "page_no": 1, "bbox": [1, 2, 3, 4], "confidence": 0.4}]
    result = _output()
    result["question_candidates"][0]["source_refs"] = [{
        **_ref(), "block_id": "b1", "page_no": 1, "bbox": [1, 2, 3, 4],
        "ocr_confidence": 0.4, "text_start": None, "text_end": None,
    }]
    result["answer_candidates"] = [{"candidate_id": "a1", "confidence": True, "source_refs": [_ref()]}]
    with pytest.raises(AgentError, match="candidate confidence was invalid"):
        validate_paper_output(result, "request-1", "mathematics", [document])
    assert result["question_candidates"][0]["confidence"] == 0.4


@pytest.mark.parametrize("start,end", [(True, 1), (0, False), (-1, 1), (1, 1), (0, 100)])
def test_direct_text_provenance_requires_a_nonempty_bounded_integer_range(start, end):
    document = _document()
    reference = _ref()
    with pytest.raises(AgentError, match='text provenance range was invalid'):
        validate_refs([{**reference, 'text_start': start, 'text_end': end}], {'source-1': document}, 'request-1')


def test_visual_page_provenance_cannot_fall_back_to_ocr_blocks():
    document = _document()
    document["_visual_page_nos"] = [1]
    document["blocks"] = [{"block_id": "b1", "page_no": 1, "bbox": [1, 2, 3, 4], "confidence": 0.4}]
    ref = {**_ref(), "page_no": 1, "text_start": None, "text_end": None}
    validate_refs([ref], {"source-1": document}, "request-1")
    with pytest.raises(AgentError, match="visual provenance did not match a supplied page"):
        validate_refs([{**ref, "block_id": "b1"}], {"source-1": document}, "request-1")


def test_direct_ref_normalization_only_clears_metadata_for_trusted_direct_sources():
    trusted = {**_ref(), "page_no": 1, "block_id": "invented", "bbox": [1, 2, 3, 4], "ocr_confidence": 1}
    foreign = {**trusted, "source_id": "other"}
    foreign_before = copy.deepcopy(foreign)
    result = _output()
    result["question_candidates"][0]["source_refs"] = [trusted, foreign]
    normalize_direct_text_refs(result, [_document()])
    assert trusted == _ref()
    assert foreign == foreign_before
