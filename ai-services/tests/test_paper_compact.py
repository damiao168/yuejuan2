import pytest

from grading_agent.errors import AgentError
from grading_agent.paper_compact import (
    MAX_BLOCKS_PER_CHUNK,
    anchored_paper_result,
    build_compact_chunks,
    expand_compact_output,
    page_furniture_issues,
)


def _document(blocks):
    return {
        "source_id": "source-1",
        "file_asset_id": "file-1",
        "document_index": 0,
        "role_hint": "auto",
        "content": "\n".join(block["text"] for block in blocks),
        "blocks": blocks,
    }


def _block(block_id, text, bbox):
    return {
        "source_id": "source-1",
        "document_index": 0,
        "page_no": 1,
        "block_id": block_id,
        "text": text,
        "bbox": bbox,
        "confidence": 0.91,
    }


def test_two_column_page_is_converted_to_column_major_reading_order():
    blocks = [_block("header", "试卷标题", [100, 10, 800, 30])]
    for row in range(4):
        # Detector order is row-major and therefore interleaves both columns.
        blocks.extend(
            [
                _block(f"right-{row}", f"右栏{row}", [600, 100 + row * 40, 300, 20]),
                _block(f"left-{row}", f"左栏{row}", [50, 100 + row * 40, 300, 20]),
            ]
        )

    chunks = build_compact_chunks([_document(blocks)])
    ordered_text = [
        text for chunk in chunks for _, text in chunk.model_document["ordered_blocks"]
    ]

    assert ordered_text == [
        "试卷标题",
        "左栏0",
        "左栏1",
        "左栏2",
        "左栏3",
        "右栏0",
        "右栏1",
        "右栏2",
        "右栏3",
    ]


def test_three_column_page_is_recursively_ordered_by_geometry():
    blocks = []
    for row in range(4):
        # Real detectors often emit same-row boxes before finishing a column.
        blocks.extend(
            [
                _block(f"middle-{row}", f"中栏{row}", [360, 80 + row * 35, 220, 18]),
                _block(f"right-{row}", f"右栏{row}", [670, 80 + row * 35, 220, 18]),
                _block(f"left-{row}", f"左栏{row}", [50, 80 + row * 35, 220, 18]),
            ]
        )

    chunks = build_compact_chunks([_document(blocks)])
    ordered_text = [
        text for chunk in chunks for _, text in chunk.model_document["ordered_blocks"]
    ]

    assert ordered_text == [
        *(f"左栏{row}" for row in range(4)),
        *(f"中栏{row}" for row in range(4)),
        *(f"右栏{row}" for row in range(4)),
    ]


def test_dense_page_is_split_into_bounded_chunks():
    blocks = [
        _block(f"b{index}", f"{index}. " + "题目内容" * 8, [50, index * 25, 500, 20])
        for index in range(1, 80)
    ]

    chunks = build_compact_chunks([_document(blocks)])

    assert len(chunks) > 1
    assert (
        max(len(chunk.model_document["ordered_blocks"]) for chunk in chunks)
        <= MAX_BLOCKS_PER_CHUNK
    )
    assert sum(len(chunk.references) for chunk in chunks) == len(blocks)


def test_compact_references_are_hydrated_from_trusted_ocr_values():
    block = _block("trusted-block", "1. 计算", [10, 20, 30, 40])
    chunk = build_compact_chunks([_document([block])])[0]
    output = {
        "documents": [
            {
                "source_id": "source-1",
                "detected_role": "question",
                "role_confidence": 0.95,
            }
        ],
        "question_candidates": [
            {
                "question_no_raw": "1",
                "question_no_normalized": "1",
                "parent_question_no": None,
                "subquestion_no": None,
                "section_hint": None,
                "stem": "计算",
                "options": [],
                "question_type": "calculation",
                "score": None,
                "knowledge_point_hints": [],
                "confidence": 0.9,
                "source_refs": ["r001"],
                "issues": [],
            }
        ],
        "answer_candidates": [],
        "solution_candidates": [],
        "rubric_candidates": [],
        "issues": [],
    }

    expanded = expand_compact_output(output, chunk, 2)
    question = expanded["question_candidates"][0]

    assert question["candidate_id"] == "q-002-001"
    assert question["source_refs"][0]["block_id"] == "trusted-block"
    assert question["source_refs"][0]["bbox"] == [10, 20, 30, 40]
    assert question["source_refs"][0]["ocr_confidence"] == 0.91


