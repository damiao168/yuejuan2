import math

from .errors import AgentError
from .guardrails import normalize_evidence_text

SCHEMA_VERSION = "grading-agent-v1"
OCR_REVIEW_THRESHOLD = 0.85
SUBJECTS = {
    "chinese",
    "mathematics",
    "english",
    "physics",
    "chemistry",
    "biology",
    "history",
    "ethics_politics",
    "geography",
}
GRADE_LEVELS = {"junior", "senior"}
QUESTION_TYPES = {"short_answer", "calculation", "essay", "discussion"}
CANONICAL_RISK_FLAGS = {
    "ocr_low_confidence",
    "ocr_text_empty_review_required",
    "ambiguous_answer",
    "insufficient_evidence",
    "possible_off_topic",
    "score_needs_review",
    "schema_repaired",
    "prompt_injection_suspected",
    "human_review_required",
}
MODEL_RISK_FLAGS = {
    "OCR_LOW_CONFIDENCE": "ocr_low_confidence",
    "OCR_TEXT_EMPTY_REVIEW_REQUIRED": "ocr_text_empty_review_required",
    "AMBIGUOUS_ANSWER": "ambiguous_answer",
    "INSUFFICIENT_EVIDENCE": "insufficient_evidence",
    "POSSIBLE_OFF_TOPIC": "possible_off_topic",
    "SCORE_NEEDS_REVIEW": "score_needs_review",
    "SCHEMA_REPAIRED": "schema_repaired",
    "PROMPT_INJECTION_SUSPECTED": "prompt_injection_suspected",
    "HUMAN_REVIEW_REQUIRED": "human_review_required",
}

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
}
_FORBIDDEN_REQUEST_FIELDS = {
    "tenant_id",
    "student_id",
    "student_name",
    "student_no",
    "final_score",
    "published_score",
}


def _fail(message, request_id="", code="invalid_request", status=400):
    raise AgentError(code, message, status=status, request_id=request_id)


def _is_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def _string(value, field, request_id, minimum=1, maximum=128):
    if not isinstance(value, str) or len(value.strip()) < minimum or len(value) > maximum:
        _fail(f"{field} must be a string between {minimum} and {maximum} characters", request_id)


def _number(value, field, request_id, minimum=0, maximum=1, exclusive_minimum=False):
    if not _is_number(value):
        operator = ">" if exclusive_minimum else ">="
        _fail(f"{field} must be {operator} {minimum} and <= {maximum}", request_id)
    invalid_minimum = value <= minimum if exclusive_minimum else value < minimum
    if invalid_minimum or value > maximum:
        operator = ">" if exclusive_minimum else ">="
        _fail(f"{field} must be {operator} {minimum} and <= {maximum}", request_id)


def _exact_fields(value, expected, field, request_id):
    if not isinstance(value, dict):
        _fail(f"{field} must be an object", request_id)
    actual = set(value)
    if actual != expected:
        missing = sorted(expected - actual)
        unknown = sorted(actual - expected)
        _fail(f"{field} fields are invalid; missing={missing}, unknown={unknown}", request_id)


def validate_request(payload):
    if not isinstance(payload, dict):
        _fail("request body must be an object")
    request_id = payload.get("request_id") if isinstance(payload.get("request_id"), str) else ""
    leaked = sorted(set(payload) & _FORBIDDEN_REQUEST_FIELDS)
    if leaked:
        _fail(f"request contains forbidden identity or final-grade fields: {leaked}", request_id)
    _exact_fields(payload, _REQUEST_FIELDS, "request", request_id)

    if payload["schema_version"] != SCHEMA_VERSION:
        _fail("schema_version is unsupported", request_id)
    _string(payload["request_id"], "request_id", request_id, 8, 128)
    if payload["subject"] not in SUBJECTS:
        _fail("subject is unsupported", request_id)
    if payload["grade_level"] not in GRADE_LEVELS:
        _fail("grade_level is outside the approved profile", request_id)
    if payload["agent_role"] not in {"single", "primary", "arbiter"}:
        _fail("agent_role is outside the approved profile", request_id)
    if payload["question_type"] not in QUESTION_TYPES:
        _fail("question_type is unsupported", request_id)
    _string(payload["question_id"], "question_id", request_id)
    _string(payload["answer_segment_id"], "answer_segment_id", request_id)
    _string(payload["question_text"], "question_text", request_id, 1, 12000)
    _string(payload["answer_text"], "answer_text", request_id, 1, 20000)
    _number(payload["max_score"], "max_score", request_id, 0, 1000, exclusive_minimum=True)
    _number(payload["ocr_confidence"], "ocr_confidence", request_id, 0, 1)
    _string(payload["rubric_version"], "rubric_version", request_id)
    _string(payload["prompt_version"], "prompt_version", request_id)

    _validate_model_policy(payload["model_policy"], request_id)
    _validate_prompt_guard(payload["prompt_guard"], request_id)
    _validate_output_constraint(payload["output_constraint"], request_id)
    _validate_rubric(payload["rubric"], payload, request_id)
    return payload


