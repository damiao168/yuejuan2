"""Compact, provenance-safe model protocol for OCR paper imports.

The durable API payload intentionally retains complete OCR provenance. Sending
that representation to a language model is both wasteful and fragile: UUIDs,
coordinates and confidence values dominate the context, and the model is then
asked to copy those trusted values back. This module replaces them with bounded
request-local references and restores the authoritative values after inference.
"""

from __future__ import annotations

import math
import re
import unicodedata
from dataclasses import dataclass

from .errors import AgentError

MAX_BLOCKS_PER_CHUNK = 48
MAX_TEXT_CHARS_PER_CHUNK = 1_100
MAX_DIRECT_TEXT_CHARS_PER_CHUNK = 2_400
MAX_REFS_PER_CANDIDATE = 6

_QUESTION_START = re.compile(r"^\s*(?:第\s*)?(?:[1-9]\d{0,2})(?:\s*题|[.．、)）])")
_SECTION_START = re.compile(
    r"^\s*(?:[一二三四五六七八九十]+[、.．]|选择题|填空题|判断题|解答题|计算题|作文题)"
)
_QUESTION_NUMBER = re.compile(
    r"^\s*(?:第\s*)?([1-9]\d{0,2})(?:\s*题|[.．、)）])\s*(.*)$"
)
_ANSWER_MARKER = re.compile(r"^\s*[【\[]?答案[】\]]?\s*[:：]?\s*(.*)$")
_SOLUTION_MARKER = re.compile(r"^\s*[【\[]?(?:详解|解析|分析)[】\]]?\s*[:：]?\s*(.*)$")
_OPTION = re.compile(r"(?:^|\s)([A-DＡ-Ｄ])[.．、]\s*")
_SCORE = re.compile(r"(?:本题|每题)?\s*(\d+(?:\.\d+)?)\s*分")
_PAGE_FOOTER = re.compile(
    r"(?:试卷|答题卡)?\s*第\s*\d+\s*页\s*[,，、]?\s*(?:共\s*\d+\s*页)?"
)
_DISTRIBUTION_MARKER = re.compile(
    r"公众号|资料分享|微信(?:公众)?号|更多资料|版权所有|扫码(?:关注|领取|下载|获取)|关注(?:公众号|我们)"
)
_REPEATED_FURNITURE_MARKER = re.compile(
    r"试卷|考试|学年度|学期|命题|学校|考生|姓名|班级|密封|装订|资料|公众号|版权|https?://|www\."
)


def _nullable(kind):
    return {"anyOf": [{"type": kind}, {"type": "null"}]}


