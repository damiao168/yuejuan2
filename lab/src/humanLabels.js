import { validateRubric } from "./schemas/gradingSchema.js";

// 这些模式用于拦截常见敏感信息，不能代替导入前的完整匿名化审核。
const SENSITIVE_PATTERNS = [
  { id: "phone", pattern: /\b1[3-9]\d{9}\b/ },
  { id: "student-number", pattern: /\b\d{8,18}\b/ },
  { id: "name-label", pattern: /姓名[:：]\s*\S+/ }
];

export function validateHumanLabeledSample(sample) {
  const errors = [];
  const required = [
    "sample_id",
    "subject",
    "grade_level",
    "question_type",
    "question_text",
    "max_score",
    "rubric",
    "answer_text",
    "ocr_confidence",
    "human_score",
    "human_matched_points",
    "human_missing_points",
    "human_deductions",
    "human_rationale",
    "labeler_id",
    "labeler_role",
    "label_timestamp",
    "label_confidence",
    "privacy_status",
    "synthetic"
  ];
  for (const key of required) {
    if (sample[key] === undefined || sample[key] === null || sample[key] === "") errors.push(`${key} is required`);
  }
  if (typeof sample.human_score !== "number" || sample.human_score < 0 || sample.human_score > sample.max_score) {
    errors.push("human_score must be between 0 and max_score");
  }
  if (sample.privacy_status !== "anonymized" && sample.synthetic !== true) {
    errors.push("real samples must be anonymized before import");
  }
  const rubricValidation = validateRubric(sample.rubric);
  errors.push(...rubricValidation.errors.map((error) => `rubric.${error}`));
  const rubricPointIds = new Set((sample.rubric?.points ?? []).map((point) => point.id));
  for (const id of [...(sample.human_matched_points ?? []), ...(sample.human_missing_points ?? [])]) {
    if (!rubricPointIds.has(id)) errors.push(`rubric point not found in human labels: ${id}`);
  }
  for (const detector of SENSITIVE_PATTERNS) {
    if (detector.pattern.test(sample.answer_text ?? "")) {
      errors.push(`answer_text contains sensitive pattern: ${detector.id}`);
    }
  }
  return { valid: errors.length === 0, errors };
}
