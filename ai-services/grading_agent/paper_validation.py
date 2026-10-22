"""Validate model output against authoritative input provenance."""

import math
import re

from .errors import AgentError
from .paper_schema import FORMULA_SUBJECTS, QUESTION_TYPES, ROLES

_MAX_QUESTION_NUMBER_CHARS = 64
_MAX_QUESTION_NUMBER_COMPONENT = (1 << 63) - 1
_QUESTION_NUMBER_FIELDS = (
    "question_no_raw", "question_no_normalized", "question_no_hint",
    "parent_question_no", "subquestion_no", "subquestion_no_hint",
)

# 模型仍可能把应用题中的得分误认为考试分值。
# 这个保守的直接文本检查仅在所有引用文档都没有
# 明确的题目或分节分值标记时，才清除建议分值。
_EXPLICIT_EXAM_MARK = re.compile(
    r"(?:本(?:小)?题|第\s*\d+\s*题)\s*(?:满分|共|计|为)?\s*[:：]?\s*\d+(?:\.\d+)?\s*分"
    r"|每\s*(?:小)?题\s*(?:共|计|为)?\s*\d+(?:\.\d+)?\s*分"
    r"|[（(]\s*\d+(?:\.\d+)?\s*分\s*[）)]"
)


def normalize_direct_text_refs(output, documents):
    """Discard OCR-only metadata when the trusted source has no OCR blocks."""
    if not isinstance(output, dict):
        return
    direct_source_ids = {
        str(document.get("source_id", ""))
        for document in documents
        if not isinstance(document.get("blocks"), list) or not document["blocks"]
    }
    collections = (
        "question_candidates",
        "answer_candidates",
        "solution_candidates",
        "rubric_candidates",
        "issues",
    )
    for collection in collections:
        for item in output.get(collection, []):
            _normalize_direct_candidate_refs(item, direct_source_ids)


def _normalize_direct_candidate_refs(item, direct_source_ids):
    if not isinstance(item, dict):
        return
    for ref in item.get("source_refs", []):
        if not isinstance(ref, dict) or ref.get("source_id") not in direct_source_ids:
            continue
        ref["page_no"] = None
        ref["block_id"] = None
        ref["bbox"] = None
        ref["ocr_confidence"] = None

@staticmethod
def validate_paper_output(output, request_id, subject, documents):
    _validate_paper_structure(output, request_id)
    documents_by_id = {str(document["source_id"]): document for document in documents}
    _validate_document_classifications(output["documents"], documents_by_id, request_id)
    # 先校验全部候选及来源，再校验评分要求和题型，保留既有首个失败的顺序。
    _validate_paper_candidates(output, documents_by_id, request_id)
    for rubric in output["rubric_candidates"]:
        _validate_paper_rubric(rubric, request_id)
    for issue in output["issues"]:
        refs = issue.get("source_refs") if isinstance(issue, dict) else None
        if refs:
            validate_refs(refs, documents_by_id, request_id)
    for question in output["question_candidates"]:
        _validate_paper_question(question, subject, documents_by_id, request_id)
    return output


def _validate_paper_structure(output, request_id):
    if isinstance(output, dict) and "rubric_candidates" not in output:
        output["rubric_candidates"] = []
    keys = (
        "documents",
        "question_candidates",
        "answer_candidates",
        "solution_candidates",
        "rubric_candidates",
        "issues",
    )
    if not isinstance(output, dict) or any(
        not isinstance(output.get(key), list) for key in keys
    ):
        raise AgentError(
            "model_output_invalid",
            "paper parser output was invalid",
            status=502,
            request_id=request_id,
        )



def _validate_document_classifications(classifications, documents_by_id, request_id):
    source_ids = set(documents_by_id)
    classified = [
        item.get("source_id")
        for item in classifications
        if isinstance(item, dict)
    ]
    if len(classified) != len(source_ids) or set(classified) != source_ids:
        raise AgentError(
            "model_output_invalid",
            "every source must be classified exactly once",
            status=502,
            request_id=request_id,
        )
    for item in classifications:
        role_confidence = item.get("role_confidence")
        if (
            item.get("source_id") not in source_ids
            or item.get("detected_role") not in ROLES
            or isinstance(role_confidence, bool)
            or not isinstance(role_confidence, (int, float))
            or not math.isfinite(role_confidence)
            or not 0 <= role_confidence <= 1
        ):
            raise AgentError(
                "model_output_invalid",
                "document classification was not grounded",
                status=502,
                request_id=request_id,
            )



