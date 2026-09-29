import test from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { buildGoldRecord, loadAnnotationPolicy, validateAnnotationBundle } from "../src/annotations/gold.js";
import {
  artifactText,
  buildBlindAnnotationPlan,
  finalizeAdjudications,
  mergeTeacherSubmissions,
  sha256,
  validateAnnotationRoster
} from "../src/annotations/workflow.js";

const fixtureBundle = JSON.parse(readFileSync("evals/annotations/synthetic-double-label.jsonl", "utf8").split(/\r?\n/).filter(Boolean)[0]);
const policy = loadAnnotationPolicy();
const roster = {
  schema_version: "annotation-roster-v1",
  workers: [
    { worker_id: "teacher-a", roles: ["teacher"], active: true, subjects: ["biology"], grade_levels: ["junior_middle"] },
    { worker_id: "teacher-b", roles: ["teacher"], active: true, subjects: ["biology"], grade_levels: ["junior_middle"] },
    { worker_id: "teacher-d", roles: ["teacher"], active: true, subjects: ["biology"], grade_levels: ["junior_middle"] },
    { worker_id: "adjudicator-c", roles: ["adjudicator"], active: true, subjects: ["biology"], grade_levels: ["junior_middle"] },
    { worker_id: "adjudicator-e", roles: ["adjudicator"], active: true, subjects: ["biology"], grade_levels: ["junior_middle"] }
  ]
};
const rosterSha256 = sha256(artifactText(roster));

function samples(count = 6) {
  return Array.from({ length: count }, (_, index) => ({
    ...structuredClone(fixtureBundle.sample),
    sample_id: `workflow-sample-${index + 1}`,
    question_id: `workflow-question-${index + 1}`,
    expected_score: 2,
    expected_matched_points: ["organic", "oxygen"],
    human_rationale: "Must not enter a blind packet.",
    gold_score: 2,
    participant_group_id: "must-not-enter-packet",
    external_model_output: { score: 2 }
  }));
}

function datasetFor(records) {
  return {
    path: ".downloads/annotation/input.jsonl",
    sha256: "a".repeat(64),
    source_id: "lab_synthetic",
    evidence_scope: "synthetic_development",
    records: records.length
  };
}

function teacherLabel(assignment, sample, partial = false) {
  return {
    label_id: `label-${assignment.assignment_id}`,
    labeler_id: assignment.labeler_id,
    labeler_role: "teacher",
    label_timestamp: "2026-07-15T07:00:00Z",
    score: partial ? 1 : 2,
    label_confidence: 0.9,
    rationale: partial ? "Only organic matter is accepted." : "Both points are explicit.",
    point_decisions: [
      { rubric_point_id: "organic", status: "matched", score: 1, evidence: ["organic matter"] },
      partial
        ? { rubric_point_id: "oxygen", status: "missing", score: 0, evidence: [] }
        : { rubric_point_id: "oxygen", status: "matched", score: 1, evidence: ["oxygen"] }
    ]
  };
}

function submissionsFor(plan, records, disputedSampleId = "workflow-sample-3") {
  const sampleById = new Map(records.map((sample) => [sample.sample_id, sample]));
  return plan.assignments.map((assignment) => ({
    plan_id: plan.plan_id,
    assignment_id: assignment.assignment_id,
    bundle_id: assignment.bundle_id,
    sample_id: assignment.sample_id,
    authenticated_actor_id: assignment.labeler_id,
    label: teacherLabel(assignment, sampleById.get(assignment.sample_id), assignment.sample_id === disputedSampleId && assignment.slot === "B")
  }));
}

