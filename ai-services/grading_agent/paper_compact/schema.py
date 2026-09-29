"""Compact, provenance-safe model protocol for OCR paper imports.

The durable API payload intentionally retains complete OCR provenance. Sending
that representation to a language model is both wasteful and fragile: UUIDs,
coordinates and confidence values dominate the context, and the model is then
asked to copy those trusted values back. This module replaces them with bounded
request-local references and restores the authoritative values after inference.
"""

from __future__ import annotations

import re

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
