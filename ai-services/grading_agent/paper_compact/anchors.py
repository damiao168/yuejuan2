"""Deterministic anchored-paper extraction and furniture filtering."""

import math
import re
import unicodedata

from .layout import _box, _reading_order
from .schema import (
    _ANSWER_MARKER,
    _DISTRIBUTION_MARKER,
    _OPTION,
    _PAGE_FOOTER,
    _QUESTION_NUMBER,
    _QUESTION_START,
    _REPEATED_FURNITURE_MARKER,
    _SCORE,
    _SECTION_START,
    _SOLUTION_MARKER,
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
        direct_text = not isinstance(blocks, list) or not blocks
        if not isinstance(blocks, list) or not blocks:
            blocks = _direct_text_blocks(document.get("content"))
        if not blocks:
            return None
        segments, _ = _anchored_segments(document, blocks)
        if len(segments) < (1 if direct_text else 2):
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
            if current:
                segments.append((current_number, segment_section, current))
                current = []
                current_number = 0
                in_solution = False
            current_section = text
            section_seen = True
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
    source_identity = re.sub(
        r"[^A-Za-z0-9]", "", str(document.get("source_id", ""))
    )[:12] or str(document.get("document_index", 0))
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
        "candidate_id": f"q-rule-{source_identity}-{sequence:03d}",
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
                "candidate_id": f"a-rule-{source_identity}-{sequence:03d}",
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
                "candidate_id": f"s-rule-{source_identity}-{sequence:03d}",
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
    if block.get("_direct_text"):
        return {
            "source_id": str(document.get("source_id", "")),
            "file_asset_id": str(document.get("file_asset_id", "")),
            "document_index": int(document.get("document_index", 0)),
            "page_no": None,
            "block_id": None,
            "bbox": None,
            "text_start": block.get("text_start"),
            "text_end": block.get("text_end"),
            "ocr_confidence": None,
        }
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


def _direct_text_blocks(content):
    """Expose Markdown/plain-text lines to the conservative anchor parser.

    Only presentation markers that can surround a whole anchor are removed.
    The source text itself remains unchanged and every synthetic block keeps
    exact character offsets, so formulas and provenance stay authoritative.
    """

    if not isinstance(content, str) or not content.strip():
        return []
    blocks = []
    offset = 0
    for line in content.splitlines(keepends=True):
        raw = line.rstrip("\r\n")
        start = offset
        offset += len(line)
        text = _markdown_anchor_text(raw)
        if not text:
            continue
        blocks.append(
            {
                "text": text,
                "page_no": 0,
                "block_id": None,
                "bbox": None,
                "confidence": None,
                "text_start": start,
                "text_end": start + len(raw),
                "_direct_text": True,
            }
        )
    return blocks


def _markdown_anchor_text(value):
    text = str(value or "").strip()
    text = re.sub(r"^(?:#{1,6}\s+|>\s*)", "", text).strip()
    # Markdown emphasis commonly wraps the complete question/answer line.
    # Removing the delimiter, rather than interpreting the content, preserves
    # LaTeX commands such as \frac and all mathematical punctuation verbatim.
    text = text.replace("**", "").replace("__", "").strip()
    return text


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