test("blind planner assigns two qualified teachers with balanced workloads", () => {
  const records = samples();
  const { plan, packets } = buildBlindAnnotationPlan({
    samples: records,
    roster,
    dataset: datasetFor(records),
    rosterSha256,
    planId: "workflow-plan-v1"
  });
  assert.equal(plan.assignments.length, records.length * 2);
  for (const sample of records) {
    const assigned = plan.assignments.filter((assignment) => assignment.sample_id === sample.sample_id);
    assert.equal(assigned.length, 2);
    assert.equal(new Set(assigned.map((assignment) => assignment.labeler_id)).size, 2);
    assert.deepEqual(assigned.map((assignment) => assignment.slot).sort(), ["A", "B"]);
  }
  const loads = Object.values(plan.workloads);
  assert.ok(Math.max(...loads) - Math.min(...loads) <= 1);
  for (const packetEntry of plan.packets) {
    const packet = packets.get(packetEntry.labeler_id);
    assert.equal(sha256(artifactText(packet)), packetEntry.sha256);
    const text = JSON.stringify(packet);
    for (const forbidden of ["expected_score", "expected_matched_points", "human_rationale", "gold_score", "participant_group_id", "external_model_output"]) {
      assert.equal(text.includes(forbidden), false);
    }
    for (const other of roster.workers.filter((worker) => worker.roles.includes("teacher") && worker.worker_id !== packetEntry.labeler_id)) {
      assert.equal(text.includes(other.worker_id), false);
    }
  }
});

test("teacher merge creates one independent adjudication and final valid Gold bundles", () => {
  const records = samples();
  const dataset = datasetFor(records);
  const { plan } = buildBlindAnnotationPlan({ samples: records, roster, dataset, rosterSha256, planId: "workflow-plan-v1" });
  const teacherSubmissions = submissionsFor(plan, records);
  const { state, adjudicatorPackets } = mergeTeacherSubmissions({
    plan,
    samples: records,
    roster,
    datasetSha256: dataset.sha256,
    rosterSha256,
    submissions: teacherSubmissions,
    policy
  });
  assert.equal(state.state, "adjudication_pending");
  assert.equal(state.completed_bundles.length, records.length - 1);
  assert.equal(state.pending_adjudication_bundles.length, 1);
  assert.equal(state.pending_teacher_assignments.length, 0);
  const assignment = state.adjudication_assignments[0];
  const pending = state.pending_adjudication_bundles[0];
  assert.equal(pending.labels.some((label) => label.labeler_id === assignment.adjudicator_id), false);
  assert.ok(adjudicatorPackets.get(assignment.adjudicator_id).tasks.length === 1);

  const adjudicationLabel = {
    label_id: "adjudication-workflow-1",
    labeler_id: assignment.adjudicator_id,
    labeler_role: "adjudicator",
    label_timestamp: "2026-07-15T08:00:00Z",
    score: 2,
    label_confidence: 0.95,
    rationale: "Both answer-local points are present.",
    source_label_ids: assignment.source_label_ids,
    point_decisions: [
      { rubric_point_id: "organic", status: "matched", score: 1, evidence: ["organic matter"] },
      { rubric_point_id: "oxygen", status: "matched", score: 1, evidence: ["oxygen"] }
    ]
  };
  const final = finalizeAdjudications({
    state,
    roster,
    datasetSha256: dataset.sha256,
    rosterSha256,
    submissions: [{
      plan_id: plan.plan_id,
      adjudication_assignment_id: assignment.adjudication_assignment_id,
      bundle_id: assignment.bundle_id,
      authenticated_actor_id: assignment.adjudicator_id,
      label: adjudicationLabel
    }],
    policy
  });
  assert.equal(final.state, "complete");
  assert.equal(final.bundles.length, records.length);
  assert.ok(final.bundles.every((bundle) => validateAnnotationBundle(bundle, policy).valid));
  assert.equal(final.bundles.map((bundle) => buildGoldRecord(bundle, policy)).length, records.length);

  const duplicatedState = structuredClone(state);
  duplicatedState.completed_bundles[0].sample.sample_id = duplicatedState.completed_bundles[1].sample.sample_id;
  assert.throws(() => finalizeAdjudications({
    state: duplicatedState,
    roster,
    datasetSha256: dataset.sha256,
    rosterSha256,
    submissions: [{
      plan_id: plan.plan_id,
      adjudication_assignment_id: assignment.adjudication_assignment_id,
      bundle_id: assignment.bundle_id,
      authenticated_actor_id: assignment.adjudicator_id,
      label: adjudicationLabel
    }],
    policy
  }), /duplicate sample IDs|sample ID set/);
});