def _validate_model_policy(policy, request_id):
    expected = {"mode", "model_version", "min_confidence"}
    _exact_fields(policy, expected, "model_policy", request_id)
    if policy["mode"] != "shadow":
        _fail("model_policy.mode must be shadow", request_id)
    _string(policy["model_version"], "model_policy.model_version", request_id, 1, 256)
    _number(policy["min_confidence"], "model_policy.min_confidence", request_id, 0, 1)


def _validate_prompt_guard(guard, request_id):
    expected = {"student_answer_is_untrusted", "suspected_injection", "signals"}
    _exact_fields(guard, expected, "prompt_guard", request_id)
    if guard["student_answer_is_untrusted"] is not True:
        _fail("prompt_guard must mark the answer as untrusted", request_id)
    if not isinstance(guard["suspected_injection"], bool):
        _fail("prompt_guard.suspected_injection must be boolean", request_id)
    if not isinstance(guard["signals"], list) or len(guard["signals"]) > 32:
        _fail("prompt_guard.signals must be a bounded array", request_id)
    for index, signal in enumerate(guard["signals"]):
        _string(signal, f"prompt_guard.signals[{index}]", request_id)


def _validate_output_constraint(constraint, request_id):
    expected = {
        "criteria_evidence_only",
        "allow_model_final_score",
        "final_score_authority",
    }
    _exact_fields(constraint, expected, "output_constraint", request_id)
    if constraint["criteria_evidence_only"] is not True:
        _fail("output_constraint must require criteria and evidence", request_id)
    if constraint["allow_model_final_score"] is not False:
        _fail("output_constraint must deny model final-score authority", request_id)
    if constraint["final_score_authority"] not in {
        "server_rubric_or_human_confirmation",
        "server_rubric_and_deterministic_rule",
        "human_review",
    }:
        _fail("output_constraint final score authority is unsupported", request_id)