def compact_paper_import_schema(question_types, roles):
    """Return the small model-facing schema; it is not a persistence schema."""

    reference_ids = [f"r{index:03d}" for index in range(1, MAX_BLOCKS_PER_CHUNK + 1)]
    refs = {
        "type": "array",
        "minItems": 1,
        "maxItems": MAX_REFS_PER_CANDIDATE,
        "items": {"type": "string", "enum": reference_ids},
    }
    candidate_common = {
        "question_no_hint": _nullable("string"),
        "question_no_normalized": _nullable("string"),
        "subquestion_no_hint": _nullable("string"),
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "source_refs": refs,
        "issues": {"type": "array", "items": {"type": "string"}},
    }
    question = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "question_no_raw",
            "question_no_normalized",
            "parent_question_no",
            "subquestion_no",
            "section_hint",
            "stem",
            "options",
            "question_type",
            "score",
            "knowledge_point_hints",
            "confidence",
            "source_refs",
            "issues",
        ],
        "properties": {
            "question_no_raw": _nullable("string"),
            "question_no_normalized": _nullable("string"),
            "parent_question_no": _nullable("string"),
            "subquestion_no": _nullable("string"),
            "section_hint": _nullable("string"),
            "stem": _nullable("string"),
            "options": {"type": "array", "items": {"type": "string"}},
            "question_type": {
                "anyOf": [
                    {"type": "string", "enum": sorted(question_types)},
                    {"type": "null"},
                ]
            },
            "score": _nullable("number"),
            "knowledge_point_hints": {
                "type": "array",
                "items": {"type": "string"},
            },
            "confidence": candidate_common["confidence"],
            "source_refs": refs,
            "issues": candidate_common["issues"],
        },
    }
    answer = {
        "type": "object",
        "additionalProperties": False,
        "required": list(candidate_common)
        + ["standard_answer", "equivalent_answers", "tolerance"],
        "properties": {
            **candidate_common,
            "standard_answer": {},
            "equivalent_answers": {"type": "array"},
            "tolerance": {},
        },
    }
    solution = {
        "type": "object",
        "additionalProperties": False,
        "required": list(candidate_common) + ["raw_text", "steps"],
        "properties": {
            **candidate_common,
            "raw_text": {"type": "string"},
            "steps": {"type": "array", "items": {"type": "string"}},
        },
    }
    rubric_point = {
        "type": "object",
        "additionalProperties": False,
        "required": ["description", "score", "required"],
        "properties": {
            "description": {"type": "string"},
            "score": _nullable("number"),
            "required": _nullable("boolean"),
        },
    }
    rubric = {
        "type": "object",
        "additionalProperties": False,
        "required": list(candidate_common)
        + ["max_score", "points", "deductions", "examples"],
        "properties": {
            **candidate_common,
            "max_score": _nullable("number"),
            "points": {"type": "array", "items": rubric_point},
            "deductions": {"type": "array"},
            "examples": {"type": "array"},
        },
    }
    issue = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "code",
            "severity",
            "certainty",
            "question_no",
            "section",
            "message",
            "confidence",
            "source_refs",
            "resolution_hint",
        ],
        "properties": {
            "code": {"type": "string"},
            "severity": {
                "type": "string",
                "enum": ["info", "warning", "error"],
            },
            "certainty": {
                "type": "string",
                "enum": ["confirmed", "suspected", "unknown"],
            },
            "question_no": _nullable("string"),
            "section": _nullable("string"),
            "message": {"type": "string"},
            "confidence": _nullable("number"),
            "source_refs": {
                "type": "array",
                "maxItems": MAX_REFS_PER_CANDIDATE,
                "items": {"type": "string", "enum": reference_ids},
            },
            "resolution_hint": _nullable("string"),
        },
    }
    document = {
        "type": "object",
        "additionalProperties": False,
        "required": ["source_id", "detected_role", "role_confidence"],
        "properties": {
            "source_id": {"type": "string"},
            "detected_role": {"type": "string", "enum": sorted(roles)},
            "role_confidence": {"type": "number", "minimum": 0, "maximum": 1},
        },
    }
    return {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "documents",
            "question_candidates",
            "answer_candidates",
            "solution_candidates",
            "rubric_candidates",
            "issues",
        ],
        "properties": {
            "documents": {"type": "array", "items": document},
            "question_candidates": {"type": "array", "items": question},
            "answer_candidates": {"type": "array", "items": answer},
            "solution_candidates": {"type": "array", "items": solution},
            "rubric_candidates": {"type": "array", "items": rubric},
            "issues": {"type": "array", "items": issue},
        },
    }


@dataclass(frozen=True)
class CompactChunk:
    model_document: dict
    source_document: dict
    references: dict[str, dict]


def needs_compact_protocol(documents):
    return any(document.get("blocks") for document in documents) or any(
        len(str(document.get("content", ""))) > 4_000 for document in documents
    )


def anchored_paper_result(documents):
    """Parse strongly anchored printed answer papers without invoking an LLM.

    This fast path is deliberately conservative. It only activates when every
    document contains a monotonic numbered-question sequence and explicit
    answer markers cover at least half of those questions. Ambiguous material
    continues through the compact model protocol.
    """

    parsed_documents = []
    questions = []
    answers = []
    solutions = []
    for document in documents:
        blocks = document.get("blocks")
        if not isinstance(blocks, list) or not blocks:
            return None
        segments, _ = _anchored_segments(document, blocks)
        if len(segments) < 2:
            return None
        answer_count = sum(
            any(_ANSWER_MARKER.match(_block_text(entry[1])) for entry in segment[2])
            for segment in segments
        )
        if answer_count * 2 < len(segments):
            return None
        document_questions = []
        document_answers = []
        document_solutions = []
        for question_no, section, entries in segments:
            question, answer, solution = _anchored_candidate(
                document,
                question_no,
                section,
                entries,
                len(questions) + len(document_questions) + 1,
            )
            document_questions.append(question)
            if answer:
                document_answers.append(answer)
            if solution:
                document_solutions.append(solution)
        questions.extend(document_questions)
        answers.extend(document_answers)
        solutions.extend(document_solutions)
        role = "mixed" if document_answers or document_solutions else "question"
        parsed_documents.append(
            {
                "source_id": document["source_id"],
                "detected_role": role,
                "role_confidence": 0.98,
            }
        )
    if not questions:
        return None
    return {
        "documents": parsed_documents,
        "question_candidates": questions,
        "answer_candidates": answers,
        "solution_candidates": solutions,
        "rubric_candidates": [],
        "issues": [],
    }


