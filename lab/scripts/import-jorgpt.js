#!/usr/bin/env node
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "csv-parse/sync";
import { auditSplitLeakage, getApprovedDatasetSource, sha256Text, splitByQuestionGroup, validateGovernedSamples } from "../src/datasets/governance.js";
import {
  JORGPT_DROPPED_COLUMNS,
  JORGPT_RAW_COLUMNS,
  JORGPT_SOURCE_ID,
  minimizeJorgptSamplesForPersistence,
  participantLeakageAudit,
  transformJorgptRows,
  validateJorgptHeaders
} from "../src/datasets/jorgpt.js";
import { sampleToInput } from "../src/evaluators/runEval.js";
import { validateGradingInput } from "../src/schemas/gradingSchema.js";

const LAB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_LOCK = "config/dataset-source-locks/jorgpt-zenodo-18981627.json";
const DEFAULT_INPUT = ".downloads/jorgpt-18981627/dataset_en.csv";
const DEFAULT_OUTPUT = ".downloads/jorgpt-18981627/governable-en.jsonl";
const DEFAULT_REPORT = "evals/reports/jorgpt-import-audit.json";

function argsOf(argv) {
  const args = {};
  for (let index = 0; index < argv.length; index += 1) {
    if (!argv[index].startsWith("--")) continue;
    args[argv[index].slice(2)] = argv[index + 1];
    index += 1;
  }
  return args;
}

function labPath(path) {
  const absolute = resolve(LAB_ROOT, path);
  const rel = relative(LAB_ROOT, absolute);
  if (rel.startsWith("..") || resolve(absolute) === resolve(LAB_ROOT)) throw new Error(`path must remain under lab: ${path}`);
  return absolute;
}

function digest(algorithm, content) {
  return createHash(algorithm).update(content).digest("hex");
}

const args = argsOf(process.argv.slice(2));
const lockPath = labPath(args.lock ?? DEFAULT_LOCK);
const inputPath = labPath(args.input ?? DEFAULT_INPUT);
const outputPath = labPath(args.out ?? DEFAULT_OUTPUT);
const reportPath = labPath(args.report ?? DEFAULT_REPORT);
const lock = JSON.parse(readFileSync(lockPath, "utf8"));
if (lock.source_id !== JORGPT_SOURCE_ID) throw new Error(`unexpected source lock: ${lock.source_id}`);
if (JSON.stringify(lock.expected_columns) !== JSON.stringify(JORGPT_RAW_COLUMNS)) {
  throw new Error("source lock columns do not match importer contract");
}

const inputBytes = readFileSync(inputPath);
const integrity = {
  expected_bytes: lock.file.expected_bytes,
  actual_bytes: statSync(inputPath).size,
  expected_md5: lock.file.expected_md5,
  actual_md5: digest("md5", inputBytes),
  expected_sha256: lock.file.expected_sha256,
  actual_sha256: digest("sha256", inputBytes)
};
integrity.passed = integrity.actual_bytes === integrity.expected_bytes &&
  integrity.actual_md5 === integrity.expected_md5 && integrity.actual_sha256 === integrity.expected_sha256;
if (!integrity.passed) throw new Error("JorGPT source file integrity mismatch");

const rows = parse(inputBytes, {
  bom: true,
  columns: true,
  encoding: "utf8",
  skip_empty_lines: true
});
const headerValidation = validateJorgptHeaders(Object.keys(rows[0] ?? {}));
if (!headerValidation.valid) {
  throw new Error(`JorGPT columns changed: missing=${headerValidation.missing.join(",")} unexpected=${headerValidation.unexpected.join(",")}`);
}

