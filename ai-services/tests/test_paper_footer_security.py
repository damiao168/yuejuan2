import copy
from itertools import product

import pytest
from grading_agent.paper_compact import anchors, page_furniture_issues
from grading_agent.paper_compact.schema import _PAGE_FOOTER, MAX_PAGE_FOOTER_TEXT_CHARS


def _document(footer):
    return {
        "source_id": "synthetic-footer-source",
        "file_asset_id": "synthetic-footer-file",
        "document_index": 0,
        "synthetic": True,
        "blocks": [
            {"block_id": "body", "page_no": 1, "text": "正文", "bbox": [0, 0, 100, 20]},
            {"block_id": "footer", "page_no": 1, "text": footer, "bbox": [0, 980, 100, 20]},
        ],
    }


def test_footer_filter_accepts_existing_prefixes_whitespace_and_punctuation():
    # 合成坐标保证候选位于页面底部，同时核对常用中文页码形式的排除契约。
    for prefix, space, punctuation, total in product(
        ("", "试卷", "答题卡"), ("", " ", "\t", "\n", "\u3000"), ("", ",", "，", "、"), (False, True)
    ):
        footer = f"{prefix}{space}第{space}１{space}页{space}{punctuation}{space}"
        if total:
            footer += f"共{space}２{space}页"
        document = _document(footer)
        original = copy.deepcopy(document)

        issues = page_furniture_issues([document])

        assert [issue["source_refs"][0]["block_id"] for issue in issues] == ["footer"], footer
        assert document == original


@pytest.mark.parametrize("footer", ["第1页，共", "第1页，共2页正文", "第1页!!", "第页", "第1页，共2"])
def test_footer_filter_retains_incomplete_or_body_like_footer_text(footer):
    assert page_furniture_issues([_document(footer)]) == []


@pytest.mark.parametrize(
    "footer",
    ["第1页" + " " * 8192 + "!", "第" + "9" * 100_000 + "页", "第1页" + " " * 100_000 + "共2页"],
    ids=["internal-whitespace-failure", "long-page-number", "long-valid-footer"],
)
def test_long_ocr_blocks_never_reach_footer_regex(monkeypatch, footer):
    class UnexpectedFooterRegex:
        def fullmatch(self, text):
            raise AssertionError("oversized OCR text reached footer matching")

    monkeypatch.setattr(anchors, "_PAGE_FOOTER", UnexpectedFooterRegex())
    document = _document(footer)
    original = copy.deepcopy(document)

    assert page_furniture_issues([document]) == []
    assert document == original


@pytest.mark.parametrize("extra", [0, 1])
def test_footer_length_budget_retains_oversized_candidate(extra):
    footer = "第" + " " * (MAX_PAGE_FOOTER_TEXT_CHARS - 3 + extra) + "1页"
    issues = page_furniture_issues([_document(footer)])

    assert bool(issues) is (extra == 0)


def test_footer_regex_rejects_long_internal_whitespace_with_failing_suffix():
    assert _PAGE_FOOTER.fullmatch("第1页" + " " * 100_000 + "!") is None