test("workflow rejects actor spoofing, duplicate submissions, and plan tampering", () => {
  const records = samples(3);
  const dataset = datasetFor(records);
  const { plan } = buildBlindAnnotationPlan({ samples: records, roster, dataset, rosterSha256, planId: "workflow-plan-v1" });
  const valid = submissionsFor(plan, records, "none");
  const spoofed = structuredClone(valid);
  spoofed[0].authenticated_actor_id = "teacher-a" === spoofed[0].authenticated_actor_id ? "teacher-b" : "teacher-a";
  assert.throws(() => mergeTeacherSubmissions({ plan, samples: records, roster, datasetSha256: dataset.sha256, rosterSha256, submissions: spoofed, policy }), /does not own assignment/);
  assert.throws(() => mergeTeacherSubmissions({ plan, samples: records, roster, datasetSha256: dataset.sha256, rosterSha256, submissions: [...valid, valid[0]], policy }), /duplicate submission/);

  const tampered = structuredClone(plan);
  tampered.assignments.push({ ...tampered.assignments[0], assignment_id: "assignment-tampered", sample_id: "unknown-sample" });
  assert.throws(() => mergeTeacherSubmissions({ plan: tampered, samples: records, roster, datasetSha256: dataset.sha256, rosterSha256, submissions: valid, policy }), /unknown sample/);
  assert.throws(() => mergeTeacherSubmissions({ plan, samples: records, roster, datasetSha256: "b".repeat(64), rosterSha256, submissions: valid, policy }), /dataset hash mismatch/);

  const biased = structuredClone(valid);
  biased[0].label.model_score = 2;
  assert.throws(() => mergeTeacherSubmissions({ plan, samples: records, roster, datasetSha256: dataset.sha256, rosterSha256, submissions: biased, policy }), /model_score is not allowed/);
});

test("missing teacher or adjudicator submissions cannot become final", () => {
  const records = samples(3);
  const dataset = datasetFor(records);
  const { plan } = buildBlindAnnotationPlan({ samples: records, roster, dataset, rosterSha256, planId: "workflow-plan-v1" });
  const submissions = submissionsFor(plan, records);
  const { state } = mergeTeacherSubmissions({
    plan,
    samples: records,
    roster,
    datasetSha256: dataset.sha256,
    rosterSha256,
    submissions: submissions.slice(1),
    policy
  });
  assert.equal(state.state, "teacher_pending");
  assert.equal(state.pending_teacher_assignments.length, 1);
  assert.throws(() => finalizeAdjudications({ state, roster, datasetSha256: dataset.sha256, rosterSha256, submissions: [], policy }), /teacher assignments remain incomplete/);
});

test("roster and adjudicator independence fail closed", () => {
  const invalidRoster = structuredClone(roster);
  invalidRoster.workers.push(structuredClone(invalidRoster.workers[0]));
  assert.equal(validateAnnotationRoster(invalidRoster).valid, false);
  const unsupportedSubjectRoster = structuredClone(roster);
  unsupportedSubjectRoster.workers[0].subjects = ["astrology"];
  assert.equal(validateAnnotationRoster(unsupportedSubjectRoster).valid, false);

  const records = samples(3);
  const dataset = datasetFor(records);
  const { plan } = buildBlindAnnotationPlan({ samples: records, roster, dataset, rosterSha256, planId: "workflow-plan-v1" });
  const { state } = mergeTeacherSubmissions({ plan, samples: records, roster, datasetSha256: dataset.sha256, rosterSha256, submissions: submissionsFor(plan, records), policy });
  const tampered = structuredClone(state);
  const pending = tampered.pending_adjudication_bundles[0];
  tampered.adjudication_assignments[0].adjudicator_id = pending.labels[0].labeler_id;
  assert.throws(() => finalizeAdjudications({ state: tampered, roster, datasetSha256: dataset.sha256, rosterSha256, submissions: [], policy }), /non-independent adjudicator/);
});

