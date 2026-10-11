"""Deterministic anchored-paper extraction and furniture filtering."""

import math
import re
import unicodedata
from dataclasses import dataclass, field

from .layout import _box, _reading_order
from .schema import (
    MAX_PAGE_FOOTER_TEXT_CHARS,
    _ANSWER_MARKER,
    _DISTRIBUTION_MARKER,
    _OPTION,
    _PAGE_FOOTER,
    _QUESTION_NUMBER,
    _QUESTION_START,
    _REPEATED_FURNITURE_MARKER,
    _SECTION_START,
    _SOLUTION_MARKER,
)

_NUMBER = r"\d+(?:\.\d+)?"
# 考试分值必须关联题目标题或分节说明。
# 题干中单独出现的“4分”可能是游戏得分，不是该题的分值。
_QUESTION_MARK = re.compile(
    rf"(?:本(?:小)?题|此题)\s*(?:满分|共|计|为)?\s*[:：]?\s*({_NUMBER})\s*分"
)
_LEADING_MARK = re.compile(rf"^\s*[（(]\s*({_NUMBER})\s*分\s*[）)]")
_SECTION_MARK = re.compile(rf"每\s*(?:小)?题\s*(?:共|计|为)?\s*({_NUMBER})\s*分")
_RUBRIC_HEADING = re.compile(r"^\s*[【\[]?(?:评分标准|评分细则|评分参考|给分点|采分点)[】\]]?\s*[:：]?")
_MARK_CLAUSE = re.compile(rf"^\s*(.+?)\s*(?:可)?得\s*({_NUMBER})\s*分\s*$")
_MARK_ACTION = re.compile(r"正确|完整|列出|写出|答出|求得|解得|算出|得到|证明|指出|画出|化简|步骤|过程")


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
    rubrics = []
    for document in documents:
        parsed = _anchored_document(document, len(questions))
        if parsed is None:
            return None
        document_questions, document_answers, document_solutions, document_rubrics = parsed
        questions.extend(document_questions)
        answers.extend(document_answers)
        solutions.extend(document_solutions)
        rubrics.extend(document_rubrics)
        role = "mixed" if document_answers or document_solutions or document_rubrics else "question"
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
        "rubric_candidates": rubrics,
        "issues": [],
    }


def _anchored_document(document, question_offset):
    blocks = document.get("blocks")
    direct_text = not isinstance(blocks, list) or not blocks
    if direct_text:
        blocks = _direct_text_blocks(document.get("content"))
    if not blocks:
        return None
    segments, _ = _anchored_segments(blocks)
    if len(segments) < (1 if direct_text else 2):
        return None
    answer_count = sum(
        any(_ANSWER_MARKER.match(_block_text(entry[1])) for entry in segment[3])
        for segment in segments
    )
    if answer_count * 2 < len(segments):
        return None
    return _anchored_document_candidates(document, segments, question_offset)


def _anchored_document_candidates(document, segments, question_offset):
    questions, answers, solutions, rubrics = [], [], [], []
    for question_no, section, section_entry, entries in segments:
        question, answer, solution = _anchored_candidate(
            document, question_no, section, entries, question_offset + len(questions) + 1, section_entry,
        )
        questions.append(question)
        if answer:
            answers.append(answer)
        if solution:
            solutions.append(solution)
        rubric = _explicit_rubric(document, question_no, entries, question)
        if rubric:
            rubrics.append(rubric)
        elif any(_RUBRIC_HEADING.match(_block_text(entry[1])) for entry in entries):
            # 任一明确评分段无法完整解析时整份资料回到模型路径，不能返回部分采分点。
            return None
    return questions, answers, solutions, rubrics


def _ordered_anchor_entries(blocks):
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

    return ordered


def _anchored_segments(blocks):
    builder = _AnchorSegmentBuilder()
    for entry in _ordered_anchor_entries(blocks):
        builder.add(entry)
    return builder.finish()


