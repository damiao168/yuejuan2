#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";
import { LAB_ROOT } from "../src/runtime/localRuntime.js";
import { getModelCandidate } from "../src/runtime/modelCandidates.js";

function run(name, command, args) {
  const started = performance.now();
  const executable = process.platform === "win32" && command.endsWith(".cmd") ? (process.env.ComSpec ?? "cmd.exe") : command;
  const executableArgs = process.platform === "win32" && command.endsWith(".cmd")
    ? ["/d", "/s", "/c", command, ...args]
    : args;
  const result = spawnSync(executable, executableArgs, { cwd: LAB_ROOT, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 });
  return {
    name,
    passed: result.status === 0,
    exit_code: result.status,
    spawn_error: result.error?.message ?? null,
    elapsed_ms: Math.round(performance.now() - started),
    stdout_tail: result.stdout?.split(/\r?\n/).slice(-12).join("\n") ?? "",
    stderr_tail: result.stderr?.split(/\r?\n/).slice(-12).join("\n") ?? ""
  };
}

const steps = [
  run("unit_tests", "npm.cmd", ["test"]),
  run("synthetic_eval", "npm.cmd", ["run", "eval"]),
  run("dev_gate", "npm.cmd", ["run", "gate:dev"]),
  run("regression", "node", ["scripts/run-regression.js"]),
  run("annotation_workflow_demo", "npm.cmd", ["run", "annotations:workflow:demo"]),
  run("jorgpt_import", "npm.cmd", ["run", "dataset:jorgpt:import"]),
  run("jorgpt_govern", "npm.cmd", ["run", "dataset:jorgpt:govern"]),
  run("jorgpt_rules_baseline", "npm.cmd", ["run", "benchmark:jorgpt:rules"]),
  run("adversarial_dev", "npm.cmd", ["run", "adversarial", "--", "--level", "dev", "--out", "evals/reports/adversarial-suite.json"]),
  run("calibration_dev", "npm.cmd", ["run", "calibration", "--", "--level", "dev", "--out", "evals/reports/calibration-fairness.json"]),
  run("training_decision", "npm.cmd", ["run", "training:decision", "--", "--out", "evals/reports/fine-tuning-decision.json"]),
  run("pilot_freeze", "npm.cmd", ["run", "pilot:freeze"])
];

