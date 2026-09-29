import test from "node:test";
import assert from "node:assert/strict";
import { LocalModelAdapter } from "../src/adapters/localModelAdapter.js";
import { validateGradingOutput } from "../src/schemas/gradingSchema.js";
import { baseInput } from "./helpers.js";

function successfulRawOutput(overrides = {}) {
  return {
    // 故意给出越界总分，验证适配器按得分点重算，而不是照信模型总分。
    suggested_score: 99,
    confidence: 0.7,
    matched_points: [
      { rubric_point_id: "p1", score: 1, evidence_ids: ["ev-p1"] },
      { rubric_point_id: "p2", score: 1, evidence_ids: ["ev-p2"] }
    ],
    missing_points: [],
    deductions: [],
    evidence: [
      { evidence_id: "ev-p1", rubric_point_id: "p1", text_excerpt: "2x=6", location: "answer_text", confidence: 0.9 },
      { evidence_id: "ev-p2", rubric_point_id: "p2", text_excerpt: "x=3", location: "answer_text", confidence: 0.9 }
    ],
    risk_flags: [],
    needs_human_review: false,
    student_feedback: "The required steps are present.",
    teacher_note: "Review the cited steps.",
    ...overrides
  };
}

function responseWith(raw, ok = true, status = 200) {
  return { ok, status, json: async () => ({ choices: [{ message: { content: JSON.stringify(raw) } }] }) };
}

test("local adapter recomputes score and forces teacher review", async () => {
  const calls = [];
  const adapter = new LocalModelAdapter({ fetchImpl: async (url, options) => { calls.push({ url, options }); return responseWith(successfulRawOutput()); } });
  const input = baseInput({ prompt_version: "prompt-local-structured-v1" });
  const output = await adapter.grade(input);
  assert.equal(output.suggested_score, 2);
  assert.equal(output.mock, false);
  assert.equal(output.confidence, 0);
  assert.equal(output.needs_human_review, true);
  assert.ok(output.risk_flags.includes("HUMAN_REVIEW_REQUIRED"));
  assert.ok(output.risk_flags.includes("SCORE_NEEDS_REVIEW"));
  assert.match(output.teacher_note, /review is mandatory/);
  assert.equal(validateGradingOutput(output, input).valid, true);
  const body = JSON.parse(calls[0].options.body);
  assert.equal(body.temperature, 0);
  assert.equal(body.chat_template_kwargs.enable_thinking, false);
  assert.equal(body.response_format.type, "json_schema");
  if (adapter.apiKey) assert.match(calls[0].options.headers.authorization, /^Bearer /);
});

test("local adapter retries one invalid JSON response", async () => {
  let calls = 0;
  const adapter = new LocalModelAdapter({
    fetchImpl: async () => {
      calls += 1;
      if (calls === 1) return { ok: true, status: 200, json: async () => ({ choices: [{ message: { content: "not-json" } }] }) };
      return responseWith(successfulRawOutput());
    }
  });
  const output = await adapter.grade(baseInput({ prompt_version: "prompt-local-structured-v1" }));
  assert.equal(calls, 2);
  assert.deepEqual(adapter.get_last_run_info().prior_error_codes, ["INVALID_JSON"]);
  assert.ok(output.risk_flags.includes("SCHEMA_REPAIRED"));
});

test("local adapter rejects point scores above rubric allowance", async () => {
  const raw = successfulRawOutput();
  raw.matched_points[0].score = 2;
  const adapter = new LocalModelAdapter({ maxRetries: 0, fetchImpl: async () => responseWith(raw) });
  await assert.rejects(() => adapter.grade(baseInput({ prompt_version: "prompt-local-structured-v1" })), /POINT_SCORE_OUT_OF_RANGE/);
});

test("local adapter rejects incomplete rubric classification", async () => {
  const raw = successfulRawOutput({ matched_points: [], missing_points: [{ rubric_point_id: "p1", reason: "missing" }], evidence: [] });
  const adapter = new LocalModelAdapter({ maxRetries: 0, fetchImpl: async () => responseWith(raw) });
  await assert.rejects(() => adapter.grade(baseInput({ prompt_version: "prompt-local-structured-v1" })), /INCOMPLETE_RUBRIC_CLASSIFICATION/);
});

test("local adapter rejects malformed arrays through the governed schema", async () => {
  const raw = successfulRawOutput({ matched_points: {} });
  const adapter = new LocalModelAdapter({ maxRetries: 0, fetchImpl: async () => responseWith(raw) });
  await assert.rejects(
    () => adapter.grade(baseInput({ prompt_version: "prompt-local-structured-v1" })),
    /SCHEMA_INVALID/
  );
});

test("local adapter rejects missing-point reasons and model deductions", async () => {
  const missingReason = successfulRawOutput({
    matched_points: [{ rubric_point_id: "p1", score: 1, evidence_ids: ["ev-p1"] }],
    missing_points: [{ rubric_point_id: "p2" }],
    evidence: [{ evidence_id: "ev-p1", rubric_point_id: "p1", text_excerpt: "2x=6", location: "answer_text", confidence: 0.9 }]
  });
  const missingAdapter = new LocalModelAdapter({ maxRetries: 0, fetchImpl: async () => responseWith(missingReason) });
  await assert.rejects(
    () => missingAdapter.grade(baseInput({ prompt_version: "prompt-local-structured-v1" })),
    /SCHEMA_INVALID/
  );

  const deductionsAdapter = new LocalModelAdapter({
    maxRetries: 0,
    fetchImpl: async () => responseWith(successfulRawOutput({ deductions: [{ reason: "invented" }] }))
  });
  await assert.rejects(
    () => deductionsAdapter.grade(baseInput({ prompt_version: "prompt-local-structured-v1" })),
    /SCHEMA_INVALID/
  );
});

test("strict alias policy downgrades a semantically wrong numeric result", async () => {
  const input = baseInput({
    answer_text: "2x=6, but x=4",
    prompt_version: "prompt-local-structured-v2",
    rubric: {
      ...baseInput().rubric,
      points: [
        { ...baseInput().rubric.points[0], match_policy: "strict_alias" },
        { ...baseInput().rubric.points[1], match_policy: "strict_alias" }
      ]
    }
  });
  const raw = successfulRawOutput({
    evidence: [
      { evidence_id: "ev-p1", rubric_point_id: "p1", text_excerpt: "2x=6", location: "answer_text", confidence: 0.9 },
      { evidence_id: "ev-p2", rubric_point_id: "p2", text_excerpt: "x=4", location: "answer_text", confidence: 0.9 }
    ]
  });
  const adapter = new LocalModelAdapter({ maxRetries: 0, fetchImpl: async () => responseWith(raw) });
  const output = await adapter.grade(input);
  assert.equal(output.suggested_score, 1);
  assert.deepEqual(output.matched_points.map((point) => point.rubric_point_id), ["p1"]);
  assert.deepEqual(output.missing_points.map((point) => point.rubric_point_id), ["p2"]);
  assert.equal(output.evidence.some((item) => item.rubric_point_id === "p2"), false);
});

test("student feedback language is owned by code", async () => {
  const input = baseInput({ question_text: "说明方程的解。", prompt_version: "prompt-local-structured-v2" });
  const adapter = new LocalModelAdapter({ fetchImpl: async () => responseWith(successfulRawOutput({ student_feedback: "Wrong language" })) });
  const output = await adapter.grade(input);
  assert.match(output.student_feedback, /教师复核/);
  assert.doesNotMatch(output.student_feedback, /Wrong language/);
  assert.doesNotMatch(output.student_feedback, /none/);
  assert.match(output.student_feedback, /uses equation/);
});
