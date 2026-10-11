import test from "node:test";
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";
import { checkGate, loadGateConfig } from "../src/evaluators/gateChecker.js";
import { computeMetrics } from "../src/evaluators/metrics.js";
import { MockGradingAdapter } from "../src/adapters/index.js";
import { runEvaluation, runEvaluationAsync, writeEvaluationReport } from "../src/evaluators/runEval.js";

function syntheticEvaluationSample(sampleId, overrides = {}) {
  return {
    synthetic: true,
    sample_id: sampleId,
    subject: "math",
    grade_level: "synthetic-grade",
    question_type: "short_answer",
    question_text: "synthetic unit question",
    max_score: 1,
    expected_score: 1,
    answer_text: "unit",
    ocr_confidence: 1,
    should_need_human_review: false,
    tags: [],
    rubric: {
      rubric_id: "synthetic-rubric",
      rubric_version: "synthetic-rubric-v1",
      max_score: 1,
      points: [{ id: "p1", description: "unit", score: 1, required: true, aliases: ["unit"], evidence_required: true }],
      deductions: [], equivalent_answers: [], examples: [], scoring_notes: []
    },
    ...overrides
  };
}

// 临时输入由显式合成夹具生成，不读取仓库数据集或真实学生内容。
function temporaryEvaluationDataset(t, samples) {
  const dir = mkdtempSync(join(tmpdir(), "lab-evaluation-batch24-"));
  t.after(() => {
    // 递归清理只允许本测试的临时子目录，避免路径变化时删除其他目录。
    const cleanupPath = resolve(dir);
    if (dirname(cleanupPath) !== resolve(tmpdir()) || !basename(cleanupPath).startsWith("lab-evaluation-batch24-")) {
      throw new Error("refusing to remove an unexpected synthetic evaluation temp directory");
    }
    rmSync(cleanupPath, { recursive: true, force: true });
  });
  const datasetPath = join(dir, "synthetic-test-input.jsonl");
  writeFileSync(datasetPath, `${samples.map((sample) => JSON.stringify(sample)).join("\n")}\n`, "utf8");
  return { dir, datasetPath };
}

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

// 这里手工构造真实报告的元数据来测试准入条件；数据仍是内存测试夹具，不是真实评估证据。
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

test("sync and async synthetic eval preserve classification, review flags and serial sample order", async (t) => {
  const { datasetPath } = temporaryEvaluationDataset(t, [
    syntheticEvaluationSample("first"),
    syntheticEvaluationSample("discussion", { question_type: "discussion", should_need_human_review: true }),
    syntheticEvaluationSample("low-ocr", { ocr_confidence: 0.5, should_need_human_review: true })
  ]);
  const synchronous = runEvaluation({ datasetPath });
  const grade = MockGradingAdapter.prototype.grade;
  const calls = [];
  t.mock.method(MockGradingAdapter.prototype, "grade", async function (input) {
    calls.push(`start:${input.request_id}`);
    // 让出一次执行权，才能发现后一条样本是否在前一条完成前启动。
    await Promise.resolve();
    const output = grade.call(this, input);
    calls.push(`end:${input.request_id}`);
    return output;
  });
  const asynchronous = await runEvaluationAsync({ datasetPath });
  assert.deepEqual({ ...asynchronous, generated_at: synchronous.generated_at }, synchronous);
  assert.equal(asynchronous.dataset_kind, "synthetic");
  assert.deepEqual(asynchronous.results.map((record) => record.sample_id), ["first", "discussion", "low-ocr"]);
  assert.deepEqual(asynchronous.results.map((record) => record.needs_human_review), [false, true, true]);
  assert.ok(asynchronous.results.every((record) => record.schema_valid && record.evidence_valid && record.risk_flags.includes("MOCK_OUTPUT")));
  assert.deepEqual(calls, ["start:first", "end:first", "start:discussion", "end:discussion", "start:low-ocr", "end:low-ocr"]);
});

test("sync and async empty filtered eval stay synthetic and hash the full input file", async (t) => {
  const { datasetPath } = temporaryEvaluationDataset(t, [syntheticEvaluationSample("filtered")]);
  const full = runEvaluation({ datasetPath });
  const options = { datasetPath, filters: { tag: "not-in-the-synthetic-fixture" } };
  const reports = [runEvaluation(options), await runEvaluationAsync(options)];
  for (const report of reports) {
    assert.equal(report.dataset_kind, "synthetic");
    assert.equal(report.sample_count, 0);
    assert.deepEqual(report.results, []);
    assert.equal(report.dataset_sha256, full.dataset_sha256);
    assert.equal(report.metrics.schema_validity_rate, 0);
  }
});

test("sync and async eval stop before grading an invalid sample or its successors", async (t) => {
  const { datasetPath } = temporaryEvaluationDataset(t, [
    syntheticEvaluationSample("first"),
    syntheticEvaluationSample("invalid", { question_text: "" }),
    syntheticEvaluationSample("unreached")
  ]);
  const grade = MockGradingAdapter.prototype.grade;
  const calls = [];
  t.mock.method(MockGradingAdapter.prototype, "grade", function (input) {
    calls.push(input.request_id);
    return grade.call(this, input);
  });
  const expected = { message: "Invalid sample input invalid: input.question_text is required" };
  assert.throws(() => runEvaluation({ datasetPath }), expected);
  assert.deepEqual(calls, ["first"]);
  calls.length = 0;
  await assert.rejects(() => runEvaluationAsync({ datasetPath }), expected);
  assert.deepEqual(calls, ["first"]);
});

test("eval writer keeps JSON and stops before failed cases when the markdown directory is missing", (t) => {
  const { dir, datasetPath } = temporaryEvaluationDataset(t, [syntheticEvaluationSample("writer")]);
  const report = runEvaluation({ datasetPath });
  const jsonPath = join(dir, "json", "report.json");
  const markdownPath = join(dir, "missing-markdown-directory", "report.md");
  const failedPath = join(dir, "failed-cases.jsonl");
  assert.throws(() => writeEvaluationReport(report, jsonPath, markdownPath, failedPath), { code: "ENOENT" });
  assert.deepEqual(JSON.parse(readFileSync(jsonPath, "utf8")), report);
  assert.equal(existsSync(markdownPath), false);
  assert.equal(existsSync(failedPath), false);
});
