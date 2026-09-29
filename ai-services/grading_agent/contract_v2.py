import base64
import binascii
import hashlib
import json
import math
import re

from .contract import (
    _exact_fields,
    _fail,
    _fail_evidence,
    _fail_model,
    _is_number,
    _number_model,
    _string,
    validate_request,
)
from .errors import AgentError

SCHEMA_VERSION = "grading-agent-v2"
MAX_MEDIA_BYTES = 5 * 1024 * 1024
MAX_MEDIA_PIXELS = 12_000_000
MAX_MEDIA_BASE64_LENGTH = 4 * ((MAX_MEDIA_BYTES + 2) // 3)
MAX_BBOX_AREA = 0.9
BBOX_SCALE = 1_000_000

_PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
_SHA256 = re.compile(r"^[a-f0-9]{64}$")
_INTERNAL_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")
_REQUEST_FIELDS = {
    "schema_version",
    "request_id",
    "subject",
    "grade_level",
    "agent_role",
    "question_id",
    "answer_segment_id",
    "question_type",
    "question_text",
    "max_score",
    "answer_text",
    "ocr_confidence",
    "rubric_version",
    "prompt_version",
    "rubric",
    "model_policy",
    "prompt_guard",
    "output_constraint",
    "media_evidence",
    "math_evidence",
}
_MEDIA_FIELDS = {
    "kind",
    "encoding",
    "media_type",
    "sha256",
    "byte_size",
    "width_pixels",
    "height_pixels",
    "normalized_bbox",
    "binding_hash",
    "data_base64",
}
_BBOX_FIELDS = {"x", "y", "width", "height"}
_RESPONSE_FIELDS = {
    "schema_version",
    "request_id",
    "status",
    "delivery",
    "criterion_candidates",
    "alternative_solution_candidate",
    "risk_flags",
    "needs_human_review",
    "model_version",
    "prompt_version",
    "rubric_version",
    "capability_profile",
    "mock",
    "telemetry",
}
_MATH_FIELDS = {"artifact_id", "artifact_version", "correction_revision", "exam_question_snapshot_id", "scoring_version", "overall_confidence", "quality", "steps", "formulas", "verifications", "rubric_evidence", "has_diagram", "graph_uncertain", "required_rubric_uncertain"}
_QUALITY_FIELDS = {"recognition", "formula", "structure", "verification", "rubric_mapping", "critical"}
_FORBIDDEN_FIELDS = {
    "tenant_id",
    "school_id",
    "student_id",
    "student_name",
    "student_no",
    "class_id",
    "exam_id",
    "submission_id",
    "final_score",
    "published_score",
    "original_filename",
    "bucket",
    "storage_key",
    "url",
}


def _bbox_units(bbox, request_id):
    _exact_fields(bbox, _BBOX_FIELDS, "media_evidence.normalized_bbox", request_id)
    values = (bbox["x"], bbox["y"], bbox["width"], bbox["height"])
    if any(not _is_number(value) for value in values):
        _fail("media_evidence.normalized_bbox values must be finite numbers", request_id)
    x, y, width, height = values
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1 or y + height > 1:
        _fail("media_evidence.normalized_bbox must stay within its source page", request_id)
    if width * height >= MAX_BBOX_AREA:
        _fail("whole-page or near-whole-page media evidence is forbidden", request_id)
    # 跨语言绑定统一使用百万分之一页尺寸的整数单位，避免浮点格式差异改变摘要。
    units = tuple(round(value * BBOX_SCALE) for value in values)
    if any(not math.isclose(value, unit / BBOX_SCALE, abs_tol=1e-12) for value, unit in zip(values, units)):
        _fail("media_evidence.normalized_bbox supports at most six decimal places", request_id)
    return units


def compute_media_binding_hash(request):
    """Return the cross-runtime binding for one attested answer crop."""

    media = request["media_evidence"]
    units = _bbox_units(media["normalized_bbox"], request.get("request_id", ""))
    parts = (
        SCHEMA_VERSION,
        request["request_id"],
        request["question_id"],
        request["answer_segment_id"],
        media["sha256"],
        *(str(value) for value in units),
    )
    # 每段以 UTF-8 字节长度定界，避免字段拼接产生歧义。
    material = "".join(f"{len(part.encode('utf-8'))}:{part}" for part in parts).encode("utf-8")
    return hashlib.sha256(material).hexdigest()


def validate_media_evidence(media, request):
    request_id = request.get("request_id", "")
    _exact_fields(media, _MEDIA_FIELDS, "media_evidence", request_id)
    if media["kind"] != "answer_segment_crop":
        _fail("media_evidence.kind must be answer_segment_crop", request_id)
    if media["encoding"] != "base64":
        _fail("media_evidence.encoding must be base64", request_id)
    if media["media_type"] != "image/png":
        _fail("media_evidence.media_type must be image/png", request_id)
    if not isinstance(media["sha256"], str) or not _SHA256.fullmatch(media["sha256"]):
        _fail("media_evidence.sha256 must be lowercase hexadecimal SHA-256", request_id)
    if not isinstance(media["binding_hash"], str) or not _SHA256.fullmatch(media["binding_hash"]):
        _fail("media_evidence.binding_hash must be lowercase hexadecimal SHA-256", request_id)

    byte_size = media["byte_size"]
    width = media["width_pixels"]
    height = media["height_pixels"]
    if isinstance(byte_size, bool) or not isinstance(byte_size, int) or not 1 <= byte_size <= MAX_MEDIA_BYTES:
        _fail("media_evidence.byte_size is outside the approved limit", request_id)
    if (
        isinstance(width, bool)
        or isinstance(height, bool)
        or not isinstance(width, int)
        or not isinstance(height, int)
        or width <= 0
        or height <= 0
        or width * height > MAX_MEDIA_PIXELS
    ):
        _fail("media_evidence dimensions are invalid or too large", request_id)

    _bbox_units(media["normalized_bbox"], request_id)
    if (
        not isinstance(media["data_base64"], str)
        or not media["data_base64"]
        or len(media["data_base64"]) > MAX_MEDIA_BASE64_LENGTH
    ):
        _fail("media_evidence.data_base64 must be non-empty and bounded", request_id)
    try:
        decoded = base64.b64decode(media["data_base64"], validate=True)
    except (ValueError, binascii.Error) as exc:
        raise AgentError(
            "invalid_request",
            "media_evidence.data_base64 is not valid base64",
            status=400,
            request_id=request_id,
        ) from exc
    if len(decoded) != byte_size:
        _fail("media_evidence.byte_size does not match decoded content", request_id)
    if len(decoded) < 24 or not decoded.startswith(_PNG_SIGNATURE) or decoded[12:16] != b"IHDR":
        _fail("media_evidence content is not a PNG", request_id)
    decoded_width = int.from_bytes(decoded[16:20], "big")
    decoded_height = int.from_bytes(decoded[20:24], "big")
    if (decoded_width, decoded_height) != (width, height):
        _fail("media_evidence dimensions do not match decoded PNG", request_id)
    if decoded_width * decoded_height > MAX_MEDIA_PIXELS:
        _fail("decoded PNG exceeds the pixel limit", request_id)
    if hashlib.sha256(decoded).hexdigest() != media["sha256"]:
        _fail("media_evidence.sha256 does not match decoded content", request_id)
    if compute_media_binding_hash(request) != media["binding_hash"]:
        _fail("media_evidence.binding_hash does not match this request", request_id)
    return media


def validate_request_v2(payload):
    if not isinstance(payload, dict):
        _fail("request body must be an object")
    request_id = payload.get("request_id") if isinstance(payload.get("request_id"), str) else ""
    leaked = sorted(set(payload) & _FORBIDDEN_FIELDS)
    if leaked:
        _fail(f"request contains forbidden identity, storage, or final-grade fields: {leaked}", request_id)
    optional = {"reference_context"} if "reference_context" in payload else set()
    _exact_fields(payload, _REQUEST_FIELDS | optional, "request", request_id)
    if payload["schema_version"] != SCHEMA_VERSION:
        _fail("schema_version is unsupported", request_id)
    for field in ("request_id", "question_id", "answer_segment_id"):
        if not isinstance(payload[field], str) or not _INTERNAL_ID.fullmatch(payload[field]):
            _fail(f"{field} must be a bounded internal identifier", request_id)

    # Reuse the already-parsed values without duplicating the bounded Base64
    # string. The v1 validator is read-only, so a shallow inherited envelope is
    # sufficient and keeps peak memory predictable for the future v2 seam.
    inherited = {key: value for key, value in payload.items() if key not in {"media_evidence", "math_evidence", "reference_context"}}
    inherited["schema_version"] = "grading-agent-v1"
    validate_request(inherited)
    validate_media_evidence(payload["media_evidence"], payload)
    validate_math_evidence(payload["math_evidence"], request_id)
    if "reference_context" in payload:
        validate_reference_context(payload["reference_context"], request_id)
    return payload


def validate_reference_context(reference, request_id):
    fields = {"source", "snapshot_hash", "standard_answer", "equivalent_answers", "solution_text", "solution_steps"}
    _exact_fields(reference, fields, "reference_context", request_id)
    if reference["source"] != "confirmed_exam_import_snapshot":
        _fail("reference_context.source is unsupported", request_id)
    if not isinstance(reference["snapshot_hash"], str) or not _SHA256.fullmatch(reference["snapshot_hash"]):
        _fail("reference_context.snapshot_hash must be a lowercase SHA-256", request_id)
    if len(json.dumps(reference["standard_answer"], ensure_ascii=False)) > 20_000:
        _fail("reference_context.standard_answer is too long", request_id)
    answers = reference["equivalent_answers"]
    if not isinstance(answers, list) or len(answers) > 100 or any(len(json.dumps(item, ensure_ascii=False)) > 4_000 for item in answers):
        _fail("reference_context.equivalent_answers is invalid", request_id)
    if not isinstance(reference["solution_text"], str) or len(reference["solution_text"]) > 20_000:
        _fail("reference_context.solution_text is invalid", request_id)
    steps = reference["solution_steps"]
    if not isinstance(steps, list) or len(steps) > 100:
        _fail("reference_context.solution_steps is invalid", request_id)
    for index, step in enumerate(steps):
        _exact_fields(step, {"step_no", "content"}, f"reference_context.solution_steps[{index}]", request_id)
        if isinstance(step["step_no"], bool) or not isinstance(step["step_no"], int) or step["step_no"] <= 0:
            _fail("reference_context.solution_steps.step_no is invalid", request_id)
        if not isinstance(step["content"], str) or len(step["content"]) > 4_000:
            _fail("reference_context.solution_steps.content is invalid", request_id)
    if reference["standard_answer"] is None and not reference["solution_text"].strip() and not steps:
        _fail("reference_context has no authored answer or solution", request_id)


def validate_math_evidence(evidence, request_id):
    if not isinstance(evidence, dict):
        _fail("math_evidence must be an object", request_id)
    _exact_fields(evidence, _MATH_FIELDS, "math_evidence", request_id)
    _string(evidence["artifact_id"], "math_evidence.artifact_id", request_id, 1, 128)
    _string(evidence["exam_question_snapshot_id"], "math_evidence.exam_question_snapshot_id", request_id, 1, 128)
    if isinstance(evidence["artifact_version"], bool) or not isinstance(evidence["artifact_version"], int) or evidence["artifact_version"] <= 0:
        _fail("math_evidence.artifact_version must be positive", request_id)
    if isinstance(evidence["correction_revision"], bool) or not isinstance(evidence["correction_revision"], int) or evidence["correction_revision"] < 0:
        _fail("math_evidence.correction_revision is invalid", request_id)
    if evidence["scoring_version"] != "math-rubric-score-v1":
        _fail("math_evidence.scoring_version is unsupported", request_id)
    _number_model(evidence["overall_confidence"], "math evidence confidence", request_id, 0, 1)
    quality = evidence["quality"]
    _exact_fields(quality, _QUALITY_FIELDS, "math_evidence.quality", request_id)
    for key in _QUALITY_FIELDS:
        _number_model(quality[key], f"math_evidence.quality.{key}", request_id, 0, 1)
    if not math.isclose(quality["critical"], min(quality[key] for key in _QUALITY_FIELDS if key != "critical"), abs_tol=1e-9):
        _fail("math_evidence.quality.critical must be the minimum dimension", request_id)
    known = set()
    steps = evidence["steps"]
    if not isinstance(steps, list) or not 1 <= len(steps) <= 512:
        _fail("math_evidence.steps must be a bounded non-empty array", request_id)
    for index, step in enumerate(steps):
        expected = {"id", "formula_ids", "confidence"} | ({"text"} if "text" in step else set())
        _exact_fields(step, expected, f"math_evidence.steps[{index}]", request_id)
        _string(step["id"], f"math_evidence.steps[{index}].id", request_id, 1, 128)
        if step["id"] in known or not isinstance(step["formula_ids"], list):
            _fail("math_evidence step identity is invalid", request_id)
        known.add(step["id"])
        _number_model(step["confidence"], "math step confidence", request_id, 0, 1)
    formulas = evidence["formulas"]
    if not isinstance(formulas, list) or len(formulas) > 512:
        _fail("math_evidence.formulas must be bounded", request_id)
    for index, formula in enumerate(formulas):
        expected = {"id", "step_ids", "parse_status", "reason_codes", "bbox", "confidence"} | ({"canonical_latex"} if "canonical_latex" in formula else set())
        _exact_fields(formula, expected, f"math_evidence.formulas[{index}]", request_id)
        _string(formula["id"], f"math_evidence.formulas[{index}].id", request_id, 1, 128)
        if formula["id"] in known or formula["parse_status"] not in {"parsed", "ambiguous", "unsupported", "failed"}:
            _fail("math_evidence formula identity or status is invalid", request_id)
        known.add(formula["id"])
        _number_model(formula["confidence"], "math formula confidence", request_id, 0, 1)
        _bbox_units(formula["bbox"], request_id)
    if not isinstance(evidence["verifications"], list) or len(evidence["verifications"]) > 2048:
        _fail("math_evidence.verifications must be bounded", request_id)
    if not isinstance(evidence["rubric_evidence"], list) or len(evidence["rubric_evidence"]) > 512:
        _fail("math_evidence.rubric_evidence must be bounded", request_id)
    for flag in ("has_diagram", "graph_uncertain", "required_rubric_uncertain"):
        if not isinstance(evidence[flag], bool):
            _fail(f"math_evidence.{flag} must be boolean", request_id)
    return known


def _validate_crop_bbox(bbox, request_id):
    _exact_fields(bbox, _BBOX_FIELDS, "answer_crop.normalized_bbox", request_id)
    values = (bbox["x"], bbox["y"], bbox["width"], bbox["height"])
    if any(not _is_number(value) for value in values):
        _fail_evidence("answer_crop bbox values must be finite numbers", request_id)
    x, y, width, height = values
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1 or y + height > 1:
        _fail_evidence("answer_crop bbox must stay within the crop", request_id)


def validate_response_v2(suggestion, request):
    """Validate a score-free semantic mapping returned by the model."""

    request_id = request["request_id"]
    if not isinstance(suggestion, dict):
        _fail_model("suggestion must be an object", request_id)
    _exact_fields(suggestion, _RESPONSE_FIELDS, "suggestion", request_id)
    if suggestion["schema_version"] != SCHEMA_VERSION or suggestion["request_id"] != request_id:
        _fail_model("suggestion contract binding mismatch", request_id)
    if suggestion["status"] != "candidate_mapping" or suggestion["delivery"] != "teacher_suggestion":
        _fail_model("suggestion route is invalid", request_id)
    if suggestion["needs_human_review"] is not True or suggestion["mock"] is not False:
        _fail_model("suggestion governance flags are invalid", request_id)
    if suggestion["rubric_version"] != request["rubric_version"] or suggestion["prompt_version"] != request["prompt_version"]:
        _fail_model("suggestion version mismatch", request_id)
    if not isinstance(suggestion["alternative_solution_candidate"], bool):
        _fail_model("alternative solution candidate flag is invalid", request_id)
    allowed_risks = {"alternative_solution_candidate", "schema_repaired", "prompt_injection_suspected", "human_review_required"}
    risks = suggestion["risk_flags"]
    if not isinstance(risks, list) or len(risks) > 20 or len(risks) != len(set(risks)) or not set(risks).issubset(allowed_risks):
        _fail_model("suggestion contains an unsupported risk flag", request_id)
    if suggestion["alternative_solution_candidate"] and "alternative_solution_candidate" not in risks:
        _fail_model("alternative solution candidate must carry its risk flag", request_id)

    rubric_points = {point["id"] for point in request["rubric"]["points"]}
    known_evidence = {step["id"] for step in request["math_evidence"]["steps"]} | {formula["id"] for formula in request["math_evidence"]["formulas"]}
    candidates = suggestion["criterion_candidates"]
    if not isinstance(candidates, list) or len(candidates) > 100:
        _fail_model("criterion_candidates must be a bounded array", request_id)
    seen = set()
    for index, candidate in enumerate(candidates):
        expected = {"rubric_point_id", "status", "evidence_ids", "confidence", "reason_code"}
        if not isinstance(candidate, dict) or set(candidate) != expected:
            _fail_model(f"criterion_candidates[{index}] is invalid", request_id)
        point_id = candidate["rubric_point_id"]
        if point_id not in rubric_points or point_id in seen:
            _fail_model("criterion candidate is unknown or duplicated", request_id)
        seen.add(point_id)
        if candidate["status"] not in {"supported", "contradicted", "uncertain"}:
            _fail_model("criterion candidate status is invalid", request_id)
        _number_model(candidate["confidence"], "criterion candidate confidence", request_id, 0, 1)
        _string(candidate["reason_code"], "criterion candidate reason", request_id, 1, 256)
        ids = candidate["evidence_ids"]
        if not isinstance(ids, list) or len(ids) > 100 or len(ids) != len(set(ids)) or any(item not in known_evidence for item in ids):
            _fail_evidence("criterion candidate evidence link is invalid", request_id)
        if candidate["status"] in {"supported", "contradicted"} and not ids:
            _fail_evidence("supported or contradicted candidate requires evidence", request_id)
    return suggestion
