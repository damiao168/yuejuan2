"""Validate model output against authoritative input provenance."""

import math
import re

from .errors import AgentError
from .paper_schema import FORMULA_SUBJECTS, QUESTION_TYPES, ROLES

# A model may still confuse points earned in a word problem with exam marks.
# This conservative direct-text guard only clears a proposed mark when none of
# the cited documents contains any explicit question/section mark notation.
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
            if not isinstance(item, dict):
                continue
            for ref in item.get("source_refs", []):
                if (
                    not isinstance(ref, dict)
                    or ref.get("source_id") not in direct_source_ids
                ):
                    continue
                ref["page_no"] = None
                ref["block_id"] = None
                ref["bbox"] = None
                ref["ocr_confidence"] = None

@staticmethod
def validate_paper_output(output, request_id, subject, documents):
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

    documents_by_id = {
        str(document["source_id"]): document for document in documents
    }
    source_ids = set(documents_by_id)
    classified = [
        item.get("source_id")
        for item in output["documents"]
        if isinstance(item, dict)
    ]
    if len(classified) != len(source_ids) or set(classified) != source_ids:
        raise AgentError(
            "model_output_invalid",
            "every source must be classified exactly once",
            status=502,
            request_id=request_id,
        )
    for item in output["documents"]:
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

    candidate_ids = []
    for collection in (
        "question_candidates",
        "answer_candidates",
        "solution_candidates",
        "rubric_candidates",
    ):
        for candidate in output[collection]:
            refs = (
                candidate.get("source_refs")
                if isinstance(candidate, dict)
                else None
            )
            candidate_id = (
                str(candidate.get("candidate_id", "")).strip()
                if isinstance(candidate, dict)
                else ""
            )
            if not candidate_id or candidate_id in candidate_ids:
                raise AgentError(
                    "model_output_invalid",
                    "candidate ids must be non-empty and unique",
                    status=502,
                    request_id=request_id,
                )
            candidate_ids.append(candidate_id)
            confidence = candidate.get("confidence")
            if (
                isinstance(confidence, bool)
                or not isinstance(confidence, (int, float))
                or not math.isfinite(confidence)
                or not 0 <= confidence <= 1
            ):
                raise AgentError(
                    "model_output_invalid",
                    "candidate confidence was invalid",
                    status=502,
                    request_id=request_id,
                )
            if not refs:
                raise AgentError(
                    "model_output_invalid",
                    "candidate provenance was not grounded",
                    status=502,
                    request_id=request_id,
                )
            validate_refs(refs, documents_by_id, request_id)
            evidence_confidences = [
                ref["ocr_confidence"]
                for ref in refs
                if ref.get("ocr_confidence") is not None
            ]
            # 候选可信度不能高于其依赖的最低 OCR 证据可信度。
            if evidence_confidences:
                candidate["confidence"] = min(confidence, *evidence_confidences)
    for rubric in output["rubric_candidates"]:
        max_score = rubric.get("max_score")
        if max_score is not None and (
            isinstance(max_score, bool)
            or not isinstance(max_score, (int, float))
            or not math.isfinite(max_score)
            or max_score < 0
        ):
            raise AgentError(
                "model_output_invalid",
                "rubric score was invalid",
                status=502,
                request_id=request_id,
            )
        for point in rubric.get("points", []):
            score = point.get("score")
            if score is not None and (
                isinstance(score, bool)
                or not isinstance(score, (int, float))
                or not math.isfinite(score)
                or score < 0
            ):
                raise AgentError(
                    "model_output_invalid",
                    "rubric point score was invalid",
                    status=502,
                    request_id=request_id,
                )
            for requirement in point.get("evidence_requirements", []):
                if not valid_evidence_requirement(requirement):
                    raise AgentError(
                        "model_output_invalid",
                        "rubric evidence requirement was invalid",
                        status=502,
                        request_id=request_id,
                    )
    for issue in output["issues"]:
        refs = issue.get("source_refs") if isinstance(issue, dict) else None
        if refs:
            validate_refs(refs, documents_by_id, request_id)
    for question in output["question_candidates"]:
        kind = question.get("question_type")
        if kind is not None and kind not in QUESTION_TYPES:
            raise AgentError(
                "model_output_invalid",
                "unsupported question type",
                status=502,
                request_id=request_id,
            )
        if kind == "formula" and subject not in FORMULA_SUBJECTS:
            raise AgentError(
                "model_output_invalid",
                "formula routing is not allowed",
                status=502,
                request_id=request_id,
            )
        if question.get("score") is not None:
            cited = [documents_by_id[ref["source_id"]] for ref in question["source_refs"]]
            if cited and all(
                not document.get("blocks") and not document.get("_visual_page_nos")
                and not _EXPLICIT_EXAM_MARK.search(str(document.get("content", "")))
                for document in cited
            ):
                question["score"] = None
                question.setdefault("issues", []).append("资料未见明确的小题满分标注；原建议分值已改为待核对")
    return output

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
            if (
                ref.get("page_no") not in visual_page_nos
                or ref.get("block_id") is not None
                or ref.get("bbox") is not None
                or ref.get("text_start") is not None
                or ref.get("text_end") is not None
                or ref.get("ocr_confidence") is not None
            ):
                raise AgentError(
                    "model_output_invalid",
                    "visual provenance did not match a supplied page",
                    status=502,
                    request_id=request_id,
                )
            continue
        if blocks:
            block_id = ref.get("block_id")
            matched = next(
                (block for block in blocks if block.get("block_id") == block_id),
                None,
            )
            if (
                matched is None
                or ref.get("page_no") != matched.get("page_no")
                or ref.get("bbox") != matched.get("bbox")
            ):
                raise AgentError(
                    "model_output_invalid",
                    "OCR provenance did not match a normalized block",
                    status=502,
                    request_id=request_id,
                )
            expected_confidence = matched.get("confidence")
            actual_confidence = ref.get("ocr_confidence")
            if (
                not isinstance(actual_confidence, (int, float))
                or isinstance(actual_confidence, bool)
                or not math.isfinite(actual_confidence)
                or not isinstance(expected_confidence, (int, float))
                or not math.isfinite(expected_confidence)
                or abs(actual_confidence - expected_confidence) > 1e-9
                or ref.get("text_start") is not None
                or ref.get("text_end") is not None
            ):
                raise AgentError(
                    "model_output_invalid",
                    "OCR provenance confidence or range was invalid",
                    status=502,
                    request_id=request_id,
                )
        else:
            start, end = ref.get("text_start"), ref.get("text_end")
            if (
                ref.get("page_no") is not None
                or ref.get("block_id") is not None
                or ref.get("bbox") is not None
                or ref.get("ocr_confidence") is not None
                or not isinstance(start, int)
                or isinstance(start, bool)
                or not isinstance(end, int)
                or isinstance(end, bool)
                or start < 0
                or end <= start
                or end > len(str(document.get("content", "")))
            ):
                raise AgentError(
                    "model_output_invalid",
                    "text provenance range was invalid",
                    status=502,
                    request_id=request_id,
                )
