import test from "node:test";
import assert from "node:assert/strict";
import { getApprovedDatasetSource, validateGovernedSamples } from "../src/datasets/governance.js";
import {
  JORGPT_RAW_COLUMNS,
  JORGPT_SOURCE_ID,
  minimizeJorgptSamplesForPersistence,
  participantLeakageAudit,
  redactStructuredIdentityValues,
  transformJorgptRows,
  validateJorgptHeaders
} from "../src/datasets/jorgpt.js";
import { sampleToInput } from "../src/evaluators/runEval.js";
import { validateGradingInput } from "../src/schemas/gradingSchema.js";
import { getSubjectStrategy } from "../src/subjects.js";

// 手工构造公开数据格式的样例；转换后的 synthetic=false 用于验证导入契约，不表示读取真实学生数据。
function rawRow(overrides = {}) {
  return {
    entry_id: "1",
    question_id: "Q1",
    question_text: "What does DNS do?",
    student_answer: "It resolves a domain name to an IP address.",
    ideal_answer: "DNS resolves domain names to IP addresses.",
    student_anom_id: "anonymous-source-id",
    deepseek_raw_json: "{\"grade\":9}",
    deepseek_grade: "9",
    qwen_raw_json: "{\"grade\":9}",
    qwen_grade: "9",
    gemini_raw_json: "{\"grade\":9}",
    gemini_grade: "9",
    judge_status: "OK",
    judge_raw_json: "{\"grade\":9}",
    judge_grade: "9",
    student_answer_length: "43",
    teacher_grade: "9.0",
    teacher_feedback: "Correct explanation with the key mapping.",
    teacher_corrected_at: "2026-01-01T00:00:00Z",
    teacher_corrected: "True",
    ...overrides
  };
}

test("JorGPT source is pinned for evaluation but not training", () => {
  const source = getApprovedDatasetSource(JORGPT_SOURCE_ID, "evaluation");
  assert.equal(source.license_id, "CC-BY-4.0");
  assert.equal(source.source_type, "public_real_external_benchmark");
  assert.throws(() => getApprovedDatasetSource(JORGPT_SOURCE_ID, "training"), /does not allow training/);
});

test("JorGPT importer contract rejects changed columns", () => {
  assert.deepEqual(validateJorgptHeaders(JORGPT_RAW_COLUMNS), { valid: true, missing: [], unexpected: [] });
  const validation = validateJorgptHeaders(JORGPT_RAW_COLUMNS.filter((column) => column !== "teacher_grade").concat("unknown"));
  assert.equal(validation.valid, false);
  assert.deepEqual(validation.missing, ["teacher_grade"]);
  assert.deepEqual(validation.unexpected, ["unknown"]);
});

test("JorGPT transform minimizes fields and passes governance and grading schema", () => {
  const samples = transformJorgptRows([
    rawRow(),
    rawRow({ entry_id: "2", question_id: "Q2", question_text: "What does DHCP do?", ideal_answer: "DHCP assigns network configuration.", student_answer: "", teacher_grade: "0.0", teacher_feedback: "No answer." })
  ]);
  assert.equal(samples.length, 2);
  assert.equal(samples[0].synthetic, false);
  assert.equal(samples[0].subject, "computer_science");
  assert.equal(samples[0].expected_score, 9);
  assert.equal(samples[1].expected_score, 0);
  assert.equal(samples[0].participant_group_id.length, 64);
  assert.equal(samples[0].participant_group_id, samples[1].participant_group_id);
  assert.equal("student_anom_id" in samples[0], false);
  assert.equal("qwen_raw_json" in samples[0], false);
  assert.equal("teacher_corrected_at" in samples[0], false);
  assert.equal(samples[0].training_eligible, false);

  const persisted = minimizeJorgptSamplesForPersistence(samples);
  assert.equal("participant_group_id" in persisted[0], false);

  const source = getApprovedDatasetSource(JORGPT_SOURCE_ID, "evaluation");
  assert.deepEqual(validateGovernedSamples(samples, source), { valid: true, errors: [] });
  for (const sample of samples) assert.deepEqual(validateGradingInput(sampleToInput(sample)), { valid: true, errors: [] });
});

test("JorGPT transform rejects duplicate records and invalid human grades", () => {
  assert.throws(() => transformJorgptRows([rawRow(), rawRow()]), /duplicate JorGPT entry_id/);
  assert.throws(() => transformJorgptRows([rawRow({ teacher_grade: "9.5" })]), /teacher_grade must be an integer/);
});

test("JorGPT import redacts structured identity values without weakening prose", () => {
  const redacted = redactStructuredIdentityValues("Example: {name: Javier, surname: Argenta, grade: 9}. It has a descriptive name: useful.");
  assert.equal(redacted.redactions, 2);
  assert.equal(redacted.text, "Example: {name: [redacted], surname: [redacted], grade: 9}. It has a descriptive name: useful.");
  const [sample] = transformJorgptRows([rawRow({ student_answer: "{name: Javier, surname: Argenta, grade: 9}" })]);
  assert.equal(sample.privacy_redaction_count, 2);
  assert.equal(sample.answer_text.includes("Javier"), false);
  assert.equal(sample.answer_text.includes("Argenta"), false);
});

test("question-held-out split reports participant overlap instead of hiding it", () => {
  const samples = transformJorgptRows([
    rawRow(),
    rawRow({ entry_id: "2", question_id: "Q2", question_text: "What does DHCP do?", ideal_answer: "DHCP assigns network configuration." })
  ]);
  assert.deepEqual(participantLeakageAudit({ train: [samples[0]], test: [samples[1]] }), {
    passed: false,
    unique_participants: 1,
    participants_across_multiple_partitions: 1
  });
});

test("computer science remains an always-reviewed external subject strategy", () => {
  const strategy = getSubjectStrategy("computer_science");
  assert.equal(strategy.default_review_policy, "always_review_external_technical_answers");
  assert.ok(strategy.required_risk_flags.includes("HUMAN_REVIEW_REQUIRED"));
});
