export function baseRubric(overrides = {}) {
  return {
    rubric_id: "rubric-test",
    rubric_version: "rubric-test-v1",
    max_score: 2,
    points: [
      { id: "p1", description: "uses equation", score: 1, required: true, aliases: ["2x=6"], evidence_required: true },
      { id: "p2", description: "x=3", score: 1, required: true, aliases: ["x=3"], evidence_required: true }
    ],
    deductions: [],
    equivalent_answers: [],
    examples: [],
    scoring_notes: [],
    ...overrides
  };
}

export function baseInput(overrides = {}) {
  const rubric = overrides.rubric ?? baseRubric();
  const input = {
    request_id: "req-test",
    question_id: "q-test",
    answer_segment_id: "a-test",
    subject: "math",
    grade_level: "grade_7",
    question_type: "short_answer",
    question_text: "Explain why x=3 solves 2x=6.",
    max_score: rubric.max_score,
    rubric,
    answer_text: "2x=6 and x=3",
    ocr_confidence: 0.99,
    model_policy: { adapter: "mock", final_score_allowed: false },
    prompt_version: "prompt-base-v1",
    rubric_version: rubric.rubric_version,
    ...overrides
  };
  // null/undefined 的 rubric 回退到默认量规；恢复选定量规，同时保留原有字段顺序及其他显式覆盖。
  input.rubric = rubric;
  return input;
}

export function baseOutput(overrides = {}) {
  return {
    request_id: "req-test",
    suggested_score: 2,
    max_score: 2,
    confidence: 0.8,
    matched_points: [
      { rubric_point_id: "p1", score: 1, evidence_ids: ["ev-p1"] },
      { rubric_point_id: "p2", score: 1, evidence_ids: ["ev-p2"] }
    ],
    missing_points: [],
    deductions: [],
    evidence: [
      { evidence_id: "ev-p1", rubric_point_id: "p1", text_excerpt: "2x=6", location: "answer_text", confidence: 0.95 },
      { evidence_id: "ev-p2", rubric_point_id: "p2", text_excerpt: "x=3", location: "answer_text", confidence: 0.95 }
    ],
    risk_flags: ["MOCK_OUTPUT"],
    needs_human_review: false,
    student_feedback: "Matched rubric evidence.",
    teacher_note: "Mock output.",
    model_version: "mock-rules-v1",
    prompt_version: "prompt-base-v1",
    rubric_version: "rubric-test-v1",
    mock: true,
    ...overrides
  };
}
