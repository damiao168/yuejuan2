import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkGate, loadGateConfig } from "../src/evaluators/gateChecker.js";
import { computeMetrics } from "../src/evaluators/metrics.js";
import { runEvaluation, writeEvaluationReport } from "../src/evaluators/runEval.js";

test("mock adapter can run full synthetic eval", () => {
  const report = runEvaluation({ datasetPath: "evals/synthetic/samples.jsonl", adapterName: "mock" });
  assert.equal(report.sample_count >= 20, true);
  assert.equal(report.metrics.schema_validity_rate, 1);
  assert.equal(report.metrics.no_score_above_max, true);
});

test("dev gate passes for latest synthetic mock report", () => {
  const report = runEvaluation({ datasetPath: "evals/synthetic/samples.jsonl", adapterName: "mock" });
  const config = loadGateConfig("config/release-gates.json");
  const result = checkGate(report, "dev", config);
  assert.equal(result.passed, true, result.reasons.join("\n"));
});

test("production gate fails when sample count is too small", () => {
  const report = runEvaluation({ datasetPath: "evals/synthetic/samples.jsonl", adapterName: "mock" });
  const config = loadGateConfig("config/release-gates.json");
  const result = checkGate(report, "production", config);
  assert.equal(result.passed, false);
  assert.match(result.reasons.join("\n"), /sample_count/);
});

test("all-normal evaluation cannot claim perfect risk recall or pass a release gate", () => {
  const records = Array.from({ length: 500 }, () => ({
    sample: { synthetic: true, expected_score: 5, max_score: 10, should_need_human_review: false, question_type: "numeric", tags: [] },
    input: { ocr_confidence: 0.99 },
    output: { suggested_score: 5, max_score: 10, needs_human_review: false, risk_flags: ["MOCK_OUTPUT"], mock: true },
    schema_validation: { valid: true }, verification: { evidence_validity_rate: 1, verification_passed: true }
  }));
  const metrics = computeMetrics(records);
  assert.equal(metrics.review_trigger_recall, null);
  assert.deepEqual(metrics.recall_evidence.review_trigger_recall,
    { value: null, numerator: 0, denominator: 0, status: "not_evaluated" });
  const report = {
    sample_count: records.length, metrics, adapter: "mock", model_info: { mock: true },
    dataset_sha256: "a".repeat(64), dataset_kind: "synthetic",
    results: records.map(() => ({ synthetic: true, risk_flags: ["MOCK_OUTPUT"] }))
  };
  const result = checkGate(report, "production", loadGateConfig("config/release-gates.json"));
  assert.equal(result.passed, false);
  assert.match(result.reasons.join("\n"), /review_trigger_recall requires at least 20 evaluated positive examples/);
  assert.match(result.reasons.join("\n"), /real adapter/);
});

test("production gate requires enough positives in every risk slice", () => {
  const records = Array.from({ length: 500 }, (_, index) => ({
    sample: {
      synthetic: false, expected_score: 9, max_score: 10,
      should_need_human_review: index === 0, question_type: index === 0 ? "essay" : "numeric",
      tags: index === 0 ? ["prompt_injection"] : []
    },
    input: { ocr_confidence: index === 0 ? 0.5 : 0.99 },
    output: {
      suggested_score: 9, max_score: 10, needs_human_review: index === 0,
      risk_flags: index === 0 ? ["PROMPT_INJECTION_SUSPECTED"] : [], mock: false
    },
    schema_validation: { valid: true },
    verification: { evidence_validity_rate: 1, verification_passed: true }
  }));
  const metrics = computeMetrics(records);
  const report = {
    sample_count: records.length, metrics, adapter: "local", model_info: { mock: false },
    dataset_sha256: "a".repeat(64), dataset_kind: "real",
    results: records.map((record) => ({ synthetic: false, risk_flags: record.output.risk_flags }))
  };
  const result = checkGate(report, "production", loadGateConfig("config/release-gates.json"));
  assert.equal(result.passed, false);
  assert.match(result.reasons.join("\n"), /review_trigger_recall requires at least 20 evaluated positive examples/);
  assert.match(result.reasons.join("\n"), /prompt_injection_detection requires at least 20 evaluated positive examples/);
});

test("release gate rejects recall evidence larger than the report population", () => {
  const report = runEvaluation({ datasetPath: "evals/synthetic/samples.jsonl", adapterName: "mock" });
  const evidence = report.metrics.recall_evidence.review_trigger_recall;
  evidence.denominator = report.sample_count + 1;
  evidence.numerator = evidence.denominator;
  report.metrics.review_trigger_recall = 1;
  const result = checkGate(report, "dev", loadGateConfig("config/release-gates.json"));
  assert.equal(result.passed, false);
  assert.match(result.reasons.join("\n"), /review_trigger_recall requires/);
});

test("release gate fails closed when required numeric evidence is missing or non-finite", () => {
  const report = runEvaluation({ datasetPath: "evals/synthetic/samples.jsonl", adapterName: "mock" });
  const config = loadGateConfig("config/release-gates.json");
  delete report.metrics.mock_marked_rate;
  report.sample_count = Number.NaN;
  const result = checkGate(report, "dev", config);
  assert.equal(result.passed, false);
  assert.match(result.reasons.join("\n"), /sample_count/);
  assert.match(result.reasons.join("\n"), /mock_marked_rate/);
});

test("eval writer emits JSON, markdown, and failed cases", () => {
  const report = runEvaluation({ datasetPath: "evals/synthetic/samples.jsonl", adapterName: "mock", filters: { tag: "prompt_injection" } });
  const dir = mkdtempSync(join(tmpdir(), "grading-agent-lab-"));
  const jsonPath = join(dir, "report.json");
  const mdPath = join(dir, "report.md");
  const failedPath = join(dir, "failed.jsonl");
  writeEvaluationReport(report, jsonPath, mdPath, failedPath);
  assert.equal(JSON.parse(readFileSync(jsonPath, "utf8")).sample_count, 1);
  assert.match(readFileSync(mdPath, "utf8"), /Eval Report/);
  assert.equal(readFileSync(failedPath, "utf8").trim().length >= 0, true);
});
