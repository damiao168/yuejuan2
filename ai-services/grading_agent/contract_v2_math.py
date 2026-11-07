"""数学 OCR 证据的结构与坐标校验，不参与模型采分点映射决策。"""

import math

from .contract import _exact_fields, _fail, _is_number, _number_model, _string

_BBOX_FIELDS = {"x", "y", "width", "height"}


_MATH_FIELDS = {"artifact_id", "artifact_version", "correction_revision", "exam_question_snapshot_id", "scoring_version", "overall_confidence", "quality", "steps", "formulas", "verifications", "rubric_evidence", "has_diagram", "graph_uncertain", "required_rubric_uncertain"}


_QUALITY_FIELDS = {"recognition", "formula", "structure", "verification", "rubric_mapping", "critical"}


def validate_math_evidence(evidence, request_id):
    _validate_math_metadata(evidence, request_id)
    _validate_math_quality(evidence["quality"], request_id)
    # 步骤和公式共用身份集合，后校验的公式也不能复用已经出现的步骤编号。
    known = set()
    _validate_math_steps(evidence["steps"], known, request_id)
    _validate_math_formulas(evidence["formulas"], known, request_id)
    if not isinstance(evidence["verifications"], list) or len(evidence["verifications"]) > 2048:
        _fail("math_evidence.verifications must be bounded", request_id)
    if not isinstance(evidence["rubric_evidence"], list) or len(evidence["rubric_evidence"]) > 512:
        _fail("math_evidence.rubric_evidence must be bounded", request_id)
    for flag in ("has_diagram", "graph_uncertain", "required_rubric_uncertain"):
        if not isinstance(evidence[flag], bool):
            _fail(f"math_evidence.{flag} must be boolean", request_id)
    return known


def _validate_math_metadata(evidence, request_id):
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


def _validate_math_quality(quality, request_id):
    _exact_fields(quality, _QUALITY_FIELDS, "math_evidence.quality", request_id)
    for key in _QUALITY_FIELDS:
        _number_model(quality[key], f"math_evidence.quality.{key}", request_id, 0, 1)
    if not math.isclose(quality["critical"], min(quality[key] for key in _QUALITY_FIELDS if key != "critical"), abs_tol=1e-9):
        _fail("math_evidence.quality.critical must be the minimum dimension", request_id)


def _validate_math_steps(steps, known, request_id):
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


def _validate_math_formulas(formulas, known, request_id):
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
        _validate_formula_bbox(formula["bbox"], request_id)


def _validate_formula_bbox(bbox, request_id):
    # 公式坐标相对答案裁图，允许占满裁图及像素相除的小数；整页媒体摘要限制仍由 _bbox_units 执行。
    _exact_fields(bbox, _BBOX_FIELDS, "math_evidence.formula.bbox", request_id)
    values = (bbox["x"], bbox["y"], bbox["width"], bbox["height"])
    if any(not _is_number(value) for value in values):
        _fail("math_evidence.formula.bbox values must be finite numbers", request_id)
    x, y, width, height = values
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1 or y + height > 1:
        _fail("math_evidence.formula.bbox must stay within the answer crop", request_id)