def _anchored_segments(document, blocks):
    ordered = []
    by_page = {}
    retained = {id(block) for block in _without_page_furniture(blocks)}
    for sequence, block in enumerate(blocks):
        if not isinstance(block, dict) or not _block_text(block):
            continue
        try:
            page_no = int(block.get("page_no", 0))
        except (TypeError, ValueError):
            page_no = 0
        by_page.setdefault(page_no, []).append((sequence, block))
    for page_no in sorted(by_page):
        ordered.extend(
            entry
            for entry in _reading_order(by_page[page_no])
            if id(entry[1]) in retained
        )

    segments = []
    current = []
    current_number = 0
    current_section = None
    segment_section = None
    in_solution = False
    section_seen = False
    for entry in ordered:
        text = _block_text(entry[1])
        if _SECTION_START.match(text):
            current_section = text
            section_seen = True
            if not current:
                continue
        match = _QUESTION_NUMBER.match(text)
        if match:
            number = int(match.group(1))
            # Numbered solution steps are not new exam questions. A real next
            # question advances the monotonic paper sequence.
            if not current or (
                number > current_number
                and not (in_solution and number <= current_number)
            ):
                if current:
                    segments.append((current_number, segment_section, current))
                current = [entry]
                current_number = number
                segment_section = current_section
                in_solution = False
                continue
        if current:
            current.append(entry)
            in_solution = in_solution or bool(_SOLUTION_MARKER.match(text))
    if current:
        segments.append((current_number, segment_section, current))
    numbers = [segment[0] for segment in segments]
    if len(numbers) != len(set(numbers)) or numbers != sorted(numbers):
        return [], section_seen
    return segments, section_seen


def _anchored_candidate(document, question_no, section, entries, sequence):
    answer_index = next(
        (
            index
            for index, entry in enumerate(entries)
            if _ANSWER_MARKER.match(_block_text(entry[1]))
        ),
        None,
    )
    solution_index = next(
        (
            index
            for index, entry in enumerate(entries)
            if _SOLUTION_MARKER.match(_block_text(entry[1]))
        ),
        None,
    )
    boundaries = [
        value for value in (answer_index, solution_index) if value is not None
    ]
    question_end = min(boundaries) if boundaries else len(entries)
    question_entries = entries[:question_end]
    question_refs = [
        _trusted_block_ref(document, entry[1]) for entry in question_entries
    ]
    raw_lines = [_block_text(entry[1]) for entry in question_entries]
    if raw_lines:
        match = _QUESTION_NUMBER.match(raw_lines[0])
        if match:
            raw_lines[0] = match.group(2).strip()
    stem, options = _stem_and_options(raw_lines)
    score_match = _SCORE.search("\n".join(raw_lines))
    score = float(score_match.group(1)) if score_match else None
    question_type = _question_type(section, options)
    normalized = str(question_no)
    question = {
        "candidate_id": f"q-rule-{sequence:03d}",
        "question_no_raw": normalized,
        "question_no_normalized": normalized,
        "parent_question_no": None,
        "subquestion_no": None,
        "section_hint": section,
        "stem": stem or None,
        "options": options,
        "question_type": question_type,
        "score": score,
        "knowledge_point_hints": [],
        "confidence": _evidence_confidence(
            question_entries, 0.94 if answer_index is not None else 0.88
        ),
        "source_refs": question_refs,
        "issues": [],
    }

    answer = None
    if answer_index is not None:
        marker = _ANSWER_MARKER.match(_block_text(entries[answer_index][1]))
        value = marker.group(1).strip() if marker else ""
        answer_end = solution_index if solution_index is not None else len(entries)
        answer_entries = entries[answer_index:answer_end]
        if not value:
            value = " ".join(
                _block_text(entry[1]) for entry in answer_entries[1:4]
            ).strip()
        if value:
            answer = {
                "candidate_id": f"a-rule-{sequence:03d}",
                "question_no_hint": normalized,
                "question_no_normalized": normalized,
                "subquestion_no_hint": None,
                "standard_answer": value,
                "equivalent_answers": [],
                "tolerance": None,
                "confidence": _evidence_confidence(answer_entries, 0.98),
                "source_refs": [
                    _trusted_block_ref(document, entry[1]) for entry in answer_entries
                ],
                "issues": [],
            }

    solution = None
    if solution_index is not None:
        solution_entries = entries[solution_index:]
        contents = []
        for offset, entry in enumerate(solution_entries):
            text = _block_text(entry[1])
            marker = _SOLUTION_MARKER.match(text) if offset == 0 else None
            text = marker.group(1).strip() if marker else text
            if text:
                contents.append(text)
        if contents:
            solution = {
                "candidate_id": f"s-rule-{sequence:03d}",
                "question_no_hint": normalized,
                "question_no_normalized": normalized,
                "subquestion_no_hint": None,
                "raw_text": "\n".join(contents),
                "steps": [
                    {"step_no": index, "content": content}
                    for index, content in enumerate(contents, 1)
                ],
                "confidence": _evidence_confidence(solution_entries, 0.93),
                "source_refs": [
                    _trusted_block_ref(document, entry[1]) for entry in solution_entries
                ],
                "issues": [],
            }
    return question, answer, solution


