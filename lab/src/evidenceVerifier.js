import { OCR_REVIEW_THRESHOLD } from "./schemas/gradingSchema.js";
import { detectPromptInjection } from "./guardrails/promptInjection.js";

// 引文与严格别名保留标点和数学符号，只容忍字宽、大小写与空白差异，不能把不同数值改成相同证据。
export function normalizeText(text) {
  return String(text ?? "")
    .normalize("NFKC")
    .toLowerCase()
    .replace(/\s+/g, "");
}

function pushUnique(list, value) {
  if (!list.includes(value)) list.push(value);
}

function indexEvidence(evidenceItems, rubricPointIds, invalidPoints) {
  const evidenceByRubricPoint = new Map();
  const evidenceById = new Map();
  for (const evidence of evidenceItems) {
    if (evidence === null || typeof evidence !== "object" || Array.isArray(evidence)) {
      invalidPoints.push({ reason: "evidence_item_invalid" });
      continue;
    }
    if (evidenceById.has(evidence.evidence_id)) {
      invalidPoints.push({ evidence_id: evidence.evidence_id, reason: "duplicate_evidence_id" });
    }
    // 重复 ID 仍报告失败，索引保留后项；按量规点分组则保留全部证据和原顺序。
    evidenceById.set(evidence.evidence_id, evidence);
    if (!rubricPointIds.has(evidence.rubric_point_id)) {
      invalidPoints.push({ rubric_point_id: evidence.rubric_point_id, reason: "evidence_rubric_point_not_found" });
    }
    if (!evidenceByRubricPoint.has(evidence.rubric_point_id)) evidenceByRubricPoint.set(evidence.rubric_point_id, []);
    evidenceByRubricPoint.get(evidence.rubric_point_id).push(evidence);
  }
  return { evidenceByRubricPoint, evidenceById };
}

function collectEvidenceReviewFlags(input, answerText) {
  const forcedRiskFlags = [];
  let forcedNeedsHumanReview = false;
  if (input.ocr_confidence < OCR_REVIEW_THRESHOLD) {
    pushUnique(forcedRiskFlags, "OCR_LOW_CONFIDENCE");
    pushUnique(forcedRiskFlags, "HUMAN_REVIEW_REQUIRED");
    forcedNeedsHumanReview = true;
  }
  if (answerText.trim().length === 0) {
    pushUnique(forcedRiskFlags, "INSUFFICIENT_EVIDENCE");
    pushUnique(forcedRiskFlags, "HUMAN_REVIEW_REQUIRED");
    forcedNeedsHumanReview = true;
    if (input.answer_image_ref) pushUnique(forcedRiskFlags, "OCR_TEXT_EMPTY_REVIEW_REQUIRED");
  }

  const injection = detectPromptInjection(answerText);
  for (const flag of injection.risk_flags) pushUnique(forcedRiskFlags, flag);
  if (injection.detected) forcedNeedsHumanReview = true;
  return { forcedRiskFlags, forcedNeedsHumanReview };
}

function verifyMatchedEvidenceLinks(matchedPoint, rubricPointId, evidenceById, invalidPoints) {
  let idLinksValid = true;
  const evidenceIds = Array.isArray(matchedPoint.evidence_ids) ? matchedPoint.evidence_ids : [];
  if (!Array.isArray(matchedPoint.evidence_ids)) {
    invalidPoints.push({ rubric_point_id: rubricPointId, reason: "evidence_id_links_invalid" });
    idLinksValid = false;
  }
  for (const evidenceId of evidenceIds) {
    const linkedById = evidenceById.get(evidenceId);
    if (!linkedById || linkedById.rubric_point_id !== rubricPointId) {
      invalidPoints.push({ rubric_point_id: rubricPointId, evidence_id: evidenceId, reason: "evidence_id_link_invalid" });
      idLinksValid = false;
    }
  }
  return idLinksValid;
}

function verifyMatchedPoint(matchedPoint, input, rubricPointIds, evidenceByRubricPoint, evidenceById, normalizedAnswer, invalidPoints) {
  if (matchedPoint === null || typeof matchedPoint !== "object" || Array.isArray(matchedPoint)) {
    invalidPoints.push({ reason: "matched_point_invalid" });
    return { score: 0, valid: false };
  }
  const rubricPointId = matchedPoint.rubric_point_id;
  let matchedScore = Number(matchedPoint.score);
  if (!Number.isFinite(matchedScore) || matchedScore < 0) {
    invalidPoints.push({ rubric_point_id: rubricPointId, reason: "matched_point_score_invalid" });
    matchedScore = 0;
  }
  // 分数累计与证据有效率分别记录；未知量规或无效证据不会撤销已读到的合法分数。
  if (!rubricPointIds.has(rubricPointId)) {
    invalidPoints.push({ rubric_point_id: rubricPointId, reason: "rubric_point_id_not_found" });
    return { score: matchedScore, valid: false };
  }
  const rubricPoint = input.rubric.points.find((point) => point.id === rubricPointId);
  const linkedEvidence = evidenceByRubricPoint.get(rubricPointId) ?? [];
  if (rubricPoint.evidence_required && linkedEvidence.length === 0) {
    invalidPoints.push({ rubric_point_id: rubricPointId, reason: "required_evidence_missing" });
    return { score: matchedScore, valid: false };
  }
  const idLinksValid = verifyMatchedEvidenceLinks(matchedPoint, rubricPointId, evidenceById, invalidPoints);
  // 片段必须能在学生原文的归一化结果中找到；这里只核对引用来源，不判断语义正确性。
  const evidenceIsValid = linkedEvidence.every((evidence) => {
    const excerpt = normalizeText(evidence.text_excerpt);
    return excerpt.length > 0 && normalizedAnswer.includes(excerpt);
  });
  if (!evidenceIsValid) {
    invalidPoints.push({ rubric_point_id: rubricPointId, reason: "evidence_excerpt_not_in_answer" });
    return { score: matchedScore, valid: false };
  }
  return { score: matchedScore, valid: idLinksValid };
}

