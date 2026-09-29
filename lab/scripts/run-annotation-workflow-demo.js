#!/usr/bin/env node
import { readFileSync, writeFileSync } from "node:fs";
import { buildGoldRecord, checkAgreementGate, computeInterRaterAgreement, loadAnnotationPolicy, validateAnnotationBundle } from "../src/annotations/gold.js";
import {
  artifactText,
  buildBlindAnnotationPlan,
  finalizeAdjudications,
  mergeTeacherSubmissions,
  sha256
} from "../src/annotations/workflow.js";

const bundleText = readFileSync("evals/annotations/synthetic-double-label.jsonl", "utf8");
const sourceBundles = bundleText.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
const samples = sourceBundles.map((bundle) => bundle.sample);
const rosterText = readFileSync("evals/annotations/synthetic-roster.json", "utf8");
const roster = JSON.parse(rosterText);
const rosterSha256 = sha256(rosterText);
const dataset = {
  path: "evals/annotations/synthetic-double-label.jsonl#sample-projection",
  sha256: sha256(bundleText),
  source_id: "lab_synthetic",
  evidence_scope: "synthetic_development",
  records: samples.length
};
const policy = loadAnnotationPolicy();
const { plan, packets } = buildBlindAnnotationPlan({
  samples,
  roster,
  dataset,
  rosterSha256,
  planId: "synthetic-workflow-demo-v1",
  seed: policy.workflow.assignment_seed
});
const bundleBySample = new Map(sourceBundles.map((bundle) => [bundle.sample.sample_id, bundle]));
// 演示复用合成标签模拟教师提交；authenticated_actor_id 在真实接入时必须来自可信身份。
const teacherSubmissions = plan.assignments.map((assignment) => {
  const source = bundleBySample.get(assignment.sample_id);
  const label = source.labels.find((item) => item.labeler_id === assignment.labeler_id);
  return {
    plan_id: plan.plan_id,
    assignment_id: assignment.assignment_id,
    bundle_id: assignment.bundle_id,
    sample_id: assignment.sample_id,
    authenticated_actor_id: assignment.labeler_id,
    label
  };
});
const { state, adjudicatorPackets } = mergeTeacherSubmissions({
  plan,
  samples,
  roster,
  datasetSha256: dataset.sha256,
  rosterSha256,
  submissions: teacherSubmissions,
  policy
});
const adjudicationSubmissions = state.adjudication_assignments.map((assignment) => {
  const source = bundleBySample.get(assignment.sample_id);
  return {
    plan_id: plan.plan_id,
    adjudication_assignment_id: assignment.adjudication_assignment_id,
    bundle_id: assignment.bundle_id,
    authenticated_actor_id: assignment.adjudicator_id,
    label: source.adjudication
  };
});
const final = finalizeAdjudications({
  state,
  roster,
  datasetSha256: dataset.sha256,
  rosterSha256,
  submissions: adjudicationSubmissions,
  policy
});
const gold = final.bundles.map((bundle) => buildGoldRecord(bundle, policy));
const metrics = computeInterRaterAgreement(final.bundles, policy);
const packetTexts = [...packets.values()].map((packet) => artifactText(packet));
const forbiddenPacketFields = ["expected_score", "gold_score", "human_rationale", "external_model_output"];
const report = {
  schema_version: "annotation-workflow-demo-report-v1",
  generated_at: new Date().toISOString(),
  synthetic_only: true,
  plan: {
    plan_id: plan.plan_id,
    dataset_sha256: plan.dataset.sha256,
    roster_sha256: plan.roster_sha256,
    samples: samples.length,
    assignments: plan.assignments.length,
    workloads: plan.workloads,
    pair_counts: plan.pair_counts,
    teacher_packet_hashes_valid: plan.packets.every((item) => sha256(artifactText(packets.get(item.labeler_id))) === item.sha256),
    teacher_packets_blind: packetTexts.every((text) => forbiddenPacketFields.every((field) => !text.includes(field)))
  },
  merge: {
    teacher_submissions: teacherSubmissions.length,
    pending_teacher_assignments: state.pending_teacher_assignments.length,
    completed_without_adjudication: state.completed_bundles.length,
    adjudication_assignments: state.adjudication_assignments.length,
    independent_adjudicators: state.adjudication_assignments.every((assignment) => {
      const bundle = state.pending_adjudication_bundles.find((item) => item.bundle_id === assignment.bundle_id);
      return bundle && bundle.labels.every((label) => label.labeler_id !== assignment.adjudicator_id);
    }),
    adjudicator_packet_hashes_valid: state.adjudicator_packets.every((item) => sha256(artifactText(adjudicatorPackets.get(item.adjudicator_id))) === item.sha256)
  },
  final: {
    state: final.state,
    bundles: final.bundles.length,
    adjudicated: final.bundles.filter((bundle) => bundle.adjudication).length,
    all_bundles_valid: final.bundles.every((bundle) => validateAnnotationBundle(bundle, policy).valid),
    gold_records: gold.length,
    agreement_metrics: metrics,
    dev_gate: checkAgreementGate(metrics, "dev", policy),
    pilot_gate: checkAgreementGate(metrics, "pilot", policy)
  }
};
const reportText = artifactText(report);
if (/answer_text|text_excerpt|point_decisions/.test(reportText)) throw new Error("annotation workflow demo report leaked annotation content");
writeFileSync("evals/reports/annotation-workflow-demo.json", reportText, "utf8");
console.log(JSON.stringify(report, null, 2));