def _stem_and_options(lines):
    stem_lines = []
    options = []
    for line in lines:
        matches = list(_OPTION.finditer(line))
        if not matches:
            stem_lines.append(line)
            continue
        prefix = line[: matches[0].start()].strip()
        if prefix:
            stem_lines.append(prefix)
        for index, match in enumerate(matches):
            end = matches[index + 1].start() if index + 1 < len(matches) else len(line)
            label = match.group(1).translate(str.maketrans("ＡＢＣＤ", "ABCD"))
            value = line[match.end() : end].strip()
            options.append(f"{label}. {value}".strip())
    return "\n".join(value for value in stem_lines if value).strip(), options


def _question_type(section, options):
    value = str(section or "")
    if "多选" in value:
        return "multiple_choice"
    if "单选" in value or options:
        return "single_choice"
    if "判断" in value:
        return "true_false"
    if "填空" in value:
        return "fill_blank"
    if "作文" in value:
        return "essay"
    if "计算" in value or "解答" in value:
        return "calculation"
    return None


def _trusted_block_ref(document, block):
    try:
        page_no = int(block.get("page_no", 0))
    except (TypeError, ValueError):
        page_no = 0
    return {
        "source_id": str(document.get("source_id", "")),
        "file_asset_id": str(document.get("file_asset_id", "")),
        "document_index": int(document.get("document_index", 0)),
        "page_no": page_no,
        "block_id": block.get("block_id"),
        "bbox": block.get("bbox"),
        "text_start": None,
        "text_end": None,
        "ocr_confidence": block.get("confidence"),
    }


def _block_text(block):
    return str(block.get("text", "")).strip()


def _evidence_confidence(entries, structural_cap):
    values = []
    for entry in entries:
        value = entry[1].get("confidence")
        if (
            isinstance(value, (int, float))
            and not isinstance(value, bool)
            and math.isfinite(value)
        ):
            values.append(max(0.0, min(1.0, float(value))))
    return min(float(structural_cap), min(values)) if values else float(structural_cap)


def _furniture_signature(text):
    value = unicodedata.normalize("NFKC", str(text or "")).lower()
    value = re.sub(r"\d+", "#", value)
    return re.sub(r"[\s,，、.．·:：_\-—]+", "", value)