def test_model_cannot_invent_a_compact_reference():
    chunk = build_compact_chunks(
        [_document([_block("trusted-block", "1. 计算", [10, 20, 30, 40])])]
    )[0]
    output = {
        "documents": [],
        "question_candidates": [
            {
                "source_refs": ["r999"],
            }
        ],
        "answer_candidates": [],
        "solution_candidates": [],
        "rubric_candidates": [],
        "issues": [],
    }

    with pytest.raises(AgentError) as raised:
        expand_compact_output(output, chunk, 1)

    assert raised.value.code == "model_output_invalid"


def test_explicit_number_answer_and_solution_markers_use_rule_fast_path():
    blocks = [
        _block("q1", "1. 已知集合A，求A的补集（ ）", [50, 100, 500, 20]),
        _block("o1", "A. R B. 空集 C. A D. 无法确定", [50, 130, 500, 20]),
        _block("a1", "【答案】B", [50, 160, 100, 20]),
        _block("s1", "【详解】根据补集定义可得。", [50, 190, 400, 20]),
        _block("q2", "2. 计算1+1（ ）", [50, 230, 500, 20]),
        _block("o2", "A. 0 B. 1 C. 2 D. 3", [50, 260, 500, 20]),
        _block("a2", "【答案】C", [50, 290, 100, 20]),
    ]
    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    assert [
        item["question_no_normalized"] for item in result["question_candidates"]
    ] == [
        "1",
        "2",
    ]
    assert [item["standard_answer"] for item in result["answer_candidates"]] == [
        "B",
        "C",
    ]
    assert result["solution_candidates"][0]["raw_text"] == "根据补集定义可得。"
    assert result["answer_candidates"][0]["source_refs"][0]["block_id"] == "a1"


def test_rule_fast_path_excludes_bottom_page_number_and_distribution_watermark():
    blocks = [
        _block("q1", "1. 计算1+1（ ）", [50, 100, 500, 20]),
        _block("a1", "【答案】C", [50, 130, 100, 20]),
        _block("s1", "【详解】计算可得2。", [50, 160, 400, 20]),
        _block("q2", "2. 计算2+2（ ）", [50, 220, 500, 20]),
        _block("a2", "【答案】D", [50, 250, 100, 20]),
        _block("s2", "【详解】计算可得4。", [50, 280, 400, 20]),
        _block("page", "试卷第1页，共1页", [350, 950, 180, 20]),
        _block("watermark", "公众号·资料分享", [650, 948, 260, 24]),
    ]

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    final_solution = result["solution_candidates"][-1]
    assert final_solution["raw_text"] == "计算可得4。"
    assert {ref["block_id"] for ref in final_solution["source_refs"]} == {"s2"}
    excluded = page_furniture_issues([_document(blocks)])
    assert {issue["source_refs"][0]["block_id"] for issue in excluded} == {"page", "watermark"}
    assert all(issue["severity"] == "info" for issue in excluded)
    assert len(blocks) == 8  # Raw OCR evidence is never mutated.


def test_furniture_filter_keeps_question_about_public_account_at_bottom():
    blocks = [
        _block("header", "数学考试", [50, 10, 500, 20]),
        _block("q1", "1. 调查某公众号的关注人数", [50, 850, 500, 20]),
        _block("s1", "【详解】该公众号的关注人数为100。", [50, 900, 500, 20]),
        _block("q2", "2. 求二维码面积", [50, 950, 500, 20]),
    ]

    chunks = build_compact_chunks([_document(blocks)])
    texts = [text for chunk in chunks for _, text in chunk.model_document["ordered_blocks"]]
    assert set(texts) == {block["text"] for block in blocks}
    assert page_furniture_issues([_document(blocks)]) == []