const readinessPath = resolve(LAB_ROOT, "evals/pilot/pilot-candidate-v1/readiness-report.json");
const manifestPath = resolve(LAB_ROOT, "evals/pilot/pilot-candidate-v1/manifest.json");
const readiness = JSON.parse(readFileSync(readinessPath, "utf8"));
const manifestText = readFileSync(manifestPath, "utf8");
const freezeManifest = JSON.parse(manifestText);
const candidate = getModelCandidate("qwen3_4b");
const rel = relative(LAB_ROOT, candidate.absolute_model_path);
const modelStaysInLab = rel !== "" && !rel.startsWith("..") && !isAbsolute(rel) && candidate.absolute_model_path.startsWith("D:\\");
const serverPidPath = resolve(LAB_ROOT, ".runtime/llama-server.pid");
const serverStopped = !existsSync(serverPidPath);
const ignoredRuntimeDirs = [".models/", ".runtime/", ".downloads/"].every((entry) => readFileSync(resolve(LAB_ROOT, ".gitignore"), "utf8").includes(entry));
const freezeContainsSecret = /(?:authorization|bearer)\s*[:=]\s*["']?[A-Za-z0-9+/=_-]{20,}/i.test(manifestText);
const requiredReports = [
  "evals/reports/local-runtime-benchmark.json",
  "evals/reports/local-adapter-smoke.json",
  "evals/reports/local-regression-v2.json",
  "evals/reports/model-selection-decision.json",
  "evals/reports/adversarial-suite.json",
  "evals/reports/annotation-reliability.json",
  "evals/reports/annotation-workflow-demo.json",
  "evals/annotations/synthetic-roster.json",
  "evals/reports/calibration-fairness.json",
  "evals/reports/fine-tuning-decision.json",
  "config/dataset-source-locks/jorgpt-zenodo-18981627.json",
  "evals/reports/jorgpt-import-audit.json",
  "evals/governed/external/jorgpt-18981627/manifest.json",
  "evals/reports/jorgpt-rules-baseline.json",
  "evals/reports/jorgpt-qwen3_4b-smoke.json",
  "evals/reports/jorgpt-qwen3_4b-diagnostic.json",
  "evals/regression/external-failed-cases.jsonl",
  "evals/pilot/pilot-candidate-v1/manifest.json",
  "evals/pilot/pilot-candidate-v1/readiness-report.json"
];
const missingReports = requiredReports.filter((path) => !existsSync(resolve(LAB_ROOT, path)));
const readRequiredJson = (path) => missingReports.includes(path) ? null : JSON.parse(readFileSync(resolve(LAB_ROOT, path), "utf8"));
const jorgptImport = readRequiredJson("evals/reports/jorgpt-import-audit.json");
const jorgptManifest = readRequiredJson("evals/governed/external/jorgpt-18981627/manifest.json");
const jorgptRules = readRequiredJson("evals/reports/jorgpt-rules-baseline.json");
const jorgptSmoke = readRequiredJson("evals/reports/jorgpt-qwen3_4b-smoke.json");
const jorgptDiagnostic = readRequiredJson("evals/reports/jorgpt-qwen3_4b-diagnostic.json");
const annotationWorkflow = readRequiredJson("evals/reports/annotation-workflow-demo.json");
const frozenArtifactPaths = new Set(freezeManifest.artifacts?.map((artifact) => artifact.path));
const controls = {
  all_commands_passed: steps.every((step) => step.passed),
  required_reports_present: missingReports.length === 0,
  model_stays_in_d_drive_lab: modelStaysInLab,
  local_model_server_stopped: serverStopped,
  runtime_directories_git_ignored: ignoredRuntimeDirs,
  freeze_contains_no_api_secret: !freezeContainsSecret,
  suggestion_only: freezeManifest.suggestion_only === true,
  final_grade_publication_disabled: freezeManifest.final_grade_publication_allowed === false,
  readiness_decision_present: ["NOT_READY", "READY_FOR_SHADOW_PILOT"].includes(readiness.readiness.decision),
  external_benchmark_governed: jorgptImport?.raw_integrity?.passed === true &&
    jorgptImport?.import?.records === 3041 && jorgptManifest?.total_records === 3041 &&
    jorgptImport?.import?.output_sha256 === jorgptManifest?.input_sha256 &&
    jorgptManifest?.source?.evidence_scope === "external_real_benchmark" &&
    jorgptManifest?.leakage_audit?.passed === true && jorgptManifest?.privacy_scan_passed === true &&
    jorgptRules?.dataset?.sha256 === jorgptManifest?.files?.test?.sha256 && jorgptRules?.dataset?.complete_dataset === true,
  external_benchmark_restricted: jorgptImport?.eligibility?.pilot_gold === false &&
    jorgptImport?.eligibility?.teacher_agreement_evidence === false && jorgptImport?.eligibility?.training === false &&
    jorgptImport?.field_minimization?.project_scoped_participant_hash_persisted === false,
  external_smoke_not_selection_eligible: jorgptSmoke?.dataset?.complete_dataset === false &&
    jorgptSmoke?.evidence_scope === "external_real_benchmark" &&
    jorgptSmoke?.dataset?.source_sha256 === jorgptManifest?.files?.test?.sha256 &&
    jorgptDiagnostic?.dataset?.complete_dataset === false &&
    jorgptDiagnostic?.dataset?.source_sha256 === jorgptManifest?.files?.test?.sha256 &&
    jorgptDiagnostic?.results?.some((result) => result.verification_invalid_points?.some((point) => point.reason === "evidence_excerpt_not_in_answer")) === true,
  blind_annotation_workflow_valid: annotationWorkflow?.synthetic_only === true &&
    annotationWorkflow?.plan?.assignments === 6 && annotationWorkflow?.plan?.teacher_packet_hashes_valid === true &&
    annotationWorkflow?.plan?.teacher_packets_blind === true && annotationWorkflow?.merge?.pending_teacher_assignments === 0 &&
    annotationWorkflow?.merge?.adjudication_assignments === 1 && annotationWorkflow?.merge?.independent_adjudicators === true &&
    annotationWorkflow?.merge?.adjudicator_packet_hashes_valid === true && annotationWorkflow?.final?.state === "complete" &&
    annotationWorkflow?.final?.all_bundles_valid === true && annotationWorkflow?.final?.dev_gate?.passed === true &&
    annotationWorkflow?.final?.pilot_gate?.passed === false && frozenArtifactPaths.has("config/annotation-policy.json") &&
    frozenArtifactPaths.has("evals/reports/annotation-workflow-demo.json")
};
// 实验室实现验收允许试点仍为 NOT_READY；真实上线资格由独立的 readiness 决定。
const auditPassed = Object.values(controls).every(Boolean);
const report = {
  schema_version: "lab-graduation-audit-v1",
  generated_at: new Date().toISOString(),
  lab_status: auditPassed ? "LAB_IMPLEMENTATION_COMPLETE" : "LAB_AUDIT_FAILED",
  pilot_status: readiness.readiness.decision,
  audit_passed: auditPassed,
  controls,
  missing_reports: missingReports,
  commands: steps,
  pilot_blockers: readiness.readiness.blockers,
  boundary: {
    lab_root: LAB_ROOT,
    model_path: candidate.absolute_model_path,
    model_bytes: candidate.expected_bytes,
    modifications_outside_lab_authorized: false
  }
};
writeFileSync(resolve(LAB_ROOT, "evals/reports/graduation-audit.json"), `${JSON.stringify(report, null, 2)}\n`, "utf8");
console.log(JSON.stringify({ lab_status: report.lab_status, pilot_status: report.pilot_status, audit_passed: report.audit_passed, controls, pilot_blockers: report.pilot_blockers }, null, 2));
if (!auditPassed) process.exitCode = 1;