test("annotation workflow CLI verifies packet hashes and completes the file protocol", () => {
  const root = ".downloads/annotation-workflow-cli-test";
  mkdirSync(root, { recursive: true });
  const sourceBundles = readFileSync("evals/annotations/synthetic-double-label.jsonl", "utf8").split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  const datasetPath = `${root}/samples.jsonl`;
  const rosterPath = `${root}/roster.json`;
  writeFileSync(datasetPath, `${sourceBundles.map((bundle) => JSON.stringify(bundle.sample)).join("\n")}\n`, "utf8");
  writeFileSync(rosterPath, readFileSync("evals/annotations/synthetic-roster.json", "utf8"), "utf8");
  const run = (...args) => spawnSync(process.execPath, ["scripts/annotation-workflow.js", ...args], { cwd: process.cwd(), encoding: "utf8" });

  const planned = run(
    "--action", "plan", "--dataset", datasetPath, "--source", "lab_synthetic", "--roster", rosterPath,
    "--plan-id", "cli-workflow-test-v1", "--out", `${root}/plan`, "--report", `${root}/plan-report.json`
  );
  assert.equal(planned.status, 0, planned.stderr);
  const plan = JSON.parse(readFileSync(`${root}/plan/coordinator-plan.json`, "utf8"));
  const bundleBySample = new Map(sourceBundles.map((bundle) => [bundle.sample.sample_id, bundle]));
  const teacherSubmissions = plan.assignments.map((assignment) => ({
    plan_id: plan.plan_id,
    assignment_id: assignment.assignment_id,
    bundle_id: assignment.bundle_id,
    sample_id: assignment.sample_id,
    authenticated_actor_id: assignment.labeler_id,
    label: bundleBySample.get(assignment.sample_id).labels.find((label) => label.labeler_id === assignment.labeler_id)
  }));
  const teacherSubmissionPath = `${root}/teacher-submissions.jsonl`;
  writeFileSync(teacherSubmissionPath, `${teacherSubmissions.map((submission) => JSON.stringify(submission)).join("\n")}\n`, "utf8");

  const packetPath = `${root}/plan/${plan.packets[0].file}`;
  const packetText = readFileSync(packetPath, "utf8");
  // 仅追加空格也必须使哈希失败，保护的是分发包原文，而不只是解析后的 JSON 值。
  writeFileSync(packetPath, `${packetText} `, "utf8");
  const tamperedMerge = run(
    "--action", "merge", "--dataset", datasetPath, "--roster", rosterPath, "--plan", `${root}/plan/coordinator-plan.json`,
    "--submissions", teacherSubmissionPath, "--out", `${root}/merge-tampered`
  );
  assert.notEqual(tamperedMerge.status, 0);
  assert.match(tamperedMerge.stderr, /packet hash mismatch/);
  writeFileSync(packetPath, packetText, "utf8");

  const merged = run(
    "--action", "merge", "--dataset", datasetPath, "--roster", rosterPath, "--plan", `${root}/plan/coordinator-plan.json`,
    "--submissions", teacherSubmissionPath, "--out", `${root}/merge`, "--report", `${root}/merge-report.json`
  );
  assert.equal(merged.status, 0, merged.stderr);
  const state = JSON.parse(readFileSync(`${root}/merge/coordinator-state.json`, "utf8"));
  assert.equal(state.state, "adjudication_pending");
  const adjudications = state.adjudication_assignments.map((assignment) => ({
    plan_id: state.plan_id,
    adjudication_assignment_id: assignment.adjudication_assignment_id,
    bundle_id: assignment.bundle_id,
    authenticated_actor_id: assignment.adjudicator_id,
    label: bundleBySample.get(assignment.sample_id).adjudication
  }));
  const adjudicationPath = `${root}/adjudications.jsonl`;
  writeFileSync(adjudicationPath, `${adjudications.map((submission) => JSON.stringify(submission)).join("\n")}\n`, "utf8");
  const finalized = run(
    "--action", "finalize", "--dataset", datasetPath, "--roster", rosterPath, "--state", `${root}/merge/coordinator-state.json`,
    "--submissions", adjudicationPath, "--out", `${root}/final`, "--report", `${root}/final-report.json`
  );
  assert.equal(finalized.status, 0, finalized.stderr);
  const finalBundles = readFileSync(`${root}/final/final-bundles.jsonl`, "utf8").split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  assert.equal(finalBundles.length, sourceBundles.length);
  assert.ok(finalBundles.every((bundle) => validateAnnotationBundle(bundle, policy).valid));
});
