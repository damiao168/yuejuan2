import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { createAdapter } from "../adapters/index.js";
import { createHash } from "node:crypto";
import { applyEvidenceVerification, verifyEvidence } from "../evidenceVerifier.js";
import { validateGradingInput, validateGradingOutput } from "../schemas/gradingSchema.js";
import { computeMetrics } from "./metrics.js";

export function readJsonl(filePath) {
  return readFileSync(filePath, "utf8")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line, index) => {
      try {
        return JSON.parse(line);
      } catch (error) {
        throw new Error(`Invalid JSONL at ${filePath}:${index + 1}: ${error.message}`);
      }
    });
}

export function sampleToInput(sample) {
  return {
    request_id: sample.sample_id,
    tenant_id: sample.tenant_id,
    exam_id: sample.exam_id,
    question_id: sample.question_id ?? `${sample.sample_id}-q`,
    answer_segment_id: sample.answer_segment_id ?? `${sample.sample_id}-a`,
    subject: sample.subject,
    grade_level: sample.grade_level,
    question_type: sample.question_type,
    question_text: sample.question_text,
    max_score: sample.max_score,
    rubric: sample.rubric,
    answer_text: sample.answer_text,
    answer_image_ref: sample.answer_image_ref,
    ocr_confidence: sample.ocr_confidence ?? 1,
    model_policy: sample.model_policy ?? { adapter: "mock", final_score_allowed: false },
    prompt_version: sample.prompt_version ?? "prompt-base-v1",
    rubric_version: sample.rubric.rubric_version
  };
}

export function filterSamples(samples, filters = {}) {
  return samples.filter((sample) => {
    if (filters.subject && sample.subject !== filters.subject) return false;
    if (filters.question_type && sample.question_type !== filters.question_type) return false;
    if (filters.tag && !(sample.tags ?? []).includes(filters.tag)) return false;
    return true;
  });
}

export function runEvaluation({ datasetPath, adapterName = "mock", filters = {} }) {
  const adapter = createAdapter(adapterName);
  const samples = filterSamples(readJsonl(datasetPath), filters);
  const records = samples.map((sample) => {
    const input = sampleToInput(sample);
    const inputValidation = validateGradingInput(input);
    if (!inputValidation.valid) {
      throw new Error(`Invalid sample input ${sample.sample_id}: ${inputValidation.errors.join("; ")}`);
    }
    const rawOutput = adapter.grade(input);
    const adjustedOutput = applyEvidenceVerification(input, rawOutput);
    const schemaValidation = validateGradingOutput(adjustedOutput, input);
    const verification = verifyEvidence(input, adjustedOutput);
    return {
      sample,
      input,
      output: adjustedOutput,
      schema_validation: schemaValidation,
      verification
    };
  });
  const metrics = computeMetrics(records);
  return {
    generated_at: new Date().toISOString(),
    dataset_path: datasetPath,
    dataset_sha256: createHash("sha256").update(readFileSync(datasetPath)).digest("hex"),
    dataset_kind: records.every((record) => record.sample.synthetic === true) ? "synthetic" : records.every((record) => record.sample.synthetic === false) ? "real" : "mixed",
    adapter: adapterName,
    model_info: adapter.get_model_info(),
    prompt_version: "prompt-base-v1",
    sample_count: records.length,
    metrics,
    results: records.map((record) => ({
      sample_id: record.sample.sample_id,
      synthetic: record.sample.synthetic === true,
      subject: record.sample.subject,
      question_type: record.sample.question_type,
      expected_score: record.sample.expected_score,
      suggested_score: record.output.suggested_score,
      needs_human_review: record.output.needs_human_review,
      risk_flags: record.output.risk_flags,
      schema_valid: record.schema_validation.valid,
      evidence_valid: record.verification.verification_passed
    }))
  };
}

export async function runEvaluationAsync({ datasetPath, adapterName = "mock", adapterOptions = {}, filters = {} }) {
  const adapter = createAdapter(adapterName, adapterOptions);
  const samples = filterSamples(readJsonl(datasetPath), filters);
  const records = [];
  for (const sample of samples) {
    const input = sampleToInput(sample);
    const inputValidation = validateGradingInput(input);
    if (!inputValidation.valid) {
      throw new Error(`Invalid sample input ${sample.sample_id}: ${inputValidation.errors.join("; ")}`);
    }
    const rawOutput = await adapter.grade(input);
    const adjustedOutput = applyEvidenceVerification(input, rawOutput);
    records.push({
      sample,
      input,
      output: adjustedOutput,
      schema_validation: validateGradingOutput(adjustedOutput, input),
      verification: verifyEvidence(input, adjustedOutput)
    });
  }
  const metrics = computeMetrics(records);
  return {
    generated_at: new Date().toISOString(),
    dataset_path: datasetPath,
    dataset_sha256: createHash("sha256").update(readFileSync(datasetPath)).digest("hex"),
    dataset_kind: records.every((record) => record.sample.synthetic === true) ? "synthetic" : records.every((record) => record.sample.synthetic === false) ? "real" : "mixed",
    adapter: adapterName,
    model_info: adapter.get_model_info(),
    prompt_version: "prompt-base-v1",
    sample_count: records.length,
    metrics,
    results: records.map((record) => ({
      sample_id: record.sample.sample_id,
      synthetic: record.sample.synthetic === true,
      subject: record.sample.subject,
      question_type: record.sample.question_type,
      expected_score: record.sample.expected_score,
      suggested_score: record.output.suggested_score,
      needs_human_review: record.output.needs_human_review,
      risk_flags: record.output.risk_flags,
      schema_valid: record.schema_validation.valid,
      evidence_valid: record.verification.verification_passed
    }))
  };
}

export function failedCasesFromReport(report, records = report.results) {
  return records.filter((record) => record.schema_valid !== true || record.evidence_valid !== true || Math.abs(record.expected_score - record.suggested_score) > 1);
}

export function writeEvaluationReport(report, jsonPath, markdownPath, failedCasesPath) {
  mkdirSync(dirname(jsonPath), { recursive: true });
  writeFileSync(jsonPath, `${JSON.stringify(report, null, 2)}\n`, "utf8");
  const lines = [
    "# Grading Agent Eval Report",
    "",
    `Generated: ${report.generated_at}`,
    `Dataset: ${report.dataset_path}`,
    `Adapter: ${report.adapter}`,
    `Samples: ${report.sample_count}`,
    "",
    "## Metrics",
    "",
    ...Object.entries(report.metrics).map(([key, value]) => `- ${key}: ${typeof value === "object" ? JSON.stringify(value) : value}`),
    ""
  ];
  writeFileSync(markdownPath, `${lines.join("\n")}\n`, "utf8");
  const failedCases = failedCasesFromReport(report);
  writeFileSync(failedCasesPath, failedCases.map((item) => JSON.stringify(item)).join("\n") + (failedCases.length ? "\n" : ""), "utf8");
}
