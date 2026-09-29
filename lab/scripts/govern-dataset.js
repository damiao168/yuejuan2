#!/usr/bin/env node
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  auditSplitLeakage,
  getApprovedDatasetSource,
  loadDatasetSourceRegistry,
  questionGroupKey,
  sha256Text,
  splitByQuestionGroup,
  validateGovernedSamples
} from "../src/datasets/governance.js";

const LAB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function labPath(path) {
  const absolute = resolve(path);
  const rel = relative(LAB_ROOT, absolute);
  if (rel.startsWith("..") || absolute === LAB_ROOT) throw new Error(`path must remain under lab: ${path}`);
  return { absolute, portable: rel.replaceAll("\\", "/") };
}

function argsOf(argv) {
  const args = {};
  for (let index = 0; index < argv.length; index += 1) {
    if (!argv[index].startsWith("--")) continue;
    args[argv[index].slice(2)] = argv[index + 1];
    index += 1;
  }
  return args;
}

function readJsonl(path) {
  return readFileSync(path, "utf8").split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
}

const args = argsOf(process.argv.slice(2));
if (!args.input || !args.source || !args.out) {
  throw new Error("Usage: govern-dataset --input samples.jsonl --source source_id --out output_dir [--use evaluation] [--manifest manifest.json]");
}
const intendedUse = args.use ?? "evaluation";
const registry = loadDatasetSourceRegistry();
const source = getApprovedDatasetSource(args.source, intendedUse, registry);
const input = labPath(args.input);
const samples = readJsonl(input.absolute);
const validation = validateGovernedSamples(samples, source);
if (!validation.valid) throw new Error(`Dataset governance failed: ${validation.errors.join("; ")}`);
// 同一题目与 Rubric 作为整体分区，避免同题答案同时进入训练和评估集合。
const split = splitByQuestionGroup(samples);
const output = labPath(args.out);
const outputDir = output.absolute;
mkdirSync(outputDir, { recursive: true });
const files = {};
for (const [name, records] of Object.entries(split.splits)) {
  const content = records.map((record) => JSON.stringify(record)).join("\n") + (records.length ? "\n" : "");
  const path = join(outputDir, `${name}.jsonl`);
  writeFileSync(path, content, "utf8");
  files[name] = { file: basename(path), path: labPath(path).portable, records: records.length, sha256: sha256Text(content) };
}
const leakage = auditSplitLeakage(split.splits);
const manifest = {
  schema_version: "governed-dataset-manifest-v1",
  generated_at: new Date().toISOString(),
  input_file: basename(input.absolute),
  input_path: input.portable,
  input_sha256: sha256Text(readFileSync(input.absolute, "utf8")),
  output_dir: output.portable,
  source: {
    source_id: source.source_id,
    license_id: source.license_id,
    intended_use: intendedUse,
    evidence_scope: source.evidence_scope
  },
  total_records: samples.length,
  unique_question_rubric_groups: new Set(samples.map(questionGroupKey)).size,
  split_seed: split.seed,
  split_ratios: split.ratios,
  split_group_counts: split.split_group_counts,
  files,
  leakage_audit: leakage,
  privacy_scan_passed: true
};
writeFileSync(join(outputDir, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`, "utf8");
if (args.manifest) {
  const manifestPath = labPath(args.manifest).absolute;
  mkdirSync(dirname(manifestPath), { recursive: true });
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, "utf8");
}
console.log(JSON.stringify(manifest, null, 2));