def test_furniture_filter_keeps_repeated_math_steps_at_page_edges():
    blocks = []
    for page in (1, 2):
        for block_id, text, box in (
            ("header", "明德中学期末数学考试", [50, 10, 500, 20]),
            ("q", f"{page}. 求解方程", [50, 100, 500, 20]),
            ("step", "所以x=1或x=-1", [50, 950, 500, 20]),
        ):
            block = _block(f"{block_id}-{page}", text, box)
            block["page_no"] = page
            blocks.append(block)

    chunks = build_compact_chunks([_document(blocks)])
    texts = [text for chunk in chunks for _, text in chunk.model_document["ordered_blocks"]]
    assert texts.count("所以x=1或x=-1") == 2
    assert "明德中学期末数学考试" not in texts


def test_furniture_filter_keeps_unanchored_question_continuation_about_qr_code():
    blocks = [
        _block("header", "数学考试", [50, 10, 500, 20]),
        _block("q1", "1. 已知正方形图案", [50, 100, 500, 20]),
        _block("continuation", "请关注二维码中黑色区域的面积", [50, 950, 500, 20]),
    ]

    assert page_furniture_issues([_document(blocks)]) == []


def test_rule_fast_path_confidence_is_bounded_by_weakest_ocr_or_formula_evidence():
    blocks = [
        _block("q1", "1. 已知x2=4（ ）", [50, 100, 500, 20]),
        _block("formula1", r"\(x^{2}=4\)", [150, 100, 120, 20]),
        _block("a1", "【答案】A", [50, 130, 100, 20]),
        _block("q2", "2. 计算2+2（ ）", [50, 180, 500, 20]),
        _block("a2", "【答案】D", [50, 210, 100, 20]),
    ]
    blocks[1]["confidence"] = 0.47

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    assert result["question_candidates"][0]["confidence"] == 0.47
    assert result["question_candidates"][1]["confidence"] == 0.91


def test_furniture_exclusion_preserves_two_column_seven_question_sequence():
    blocks = [_block("header", "期末数学考试", [218, 93, 845, 33])]
    for row in range(4):
        for question_no, x in ((row + 1, 77), (row + 5, 661)):
            if question_no > 7:
                continue
            y = 190 + row * 340
            blocks.extend([
                _block(f"q{question_no}", f"{question_no}. 求解方程", [x, y, 537, 27]),
                _block(f"o{question_no}", "A. 1 B. 2 C. 3 D. 4", [x, y + 40, 537, 27]),
                _block(f"a{question_no}", "【答案】A", [x, y + 80, 108, 27]),
                _block(f"s{question_no}", "【详解】计算得到1。", [x, y + 120, 537, 27]),
            ])
    blocks.extend([
        _block("footer", "试卷第1页，共1页", [540, 1739, 199, 20]),
        _block("watermark", "公众号·资料分享", [896, 1737, 345, 30]),
    ])

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    assert [q["question_no_normalized"] for q in result["question_candidates"]] == [str(n) for n in range(1, 8)]
    assert len(result["solution_candidates"]) == 7
    assert all(s["raw_text"] == "计算得到1。" for s in result["solution_candidates"])


def test_rule_fast_path_declines_unanchored_or_sparse_answer_material():
    blocks = [
        _block("q1", "1. 简述函数概念", [50, 100, 500, 20]),
        _block("q2", "2. 简述导数概念", [50, 140, 500, 20]),
        _block("q3", "3. 简述数列概念", [50, 180, 500, 20]),
        _block("a3", "【答案】略", [50, 220, 100, 20]),
    ]

    assert anchored_paper_result([_document(blocks)]) is None


def test_game_points_are_not_mistaken_for_question_marks():
    blocks = [
        _block("q8", "8. 取到1次白球得4分，求获胜概率", [50, 100, 500, 20]),
        _block("a8", "【答案】二分之一", [50, 130, 200, 20]),
        _block("q18", "18. 每局比赛胜者得1分，负者得0分", [50, 180, 500, 20]),
        _block("a18", "【答案】见解析", [50, 210, 200, 20]),
    ]

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    assert [q["score"] for q in result["question_candidates"]] == [None, None]


