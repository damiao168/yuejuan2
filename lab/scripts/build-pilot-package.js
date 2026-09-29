#!/usr/bin/env node
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { loadAnnotationPolicy, checkAgreementGate } from "../src/annotations/gold.js";
import { validateCapabilityMatrix, loadCapabilityMatrix } from "../src/capabilities.js";
import { checkAdversarialGate } from "../src/evaluators/adversarial.js";
import { qualifyPilotModelSelection } from "../src/evaluators/modelSelection.js";
import { evaluateCalibration, fitIsotonicCalibration } from "../src/evaluators/calibration.js";
import { checkCalibrationFairnessGate, evaluateOperationalSlices } from "../src/evaluators/fairness.js";
import { listPrompts } from "../src/prompts/registry.js";
import { evaluatePilotReadiness } from "../src/release/pilot.js";
import { hashRubric } from "../src/rubric.js";
import { LAB_ROOT, sha256File } from "../src/runtime/localRuntime.js";
import { getModelCandidate } from "../src/runtime/modelCandidates.js";

const freezeId = "pilot-candidate-v1";
const outputDir = join(LAB_ROOT, "evals", "pilot", freezeId);
mkdirSync(outputDir, { recursive: true });
const readJson = (path) => JSON.parse(readFileSync(join(LAB_ROOT, path), "utf8"));
const fileHash = (path) => createHash("sha256").update(readFileSync(join(LAB_ROOT, path))).digest("hex");

const candidate = getModelCandidate("qwen3_4b");
const actualModelHash = await sha256File(candidate.absolute_model_path);
const modelIntegrity = statSync(candidate.absolute_model_path).size === candidate.expected_bytes && actualModelHash === candidate.expected_sha256;
const capability = loadCapabilityMatrix();
const capabilitySafe = validateCapabilityMatrix(capability).valid && capability.final_grade_publication_allowed === false;
const localRegression = readJson("evals/reports/local-regression-v2.json");
const modelSelection = readJson("evals/reports/model-selection-decision.json");
const modelSelection4b = readJson("evals/reports/model-selection-qwen3_4b.json");
const annotation = readJson("evals/reports/annotation-reliability.json");
const annotationPilot = checkAgreementGate(annotation.metrics, "pilot", loadAnnotationPolicy());
const adversarial = readJson("evals/reports/adversarial-suite.json");
const adversarialPilot = checkAdversarialGate(adversarial.agents, "pilot", "config/adversarial-gates.json");
const calibrationObservations = readJson("evals/calibration/synthetic-observations.json");
const calibrationSet = calibrationObservations.filter((item) => item.partition === "calibration");
const evaluationSet = calibrationObservations.filter((item) => item.partition === "evaluation");
const calibrationModel = fitIsotonicCalibration(calibrationSet);
const calibration = evaluateCalibration(evaluationSet, calibrationModel);
const calibrationConfig = readJson("config/calibration-gates.json");
const fairness = evaluateOperationalSlices(calibration.predictions, calibrationConfig.pilot.minimum_records_per_slice);
const calibrationPilot = checkCalibrationFairnessGate(calibration, fairness, "pilot", calibrationConfig);
const realManifestPath = join(LAB_ROOT, "evals", "gold", "real", "manifest.json");
const realManifest = existsSync(realManifestPath) ? readJson("evals/gold/real/manifest.json") : null;
const realDatasetGoverned = realManifest?.leakage_audit?.passed === true && realManifest?.privacy_scan_passed === true &&
  realManifest?.source?.evidence_scope === "pilot_in_domain_gold";
const realModelSelection = qualifyPilotModelSelection(modelSelection, realManifest);
const latencyPassed = modelSelection4b.operations.failed === 0 && modelSelection4b.operations.latency_ms_p95 <= 120000;

