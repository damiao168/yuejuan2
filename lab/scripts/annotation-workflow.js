#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { getApprovedDatasetSource, validateGovernedSamples } from "../src/datasets/governance.js";
import { loadAnnotationPolicy } from "../src/annotations/gold.js";
import {
  artifactText,
  buildBlindAnnotationPlan,
  finalizeAdjudications,
  mergeTeacherSubmissions,
  sha256
} from "../src/annotations/workflow.js";

const LAB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");

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
  if (!path) throw new Error("required path argument is missing");
  const absolute = resolve(LAB_ROOT, path);
  const rel = relative(LAB_ROOT, absolute);
  if (rel.startsWith("..") || isAbsolute(rel) || absolute === LAB_ROOT) throw new Error(`path must remain under lab: ${path}`);
  return { absolute, portable: rel.replaceAll("\\", "/") };
}

function childPath(base, path) {
  const absolute = resolve(base, path);
  const rel = relative(base, absolute);
  if (rel.startsWith("..") || isAbsolute(rel)) throw new Error(`artifact path escapes workflow directory: ${path}`);
  return absolute;
}

function readJson(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function readJsonlText(text) {
  return text.split(/\r?\n/).filter(Boolean).map((line, index) => {
    try { return JSON.parse(line); } catch (error) { throw new Error(`invalid JSONL line ${index + 1}: ${error.message}`); }
  });
}

function writeJson(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, artifactText(value), "utf8");
}

function writeJsonl(path, values) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, values.map((value) => JSON.stringify(value)).join("\n") + (values.length ? "\n" : ""), "utf8");
}

// 真实标注包只落入忽略目录；可跟踪报告另经字段检查，避免把答题内容带入仓库。
function enforceSensitiveOutput(samples, outputDir) {
  if (samples.every((sample) => sample.synthetic === true)) return;
  const rel = relative(resolve(LAB_ROOT, ".downloads"), outputDir);
  if (rel.startsWith("..") || isAbsolute(rel)) throw new Error("real annotation artifacts must stay under lab/.downloads");
}

// 合并/裁决前核对已分发包的正文哈希，避免使用被改写的任务包继续流程。
function verifyPacketHashes(manifest, baseDir, idField) {
  for (const item of manifest ?? []) {
    const path = childPath(baseDir, item.file);
    if (!existsSync(path)) throw new Error(`workflow packet is missing: ${item.file}`);
    const actual = sha256(readFileSync(path, "utf8"));
    if (actual !== item.sha256) throw new Error(`workflow packet hash mismatch for ${item[idField]}`);
  }
}

function writeReport(args, report) {
  if (!args.report) return null;
  const path = labPath(args.report);
  const text = artifactText(report);
  if (/answer_text|text_excerpt|point_decisions/.test(text)) throw new Error("tracked workflow report contains annotation content");
  mkdirSync(dirname(path.absolute), { recursive: true });
  writeFileSync(path.absolute, text, "utf8");
  return path.portable;
}

const args = argsOf(process.argv.slice(2));
const action = args.action;
if (!new Set(["plan", "merge", "finalize"]).has(action)) {
  throw new Error("Usage: annotation-workflow --action plan|merge|finalize with --dataset --roster --out and action-specific arguments");
}
const datasetPath = labPath(args.dataset);
const rosterPath = labPath(args.roster);
const output = labPath(args.out);
const datasetText = readFileSync(datasetPath.absolute, "utf8");
const rosterText = readFileSync(rosterPath.absolute, "utf8");
const samples = readJsonlText(datasetText);
const roster = JSON.parse(rosterText);
const datasetSha256 = sha256(datasetText);
const rosterSha256 = sha256(rosterText);
const policy = loadAnnotationPolicy();
enforceSensitiveOutput(samples, output.absolute);
mkdirSync(output.absolute, { recursive: true });

