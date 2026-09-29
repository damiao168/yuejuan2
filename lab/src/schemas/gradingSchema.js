export const OCR_REVIEW_THRESHOLD = 0.85;

export const QUESTION_TYPES = Object.freeze([
  "fill_blank",
  "numeric",
  "short_answer",
  "calculation",
  "essay",
  "discussion"
]);

export const SUBJECTS = Object.freeze([
  "chinese",
  "math",
  "english",
  "physics",
  "chemistry",
  "biology",
  "history",
  "politics",
  "geography",
  "computer_science"
]);

export const RISK_FLAGS = Object.freeze([
  "OCR_LOW_CONFIDENCE",
  "OCR_TEXT_EMPTY_REVIEW_REQUIRED",
  "AMBIGUOUS_ANSWER",
  "INSUFFICIENT_EVIDENCE",
  "POSSIBLE_OFF_TOPIC",
  "SCORE_NEEDS_REVIEW",
  "SCHEMA_REPAIRED",
  "PROMPT_INJECTION_SUSPECTED",
  "HUMAN_REVIEW_REQUIRED",
  "MOCK_OUTPUT"
]);

const isObject = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
const isNonEmptyString = (value) => typeof value === "string" && value.trim().length > 0;
const isNumber = (value) => typeof value === "number" && Number.isFinite(value);
const round = (value) => Math.round(value * 1000000) / 1000000;

export function validationResult(errors) {
  return { valid: errors.length === 0, errors };
}

export function validateRubricPoint(point, index = 0) {
  const errors = [];
  if (!isObject(point)) {
    return [`rubric.points[${index}] must be an object`];
  }
  if (!isNonEmptyString(point.id)) errors.push(`rubric.points[${index}].id is required`);
  if (!isNonEmptyString(point.description)) errors.push(`rubric.points[${index}].description is required`);
  if (!isNumber(point.score) || point.score <= 0) errors.push(`rubric.points[${index}].score must be > 0`);
  if (typeof point.required !== "boolean") errors.push(`rubric.points[${index}].required must be boolean`);
  if (!Array.isArray(point.aliases)) errors.push(`rubric.points[${index}].aliases must be an array`);
  if (Array.isArray(point.aliases)) {
    point.aliases.forEach((alias, aliasIndex) => {
      if (!isNonEmptyString(alias)) errors.push(`rubric.points[${index}].aliases[${aliasIndex}] must be a non-empty string`);
    });
  }
  if (typeof point.evidence_required !== "boolean") {
    errors.push(`rubric.points[${index}].evidence_required must be boolean`);
  }
  if (point.match_policy !== undefined && !["semantic", "strict_alias"].includes(point.match_policy)) {
    errors.push(`rubric.points[${index}].match_policy is unsupported`);
  }
  return errors;
}

export function validateRubric(rubric) {
  const errors = [];
  if (!isObject(rubric)) return validationResult(["rubric must be an object"]);

  if (!isNonEmptyString(rubric.rubric_id)) errors.push("rubric.rubric_id is required");
  if (!isNonEmptyString(rubric.rubric_version)) errors.push("rubric.rubric_version is required");
  if (!isNumber(rubric.max_score) || rubric.max_score <= 0) errors.push("rubric.max_score must be > 0");
  if (!Array.isArray(rubric.points) || rubric.points.length === 0) {
    errors.push("rubric.points must be a non-empty array");
  }
  for (const key of ["deductions", "equivalent_answers", "examples", "scoring_notes"]) {
    if (!Array.isArray(rubric[key])) errors.push(`rubric.${key} must be an array`);
  }
  if (Array.isArray(rubric.points)) {
    const seen = new Set();
    const aliasOwners = new Map();
    let total = 0;
    rubric.points.forEach((point, index) => {
      errors.push(...validateRubricPoint(point, index));
      if (point && point.id) {
        if (seen.has(point.id)) errors.push(`rubric point id is duplicated: ${point.id}`);
        seen.add(point.id);
      }
      if (isNumber(point?.score)) total += point.score;
      for (const alias of Array.isArray(point?.aliases) ? point.aliases : []) {
        if (!isNonEmptyString(alias)) continue;
        const normalizedAlias = alias.normalize("NFKC").trim().toLowerCase();
        if (aliasOwners.has(normalizedAlias) && aliasOwners.get(normalizedAlias) !== point.id) {
          errors.push(`rubric alias is shared by multiple points: ${alias}`);
        } else aliasOwners.set(normalizedAlias, point.id);
      }
    });
    if (!rubric.allow_partial_total && isNumber(rubric.max_score) && round(total) !== round(rubric.max_score)) {
      errors.push(`rubric points total ${round(total)} must equal max_score ${rubric.max_score}`);
    }
  }
  if (Array.isArray(rubric.deductions)) {
    rubric.deductions.forEach((deduction, index) => {
      if (!isObject(deduction)) errors.push(`rubric.deductions[${index}] must be an object`);
      if (isObject(deduction) && !isNonEmptyString(deduction.id)) {
        errors.push(`rubric.deductions[${index}].id is required`);
      }
      if (isObject(deduction) && (!isNumber(deduction.max_deduction) || deduction.max_deduction < 0)) {
        errors.push(`rubric.deductions[${index}].max_deduction must be >= 0`);
      }
    });
  }
  if (rubric.question_type === "essay" && !rubric.dimensions) {
    errors.push("essay rubric must declare dimensions");
  }
  if (rubric.question_type === "calculation" && !rubric.steps) {
    errors.push("calculation rubric must declare steps");
  }
  return validationResult(errors);
}