def _validate_rubric(rubric, request, request_id):
    expected = {
        "rubric_id",
        "rubric_version",
        "question_type",
        "max_score",
        "allow_partial_total",
        "points",
        "deductions",
        "equivalent_answers",
        "examples",
        "scoring_notes",
    }
    _exact_fields(rubric, expected, "rubric", request_id)
    _string(rubric["rubric_id"], "rubric.rubric_id", request_id)
    _string(rubric["rubric_version"], "rubric.rubric_version", request_id)
    if rubric["rubric_version"] != request["rubric_version"]:
        _fail("rubric version does not match request", request_id)
    if rubric["question_type"] != request["question_type"]:
        _fail("rubric question type does not match request", request_id)
    _number(rubric["max_score"], "rubric.max_score", request_id, 0, 1000, exclusive_minimum=True)
    if not math.isclose(rubric["max_score"], request["max_score"], abs_tol=1e-6):
        _fail("rubric max score does not match request", request_id)
    if not isinstance(rubric["allow_partial_total"], bool):
        _fail("rubric.allow_partial_total must be boolean", request_id)
    if not isinstance(rubric["points"], list) or not 1 <= len(rubric["points"]) <= 100:
        _fail("rubric.points must contain between 1 and 100 points", request_id)

    point_ids = set()
    aliases = set()
    total = 0.0
    point_fields = {"id", "description", "score", "required", "aliases", "evidence_required", "match_policy"}
    for index, point in enumerate(rubric["points"]):
        _exact_fields(point, point_fields, f"rubric.points[{index}]", request_id)
        _string(point["id"], f"rubric.points[{index}].id", request_id)
        _string(point["description"], f"rubric.points[{index}].description", request_id, 1, 4000)
        if point["id"] in point_ids:
            _fail("rubric point ids must be unique", request_id)
        point_ids.add(point["id"])
        _number(point["score"], f"rubric.points[{index}].score", request_id, 0, 1000, exclusive_minimum=True)
        total += point["score"]
        if not isinstance(point["required"], bool) or not isinstance(point["evidence_required"], bool):
            _fail("rubric point review fields must be boolean", request_id)
        if point["match_policy"] not in {"semantic", "strict_alias"}:
            _fail("rubric point match_policy is unsupported", request_id)
        if not isinstance(point["aliases"], list) or len(point["aliases"]) > 100:
            _fail("rubric point aliases must be a bounded array", request_id)
        for alias in point["aliases"]:
            _string(alias, "rubric point alias", request_id, 1, 500)
            normalized = normalize_evidence_text(alias)
            if normalized in aliases:
                _fail("rubric aliases must not be shared", request_id)
            aliases.add(normalized)
        if point["match_policy"] == "strict_alias" and not point["aliases"]:
            _fail("strict_alias points must define aliases", request_id)
    if not rubric["allow_partial_total"] and not math.isclose(total, rubric["max_score"], abs_tol=1e-6):
        _fail("rubric point total must equal max_score", request_id)
    for field in ("deductions", "equivalent_answers", "examples", "scoring_notes"):
        if not isinstance(rubric[field], list) or len(rubric[field]) > 100:
            _fail(f"rubric.{field} must be a bounded array", request_id)