def test_question_heading_and_section_marks_are_grounded_separately():
    blocks = [
        _block("section", "一、选择题，每小题5分，共10分", [50, 50, 500, 20]),
        _block("q1", "1. 计算1+1", [50, 100, 500, 20]),
        _block("a1", "【答案】2", [50, 130, 200, 20]),
        _block("q2", "2. （6分）计算2+2", [50, 180, 500, 20]),
        _block("a2", "【答案】4", [50, 210, 200, 20]),
    ]

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    q1, q2 = result["question_candidates"]
    assert [q1["score"], q2["score"]] == [5, 6]
    assert {ref["block_id"] for ref in q1["source_refs"]} == {"section", "q1"}
    assert {ref["block_id"] for ref in q2["source_refs"]} == {"q2"}


def test_section_mark_on_separate_line_is_applied_with_its_own_source():
    blocks = [
        _block("section", "二、填空题", [50, 50, 500, 20]),
        _block("section-mark", "每小题3分", [50, 75, 500, 20]),
        _block("q12", "12. 填空：1+1=____", [50, 100, 500, 20]),
        _block("a12", "【答案】2", [50, 130, 200, 20]),
        _block("q13", "13. 填空：2+2=____", [50, 180, 500, 20]),
        _block("a13", "【答案】4", [50, 210, 200, 20]),
    ]

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    assert [q["score"] for q in result["question_candidates"]] == [3, 3]
    assert all("section-mark" in {ref["block_id"] for ref in q["source_refs"]} for q in result["question_candidates"])


def test_explicit_marking_points_survive_anchored_fast_path():
    blocks = [
        _block("q1", "1. （4分）解方程", [50, 100, 500, 20]),
        _block("a1", "【答案】x=2", [50, 130, 200, 20]),
        _block("s1", "【详解】移项得2x=4。", [50, 160, 500, 20]),
        _block("r1", "【评分标准】移项正确得2分；求得x=2得2分", [50, 190, 500, 20]),
        _block("q2", "2. 计算1+1", [50, 230, 500, 20]),
        _block("a2", "【答案】2", [50, 260, 200, 20]),
    ]

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    rubric = result["rubric_candidates"][0]
    assert rubric["max_score"] == 4
    assert [(p["description"], p["score"]) for p in rubric["points"]] == [
        ("移项正确", 2), ("求得x=2", 2)
    ]
    assert [ref["block_id"] for ref in rubric["source_refs"]] == ["r1"]
    assert result["answer_candidates"][0]["standard_answer"] == "x=2"
    assert result["solution_candidates"][0]["raw_text"] == "移项得2x=4。"


def test_problem_story_awarding_points_is_not_a_marking_scheme():
    blocks = [
        _block("q1", "1. 判断正确得2分，错误得0分；求小明的总得分", [50, 100, 500, 20]),
        _block("a1", "【答案】6", [50, 130, 200, 20]),
        _block("q2", "2. 计算1+1", [50, 180, 500, 20]),
        _block("a2", "【答案】2", [50, 210, 200, 20]),
    ]

    result = anchored_paper_result([_document(blocks)])

    assert result is not None
    assert result["rubric_candidates"] == []
    assert result["question_candidates"][0]["score"] is None


def test_unstructured_explicit_marking_prose_routes_to_model():
    blocks = [
        _block("q1", "1. 解方程", [50, 100, 500, 20]),
        _block("a1", "【答案】x=2", [50, 130, 200, 20]),
        _block("r1", "【评分细则】方法合理且答案正确时酌情给分", [50, 160, 500, 20]),
        _block("q2", "2. 计算1+1", [50, 200, 500, 20]),
        _block("a2", "【答案】2", [50, 230, 200, 20]),
    ]

    assert anchored_paper_result([_document(blocks)]) is None


def test_partial_explicit_marking_prose_routes_to_model_without_dropping_remainder():
    blocks = [
        _block("q1", "1. 解方程", [50, 100, 500, 20]),
        _block("a1", "【答案】x=2", [50, 130, 200, 20]),
        _block("r1", "【评分标准】移项正确得2分；其他合理解法酌情给分", [50, 160, 500, 20]),
        _block("q2", "2. 计算1+1", [50, 200, 500, 20]),
        _block("a2", "【答案】2", [50, 230, 200, 20]),
    ]

    assert anchored_paper_result([_document(blocks)]) is None