def _without_page_furniture(blocks):
    """Exclude strong page furniture while retaining raw OCR in storage.

    Single-page imports need explicit footer/watermark markers; repeated text
    near the top or bottom of multiple pages is also treated as furniture.
    Position alone never removes content, so a last question near the page edge
    remains available to the parser.
    """

    valid = [block for block in blocks if isinstance(block, dict)]
    page_bounds = {}
    signatures = {}
    for block in valid:
        box = _box((0, block))
        if box is None:
            continue
        try:
            page_no = int(block.get("page_no", 0))
        except (TypeError, ValueError):
            page_no = 0
        top, bottom = box[1], box[1] + box[3]
        previous = page_bounds.get(page_no)
        page_bounds[page_no] = (
            min(previous[0], top) if previous else top,
            max(previous[1], bottom) if previous else bottom,
        )
        signature = _furniture_signature(_block_text(block))
        if len(signature) >= 6:
            signatures.setdefault(signature, set()).add(page_no)

    repeated = {
        signature for signature, pages in signatures.items() if len(pages) >= 2
    }
    result = []
    for block in valid:
        text = _block_text(block)
        box = _box((0, block))
        try:
            page_no = int(block.get("page_no", 0))
        except (TypeError, ValueError):
            page_no = 0
        bounds = page_bounds.get(page_no)
        if box is None or bounds is None or bounds[1] <= bounds[0]:
            result.append(block)
            continue
        page_height = bounds[1] - bounds[0]
        center = box[1] + box[3] / 2
        near_top = center <= bounds[0] + page_height * 0.08
        near_bottom = center >= bounds[0] + page_height * 0.90
        protected_anchor = any(
            pattern.match(text)
            for pattern in (_QUESTION_START, _ANSWER_MARKER, _SOLUTION_MARKER, _OPTION)
        )
        strong_footer = not protected_anchor and near_bottom and (
            _PAGE_FOOTER.fullmatch(text.strip()) is not None
            or (len(text) <= 80 and _DISTRIBUTION_MARKER.search(text) is not None)
        )
        repeated_edge = not protected_anchor and (near_top or near_bottom) and (
            _furniture_signature(text) in repeated
            and _REPEATED_FURNITURE_MARKER.search(text) is not None
        )
        if not strong_footer and not repeated_edge:
            result.append(block)
    return result


def page_furniture_issues(documents):
    """Keep excluded blocks discoverable by their original source provenance."""

    issues = []
    for document in documents:
        blocks = document.get("blocks")
        if not isinstance(blocks, list):
            continue
        retained = {id(block) for block in _without_page_furniture(blocks)}
        for block in blocks:
            if not isinstance(block, dict) or id(block) in retained:
                continue
            issues.append(
                {
                    "code": "PAGE_FURNITURE_EXCLUDED",
                    "severity": "info",
                    "certainty": "suspected",
                    "question_no": None,
                    "section": None,
                    "message": f"已排除页眉、页脚或水印：{_block_text(block)[:80]}",
                    "confidence": None,
                    "source_refs": [_trusted_block_ref(document, block)],
                    "resolution_hint": "原图及 OCR 块仍保留；若误排除，请对照来源人工补充",
                }
            )
    return issues


def build_compact_chunks(documents):
    chunks = []
    for document in documents:
        blocks = document.get("blocks")
        if isinstance(blocks, list) and blocks:
            chunks.extend(_ocr_chunks(document, blocks))
        else:
            chunks.extend(_direct_text_chunks(document))
    return chunks


def _ocr_chunks(document, blocks):
    by_page = {}
    retained = {id(block) for block in _without_page_furniture(blocks)}
    for sequence, block in enumerate(blocks):
        if not isinstance(block, dict) or not str(block.get("text", "")).strip():
            continue
        try:
            page_no = int(block.get("page_no", 0))
        except (TypeError, ValueError):
            page_no = 0
        by_page.setdefault(page_no, []).append((sequence, block))

    chunks = []
    for page_no in sorted(by_page):
        ordered = [
            entry
            for entry in _reading_order(by_page[page_no])
            if id(entry[1]) in retained
        ]
        for block_group in _split_block_groups(ordered):
            references = {}
            compact_blocks = []
            for ref_number, (_, block) in enumerate(block_group, 1):
                ref_id = f"r{ref_number:03d}"
                compact_blocks.append([ref_id, str(block.get("text", "")).strip()])
                references[ref_id] = {
                    "source_id": str(document.get("source_id", "")),
                    "file_asset_id": str(document.get("file_asset_id", "")),
                    "document_index": int(document.get("document_index", 0)),
                    "page_no": page_no,
                    "block_id": block.get("block_id"),
                    "bbox": block.get("bbox"),
                    "text_start": None,
                    "text_end": None,
                    "ocr_confidence": block.get("confidence"),
                }
            chunks.append(
                CompactChunk(
                    model_document={
                        "source_id": str(document.get("source_id", "")),
                        "document_index": int(document.get("document_index", 0)),
                        "role_hint": str(document.get("role_hint", "auto")),
                        "page_no": page_no,
                        "ordered_blocks": compact_blocks,
                    },
                    source_document=document,
                    references=references,
                )
            )
    return chunks


