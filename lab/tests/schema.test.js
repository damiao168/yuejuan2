import test from "node:test";
import assert from "node:assert/strict";
import { validateGradingInput, validateGradingOutput, validateRubric } from "../src/schemas/gradingSchema.js";
import { baseInput, baseOutput, baseRubric } from "./helpers.js";

test("valid input and output pass schema validation", () => {
  const input = baseInput();
  assert.equal(validateGradingInput(input).valid, true);
  assert.equal(validateGradingOutput(baseOutput(), input).valid, true);
});

test("output cannot exceed max score", () => {
  const validation = validateGradingOutput(baseOutput({ suggested_score: 3 }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /cannot exceed/);
});

test("output cannot use negative score", () => {
  const validation = validateGradingOutput(baseOutput({ suggested_score: -1 }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), />= 0/);
});

test("confidence must be between 0 and 1", () => {
  const validation = validateGradingOutput(baseOutput({ confidence: 1.2 }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /confidence/);
});

test("model, prompt, and rubric versions are required", () => {
  const validation = validateGradingOutput(baseOutput({ model_version: "" }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /model_version/);
});

test("mock output must be marked with MOCK_OUTPUT", () => {
  const validation = validateGradingOutput(baseOutput({ risk_flags: [] }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /MOCK_OUTPUT/);
});

test("low OCR confidence requires human review", () => {
  const input = baseInput({ ocr_confidence: 0.5 });
  const validation = validateGradingOutput(baseOutput({ needs_human_review: false }), input);
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /low OCR/);
});

test("essay output requires human review", () => {
  const rubric = baseRubric({
    question_type: "essay",
    max_score: 2,
    dimensions: ["content"],
    points: [
      { id: "p1", description: "topic", score: 2, required: true, aliases: ["environment"], evidence_required: true }
    ]
  });
  const input = baseInput({
    subject: "english",
    question_type: "essay",
    rubric,
    answer_text: "environment"
  });
  const output = baseOutput({
    max_score: 2,
    suggested_score: 2,
    matched_points: [{ rubric_point_id: "p1", score: 2, evidence_ids: ["ev-p1"] }],
    evidence: [{ evidence_id: "ev-p1", rubric_point_id: "p1", text_excerpt: "environment", location: "answer_text", confidence: 0.9 }],
    needs_human_review: false,
    rubric_version: rubric.rubric_version
  });
  const validation = validateGradingOutput(output, input);
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /essay/);
});

test("matched required rubric point must have evidence", () => {
  const validation = validateGradingOutput(baseOutput({ evidence: [], matched_points: [{ rubric_point_id: "p1", score: 1, evidence_ids: [] }], suggested_score: 1 }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /lacks evidence/);
});

test("malformed output arrays return validation errors instead of throwing", () => {
  const validation = validateGradingOutput(baseOutput({ evidence: {}, matched_points: {} }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /must be an array/);
});

test("every rubric point must be classified exactly once", () => {
  const duplicate = validateGradingOutput(baseOutput({
    missing_points: [{ rubric_point_id: "p1", reason: "also missing" }]
  }), baseInput());
  assert.equal(duplicate.valid, false);
  assert.match(duplicate.errors.join("\n"), /classified more than once/);

  const incomplete = validateGradingOutput(baseOutput({
    suggested_score: 1,
    matched_points: [{ rubric_point_id: "p1", score: 1, evidence_ids: ["ev-p1"] }],
    evidence: [baseOutput().evidence[0]]
  }), baseInput());
  assert.equal(incomplete.valid, false);
  assert.match(incomplete.errors.join("\n"), /was not classified: p2/);
});

test("matched point score and evidence links must obey their rubric contract", () => {
  const validation = validateGradingOutput(baseOutput({
    suggested_score: 2,
    matched_points: [
      { rubric_point_id: "p1", score: 2, evidence_ids: ["ev-p1", "ev-p1"] },
      { rubric_point_id: "p2", score: 0, evidence_ids: ["ev-p2"] }
    ]
  }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /exceeds rubric allowance/);
  assert.match(validation.errors.join("\n"), /duplicate id/);
});

test("missing rubric points require a known id and a reason", () => {
  const validation = validateGradingOutput(baseOutput({
    suggested_score: 1,
    matched_points: [{ rubric_point_id: "p1", score: 1, evidence_ids: ["ev-p1"] }],
    missing_points: [{ rubric_point_id: "unknown", reason: "" }],
    evidence: [baseOutput().evidence[0]]
  }), baseInput());
  assert.equal(validation.valid, false);
  assert.match(validation.errors.join("\n"), /reason is required/);
  assert.match(validation.errors.join("\n"), /does not exist/);
});

test("rubric validation preserves point, alias, deduction, and question errors in order", () => {
  const rubric = baseRubric({
    synthetic: true,
    max_score: 3,
    question_type: "essay",
    points: [
      null,
      false,
      { ...baseRubric().points[0], aliases: ["\uFF21"] },
      { ...baseRubric().points[1], aliases: ["a"] },
      { ...baseRubric().points[1], aliases: ["b"] }
    ],
    deductions: [null, { id: "", max_deduction: -1 }]
  });
  // 全角别名经 NFKC 后与半角别名相同，仍须按采分点归属报告冲突。
  assert.deepEqual(validateRubric(rubric).errors, [
    "rubric.points[0] must be an object",
    "rubric.points[1] must be an object",
    "rubric alias is shared by multiple points: a",
    "rubric point id is duplicated: p2",
    "rubric.deductions[0] must be an object",
    "rubric.deductions[1].id is required",
    "rubric.deductions[1].max_deduction must be >= 0",
    "essay rubric must declare dimensions"
  ]);
});

test("input rubric binding errors follow rubric validation", () => {
  const input = baseInput({
    synthetic: true,
    max_score: 3,
    rubric_version: "other-version",
    rubric: baseRubric({ synthetic: true, rubric_id: "" })
  });
  assert.deepEqual(validateGradingInput(input).errors, [
    "input.rubric.rubric_id is required",
    "input.max_score must equal input.rubric.max_score",
    "input.rubric_version must equal input.rubric.rubric_version"
  ]);
});

test("output validation accumulates structural and binding errors in order", () => {
  const evidence = baseOutput({ synthetic: true }).evidence[0];
  const output = baseOutput({
    synthetic: true,
    model_version: "",
    risk_flags: ["MOCK_OUTPUT", "MOCK_OUTPUT", "UNKNOWN"],
    evidence: [evidence, structuredClone(evidence)],
    matched_points: [{ rubric_point_id: "p1", score: 3, evidence_ids: ["ev-p1", "ev-p1", ""] }],
    missing_points: [{ rubric_point_id: "p1", reason: "" }],
    deductions: [null]
  });
  assert.deepEqual(validateGradingOutput(output, baseInput({ synthetic: true })).errors, [
    "output.model_version is required",
    "output.risk_flags contains duplicate flag: MOCK_OUTPUT",
    "output.risk_flags contains unsupported flag: UNKNOWN",
    "output.evidence contains duplicate evidence_id: ev-p1",
    "output.matched_points[0].evidence_ids contains duplicate id: ev-p1",
    "output.matched_points[0].evidence_ids[2] must be a non-empty string",
    "output.matched_points score total cannot exceed output.max_score",
    "output rubric point is classified more than once: p1",
    "output.missing_points[0].reason is required",
    "output.deductions[0] must be an object",
    "matched point score exceeds rubric allowance: p1",
    "matched point has invalid evidence_id link: p1:",
    "rubric point was not classified: p2"
  ]);
});

test("output binding requires valid input and skips absent input", () => {
  const output = baseOutput({ synthetic: true, request_id: "other-request" });
  assert.deepEqual(validateGradingOutput(output), { valid: true, errors: [] });
  for (const input of [null, [], 1]) {
    assert.deepEqual(validateGradingOutput(output, input), {
      valid: false, errors: ["output validation input is invalid: input must be an object"]
    });
  }
  const invalid = validateGradingOutput(output, { ...baseInput({ synthetic: true }), rubric: null });
  assert.equal(invalid.valid, false);
  assert.ok(invalid.errors.every((error) => error.startsWith("output validation input is invalid: ")));
  assert.deepEqual(validateGradingOutput(output, baseInput({ synthetic: true })).errors, [
    "output.request_id must equal input.request_id"
  ]);
});
