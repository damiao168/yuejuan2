import pytest
from grading_agent.paper_compact import anchored_paper_result


def _document(lines):
    return {
        "source_id": "marker-source",
        "file_asset_id": "marker-file",
        "document_index": 0,
        "content": "\n".join(lines),
        "blocks": [
            {
                "page_no": 1,
                "block_id": f"block-{index}",
                "text": text,
                "bbox": [50, 100 + index * 30, 400, 20],
                "confidence": 0.99,
            }
            for index, text in enumerate(lines)
        ],
    }


@pytest.mark.parametrize(
    "heading,number,next_number",
    [
        ("第\t1\u3000题\u00a0题干", "1", "2"),
        ("1.\u00a0题干", "1", "2"),
        ("1．\u3000题干", "1", "2"),
        ("1、\t题干", "1", "2"),
        ("1) 题干", "1", "2"),
        ("1） 题干", "1", "2"),
        ("第\n1\n题\n题干", "1", "2"),
        ("1١. 题干", "11", "12"),
        ("998. 题干", "998", "999"),
    ],
)
def test_question_heading_keeps_number_and_stem(heading, number, next_number):
    result = anchored_paper_result(
        [_document([heading, "答案A", f"{next_number}. 第二题", "答案B"])]
    )

    assert result is not None
    assert result["question_candidates"][0]["question_no_normalized"] == number
    assert result["question_candidates"][0]["stem"] == "题干"
    assert result["answer_candidates"][0]["standard_answer"] == "A"


@pytest.mark.parametrize(
    "answer,solution",
    [
        ("【答案】\u3000：\u00a0B", "【详解】\t: 根据定义。"),
        ("[答案]\t: B", "[解析]\u3000：根据定义。"),
        ("[答案】\n：\tB\n", "【分析]\u00a0: 根据定义。\n"),
        ("答案B", "解析根据定义。"),
        ("【答案\tB", "详解】\u3000根据定义。"),
        ("答案】\nB", "【分析\n根据定义。"),
    ],
)
def test_marker_punctuation_and_unicode_whitespace_keep_contents(answer, solution):
    # 左右括号在现有协议中各自可选，混合括号也属于需要保留的兼容范围。
    result = anchored_paper_result(
        [_document(["1. 题干", answer, solution, "2. 第二题", "答案C"])]
    )

    assert result is not None
    assert result["answer_candidates"][0]["standard_answer"] == "B"
    assert result["solution_candidates"][0]["raw_text"] == "根据定义。"


def test_blank_markers_keep_following_content_and_source_refs():
    result = anchored_paper_result(
        [_document(["1. 题干", "【答案】\u3000：\t", "B", "解析\u00a0", "根据定义。", "2. 第二题", "答案C"])]
    )

    assert result is not None
    answer = result["answer_candidates"][0]
    solution = result["solution_candidates"][0]
    assert answer["standard_answer"] == "B"
    assert [ref["block_id"] for ref in answer["source_refs"]] == ["block-1", "block-2"]
    assert solution["raw_text"] == "根据定义。"
    assert [ref["block_id"] for ref in solution["source_refs"]] == ["block-3", "block-4"]


@pytest.mark.parametrize(
    "lines",
    [
        ["1. 题干\n第二行", "答案A", "2. 第二题", "答案B"],
        ["1. 题干", "答案A\nB", "2. 第二题", "答案C\nD"],
    ],
)
def test_internal_newline_in_body_keeps_fast_path_fallback(lines):
    # 正文仍不跨换行匹配；不能把失败输入改成成功的确定性解析。
    assert anchored_paper_result([_document(lines)]) is None