def _validate_paper_candidates(output, documents_by_id, request_id):
    candidate_ids = []
    for collection in (
        "question_candidates",
        "answer_candidates",
        "solution_candidates",
        "rubric_candidates",
    ):
        for candidate in output[collection]:
            _validate_paper_candidate(candidate, candidate_ids, documents_by_id, request_id)


def _validate_paper_candidate(candidate, candidate_ids, documents_by_id, request_id):
    refs = candidate.get("source_refs") if isinstance(candidate, dict) else None
    candidate_id = str(candidate.get("candidate_id", "")).strip() if isinstance(candidate, dict) else ""
    if not candidate_id or candidate_id in candidate_ids:
        raise AgentError("model_output_invalid", "candidate ids must be non-empty and unique", status=502, request_id=request_id)
    candidate_ids.append(candidate_id)
    confidence = candidate.get("confidence")
    if (
        isinstance(confidence, bool) or not isinstance(confidence, (int, float))
        or not math.isfinite(confidence) or not 0 <= confidence <= 1
    ):
        raise AgentError("model_output_invalid", "candidate confidence was invalid", status=502, request_id=request_id)
    if not refs:
        raise AgentError("model_output_invalid", "candidate provenance was not grounded", status=502, request_id=request_id)
    validate_refs(refs, documents_by_id, request_id)
    _validate_question_number_bounds(candidate, request_id)
    evidence_confidences = [ref["ocr_confidence"] for ref in refs if ref.get("ocr_confidence") is not None]
    # 候选可信度不能高于其依赖的最低 OCR 证据可信度。
    if evidence_confidences:
        candidate["confidence"] = min(confidence, *evidence_confidences)


def _validate_question_number_bounds(candidate, request_id):
    # 与Go协调入口使用同一边界；保留中文、子题和稀疏编号，不允许异常数字进入缺题枚举。
    for field in _QUESTION_NUMBER_FIELDS:
        value = candidate.get(field)
        if value is None:
            continue
        if not isinstance(value, str) or len(value) > _MAX_QUESTION_NUMBER_CHARS:
            raise AgentError("model_output_invalid", "question number must be text of at most 64 characters", status=502, request_id=request_id)
        compact = "".join(char for char in value if not char.isspace()).replace("第", "").replace("题", "")
        if any(int(digits) > _MAX_QUESTION_NUMBER_COMPONENT for digits in re.findall(r"[0-9]+", compact)):
            # 错误说明不回显模型长数字，避免把异常输入再次放大到响应和日志。
            raise AgentError("model_output_invalid", "question number numeric component exceeds non-negative int64 range", status=502, request_id=request_id)


def _validate_paper_rubric(rubric, request_id):
    max_score = rubric.get("max_score")
    if max_score is not None and (
        isinstance(max_score, bool) or not isinstance(max_score, (int, float))
        or not math.isfinite(max_score) or max_score < 0
    ):
        raise AgentError("model_output_invalid", "rubric score was invalid", status=502, request_id=request_id)
    for point in rubric.get("points", []):
        _validate_paper_rubric_point(point, request_id)


def _validate_paper_rubric_point(point, request_id):
    score = point.get("score")
    if score is not None and (
        isinstance(score, bool) or not isinstance(score, (int, float))
        or not math.isfinite(score) or score < 0
    ):
        raise AgentError("model_output_invalid", "rubric point score was invalid", status=502, request_id=request_id)
    for requirement in point.get("evidence_requirements", []):
        if not valid_evidence_requirement(requirement):
            raise AgentError("model_output_invalid", "rubric evidence requirement was invalid", status=502, request_id=request_id)


def _validate_paper_question(question, subject, documents_by_id, request_id):
    kind = question.get("question_type")
    if kind is not None and kind not in QUESTION_TYPES:
        raise AgentError("model_output_invalid", "unsupported question type", status=502, request_id=request_id)
    if kind == "formula" and subject not in FORMULA_SUBJECTS:
        raise AgentError("model_output_invalid", "formula routing is not allowed", status=502, request_id=request_id)
    if question.get("score") is not None:
        cited = [documents_by_id[ref["source_id"]] for ref in question["source_refs"]]
        if cited and all(
            not document.get("blocks") and not document.get("_visual_page_nos")
            and not _EXPLICIT_EXAM_MARK.search(str(document.get("content", "")))
            for document in cited
        ):
            question["score"] = None
            question.setdefault("issues", []).append("资料未见明确的小题满分标注；原建议分值已改为待核对")

