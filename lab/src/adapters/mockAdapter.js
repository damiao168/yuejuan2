import { OCR_REVIEW_THRESHOLD, validateGradingOutput } from "../schemas/gradingSchema.js";
import { detectPromptInjection } from "../guardrails/promptInjection.js";

function findFirstEvidence(answerText, aliases) {
  const lower = answerText.toLowerCase();
  for (const alias of aliases) {
    const candidate = String(alias);
    if (!candidate) continue;
    if (lower.includes(candidate.toLowerCase())) return candidate;
  }
  return undefined;
}

export class MockGradingAdapter {
  constructor(options = {}) {
    this.modelVersion = options.modelVersion ?? "mock-rules-v1";
  }

  get_model_info() {
    return {
      adapter: "mock",
      model_version: this.modelVersion,
      mock: true,
      note: "Deterministic local rules for tests only; not a real model capability."
    };
  }

  supports_vision() {
    return false;
  }

  supports_structured_output() {
    return true;
  }

  grade(input) {
    const answerText = String(input.answer_text ?? "");
    const matchedPoints = [];
    const missingPoints = [];
    const evidence = [];
    let score = 0;

    // 按别名出现与否构造可重复的测试结果；不代表模型理解或真实判分能力。
    for (const point of input.rubric.points) {
      const aliases = [...(point.aliases ?? []), point.description];
      const excerpt = findFirstEvidence(answerText, aliases);
      if (excerpt) {
        const evidenceId = `ev-${point.id}`;
        matchedPoints.push({ rubric_point_id: point.id, score: point.score, evidence_ids: [evidenceId] });
        evidence.push({
          evidence_id: evidenceId,
          rubric_point_id: point.id,
          text_excerpt: excerpt,
          location: "answer_text",
          confidence: 0.95
        });
        score += point.score;
      } else {
        missingPoints.push({ rubric_point_id: point.id, reason: "alias_not_found_by_mock_rules" });
      }
    }

    const noRubricEvidence = answerText.trim().length > 0 && matchedPoints.length === 0;
    const ambiguousAnswer = /maybe|some force thing|not sure|可能|大概|不确定/i.test(answerText);
    const riskFlags = ["MOCK_OUTPUT"];
    const injection = detectPromptInjection(answerText);
    if (injection.detected) riskFlags.push("PROMPT_INJECTION_SUSPECTED", "HUMAN_REVIEW_REQUIRED");
    if (input.ocr_confidence < OCR_REVIEW_THRESHOLD) riskFlags.push("OCR_LOW_CONFIDENCE", "HUMAN_REVIEW_REQUIRED");
    if (answerText.trim().length === 0 || noRubricEvidence) {
      riskFlags.push("INSUFFICIENT_EVIDENCE", "HUMAN_REVIEW_REQUIRED");
    }
    if (noRubricEvidence || ambiguousAnswer) riskFlags.push("AMBIGUOUS_ANSWER");

    const needsHumanReview =
      ["essay", "discussion"].includes(input.question_type) ||
      input.ocr_confidence < OCR_REVIEW_THRESHOLD ||
      answerText.trim().length === 0 ||
      noRubricEvidence ||
      ambiguousAnswer ||
      injection.detected;

    if (needsHumanReview && !riskFlags.includes("HUMAN_REVIEW_REQUIRED")) riskFlags.push("HUMAN_REVIEW_REQUIRED");

    const output = {
      request_id: input.request_id,
      suggested_score: Math.min(score, input.max_score),
      max_score: input.max_score,
      confidence: matchedPoints.length === input.rubric.points.length ? 0.82 : 0.62,
      matched_points: matchedPoints,
      missing_points: missingPoints,
      deductions: [],
      evidence,
      risk_flags: [...new Set(riskFlags)],
      needs_human_review: needsHumanReview,
      student_feedback: needsHumanReview
        ? "This answer needs teacher review before any final score is used."
        : "The answer matched the available rubric evidence in the lab rules.",
      teacher_note: "Mock adapter used deterministic alias matching only; do not treat this as real AI grading.",
      model_version: this.modelVersion,
      prompt_version: input.prompt_version,
      rubric_version: input.rubric_version,
      mock: true
    };
    const validation = validateGradingOutput(output, input);
    if (!validation.valid) {
      throw new Error(`mock adapter produced invalid output: ${validation.errors.join("; ")}`);
    }
    return output;
  }
}