const source = getApprovedDatasetSource(JORGPT_SOURCE_ID, "evaluation");
const samples = transformJorgptRows(rows);
const split = splitByQuestionGroup(samples);
const participantAudit = participantLeakageAudit(split.splits);
const questionRubricAudit = auditSplitLeakage(split.splits);
// 先在内存中用参与者分组做泄漏审计，再移除参与者标识后持久化；顺序不能交换。
const persistedSamples = minimizeJorgptSamplesForPersistence(samples);
const governance = validateGovernedSamples(persistedSamples, source);
if (!governance.valid) throw new Error(`JorGPT governance failed: ${governance.errors.join("; ")}`);
const invalidInputs = persistedSamples.map((sample) => ({ sample_id: sample.sample_id, validation: validateGradingInput(sampleToInput(sample)) }))
  .filter((item) => !item.validation.valid);
if (invalidInputs.length) {
  throw new Error(`JorGPT grading schema failed: ${invalidInputs.slice(0, 5).map((item) => `${item.sample_id}: ${item.validation.errors.join("; ")}`).join(" | ")}`);
}

const outputText = persistedSamples.map((sample) => JSON.stringify(sample)).join("\n") + "\n";
mkdirSync(dirname(outputPath), { recursive: true });
writeFileSync(outputPath, outputText, "utf8");
const gradeDistribution = Object.fromEntries([...new Set(samples.map((sample) => sample.expected_score))].sort((a, b) => a - b)
  .map((grade) => [String(grade), samples.filter((sample) => sample.expected_score === grade).length]));
const report = {
  schema_version: "jorgpt-import-audit-v1",
  generated_at: new Date().toISOString(),
  source: {
    source_id: source.source_id,
    record_id: lock.record_id,
    record_doi: lock.record_doi,
    license_id: source.license_id,
    intended_use: "evaluation",
    attribution: lock.attribution
  },
  raw_integrity: integrity,
  import: {
    input_path: relative(LAB_ROOT, inputPath).replaceAll("\\", "/"),
    output_path: relative(LAB_ROOT, outputPath).replaceAll("\\", "/"),
    output_sha256: sha256Text(outputText),
    records: samples.length,
    questions: new Set(samples.map((sample) => sample.question_id)).size,
    participants: new Set(samples.map((sample) => sample.participant_group_id)).size,
    empty_answers: samples.filter((sample) => sample.answer_text.length === 0).length,
    structured_identity_values_redacted: samples.reduce((sum, sample) => sum + sample.privacy_redaction_count, 0),
    grade_distribution: gradeDistribution
  },
  field_minimization: {
    raw_columns: JORGPT_RAW_COLUMNS,
    dropped_columns: JORGPT_DROPPED_COLUMNS,
    source_participant_id_retained: false,
    project_scoped_participant_hash_used_in_memory_for_audit: true,
    project_scoped_participant_hash_persisted: false,
    external_model_outputs_retained: false,
    timestamps_retained: false
  },
  validation: {
    source_approved_for_evaluation: true,
    privacy_scan_passed: true,
    grading_schema_valid_records: samples.length,
    question_rubric_leakage: {
      passed: questionRubricAudit.passed,
      leaked_groups: questionRubricAudit.leaked_groups.length,
      unique_groups: questionRubricAudit.unique_groups
    },
    participant_leakage_under_question_group_split: participantAudit
  },
  split_preview: {
    seed: split.seed,
    question_rubric_groups: split.group_count,
    group_counts: split.split_group_counts,
    record_counts: Object.fromEntries(Object.entries(split.splits).map(([name, records]) => [name, records.length]))
  },
  eligibility: {
    external_benchmark: true,
    pilot_gold: false,
    teacher_agreement_evidence: false,
    training: false,
    reasons: [
      "English university computer-science data is out of the local-pilot domain.",
      "Only a single holistic teacher grade is published per answer.",
      "The machine Rubric is reconstructed from the ideal answer, not the original teacher Rubric.",
      "Question-group splits retain cross-partition participant overlap, reported explicitly."
    ]
  }
};
mkdirSync(dirname(reportPath), { recursive: true });
writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`, "utf8");
console.log(JSON.stringify({
  source_id: source.source_id,
  records: samples.length,
  questions: report.import.questions,
  participants: report.import.participants,
  output: report.import.output_path,
  report: relative(LAB_ROOT, reportPath).replaceAll("\\", "/"),
  pilot_gold: false,
  training: false
}, null, 2));