@staticmethod
def valid_evidence_requirement(requirement, depth=0):
    if not isinstance(requirement, dict) or depth > 8:
        return False
    kind = requirement.get("type")
    if kind in {"all_of", "any_of", "at_least"}:
        children = requirement.get("children")
        if not isinstance(children, list) or not children or len(children) > 32:
            return False
        minimum = requirement.get("minimum")
        if kind == "at_least" and (
            isinstance(minimum, bool)
            or not isinstance(minimum, int)
            or minimum < 1
            or minimum > len(children)
        ):
            return False
        return all(
            valid_evidence_requirement(child, depth + 1)
            for child in children
        )
    if kind in {"valid_transformation", "final_result"}:
        return True
    return (
        kind in {"concept", "unit", "domain"}
        and isinstance(requirement.get("target"), str)
        and bool(requirement["target"].strip())
    )

@staticmethod
def validate_refs(refs, documents_by_id, request_id):
    for ref in refs:
        if not isinstance(ref, dict) or ref.get("source_id") not in documents_by_id:
            raise AgentError(
                "model_output_invalid",
                "candidate provenance was not grounded",
                status=502,
                request_id=request_id,
            )
        document = documents_by_id[ref["source_id"]]
        if ref.get("file_asset_id") != str(
            document.get("file_asset_id", "")
        ) or ref.get("document_index") != document.get("document_index"):
            raise AgentError(
                "model_output_invalid",
                "candidate provenance did not match its source",
                status=502,
                request_id=request_id,
            )
        blocks = (
            document.get("blocks")
            if isinstance(document.get("blocks"), list)
            else []
        )
        # 引用模式由实际输入决定：原图只引用页，OCR 精确匹配块，纯文本使用字符半开区间。
        visual_page_nos = document.get("_visual_page_nos", [])
        if visual_page_nos:
            _validate_visual_ref(ref, visual_page_nos, request_id)
            continue
        if blocks:
            _validate_ocr_ref(ref, blocks, request_id)
        else:
            _validate_text_ref(ref, document, request_id)


def _validate_visual_ref(ref, visual_page_nos, request_id):
    if (
        ref.get("page_no") not in visual_page_nos or ref.get("block_id") is not None
        or ref.get("bbox") is not None or ref.get("text_start") is not None
        or ref.get("text_end") is not None or ref.get("ocr_confidence") is not None
    ):
        raise AgentError("model_output_invalid", "visual provenance did not match a supplied page", status=502, request_id=request_id)


def _validate_ocr_ref(ref, blocks, request_id):
    block_id = ref.get("block_id")
    matched = next((block for block in blocks if block.get("block_id") == block_id), None)
    if matched is None or ref.get("page_no") != matched.get("page_no") or ref.get("bbox") != matched.get("bbox"):
        raise AgentError("model_output_invalid", "OCR provenance did not match a normalized block", status=502, request_id=request_id)
    expected_confidence = matched.get("confidence")
    actual_confidence = ref.get("ocr_confidence")
    if (
        not isinstance(actual_confidence, (int, float)) or isinstance(actual_confidence, bool)
        or not math.isfinite(actual_confidence) or not isinstance(expected_confidence, (int, float))
        or not math.isfinite(expected_confidence) or abs(actual_confidence - expected_confidence) > 1e-9
        or ref.get("text_start") is not None or ref.get("text_end") is not None
    ):
        raise AgentError("model_output_invalid", "OCR provenance confidence or range was invalid", status=502, request_id=request_id)


def _validate_text_ref(ref, document, request_id):
    start, end = ref.get("text_start"), ref.get("text_end")
    if (
        ref.get("page_no") is not None or ref.get("block_id") is not None
        or ref.get("bbox") is not None or ref.get("ocr_confidence") is not None
        or not isinstance(start, int) or isinstance(start, bool)
        or not isinstance(end, int) or isinstance(end, bool)
        or start < 0 or end <= start or end > len(str(document.get("content", "")))
    ):
        raise AgentError("model_output_invalid", "text provenance range was invalid", status=502, request_id=request_id)