function verifyMatchedPoints(matchedItems, input, rubricPointIds, evidenceByRubricPoint, evidenceById, normalizedAnswer, invalidPoints) {
  let matchedScoreTotal = 0;
  let validMatchedPoints = 0;
  for (const matchedPoint of matchedItems) {
    const result = verifyMatchedPoint(matchedPoint, input, rubricPointIds, evidenceByRubricPoint, evidenceById, normalizedAnswer, invalidPoints);
    matchedScoreTotal += result.score;
    if (result.valid) validMatchedPoints += 1;
  }
  return { matchedScoreTotal, validMatchedPoints };
}

function verifyMissingAndDeductions(input, missingItems, deductionItems, rubricPointIds, invalidPoints) {
  for (const missing of missingItems) {
    if (missing === null || typeof missing !== "object" || Array.isArray(missing)) {
      invalidPoints.push({ reason: "missing_point_invalid" });
      continue;
    }
    if (!rubricPointIds.has(missing.rubric_point_id)) {
      invalidPoints.push({ rubric_point_id: missing.rubric_point_id, reason: "missing_point_not_in_rubric" });
    }
  }
  const deductionIds = new Set((input.rubric.deductions ?? []).map((deduction) => deduction.id));
  for (const deduction of deductionItems) {
    if (deduction === null || typeof deduction !== "object" || Array.isArray(deduction)) {
      invalidPoints.push({ reason: "deduction_invalid" });
      continue;
    }
    if (deduction.rubric_deduction_id && !deductionIds.has(deduction.rubric_deduction_id)) {
      invalidPoints.push({ rubric_deduction_id: deduction.rubric_deduction_id, reason: "deduction_not_in_rubric" });
    }
  }
}

export function verifyEvidence(input, output) {
  const safeOutput = output !== null && typeof output === "object" && !Array.isArray(output) ? output : {};
  const evidenceItems = Array.isArray(safeOutput.evidence) ? safeOutput.evidence : [];
  const matchedItems = Array.isArray(safeOutput.matched_points) ? safeOutput.matched_points : [];
  const missingItems = Array.isArray(safeOutput.missing_points) ? safeOutput.missing_points : [];
  const deductionItems = Array.isArray(safeOutput.deductions) ? safeOutput.deductions : [];
  const invalidPoints = [];
  const warnings = [];
  const answerText = String(input.answer_text ?? "");
  const normalizedAnswer = normalizeText(answerText);
  const rubricPointIds = new Set(input.rubric.points.map((point) => point.id));
  const { evidenceByRubricPoint, evidenceById } = indexEvidence(evidenceItems, rubricPointIds, invalidPoints);
  const review = collectEvidenceReviewFlags(input, answerText);
  const forcedRiskFlags = review.forcedRiskFlags;
  let forcedNeedsHumanReview = review.forcedNeedsHumanReview;
  const { matchedScoreTotal, validMatchedPoints } = verifyMatchedPoints(matchedItems, input, rubricPointIds, evidenceByRubricPoint, evidenceById, normalizedAnswer, invalidPoints);
  verifyMissingAndDeductions(input, missingItems, deductionItems, rubricPointIds, invalidPoints);

  const allowance = Number(input.rubric.holistic_score_allowance ?? 0);
  if (safeOutput.suggested_score > matchedScoreTotal + allowance) {
    invalidPoints.push({ reason: "suggested_score_exceeds_matched_points", suggested_score: safeOutput.suggested_score });
  }
  if (invalidPoints.length > 0) {
    pushUnique(forcedRiskFlags, "INSUFFICIENT_EVIDENCE");
    pushUnique(forcedRiskFlags, "HUMAN_REVIEW_REQUIRED");
    forcedNeedsHumanReview = true;
  }

  const matchedCount = matchedItems.length;
  let evidenceValidityRate;
  // 没有命中点时只看答案是否非空；有效率与上面的整体校验通过状态是独立结果。
  if (matchedCount === 0) evidenceValidityRate = answerText.trim() ? 1 : 0;
  else evidenceValidityRate = validMatchedPoints / matchedCount;
  return {
    verification_passed: invalidPoints.length === 0,
    evidence_validity_rate: evidenceValidityRate,
    invalid_points: invalidPoints,
    warnings,
    forced_risk_flags: forcedRiskFlags,
    forced_needs_human_review: forcedNeedsHumanReview
  };
}

// 证据校验只追加风险并提升复核要求；不会清除模型已有风险，也不在此处重算分数。
export function applyEvidenceVerification(input, output) {
  const safeOutput = output !== null && typeof output === "object" && !Array.isArray(output) ? output : {};
  const verification = verifyEvidence(input, safeOutput);
  const riskFlags = Array.isArray(safeOutput.risk_flags) ? safeOutput.risk_flags : [];
  return {
    ...safeOutput,
    risk_flags: [...new Set([...riskFlags, ...verification.forced_risk_flags])],
    needs_human_review: safeOutput.needs_human_review === true || verification.forced_needs_human_review
  };
}
