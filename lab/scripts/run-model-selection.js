#!/usr/bin/env node
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { LocalModelAdapter, MockGradingAdapter } from "../src/adapters/index.js";
import { applyEvidenceVerification, verifyEvidence } from "../src/evidenceVerifier.js";
import { computeMetrics } from "../src/evaluators/metrics.js";
import { selectDiverseBenchmarkSamples } from "../src/evaluators/modelSelection.js";
import { readJsonl, sampleToInput } from "../src/evaluators/runEval.js";
import { getApprovedDatasetSource, validateGovernedSamples } from "../src/datasets/governance.js";
import { getModelCandidate } from "../src/runtime/modelCandidates.js";
import { validateGradingInput, validateGradingOutput } from "../src/schemas/gradingSchema.js";

function argsOf(argv) {
  const args = {};
  for (let index = 0; index < argv.length; index += 1) {
    if (!argv[index].startsWith("--")) continue;
    args[argv[index].slice(2)] = argv[index + 1];
    index += 1;
  }
  return args;
}
function percentile(values, p) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.ceil(p * sorted.length) - 1)];
}
const args = argsOf(process.argv.slice(2));
const datasetPath = args.dataset ?? "evals/model-selection/fixed-synthetic-v1.jsonl";
const candidateId = args.candidate ?? "rules_alias";
const outputPath = args.out ?? `evals/reports/model-selection-${candidateId}.json`;
const sourceId = args.source ?? "lab_synthetic";
const datasetText = readFileSync(datasetPath, "utf8");
const sourceSamples = readJsonl(datasetPath);
const limit = args.limit === undefined ? undefined : Number(args.limit);
const requestedSampleId = args["sample-id"];
const samples = requestedSampleId ? sourceSamples.filter((sample) => sample.sample_id === requestedSampleId) :
  selectDiverseBenchmarkSamples(sourceSamples, limit);
if (requestedSampleId && samples.length !== 1) throw new Error(`sample_id not found or duplicated: ${requestedSampleId}`);
const completeDataset = samples.length === sourceSamples.length;
// 诊断子集单独计算哈希并标注不完整，防止与完整数据集的选型报告混比。
const evaluatedDatasetText = completeDataset ? datasetText : `${samples.map((sample) => JSON.stringify(sample)).join("\n")}\n`;
const datasetSource = getApprovedDatasetSource(sourceId, "evaluation");
const governance = validateGovernedSamples(samples, datasetSource);
if (!governance.valid) throw new Error(`Model selection dataset failed governance: ${governance.errors.join("; ")}`);
const invalidInputs = samples.map((sample) => ({ sample_id: sample.sample_id, validation: validateGradingInput(sampleToInput(sample)) }))
  .filter((item) => !item.validation.valid);
if (invalidInputs.length) {
  throw new Error(`Model selection inputs are invalid: ${invalidInputs.slice(0, 5).map((item) => `${item.sample_id}: ${item.validation.errors.join("; ")}`).join(" | ")}`);
}
let adapter;
if (candidateId === "rules_alias") {
  adapter = new MockGradingAdapter({ modelVersion: "deterministic-alias-baseline-v1" });
} else {
  const candidate = getModelCandidate(candidateId);
  adapter = new LocalModelAdapter({ modelVersion: `${candidate.repository}:${candidate.quantization}` });
}
const records = [];
const failures = [];
const latencies = [];
let firstPass = 0;
for (const sample of samples) {
  const input = sampleToInput({ ...sample, prompt_version: candidateId === "rules_alias" ? "prompt-base-v1" : "prompt-local-structured-v1" });
  const started = performance.now();
  try {
    const output = applyEvidenceVerification(input, await adapter.grade(input));
    const elapsedMs = Math.round(performance.now() - started);
    latencies.push(elapsedMs);
    const schemaValidation = validateGradingOutput(output, input);
    const verification = verifyEvidence(input, output);
    const run = adapter.get_last_run_info?.();
    if (!run || run.attempts === 1) firstPass += 1;
    records.push({ sample, input, output, schema_validation: schemaValidation, verification, latency_ms: elapsedMs, adapter_run: run });
  } catch (error) {
    failures.push({ sample_id: sample.sample_id, code: error.code ?? "ERROR", message: error.message, latency_ms: Math.round(performance.now() - started) });
  }
}
// 质量指标只统计完成的样本；失败另计 completion_rate，解读时必须同时看完成率。
const quality = computeMetrics(records);
const report = {
  schema_version: "model-selection-report-v1",
  generated_at: new Date().toISOString(),
  synthetic_only: samples.every((sample) => sample.synthetic === true),
  evidence_scope: datasetSource.evidence_scope,
  candidate_id: candidateId,
  model_info: adapter.get_model_info(),
  dataset: {
    path: datasetPath,
    sha256: createHash("sha256").update(evaluatedDatasetText).digest("hex"),
    total_samples: samples.length,
    source_total_samples: sourceSamples.length,
    source_sha256: createHash("sha256").update(datasetText).digest("hex"),
    complete_dataset: completeDataset,
    selection_method: completeDataset ? "full_dataset" :
      requestedSampleId ? "explicit_sample_id_diagnostic" : "deterministic_question_round_robin_smoke",
    source_id: datasetSource.source_id,
    evidence_scope: datasetSource.evidence_scope,
    governance_passed: true
  },
  quality,
  quality_scope: "completed_samples_only",
  operations: {
    completed: records.length,
    failed: failures.length,
    completion_rate: samples.length ? records.length / samples.length : 0,
    first_pass_rate: samples.length ? firstPass / samples.length : 0,
    latency_ms_p50: percentile(latencies, 0.5),
    latency_ms_p95: percentile(latencies, 0.95),
    latency_ms_total: latencies.reduce((sum, value) => sum + value, 0)
  },
  results: records.map((record) => ({
    sample_id: record.sample.sample_id,
    expected_score: record.sample.expected_score,
    suggested_score: record.output.suggested_score,
    schema_valid: record.schema_validation.valid,
    evidence_valid: record.verification.verification_passed,
    verification_invalid_points: record.verification.invalid_points,
    verification_warnings: record.verification.warnings,
    risk_flags: record.output.risk_flags,
    needs_human_review: record.output.needs_human_review,
    matched_point_scores: record.output.matched_points.map((point) => ({ rubric_point_id: point.rubric_point_id, score: point.score })),
    missing_point_ids: record.output.missing_points.map((point) => point.rubric_point_id),
    evidence_count: record.output.evidence.length,
    latency_ms: record.latency_ms,
    adapter_run: record.adapter_run
  })),
  failures
};
writeFileSync(outputPath, `${JSON.stringify(report, null, 2)}\n`, "utf8");
const consoleOutput = args.verbose === "true" ? report : {
  candidate_id: report.candidate_id,
  evidence_scope: report.evidence_scope,
  dataset: report.dataset,
  quality: report.quality,
  operations: report.operations,
  failure_count: report.failures.length,
  output: outputPath
};
console.log(JSON.stringify(consoleOutput, null, 2));
if (failures.length) process.exitCode = 1;