def normalize_model_output(raw, request, route, model_version, prompt_version, profile_id, telemetry):
    request_id = request["request_id"]
    expected = {
        "suggested_score",
        "confidence",
        "matched_points",
        "missing_points",
        "deductions",
        "evidence",
        "risk_flags",
        "needs_human_review",
        "student_feedback",
        "teacher_note",
    }
    if not isinstance(raw, dict):
        _fail_model("model output must be an object", request_id)
    if set(raw) != expected:
        _fail_model("model output fields do not match the required schema", request_id)
    _number_model(raw["suggested_score"], "suggested_score", request_id, 0, request["max_score"])
    _number_model(raw["confidence"], "confidence", request_id, 0, 1)
    if raw["deductions"] != []:
        _fail_model("model deductions are not enabled for this profile", request_id)
    if not isinstance(raw["needs_human_review"], bool):
        _fail_model("needs_human_review must be boolean", request_id)
    if not isinstance(raw["student_feedback"], str) or not raw["student_feedback"].strip():
        _fail_model("student_feedback must be non-empty", request_id)
    if not isinstance(raw["teacher_note"], str) or not raw["teacher_note"].strip():
        _fail_model("teacher_note must be non-empty", request_id)

    rubric_points = {point["id"]: point for point in request["rubric"]["points"]}
    classified = set()
    matched = []
    missing = []
    rejected_evidence_ids = set()
    for index, item in enumerate(_model_array(raw, "matched_points", request_id, 100)):
        if not isinstance(item, dict) or set(item) != {"rubric_point_id", "score", "evidence_ids"}:
            _fail_model(f"matched_points[{index}] is invalid", request_id)
        point_id = item.get("rubric_point_id")
        if not isinstance(point_id, str) or not point_id.strip():
            _fail_model("matched rubric point id must be non-empty", request_id)
        point = rubric_points.get(point_id)
        if not point or point_id in classified:
            _fail_model("model referenced an unknown or duplicate rubric point", request_id)
        _number_model(item.get("score"), "matched point score", request_id, 0, point["score"])
        if not isinstance(item.get("evidence_ids"), list) or not item["evidence_ids"]:
            _fail_model("matched rubric point must link evidence", request_id)
        for evidence_id in item["evidence_ids"]:
            if not isinstance(evidence_id, str) or not evidence_id.strip():
                _fail_model("evidence id must be non-empty", request_id)
        if len(set(item["evidence_ids"])) != len(item["evidence_ids"]):
            _fail_model("matched rubric point contains duplicate evidence links", request_id)
        classified.add(point_id)
        if point["match_policy"] == "strict_alias":
            answer = normalize_evidence_text(request["answer_text"])
            if not any(normalize_evidence_text(alias) in answer for alias in point["aliases"]):
                missing.append({"rubric_point_id": point_id, "label": point["description"], "reason": "strict_alias_not_found"})
                rejected_evidence_ids.update(item["evidence_ids"])
                continue
        matched.append({
            "rubric_point_id": point_id,
            "label": point["description"],
            "score": item["score"],
            "evidence_ids": list(item["evidence_ids"]),
        })

    for index, item in enumerate(_model_array(raw, "missing_points", request_id, 100)):
        if not isinstance(item, dict) or set(item) != {"rubric_point_id", "reason"}:
            _fail_model(f"missing_points[{index}] is invalid", request_id)
        point_id = item.get("rubric_point_id")
        if not isinstance(point_id, str) or not point_id.strip():
            _fail_model("missing rubric point id must be non-empty", request_id)
        point = rubric_points.get(point_id)
        if not point or point_id in classified:
            _fail_model("model referenced an unknown or duplicate missing rubric point", request_id)
        if not isinstance(item.get("reason"), str) or not item["reason"].strip():
            _fail_model("missing point reason must be non-empty", request_id)
        classified.add(point_id)
        missing.append({"rubric_point_id": point_id, "label": point["description"], "reason": item["reason"]})
    if classified != set(rubric_points):
        _fail_model("model must classify every rubric point exactly once", request_id)

    evidence_by_id = {}
    evidence = []
    accepted_points = {point["rubric_point_id"] for point in matched}
    for index, item in enumerate(_model_array(raw, "evidence", request_id, 200)):
        expected_evidence = {"evidence_id", "rubric_point_id", "text_excerpt", "location", "confidence"}
        if not isinstance(item, dict) or set(item) != expected_evidence:
            _fail_evidence(f"evidence[{index}] is invalid", request_id)
        evidence_id = item["evidence_id"]
        rubric_point_id = item["rubric_point_id"]
        if not isinstance(evidence_id, str) or not evidence_id.strip():
            _fail_evidence("evidence id must be non-empty", request_id)
        if not isinstance(rubric_point_id, str) or not rubric_point_id.strip():
            _fail_evidence("evidence rubric point id must be non-empty", request_id)
        if evidence_id in rejected_evidence_ids:
            continue
        if evidence_id in evidence_by_id:
            _fail_evidence("model emitted duplicate evidence ids", request_id)
        if rubric_point_id not in accepted_points:
            continue
        if item["location"] != "answer_text":
            _fail_evidence("evidence must reference answer_text", request_id)
        if not isinstance(item["text_excerpt"], str) or not item["text_excerpt"].strip():
            _fail_evidence("evidence excerpt must be non-empty", request_id)
        _number_evidence(item["confidence"], "evidence confidence", request_id, 0, 1)
        excerpt = normalize_evidence_text(item["text_excerpt"])
        if not excerpt or excerpt not in normalize_evidence_text(request["answer_text"]):
            _fail_evidence("evidence excerpt does not occur in the answer", request_id)
        normalized = {
            "evidence_id": evidence_id,
            "rubric_point_id": rubric_point_id,
            "text_excerpt": item["text_excerpt"],
            "location": "answer_text",
            "confidence": item["confidence"],
        }
        evidence_by_id[evidence_id] = normalized
        evidence.append(normalized)

    referenced_evidence_ids = set()
    for point in matched:
        for evidence_id in point["evidence_ids"]:
            linked = evidence_by_id.get(evidence_id)
            if not linked or linked["rubric_point_id"] != point["rubric_point_id"]:
                _fail_evidence("matched point evidence link is invalid", request_id)
            referenced_evidence_ids.add(evidence_id)
    evidence = [item for item in evidence if item["evidence_id"] in referenced_evidence_ids]

    risk_flags = []
    for flag in _model_array(raw, "risk_flags", request_id, 20):
        mapped = MODEL_RISK_FLAGS.get(flag) if isinstance(flag, str) else None
        if not mapped:
            _fail_model("model emitted an unsupported risk flag", request_id)
        _append_unique(risk_flags, mapped)
    _append_unique(risk_flags, "score_needs_review")
    _append_unique(risk_flags, "human_review_required")
    if request["ocr_confidence"] < OCR_REVIEW_THRESHOLD:
        _append_unique(risk_flags, "ocr_low_confidence")
    if request["prompt_guard"]["suspected_injection"]:
        _append_unique(risk_flags, "prompt_injection_suspected")
    if telemetry.get("repair_attempted"):
        _append_unique(risk_flags, "schema_repaired")

    # 只对证据校验后保留的采分点重算建议总分，不采用模型自行报告的总分。
    score = sum(point["score"] for point in matched)
    uses_chinese = any("\u3400" <= character <= "\u9fff" for character in request["question_text"])
    matched_labels = [point["label"] for point in matched]
    missing_labels = [point["label"] for point in missing]
    if uses_chinese:
        feedback = f"已识别采分点：{'、'.join(matched_labels) or '无'}；待教师核查或补充：{'、'.join(missing_labels) or '无'}。最终结果以教师复核为准。"
        teacher_note = "本地模型置信度尚未校准；当前结果仅作影子建议，教师必须复核。"
    else:
        feedback = f"Recognized rubric points: {', '.join(matched_labels) or 'none'}. Review or complete: {', '.join(missing_labels) or 'none'}. The teacher makes the final decision."
        teacher_note = "Local model confidence is not calibrated. This shadow suggestion requires teacher review."

    suggestion = {
        "schema_version": SCHEMA_VERSION,
        "request_id": request_id,
        "status": "suggestion",
        "delivery": route["delivery"],
        "suggested_score": max(0, min(score, request["max_score"])),
        "max_score": request["max_score"],
        # 模型自报置信度尚未校准，不能把它转换成可自动放行的可靠度。
        "confidence": 0,
        "matched_points": matched,
        "missing_points": missing,
        "deductions": [],
        "evidence": evidence,
        "risk_flags": risk_flags,
        "needs_human_review": True,
        "student_feedback": feedback,
        "teacher_note": teacher_note,
        "model_version": model_version,
        "prompt_version": prompt_version,
        "rubric_version": request["rubric_version"],
        "capability_profile": profile_id,
        "mock": False,
        "telemetry": telemetry,
    }
    validate_suggestion(suggestion, request)
    return suggestion