if (action === "plan") {
  if (!args.source || !args["plan-id"]) throw new Error("plan requires --source and --plan-id");
  const source = getApprovedDatasetSource(args.source, "annotation");
  const governance = validateGovernedSamples(samples, source);
  if (!governance.valid) throw new Error(`annotation input governance failed: ${governance.errors.join("; ")}`);
  const dataset = {
    path: datasetPath.portable,
    sha256: datasetSha256,
    source_id: source.source_id,
    evidence_scope: source.evidence_scope,
    records: samples.length
  };
  const { plan, packets } = buildBlindAnnotationPlan({
    samples,
    roster,
    dataset,
    rosterSha256,
    planId: args["plan-id"],
    seed: args.seed ?? policy.workflow.assignment_seed
  });
  const planPath = resolve(output.absolute, "coordinator-plan.json");
  writeJson(planPath, plan);
  for (const [labelerId, packet] of packets) writeJson(resolve(output.absolute, "teacher-packets", `${labelerId}.json`), packet);
  const report = {
    schema_version: "annotation-workflow-operation-report-v1",
    action,
    plan_id: plan.plan_id,
    dataset: plan.dataset,
    roster_sha256: plan.roster_sha256,
    plan_sha256: sha256(artifactText(plan)),
    sample_count: samples.length,
    assignment_count: plan.assignments.length,
    workloads: plan.workloads,
    pair_counts: plan.pair_counts,
    packet_manifests: plan.packets,
    blind: true
  };
  console.log(JSON.stringify({ ...report, report: writeReport(args, report), output: output.portable }, null, 2));
}

if (action === "merge") {
  if (!args.plan || !args.submissions) throw new Error("merge requires --plan and --submissions");
  const planPath = labPath(args.plan);
  const submissionPath = labPath(args.submissions);
  const plan = readJson(planPath.absolute);
  const source = getApprovedDatasetSource(plan.dataset.source_id, "annotation");
  const governance = validateGovernedSamples(samples, source);
  if (!governance.valid) throw new Error(`annotation input governance failed: ${governance.errors.join("; ")}`);
  verifyPacketHashes(plan.packets, dirname(planPath.absolute), "labeler_id");
  const submissions = readJsonlText(readFileSync(submissionPath.absolute, "utf8"));
  const { state, adjudicatorPackets } = mergeTeacherSubmissions({
    plan,
    samples,
    roster,
    datasetSha256,
    rosterSha256,
    submissions,
    policy
  });
  const statePath = resolve(output.absolute, "coordinator-state.json");
  writeJson(statePath, state);
  for (const [adjudicatorId, packet] of adjudicatorPackets) writeJson(resolve(output.absolute, "adjudicator-packets", `${adjudicatorId}.json`), packet);
  const report = {
    schema_version: "annotation-workflow-operation-report-v1",
    action,
    plan_id: state.plan_id,
    dataset: state.dataset,
    roster_sha256: state.roster_sha256,
    state_sha256: sha256(artifactText(state)),
    state: state.state,
    teacher_submission_count: submissions.length,
    completed_bundle_count: state.completed_bundles.length,
    pending_teacher_count: state.pending_teacher_assignments.length,
    pending_adjudication_count: state.pending_adjudication_bundles.length,
    adjudicator_workloads: state.adjudicator_workloads,
    packet_manifests: state.adjudicator_packets
  };
  console.log(JSON.stringify({ ...report, report: writeReport(args, report), output: output.portable }, null, 2));
}

if (action === "finalize") {
  if (!args.state || !args.submissions) throw new Error("finalize requires --state and --submissions");
  const statePath = labPath(args.state);
  const submissionPath = labPath(args.submissions);
  const state = readJson(statePath.absolute);
  verifyPacketHashes(state.adjudicator_packets, dirname(statePath.absolute), "adjudicator_id");
  const submissions = readJsonlText(readFileSync(submissionPath.absolute, "utf8"));
  const final = finalizeAdjudications({ state, roster, datasetSha256, rosterSha256, submissions, policy });
  const bundlePath = resolve(output.absolute, "final-bundles.jsonl");
  writeJsonl(bundlePath, final.bundles);
  const bundleText = readFileSync(bundlePath, "utf8");
  const report = {
    schema_version: "annotation-workflow-operation-report-v1",
    action,
    plan_id: final.plan_id,
    dataset: final.dataset,
    roster_sha256: final.roster_sha256,
    state: final.state,
    final_bundle_count: final.bundles.length,
    adjudicated_bundle_count: final.bundles.filter((bundle) => bundle.adjudication).length,
    output_sha256: sha256(bundleText)
  };
  console.log(JSON.stringify({ ...report, report: writeReport(args, report), output: output.portable }, null, 2));
}