export function validateGradingInput(input) {
  const errors = [];
  if (!isObject(input)) return validationResult(["input must be an object"]);

  const requiredStrings = [
    "request_id",
    "question_id",
    "answer_segment_id",
    "grade_level",
    "question_text",
    "prompt_version",
    "rubric_version"
  ];
  for (const field of requiredStrings) {
    if (!isNonEmptyString(input[field])) errors.push(`input.${field} is required`);
  }
  if (!SUBJECTS.includes(input.subject)) errors.push(`input.subject is unsupported: ${input.subject}`);
  if (!QUESTION_TYPES.includes(input.question_type)) errors.push(`input.question_type is unsupported: ${input.question_type}`);
  if (!isNumber(input.max_score) || input.max_score <= 0) errors.push("input.max_score must be > 0");
  if (typeof input.answer_text !== "string") errors.push("input.answer_text must be a string");
  if (input.answer_image_ref !== undefined && typeof input.answer_image_ref !== "string") {
    errors.push("input.answer_image_ref must be a string when present");
  }
  if (!isNumber(input.ocr_confidence) || input.ocr_confidence < 0 || input.ocr_confidence > 1) {
    errors.push("input.ocr_confidence must be between 0 and 1");
  }
  if (!isObject(input.model_policy)) errors.push("input.model_policy is required");

  const rubricValidation = validateRubric(input.rubric);
  errors.push(...rubricValidation.errors.map((error) => `input.${error}`));
  if (isObject(input.rubric) && isNumber(input.max_score) && isNumber(input.rubric.max_score)) {
    if (round(input.max_score) !== round(input.rubric.max_score)) {
      errors.push("input.max_score must equal input.rubric.max_score");
    }
  }
  if (isObject(input.rubric) && input.rubric_version !== input.rubric.rubric_version) {
    errors.push("input.rubric_version must equal input.rubric.rubric_version");
  }
  return validationResult(errors);
}

export function validateEvidence(evidence, index = 0) {
  const errors = [];
  if (!isObject(evidence)) return [`evidence[${index}] must be an object`];
  if (!isNonEmptyString(evidence.evidence_id)) errors.push(`evidence[${index}].evidence_id is required`);
  if (!isNonEmptyString(evidence.rubric_point_id)) errors.push(`evidence[${index}].rubric_point_id is required`);
  if (!isNonEmptyString(evidence.text_excerpt)) errors.push(`evidence[${index}].text_excerpt is required`);
  if (evidence.location !== undefined && typeof evidence.location !== "string") {
    errors.push(`evidence[${index}].location must be a string when present`);
  }
  if (!isNumber(evidence.confidence) || evidence.confidence < 0 || evidence.confidence > 1) {
    errors.push(`evidence[${index}].confidence must be between 0 and 1`);
  }
  return errors;
}