def validate_suggestion(suggestion, request):
    request_id = request["request_id"]
    if suggestion.get("request_id") != request_id:
        _fail_model("suggestion request id mismatch", request_id)
    if suggestion.get("needs_human_review") is not True or suggestion.get("mock") is not False:
        _fail_model("suggestion governance flags are invalid", request_id)
    if suggestion.get("delivery") not in {"teacher_suggestion", "shadow_only"}:
        _fail_model("suggestion delivery is invalid", request_id)
    if suggestion.get("rubric_version") != request["rubric_version"] or suggestion.get("prompt_version") != request["prompt_version"]:
        _fail_model("suggestion version mismatch", request_id)
    score = sum(point["score"] for point in suggestion["matched_points"])
    if not math.isclose(score, suggestion["suggested_score"], abs_tol=1e-6):
        _fail_model("suggested score was not code-recomputed", request_id)
    if score > request["max_score"]:
        _fail_model("suggested score exceeds max score", request_id)
    if not set(suggestion["risk_flags"]).issubset(CANONICAL_RISK_FLAGS):
        _fail_model("suggestion contains an unsupported platform risk flag", request_id)


def _model_array(raw, field, request_id, maximum):
    value = raw.get(field)
    if not isinstance(value, list) or len(value) > maximum:
        _fail_model(f"{field} must be a bounded array", request_id)
    return value


def _number_model(value, field, request_id, minimum, maximum):
    if not _is_number(value) or value < minimum or value > maximum:
        _fail_model(f"{field} is outside its allowed range", request_id)


def _number_evidence(value, field, request_id, minimum, maximum):
    if not _is_number(value) or value < minimum or value > maximum:
        _fail_evidence(f"{field} is outside its allowed range", request_id)


def _fail_model(message, request_id):
    raise AgentError("model_output_invalid", message, status=502, request_id=request_id)


def _fail_evidence(message, request_id):
    raise AgentError("evidence_verification_failed", message, status=502, request_id=request_id)


def _append_unique(values, value):
    if value not in values:
        values.append(value)