def _direct_text_chunks(document):
    content = str(document.get("content", "")).strip()
    if not content:
        return []
    chunks = []
    start = 0
    while start < len(content):
        end = min(len(content), start + MAX_DIRECT_TEXT_CHARS_PER_CHUNK)
        if end < len(content):
            boundary = max(
                content.rfind("\n", start, end), content.rfind("。", start, end)
            )
            if boundary > start + MAX_DIRECT_TEXT_CHARS_PER_CHUNK // 2:
                end = boundary + 1
        ref = {
            "source_id": str(document.get("source_id", "")),
            "file_asset_id": str(document.get("file_asset_id", "")),
            "document_index": int(document.get("document_index", 0)),
            "page_no": None,
            "block_id": None,
            "bbox": None,
            "text_start": start,
            "text_end": end,
            "ocr_confidence": None,
        }
        chunks.append(
            CompactChunk(
                model_document={
                    "source_id": str(document.get("source_id", "")),
                    "document_index": int(document.get("document_index", 0)),
                    "role_hint": str(document.get("role_hint", "auto")),
                    "page_no": None,
                    "ordered_blocks": [["r001", content[start:end]]],
                },
                source_document=document,
                references={"r001": ref},
            )
        )
        start = end
    return chunks


def _box(item):
    value = item[1].get("bbox")
    if not isinstance(value, (list, tuple)) or len(value) != 4:
        return None
    try:
        x, y, width, height = (float(part) for part in value)
    except (TypeError, ValueError):
        return None
    if not all(math.isfinite(part) for part in (x, y, width, height)):
        return None
    if width <= 0 or height <= 0:
        return None
    return x, y, width, height


def _reading_order(items, depth=0):
    """Bounded recursive XY cut with a stable geometric fallback."""

    if len(items) < 8 or depth >= 3:
        return _sort_lines(items)
    partition = _column_partition(items)
    if partition is None:
        return _sort_lines(items)
    left, right, spanning = partition
    if not spanning:
        return _reading_order(left, depth + 1) + _reading_order(right, depth + 1)

    ordered = []
    remaining_left = list(left)
    remaining_right = list(right)
    for separator in sorted(spanning, key=_geometric_key):
        separator_box = _box(separator)
        separator_y = separator_box[1] if separator_box else float("inf")
        before_left = [item for item in remaining_left if _center_y(item) < separator_y]
        before_right = [
            item for item in remaining_right if _center_y(item) < separator_y
        ]
        ordered.extend(_reading_order(before_left, depth + 1))
        ordered.extend(_reading_order(before_right, depth + 1))
        remaining_left = [item for item in remaining_left if item not in before_left]
        remaining_right = [item for item in remaining_right if item not in before_right]
        ordered.append(separator)
    ordered.extend(_reading_order(remaining_left, depth + 1))
    ordered.extend(_reading_order(remaining_right, depth + 1))
    return ordered