@dataclass
class _AnchorSegmentBuilder:
    segments: list = field(default_factory=list)
    current: list = field(default_factory=list)
    current_number: int = 0
    current_section: str | None = None
    current_section_entry: tuple | None = None
    segment_section: str | None = None
    segment_section_entry: tuple | None = None
    in_solution: bool = False
    section_seen: bool = False

    def add(self, entry):
        text = _block_text(entry[1])
        if _SECTION_START.match(text):
            self._start_section(entry, text)
            return
        if not self.current and _SECTION_MARK.search(text):
            # 独立的每题分值行属于章节来源；保留这行引用，不能只引用没有分值的标题。
            self.current_section = f"{self.current_section}\n{text}" if self.current_section else text
            self.current_section_entry = entry
            return
        match = _QUESTION_NUMBER.match(text)
        if match:
            number = int(match.group(1))
            # 编号的解题步骤不是新题目。真正的下一题
            # 会推进试卷中单调递增的题号序列。
            if not self.current or (
                number > self.current_number
                and not (self.in_solution and number <= self.current_number)
            ):
                self._start_question(entry, number)
                return
        if self.current:
            self.current.append(entry)
            self.in_solution = self.in_solution or bool(_SOLUTION_MARKER.match(text))

    def _append_current(self):
        if self.current:
            self.segments.append((self.current_number, self.segment_section, self.segment_section_entry, self.current))

    def _start_section(self, entry, text):
        if self.current:
            self._append_current()
            self.current = []
            self.current_number = 0
            self.in_solution = False
        self.current_section = text
        self.current_section_entry = entry
        self.section_seen = True

    def _start_question(self, entry, number):
        self._append_current()
        self.current = [entry]
        self.current_number = number
        self.segment_section = self.current_section
        self.segment_section_entry = self.current_section_entry
        self.in_solution = False

    def finish(self):
        self._append_current()
        numbers = [segment[0] for segment in self.segments]
        if len(numbers) != len(set(numbers)) or numbers != sorted(numbers):
            return [], self.section_seen
        return self.segments, self.section_seen


def _anchored_candidate(document, question_no, section, entries, sequence, section_entry=None):
    source_identity = re.sub(
        r"[^A-Za-z0-9]", "", str(document.get("source_id", ""))
    )[:12] or str(document.get("document_index", 0))
    suffix = f"{source_identity}-{sequence:03d}"
    answer_index = _first_marker_index(entries, _ANSWER_MARKER)
    solution_index = _first_marker_index(entries, _SOLUTION_MARKER)
    rubric_index = _first_marker_index(entries, _RUBRIC_HEADING)
    boundaries = [
        value for value in (answer_index, solution_index, rubric_index) if value is not None
    ]
    question_end = min(boundaries) if boundaries else len(entries)
    question_entries = entries[:question_end]
    normalized = str(question_no)
    question = _anchored_question(document, normalized, section, question_entries, suffix, section_entry, answer_index)
    answer = _anchored_answer(document, normalized, entries, suffix, answer_index, (solution_index, rubric_index))
    solution = _anchored_solution(document, normalized, entries, suffix, solution_index, (answer_index, rubric_index))
    return question, answer, solution


def _first_marker_index(entries, pattern):
    return next((index for index, entry in enumerate(entries) if pattern.match(_block_text(entry[1]))), None)