export function validateGradingOutput(output, input = undefined) {
  const errors = [];
  if (!isObject(output)) return validationResult(["output must be an object"]);
  const evidenceItems = Array.isArray(output.evidence) ? output.evidence : [];
  const matchedItems = Array.isArray(output.matched_points) ? output.matched_points : [];
  const missingItems = Array.isArray(output.missing_points) ? output.missing_points : [];
  // 命中和缺失共用集合，防止同一个采分点被重复或矛盾归类。
  const classifiedPointIds = new Set();

  const requiredStrings = [
    "request_id",
    "student_feedback",
    "teacher_note",
    "model_version",
    "prompt_version",
    "rubric_version"
  ];
  for (const field of requiredStrings) {
    if (!isNonEmptyString(output[field])) errors.push(`output.${field} is required`);
  }
  if (!isNumber(output.suggested_score) || output.suggested_score < 0) {
    errors.push("output.suggested_score must be >= 0");
  }
  if (!isNumber(output.max_score) || output.max_score <= 0) errors.push("output.max_score must be > 0");
  if (isNumber(output.suggested_score) && isNumber(output.max_score) && output.suggested_score > output.max_score) {
    errors.push("output.suggested_score cannot exceed output.max_score");
  }
  if (!isNumber(output.confidence) || output.confidence < 0 || output.confidence > 1) {
    errors.push("output.confidence must be between 0 and 1");
  }
  for (const key of ["matched_points", "missing_points", "deductions", "evidence", "risk_flags"]) {
    if (!Array.isArray(output[key])) errors.push(`output.${key} must be an array`);
  }
  if (typeof output.needs_human_review !== "boolean") errors.push("output.needs_human_review must be boolean");
  if (typeof output.mock !== "boolean") errors.push("output.mock must be boolean");

  if (Array.isArray(output.risk_flags)) {
    const seenFlags = new Set();
    output.risk_flags.forEach((flag) => {
      if (!RISK_FLAGS.includes(flag)) errors.push(`output.risk_flags contains unsupported flag: ${flag}`);
      if (seenFlags.has(flag)) errors.push(`output.risk_flags contains duplicate flag: ${flag}`);
      seenFlags.add(flag);
    });
  }
  if (Array.isArray(output.evidence)) {
    const evidenceIds = new Set();
    output.evidence.forEach((evidence, index) => {
      errors.push(...validateEvidence(evidence, index));
      if (isNonEmptyString(evidence?.evidence_id)) {
        if (evidenceIds.has(evidence.evidence_id)) errors.push(`output.evidence contains duplicate evidence_id: ${evidence.evidence_id}`);
        evidenceIds.add(evidence.evidence_id);
      }
    });
  }
  if (Array.isArray(output.matched_points)) {
    let total = 0;
    output.matched_points.forEach((point, index) => {
      if (!isObject(point)) {
        errors.push(`output.matched_points[${index}] must be an object`);
        return;
      }
      if (!isNonEmptyString(point.rubric_point_id)) errors.push(`output.matched_points[${index}].rubric_point_id is required`);
      if (isNonEmptyString(point.rubric_point_id)) {
        if (classifiedPointIds.has(point.rubric_point_id)) {
          errors.push(`output rubric point is classified more than once: ${point.rubric_point_id}`);
        }
        classifiedPointIds.add(point.rubric_point_id);
      }
      if (!isNumber(point.score) || point.score < 0) errors.push(`output.matched_points[${index}].score must be >= 0`);
      if (!Array.isArray(point.evidence_ids)) errors.push(`output.matched_points[${index}].evidence_ids must be an array`);
      if (Array.isArray(point.evidence_ids)) {
        const linkedIds = new Set();
        point.evidence_ids.forEach((evidenceId, evidenceIndex) => {
          if (!isNonEmptyString(evidenceId)) {
            errors.push(`output.matched_points[${index}].evidence_ids[${evidenceIndex}] must be a non-empty string`);
          } else if (linkedIds.has(evidenceId)) {
            errors.push(`output.matched_points[${index}].evidence_ids contains duplicate id: ${evidenceId}`);
          }
          linkedIds.add(evidenceId);
        });
      }
      if (isNumber(point.score)) total += point.score;
    });
    if (isNumber(output.max_score) && total > output.max_score) {
      errors.push("output.matched_points score total cannot exceed output.max_score");
    }
  }
  if (Array.isArray(output.missing_points)) {
    output.missing_points.forEach((point, index) => {
      if (!isObject(point)) {
        errors.push(`output.missing_points[${index}] must be an object`);
        return;
      }
      if (!isNonEmptyString(point.rubric_point_id)) {
        errors.push(`output.missing_points[${index}].rubric_point_id is required`);
      } else {
        if (classifiedPointIds.has(point.rubric_point_id)) {
          errors.push(`output rubric point is classified more than once: ${point.rubric_point_id}`);
        }
        classifiedPointIds.add(point.rubric_point_id);
      }
      if (!isNonEmptyString(point.reason)) errors.push(`output.missing_points[${index}].reason is required`);
    });
  }
  if (Array.isArray(output.deductions)) {
    output.deductions.forEach((deduction, index) => {
      if (!isObject(deduction)) errors.push(`output.deductions[${index}] must be an object`);
    });
  }
  if (output.mock === true && !output.risk_flags?.includes("MOCK_OUTPUT")) {
    errors.push("mock output must include MOCK_OUTPUT risk flag");
  }
  if (output.mock === false && output.risk_flags?.includes("MOCK_OUTPUT")) {
    errors.push("non-mock output cannot include MOCK_OUTPUT risk flag");
  }

  const inputValidation = input === undefined ? undefined : validateGradingInput(input);
  if (inputValidation && !inputValidation.valid) {
    errors.push(...inputValidation.errors.map((error) => `output validation input is invalid: ${error}`));
  }
  // 只有输入本身有效，才能依其量规校验输出关联关系，避免把损坏输入当作权威。
  if (inputValidation?.valid) {
    if (output.request_id !== input.request_id) errors.push("output.request_id must equal input.request_id");
    if (round(output.max_score) !== round(input.max_score)) errors.push("output.max_score must equal input.max_score");
    if (output.prompt_version !== input.prompt_version) errors.push("output.prompt_version must equal input.prompt_version");
    if (output.rubric_version !== input.rubric_version) errors.push("output.rubric_version must equal input.rubric_version");
    if (["essay", "discussion"].includes(input.question_type) && output.needs_human_review !== true) {
      errors.push("essay and discussion outputs must set needs_human_review=true");
    }
    if (input.ocr_confidence < OCR_REVIEW_THRESHOLD && output.needs_human_review !== true) {
      errors.push("low OCR confidence outputs must set needs_human_review=true");
    }
    const rubricPoints = new Map(input.rubric.points.map((point) => [point.id, point]));
    const evidenceById = new Map(evidenceItems.map((evidence) => [evidence?.evidence_id, evidence]));
    const evidenceByRubricPoint = new Map();
    for (const evidence of evidenceItems) {
      if (!isObject(evidence)) continue;
      if (!rubricPoints.has(evidence.rubric_point_id)) {
        errors.push(`evidence rubric point does not exist: ${evidence.rubric_point_id}`);
      }
      if (!evidenceByRubricPoint.has(evidence.rubric_point_id)) evidenceByRubricPoint.set(evidence.rubric_point_id, []);
      evidenceByRubricPoint.get(evidence.rubric_point_id).push(evidence);
    }
    for (const point of matchedItems) {
      if (!isObject(point)) continue;
      const rubricPoint = rubricPoints.get(point.rubric_point_id);
      if (!rubricPoint) {
        errors.push(`matched rubric point does not exist: ${point.rubric_point_id}`);
        continue;
      }
      if (isNumber(point.score) && point.score > rubricPoint.score) {
        errors.push(`matched point score exceeds rubric allowance: ${point.rubric_point_id}`);
      }
      if (rubricPoint.required && rubricPoint.evidence_required) {
        const linkedEvidence = evidenceByRubricPoint.get(point.rubric_point_id) ?? [];
        if (linkedEvidence.length === 0 || (Array.isArray(point.evidence_ids) && point.evidence_ids.length === 0)) {
          errors.push(`matched required point lacks evidence: ${point.rubric_point_id}`);
        }
      }
      for (const evidenceId of Array.isArray(point.evidence_ids) ? point.evidence_ids : []) {
        const linked = evidenceById.get(evidenceId);
        if (!linked || linked.rubric_point_id !== point.rubric_point_id) {
          errors.push(`matched point has invalid evidence_id link: ${point.rubric_point_id}:${evidenceId}`);
        }
      }
    }
    for (const point of missingItems) {
      if (isObject(point) && isNonEmptyString(point.rubric_point_id) && !rubricPoints.has(point.rubric_point_id)) {
        errors.push(`missing rubric point does not exist: ${point.rubric_point_id}`);
      }
    }
    // 每个量规点都必须显式归为命中或缺失，不能通过漏报难判项目提高完整性。
    for (const rubricPointId of rubricPoints.keys()) {
      if (!classifiedPointIds.has(rubricPointId)) errors.push(`rubric point was not classified: ${rubricPointId}`);
    }
  }
  return validationResult(errors);
}