def _sort_lines(items):
    lines = []
    unboxed = []
    for item in sorted(items, key=_geometric_key):
        box = _box(item)
        if box is None:
            unboxed.append(item)
            continue
        center = box[1] + box[3] / 2
        best = None
        best_distance = float("inf")
        for line in lines:
            distance = abs(center - line["center"])
            threshold = max(8.0, min(box[3], line["median_height"]) * 0.75)
            if distance <= threshold and distance < best_distance:
                best = line
                best_distance = distance
        if best is None:
            lines.append(
                {
                    "center": center,
                    "median_height": box[3],
                    "items": [(item, box)],
                }
            )
            continue
        best["items"].append((item, box))
        centers = [value[1][1] + value[1][3] / 2 for value in best["items"]]
        heights = sorted(value[1][3] for value in best["items"])
        best["center"] = sum(centers) / len(centers)
        best["median_height"] = heights[len(heights) // 2]
    ordered = []
    for line in sorted(lines, key=lambda value: value["center"]):
        ordered.extend(
            item for item, _ in sorted(line["items"], key=lambda value: value[1][0])
        )
    ordered.extend(unboxed)
    return ordered


def _column_partition(items):
    boxed = [(item, _box(item)) for item in items]
    boxed = [(item, box) for item, box in boxed if box is not None]
    if len(boxed) < 8:
        return None
    page_left = min(box[0] for _, box in boxed)
    page_right = max(box[0] + box[2] for _, box in boxed)
    page_width = page_right - page_left
    if page_width <= 0:
        return None
    candidates = []
    for step in range(15, 86):
        split = page_left + page_width * step / 100
        crossing = sum(1 for _, box in boxed if box[0] < split < box[0] + box[2])
        left_boxes = [box for _, box in boxed if box[0] + box[2] <= split]
        right_boxes = [box for _, box in boxed if box[0] >= split]
        if len(left_boxes) < 4 or len(right_boxes) < 4:
            continue
        left_x = sorted(box[0] for box in left_boxes)[len(left_boxes) // 2]
        right_x = sorted(box[0] for box in right_boxes)[len(right_boxes) // 2]
        if right_x - left_x < page_width * 0.25:
            continue
        balance = min(len(left_boxes), len(right_boxes)) / max(
            len(left_boxes), len(right_boxes)
        )
        # Prefer a clear whitespace gutter, then a well-supported split. Sampling
        # every one percent also handles three-column pages through recursion.
        candidates.append((crossing, -balance, abs(step - 50), split))
    for crossing, _, _, split in sorted(candidates):
        if crossing > max(3, int(len(boxed) * 0.05)):
            continue
        left = []
        right = []
        spanning = []
        for item in items:
            box = _box(item)
            if box is None:
                spanning.append(item)
                continue
            crosses = box[0] < split < box[0] + box[2]
            center = box[0] + box[2] / 2
            if crosses:
                spanning.append(item)
            elif center <= split:
                left.append(item)
            else:
                right.append(item)
        if len(left) < 4 or len(right) < 4:
            continue
        if _vertical_overlap(left, right) < 0.25:
            continue
        return left, right, spanning
    return None


def _vertical_overlap(left, right):
    left_boxes = [_box(item) for item in left]
    right_boxes = [_box(item) for item in right]
    left_boxes = [box for box in left_boxes if box]
    right_boxes = [box for box in right_boxes if box]
    if not left_boxes or not right_boxes:
        return 0
    left_min = min(box[1] for box in left_boxes)
    left_max = max(box[1] + box[3] for box in left_boxes)
    right_min = min(box[1] for box in right_boxes)
    right_max = max(box[1] + box[3] for box in right_boxes)
    overlap = max(0.0, min(left_max, right_max) - max(left_min, right_min))
    smaller = min(left_max - left_min, right_max - right_min)
    return overlap / smaller if smaller > 0 else 0


def _center_y(item):
    box = _box(item)
    return box[1] + box[3] / 2 if box else float(item[0])


def _geometric_key(item):
    box = _box(item)
    # Formula crops are deliberately padded and can start above their host text
    # line. Their vertical centre is a better baseline proxy than the top edge.
    return (box[1] + box[3] / 2, box[0], item[0]) if box else (float("inf"), 0, item[0])


def _split_block_groups(blocks):
    logical_groups = []
    current = []
    has_question = False
    for item in blocks:
        text = str(item[1].get("text", "")).strip()
        starts_question = bool(_QUESTION_START.match(text))
        starts_section = bool(_SECTION_START.match(text))
        if current and ((starts_question and has_question) or starts_section):
            logical_groups.append(current)
            current = []
            has_question = False
        current.append(item)
        has_question = has_question or starts_question
    if current:
        logical_groups.append(current)

    chunks = []
    current = []
    current_chars = 0
    for group in logical_groups:
        group_chars = sum(len(str(item[1].get("text", ""))) for item in group)
        if current and (
            len(current) + len(group) > MAX_BLOCKS_PER_CHUNK
            or current_chars + group_chars > MAX_TEXT_CHARS_PER_CHUNK
        ):
            chunks.append(current)
            current = []
            current_chars = 0
        while (
            len(group) > MAX_BLOCKS_PER_CHUNK or group_chars > MAX_TEXT_CHARS_PER_CHUNK
        ):
            take = []
            take_chars = 0
            for item in group:
                size = len(str(item[1].get("text", "")))
                if take and (
                    len(take) >= MAX_BLOCKS_PER_CHUNK
                    or take_chars + size > MAX_TEXT_CHARS_PER_CHUNK
                ):
                    break
                take.append(item)
                take_chars += size
            chunks.append(take)
            group = group[len(take) :]
            group_chars = sum(len(str(item[1].get("text", ""))) for item in group)
        current.extend(group)
        current_chars += group_chars
    if current:
        chunks.append(current)
    return chunks


def expand_compact_output(output, chunk, chunk_index):
    if not isinstance(output, dict):
        raise AgentError(
            "model_output_invalid", "paper parser output was invalid", status=502
        )
    result = {
        "documents": output.get("documents", []),
        "question_candidates": [],
        "answer_candidates": [],
        "solution_candidates": [],
        "rubric_candidates": [],
        "issues": [],
    }
    definitions = (
        ("question_candidates", "q"),
        ("answer_candidates", "a"),
        ("solution_candidates", "s"),
        ("rubric_candidates", "r"),
    )
    for collection, prefix in definitions:
        values = output.get(collection)
        if not isinstance(values, list):
            raise AgentError(
                "model_output_invalid", "paper parser output was invalid", status=502
            )
        for item_index, item in enumerate(values, 1):
            if not isinstance(item, dict):
                raise AgentError(
                    "model_output_invalid",
                    "paper parser output was invalid",
                    status=502,
                )
            expanded = dict(item)
            expanded["candidate_id"] = f"{prefix}-{chunk_index:03d}-{item_index:03d}"
            expanded["source_refs"] = _hydrate_refs(item.get("source_refs"), chunk)
            if collection == "solution_candidates":
                steps = item.get("steps", [])
                expanded["steps"] = [
                    {"step_no": index, "content": str(content)}
                    for index, content in enumerate(steps, 1)
                ]
            elif collection == "rubric_candidates":
                points = []
                for point_index, point in enumerate(item.get("points", []), 1):
                    if not isinstance(point, dict):
                        raise AgentError(
                            "model_output_invalid",
                            "rubric point was invalid",
                            status=502,
                        )
                    points.append(
                        {
                            "id": f"rp-{chunk_index:03d}-{item_index:03d}-{point_index:03d}",
                            "description": point.get("description", ""),
                            "score": point.get("score"),
                            "required": point.get("required"),
                            "evidence_requirements": [],
                        }
                    )
                expanded["points"] = points
            result[collection].append(expanded)

    issues = output.get("issues")
    if not isinstance(issues, list):
        raise AgentError(
            "model_output_invalid", "paper parser output was invalid", status=502
        )
    for issue in issues:
        if not isinstance(issue, dict):
            raise AgentError(
                "model_output_invalid", "paper parser issue was invalid", status=502
            )
        expanded = dict(issue)
        aliases = issue.get("source_refs", [])
        expanded["source_refs"] = _hydrate_refs(aliases, chunk) if aliases else []
        result["issues"].append(expanded)
    return result


def _hydrate_refs(aliases, chunk):
    if not isinstance(aliases, list) or not aliases:
        raise AgentError(
            "model_output_invalid", "candidate provenance was not grounded", status=502
        )
    refs = []
    seen = set()
    for alias in aliases:
        if not isinstance(alias, str) or alias in seen or alias not in chunk.references:
            raise AgentError(
                "model_output_invalid",
                "candidate provenance was not grounded",
                status=502,
            )
        seen.add(alias)
        refs.append(dict(chunk.references[alias]))
    return refs