def _anchored_question(document, normalized, section, question_entries, suffix, section_entry, answer_index):
    question_refs = [
        _trusted_block_ref(document, entry[1]) for entry in question_entries
    ]
    raw_lines = [_block_text(entry[1]) for entry in question_entries]
    if raw_lines:
        match = _QUESTION_NUMBER.match(raw_lines[0])
        if match:
            raw_lines[0] = match.group(2).strip()
    stem, options = _stem_and_options(raw_lines)
    score, section_sourced = _explicit_question_score(raw_lines, section)
    if section_sourced and section_entry is not None:
        question_refs.append(_trusted_block_ref(document, section_entry[1]))
    question_type = _question_type(section, options)
    return {
        "candidate_id": f"q-rule-{suffix}",
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



def _anchored_answer(document, normalized, entries, suffix, answer_index, boundaries):
    if answer_index is None:
        return None
    marker = _ANSWER_MARKER.match(_block_text(entries[answer_index][1]))
    value = marker.group(1).strip() if marker else ""
    answer_entries = _entries_until_next_marker(entries, answer_index, boundaries)
    if not value:
        value = " ".join(_block_text(entry[1]) for entry in answer_entries[1:4]).strip()
    if not value:
        return None
    return {
        "candidate_id": f"a-rule-{suffix}",
        "question_no_hint": normalized,
        "question_no_normalized": normalized,
        "subquestion_no_hint": None,
        "standard_answer": value,
        "equivalent_answers": [],
        "tolerance": None,
        "confidence": _evidence_confidence(answer_entries, 0.98),
        "source_refs": [_trusted_block_ref(document, entry[1]) for entry in answer_entries],
        "issues": [],
    }


def _entries_until_next_marker(entries, start, boundaries):
    # 答案、解析和评分段各自止于后面的第一个标记，不能吞入另一个段落的内容。
    end = min((index for index in boundaries if index is not None and index > start), default=len(entries))
    return entries[start:end]


def _anchored_solution(document, normalized, entries, suffix, solution_index, boundaries):
    if solution_index is None:
        return None
    solution_entries = _entries_until_next_marker(entries, solution_index, boundaries)
    contents = []
    for offset, entry in enumerate(solution_entries):
        text = _block_text(entry[1])
        marker = _SOLUTION_MARKER.match(text) if offset == 0 else None
        text = marker.group(1).strip() if marker else text
        if text:
            contents.append(text)
    if not contents:
        return None
    return {
        "candidate_id": f"s-rule-{suffix}",
        "question_no_hint": normalized,
        "question_no_normalized": normalized,
        "subquestion_no_hint": None,
        "raw_text": "\n".join(contents),
        "steps": [{"step_no": index, "content": content} for index, content in enumerate(contents, 1)],
        "confidence": _evidence_confidence(solution_entries, 0.93),
        "source_refs": [_trusted_block_ref(document, entry[1]) for entry in solution_entries],
        "issues": [],
    }


def _explicit_question_score(lines, section):
    """Only accept marks explicitly attached to the question or section."""

    if lines:
        header = next((line for line in lines[:2] if line.strip()), "")
        match = _QUESTION_MARK.search(header) or _LEADING_MARK.match(header)
        if match:
            return float(match.group(1)), False
    match = _SECTION_MARK.search(str(section or ""))
    return (float(match.group(1)), True) if match else (None, False)


def _explicit_rubric(document, question_no, entries, question):
    """Preserve explicit marking clauses on the deterministic answer path.

    This does not infer marks from solution steps. When a marking heading is
    present but its prose cannot be parsed safely, the caller must route the
    document to the model instead of silently returning zero rubrics.
    """

    points = []
    refs = []
    unparsed_heading_content = False
    answer_or_solution_seen = False
    in_rubric = False
    for _, block in entries:
        line, heading, in_rubric, answer_or_solution_seen = _rubric_line_state(
            _block_text(block), in_rubric, answer_or_solution_seen,
        )
        clauses, unparsed = _explicit_mark_clauses(line, heading, answer_or_solution_seen)
        unparsed_heading_content = unparsed_heading_content or (in_rubric and unparsed)
        for description, score in clauses:
            points.append(
                {
                    "id": f"rp-rule-{question['candidate_id']}-{len(points) + 1:02d}",
                    "description": description,
                    "score": score,
                    "required": None,
                    "evidence_requirements": [],
                }
            )
            ref = _trusted_block_ref(document, block)
            if ref not in refs:
                refs.append(ref)
    if unparsed_heading_content:
        return None
    if not points:
        return None
    normalized = str(question_no)
    return {
        "candidate_id": f"r-rule-{question['candidate_id']}",
        "question_no_hint": normalized,
        "question_no_normalized": normalized,
        "max_score": question["score"],
        "points": points,
        "deductions": [],
        "examples": [],
        "confidence": _evidence_confidence([(0, block) for _, block in entries if _trusted_block_ref(document, block) in refs], 0.91),
        "source_refs": refs,
        "issues": [] if question["score"] is not None else ["题目满分未明确标注，评分点分值合计需核对"],
    }


def _rubric_line_state(line, in_rubric, answer_or_solution_seen):
    heading = _RUBRIC_HEADING.match(line)
    if heading:
        return line[heading.end():], heading, True, answer_or_solution_seen
    if _ANSWER_MARKER.match(line) or _SOLUTION_MARKER.match(line):
        answer_or_solution_seen = True
        in_rubric = False
    marker = _SOLUTION_MARKER.match(line)
    if marker:
        line = marker.group(1)
    return line, heading, in_rubric, answer_or_solution_seen


def _explicit_mark_clauses(line, heading, answer_or_solution_seen):
    points = []
    unparsed = False
    for clause in re.split(r"[；;，,。]\s*", line):
        if not clause.strip():
            continue
        parsed = _explicit_mark_clause(clause, heading, answer_or_solution_seen)
        if parsed is None:
            unparsed = True
        else:
            points.append(parsed)
    return points, unparsed


def _explicit_mark_clause(clause, heading, answer_or_solution_seen):
    match = _MARK_CLAUSE.match(clause)
    # 非评分标题下的分值必须出现在答案或解析之后，并带明确采分动作，避免把题干比赛得分当标准。
    if not match or (not heading and (not answer_or_solution_seen or not _MARK_ACTION.search(match.group(1)))):
        return None
    description = match.group(1).strip()
    if not description:
        return None
    return description, float(match.group(2))


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
    # Markdown 强调标记通常包住完整的题目或答案行。
    # 只移除分隔符，不解释内容，可以原样保留
    # LaTeX 命令（如 \frac）和所有数学标点。
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
    page_bounds, signatures = _furniture_page_context(valid)
    repeated = {signature for signature, pages in signatures.items() if len(pages) >= 2}
    return [block for block in valid if not _is_page_furniture(block, page_bounds, repeated)]


def _furniture_page_context(blocks):
    page_bounds = {}
    signatures = {}
    for block in blocks:
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

    return page_bounds, signatures


def _is_page_furniture(block, page_bounds, repeated):
    text = _block_text(block)
    box = _box((0, block))
    try:
        page_no = int(block.get("page_no", 0))
    except (TypeError, ValueError):
        page_no = 0
    bounds = page_bounds.get(page_no)
    if box is None or bounds is None or bounds[1] <= bounds[0]:
        return False
    page_height = bounds[1] - bounds[0]
    center = box[1] + box[3] / 2
    near_top = center <= bounds[0] + page_height * 0.08
    near_bottom = center >= bounds[0] + page_height * 0.90
    protected_anchor = any(
        pattern.match(text)
        for pattern in (_QUESTION_START, _ANSWER_MARKER, _SOLUTION_MARKER, _OPTION)
    )
    # 长度预算必须先于正则调用；后续分块上限不能保护此处的原始 OCR 文本。
    strong_footer = not protected_anchor and near_bottom and len(text) <= MAX_PAGE_FOOTER_TEXT_CHARS and (
        _PAGE_FOOTER.fullmatch(text) is not None
        or _DISTRIBUTION_MARKER.search(text) is not None
    )
    repeated_edge = not protected_anchor and (near_top or near_bottom) and (
        _furniture_signature(text) in repeated
        and _REPEATED_FURNITURE_MARKER.search(text) is not None
    )
    return strong_footer or repeated_edge


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