const evidence = {
  capability_boundary_safe: capabilitySafe,
  model_integrity_verified: modelIntegrity,
  local_regression_passed: localRegression.passed === true,
  real_dataset_governed: realDatasetGoverned,
  real_model_selection_passed: realModelSelection.passed,
  teacher_agreement_pilot_passed: annotation.synthetic_only === false && annotationPilot.passed,
  adversarial_pilot_passed: adversarialPilot.passed,
  calibration_fairness_pilot_passed: calibrationPilot.passed && calibrationObservations.some((item) => item.synthetic === false),
  operational_latency_passed: latencyPassed,
  details: {
    capability_boundary_safe: capability.profile_id,
    model_integrity_verified: `${candidate.file} sha256=${actualModelHash}`,
    local_regression_passed: `${localRegression.passed_count}/${localRegression.total}`,
    real_dataset_governed: realDatasetGoverned ? realManifestPath : "evals/gold/real/manifest.json is absent",
    real_model_selection_passed: realModelSelection.reasons,
    teacher_agreement_pilot_passed: annotationPilot.reasons,
    adversarial_pilot_passed: adversarialPilot.reasons,
    calibration_fairness_pilot_passed: calibrationPilot.reasons,
    operational_latency_passed: `p95=${modelSelection4b.operations.latency_ms_p95}ms ceiling=120000ms`
  }
};
// 冻结包也记录 NOT_READY 和阻塞原因；生成成功本身不代表获准开展试点。
const readiness = evaluatePilotReadiness(evidence);
const regressionRecords = readFileSync(join(LAB_ROOT, "evals/regression/prompt-rubric-v2.jsonl"), "utf8")
  .split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
const artifactPaths = [
  "config/capability-matrix.json",
  "config/local-runtime.json",
  "config/model-candidates.json",
  "config/annotation-policy.json",
  "config/release-gates.json",
  "evals/regression/prompt-rubric-v2.jsonl",
  "evals/reports/local-runtime-benchmark.json",
  "evals/reports/local-adapter-smoke.json",
  "evals/reports/model-selection-decision.json",
  "evals/reports/local-regression-v2.json",
  "evals/reports/adversarial-suite.json",
  "evals/reports/annotation-reliability.json",
  "evals/reports/annotation-workflow-demo.json",
  "evals/annotations/synthetic-roster.json",
  "evals/reports/calibration-fairness.json",
  "evals/reports/fine-tuning-decision.json"
];
const manifest = {
  schema_version: "pilot-freeze-manifest-v1",
  freeze_id: freezeId,
  generated_at: new Date().toISOString(),
  readiness_decision: readiness.decision,
  suggestion_only: true,
  final_grade_publication_allowed: false,
  runtime: { name: "llama.cpp", release: "b10012", host: "127.0.0.1", concurrency: 1 },
  model: {
    candidate_id: candidate.candidate_id,
    repository: candidate.repository,
    revision: candidate.revision,
    file: candidate.file,
    path: candidate.model_path,
    bytes: candidate.expected_bytes,
    sha256: actualModelHash,
    integrity_verified: modelIntegrity
  },
  prompts: listPrompts().map((prompt) => ({ prompt_id: prompt.prompt_id, prompt_version: prompt.prompt_version, checksum: prompt.checksum })),
  rubrics: regressionRecords.map((record) => ({ rubric_id: record.rubric.rubric_id, rubric_version: record.rubric.rubric_version, hash: hashRubric(record.rubric) })),
  artifacts: artifactPaths.map((path) => ({ path, sha256: fileHash(path) })),
  evidence_summary: evidence,
  training_decision: readJson("evals/reports/fine-tuning-decision.json").decision.decision,
  confidence_runtime_enabled: false
};
writeFileSync(join(outputDir, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`, "utf8");
writeFileSync(join(outputDir, "readiness-report.json"), `${JSON.stringify({ schema_version: "pilot-readiness-report-v1", freeze_id: freezeId, generated_at: new Date().toISOString(), readiness }, null, 2)}\n`, "utf8");
console.log(JSON.stringify({ freeze_id: freezeId, output_dir: outputDir, readiness }, null, 2));
