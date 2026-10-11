import copy

import pytest

from grading_agent.errors import AgentError
from grading_agent.paper_visual import PaperVisualParser


def _document(pages):
    return {
        "source_id": "source-1", "file_asset_id": "trusted-file", "document_index": 2,
        "content": "authored text", "_visual_page_nos": pages,
    }


def _output(*refs):
    return {"question_candidates": [{"source_refs": list(refs)}]}


def test_single_page_defaults_only_missing_page_and_replaces_model_storage_metadata():
    ref = {
        "source_id": "source-1", "file_asset_id": "invented", "document_index": 99,
        "page_no": None, "block_id": "fabricated", "bbox": [1, 2, 3, 4],
        "text_start": 10, "text_end": 20, "ocr_confidence": 1,
    }
    PaperVisualParser._normalize_visual_refs(_output(ref), [_document([3])], "request-1")
    assert ref == {
        "source_id": "source-1", "file_asset_id": "trusted-file", "document_index": 2,
        "page_no": 3, "block_id": None, "bbox": None,
        "text_start": None, "text_end": None, "ocr_confidence": None,
    }


@pytest.mark.parametrize("pages,page", [([1, 2], None), ([1], True), ([1], "1"), ([1], 2)])
def test_ambiguous_or_invalid_page_fails_without_overwriting_that_reference(pages, page):
    ref = {"source_id": "source-1", "page_no": page, "file_asset_id": "invented"}
    before = copy.deepcopy(ref)
    output = _output(ref)
    document = _document(pages)
    with pytest.raises(AgentError, match='did not match a supplied page'):
        PaperVisualParser._normalize_visual_refs(output, [document], 'request-1')
    assert ref == before


def test_unknown_source_failure_can_be_corrected_and_retried_after_prior_ref_was_normalized():
    # 顺序处理保留前面已经可信归一化的引用；修正失败引用后，重试仍可重复执行。
    first = {"source_id": "source-1", "page_no": 1}
    second = {"source_id": "unknown", "page_no": 1}
    result = _output(first, second)
    document = _document([1])
    with pytest.raises(AgentError, match='unknown source'):
        PaperVisualParser._normalize_visual_refs(result, [document], 'request-1')
    assert first["file_asset_id"] == "trusted-file"
    assert second == {"source_id": "unknown", "page_no": 1}
    second["source_id"] = "source-1"
    PaperVisualParser._normalize_visual_refs(result, [_document([1])], "request-1")
    assert first == second


def test_visual_request_direct_text_source_uses_full_authored_character_range():
    document = _document([])
    ref = {"source_id": "source-1", "page_no": 100, "text_start": 9, "text_end": 10}
    PaperVisualParser._normalize_visual_refs(_output(ref), [document], "request-1")
    assert ref["page_no"] is None
    assert ref["text_start"] == 0
    assert ref["text_end"] == len(document["content"])


def test_non_object_candidate_and_reference_are_left_for_output_validator():
    result = {"question_candidates": [None, {"source_refs": [None]}]}
    before = copy.deepcopy(result)
    PaperVisualParser._normalize_visual_refs(result, [_document([1])], "request-1")
    assert result == before
