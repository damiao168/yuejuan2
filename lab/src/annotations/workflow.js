import { createHash } from "node:crypto";
import { labelsRequireAdjudication, validateAnnotationBundle, validateAnnotationLabel } from "./gold.js";
import { SUBJECTS } from "../schemas/gradingSchema.js";

const WORKER_ROLES = new Set(["teacher", "adjudicator"]);
// 盲标包采用白名单，只发送作答所需内容，排除已有人工分数、理由和模型结果。
const SAMPLE_PACKET_FIELDS = Object.freeze([
  "sample_id",
  "synthetic",
  "privacy_status",
  "source_id",
  "question_id",
  "answer_segment_id",
  "subject",
  "grade_level",
  "question_type",
  "question_text",
  "max_score",
  "rubric",
  "rubric_version",
  "answer_text",
  "ocr_confidence"
]);

export function artifactText(value) {
  return `${JSON.stringify(value, null, 2)}\n`;
}

export function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function hashOrder(...parts) {
  return sha256(parts.join("\0"));
}

function workerQualified(worker, sample, role) {
  return worker.active === true && worker.roles.includes(role) &&
    (worker.subjects.includes("*") || worker.subjects.includes(sample.subject)) &&
    (worker.grade_levels.includes("*") || worker.grade_levels.includes(sample.grade_level));
}

export function validateAnnotationRoster(roster) {
  const errors = [];
  if (roster?.schema_version !== "annotation-roster-v1") errors.push("unsupported annotation roster schema_version");
  if (!Array.isArray(roster?.workers) || roster.workers.length === 0) return { valid: false, errors: [...errors, "workers must be non-empty"] };
  const ids = new Set();
  for (const [index, worker] of roster.workers.entries()) {
    const path = `workers[${index}]`;
    if (!/^[a-z0-9][a-z0-9_-]{2,63}$/i.test(worker?.worker_id ?? "")) errors.push(`${path}.worker_id must be pseudonymous`);
    if (ids.has(worker?.worker_id)) errors.push(`duplicate worker_id: ${worker?.worker_id}`);
    ids.add(worker?.worker_id);
    if (!Array.isArray(worker?.roles) || worker.roles.length === 0 || worker.roles.some((role) => !WORKER_ROLES.has(role))) {
      errors.push(`${path}.roles is invalid`);
    }
    if (Array.isArray(worker?.roles) && new Set(worker.roles).size !== worker.roles.length) errors.push(`${path}.roles contains duplicates`);
    if (typeof worker?.active !== "boolean") errors.push(`${path}.active must be boolean`);
    if (!Array.isArray(worker?.subjects) || worker.subjects.length === 0) errors.push(`${path}.subjects must be non-empty`);
    if (Array.isArray(worker?.subjects) && worker.subjects.some((subject) => subject !== "*" && !SUBJECTS.includes(subject))) {
      errors.push(`${path}.subjects contains an unsupported subject`);
    }
    if (!Array.isArray(worker?.grade_levels) || worker.grade_levels.length === 0) errors.push(`${path}.grade_levels must be non-empty`);
  }
  return { valid: errors.length === 0, errors };
}

export function minimizeSampleForAnnotation(sample) {
  const minimized = {};
  for (const field of SAMPLE_PACKET_FIELDS) {
    if (sample[field] !== undefined) minimized[field] = structuredClone(sample[field]);
  }
  return minimized;
}

function labelTemplate(labelerId, role, rubric, sourceLabelIds = undefined) {
  return {
    label_id: "",
    labeler_id: labelerId,
    labeler_role: role,
    label_timestamp: "",
    score: null,
    label_confidence: null,
    rationale: "",
    ...(sourceLabelIds ? { source_label_ids: sourceLabelIds } : {}),
    point_decisions: rubric.points.map((point) => ({
      rubric_point_id: point.id,
      status: null,
      score: null,
      evidence: []
    }))
  };
}

function compareTuple(left, right) {
  for (let index = 0; index < left.length; index += 1) {
    if (left[index] < right[index]) return -1;
    if (left[index] > right[index]) return 1;
  }
  return 0;
}

function chooseTeacherPair(sample, roster, workloads, pairCounts, seed) {
  const teachers = roster.workers.filter((worker) => workerQualified(worker, sample, "teacher"));
  const candidates = [];
  for (let left = 0; left < teachers.length; left += 1) {
    for (let right = left + 1; right < teachers.length; right += 1) {
      const ids = [teachers[left].worker_id, teachers[right].worker_id].sort();
      // 派双评前就预留独立仲裁资格，防止出现争议后无人可合法接手。
      const independentAdjudicatorExists = roster.workers.some((worker) =>
        workerQualified(worker, sample, "adjudicator") && !ids.includes(worker.worker_id));
      if (!independentAdjudicatorExists) continue;
      const pairKey = ids.join("+");
      candidates.push({
        ids,
        pairKey,
        // 先平衡最大负担，再看总负担和配对次数；带种子的哈希只用于稳定打破平局。
        rank: [
          Math.max((workloads.get(ids[0]) ?? 0) + 1, (workloads.get(ids[1]) ?? 0) + 1),
          (workloads.get(ids[0]) ?? 0) + (workloads.get(ids[1]) ?? 0),
          pairCounts.get(pairKey) ?? 0,
          hashOrder(seed, sample.sample_id, pairKey)
        ]
      });
    }
  }
  if (candidates.length === 0) throw new Error(`sample ${sample.sample_id} lacks two qualified teachers plus an independent adjudicator`);
  candidates.sort((left, right) => compareTuple(left.rank, right.rank));
  return candidates[0];
}

function assertDatasetBinding(dataset, samples) {
  if (!dataset || !/^[a-f0-9]{64}$/i.test(dataset.sha256 ?? "")) throw new Error("dataset.sha256 is required");
  if (!dataset.source_id || !dataset.evidence_scope) throw new Error("dataset source_id and evidence_scope are required");
  if (dataset.records !== samples.length) throw new Error(`dataset record count ${dataset.records} does not match samples ${samples.length}`);
  const ids = new Set();
  for (const sample of samples) {
    if (!sample?.sample_id) throw new Error("every annotation sample requires sample_id");
    if (ids.has(sample.sample_id)) throw new Error(`duplicate annotation sample_id: ${sample.sample_id}`);
    ids.add(sample.sample_id);
    if (!sample?.rubric?.rubric_version || !Array.isArray(sample?.rubric?.points)) throw new Error(`sample ${sample.sample_id} lacks a complete Rubric`);
  }
  const sampleIdsSha256 = sha256([...ids].sort().join("\n"));
  if (dataset.sample_ids_sha256 && dataset.sample_ids_sha256 !== sampleIdsSha256) throw new Error("dataset sample ID set hash mismatch");
  return sampleIdsSha256;
}

export function buildBlindAnnotationPlan({ samples, roster, dataset, rosterSha256, planId, seed = "edugrade-blind-annotation-v1" }) {
  const rosterValidation = validateAnnotationRoster(roster);
  if (!rosterValidation.valid) throw new Error(`invalid annotation roster: ${rosterValidation.errors.join("; ")}`);
  if (!/^[a-z0-9][a-z0-9_-]{2,63}$/i.test(planId ?? "")) throw new Error("planId must be stable and pseudonymous");
  if (!/^[a-f0-9]{64}$/i.test(rosterSha256 ?? "")) throw new Error("rosterSha256 is required");
  const sampleIdsSha256 = assertDatasetBinding(dataset, samples);

  const workloads = new Map(roster.workers.filter((worker) => worker.roles.includes("teacher")).map((worker) => [worker.worker_id, 0]));
  const pairCounts = new Map();
  const assignments = [];
  const sampleById = new Map(samples.map((sample) => [sample.sample_id, sample]));
  for (const sample of [...samples].sort((left, right) => left.sample_id.localeCompare(right.sample_id))) {
    const pair = chooseTeacherPair(sample, roster, workloads, pairCounts, seed);
    let slotIds = [...pair.ids];
    if (Number.parseInt(hashOrder(seed, sample.sample_id, "slot-order").slice(0, 2), 16) % 2 === 1) slotIds.reverse();
    const bundleId = `bundle-${hashOrder(planId, sample.sample_id).slice(0, 20)}`;
    slotIds.forEach((labelerId, index) => {
      const slot = index === 0 ? "A" : "B";
      assignments.push({
        assignment_id: `assignment-${hashOrder(planId, sample.sample_id, slot, labelerId).slice(0, 20)}`,
        bundle_id: bundleId,
        sample_id: sample.sample_id,
        labeler_id: labelerId,
        slot
      });
      workloads.set(labelerId, (workloads.get(labelerId) ?? 0) + 1);
    });
    pairCounts.set(pair.pairKey, (pairCounts.get(pair.pairKey) ?? 0) + 1);
  }

  const packets = new Map();
  for (const worker of roster.workers.filter((item) => item.active && item.roles.includes("teacher"))) {
    const tasks = assignments.filter((assignment) => assignment.labeler_id === worker.worker_id).map((assignment) => {
      const sample = minimizeSampleForAnnotation(sampleById.get(assignment.sample_id));
      return {
        assignment_id: assignment.assignment_id,
        bundle_id: assignment.bundle_id,
        sample_id: assignment.sample_id,
        slot: assignment.slot,
        sample,
        label_template: labelTemplate(worker.worker_id, "teacher", sample.rubric)
      };
    });
    if (tasks.length === 0) continue;
    packets.set(worker.worker_id, {
      schema_version: "blind-teacher-packet-v1",
      plan_id: planId,
      authenticated_actor_id: worker.worker_id,
      blind: true,
      tasks
    });
  }

  const packetManifests = [...packets.entries()].map(([labelerId, packet]) => ({
    labeler_id: labelerId,
    file: `teacher-packets/${labelerId}.json`,
    assignment_count: packet.tasks.length,
    sha256: sha256(artifactText(packet))
  })).sort((left, right) => left.labeler_id.localeCompare(right.labeler_id));
  const plan = {
    schema_version: "blind-annotation-plan-v1",
    plan_id: planId,
    state: "planned",
    blind: true,
    assignment_seed: seed,
    dataset: { ...structuredClone(dataset), sample_ids_sha256: sampleIdsSha256 },
    roster_sha256: rosterSha256,
    assignments,
    workloads: Object.fromEntries([...workloads.entries()].sort()),
    pair_counts: Object.fromEntries([...pairCounts.entries()].sort()),
    packets: packetManifests
  };
  return { plan, packets };
}

// 合并时重新核对数据与人员清单绑定及双评分配，不能只相信已落盘计划的结构。
function assertPlanBinding(plan, samples, roster, datasetSha256, rosterSha256) {
  if (plan?.schema_version !== "blind-annotation-plan-v1" || plan.blind !== true) throw new Error("invalid or non-blind annotation plan");
  if (plan.dataset?.sha256 !== datasetSha256) throw new Error("annotation plan dataset hash mismatch");
  if (plan.roster_sha256 !== rosterSha256) throw new Error("annotation plan roster hash mismatch");
  assertDatasetBinding(plan.dataset, samples);
  const assignmentIds = new Set();
  const bySample = new Map();
  const sampleById = new Map(samples.map((sample) => [sample.sample_id, sample]));
  const workerById = new Map(roster.workers.map((worker) => [worker.worker_id, worker]));
  for (const assignment of plan.assignments ?? []) {
    if (assignmentIds.has(assignment.assignment_id)) throw new Error(`duplicate assignment_id: ${assignment.assignment_id}`);
    assignmentIds.add(assignment.assignment_id);
    const sample = sampleById.get(assignment.sample_id);
    if (!sample) throw new Error(`assignment ${assignment.assignment_id} references unknown sample ${assignment.sample_id}`);
    const worker = workerById.get(assignment.labeler_id);
    if (!worker || !workerQualified(worker, sample, "teacher")) {
      throw new Error(`assignment ${assignment.assignment_id} uses an unqualified teacher`);
    }
    if (!bySample.has(assignment.sample_id)) bySample.set(assignment.sample_id, []);
    bySample.get(assignment.sample_id).push(assignment);
  }
  for (const sample of samples) {
    const sampleAssignments = bySample.get(sample.sample_id) ?? [];
    if (sampleAssignments.length !== 2) throw new Error(`sample ${sample.sample_id} must have exactly two assignments`);
    if (new Set(sampleAssignments.map((item) => item.labeler_id)).size !== 2) throw new Error(`sample ${sample.sample_id} assignments must use distinct teachers`);
    if (sampleAssignments.map((item) => item.slot).sort().join("") !== "AB") throw new Error(`sample ${sample.sample_id} assignments must use slots A and B`);
    if (new Set(sampleAssignments.map((item) => item.bundle_id)).size !== 1) throw new Error(`sample ${sample.sample_id} assignments must share one bundle_id`);
  }
}

function chooseAdjudicator(sample, teacherIds, roster, workloads, seed) {
  const candidates = roster.workers.filter((worker) => workerQualified(worker, sample, "adjudicator") && !teacherIds.includes(worker.worker_id));
  if (candidates.length === 0) throw new Error(`bundle for ${sample.sample_id} lacks an independent qualified adjudicator`);
  candidates.sort((left, right) => compareTuple([
    workloads.get(left.worker_id) ?? 0,
    hashOrder(seed, sample.sample_id, left.worker_id)
  ], [
    workloads.get(right.worker_id) ?? 0,
    hashOrder(seed, sample.sample_id, right.worker_id)
  ]));
  return candidates[0];
}

export function mergeTeacherSubmissions({ plan, samples, roster, datasetSha256, rosterSha256, submissions, policy }) {
  const rosterValidation = validateAnnotationRoster(roster);
  if (!rosterValidation.valid) throw new Error(`invalid annotation roster: ${rosterValidation.errors.join("; ")}`);
  assertPlanBinding(plan, samples, roster, datasetSha256, rosterSha256);
  if (!Array.isArray(submissions)) throw new Error("teacher submissions must be an array");
  const sampleById = new Map(samples.map((sample) => [sample.sample_id, sample]));
  const assignmentById = new Map(plan.assignments.map((assignment) => [assignment.assignment_id, assignment]));
  const submissionByAssignment = new Map();
  const labelIds = new Set();
  const errors = [];

  for (const [index, submission] of submissions.entries()) {
    const path = `submissions[${index}]`;
    const assignment = assignmentById.get(submission?.assignment_id);
    if (!assignment) { errors.push(`${path}.assignment_id is unknown`); continue; }
    if (submissionByAssignment.has(assignment.assignment_id)) { errors.push(`duplicate submission for ${assignment.assignment_id}`); continue; }
    if (submission.plan_id !== plan.plan_id || submission.bundle_id !== assignment.bundle_id || submission.sample_id !== assignment.sample_id) {
      errors.push(`${path} identity does not match assignment`);
    }
    // 调用方负责提供可信身份；这里仅检查身份、标注者和任务归属是否一致。
    if (submission.authenticated_actor_id !== assignment.labeler_id || submission.label?.labeler_id !== assignment.labeler_id) {
      errors.push(`${path} actor or labeler does not own assignment`);
    }
    if (submission.label?.labeler_role !== "teacher") errors.push(`${path}.label.labeler_role must be teacher`);
    if (labelIds.has(submission.label?.label_id)) errors.push(`duplicate label_id: ${submission.label?.label_id}`);
    if (submission.label?.label_id) labelIds.add(submission.label.label_id);
    const sample = sampleById.get(assignment.sample_id);
    errors.push(...validateAnnotationLabel(submission.label, sample, `${path}.label`));
    submissionByAssignment.set(assignment.assignment_id, submission);
  }
  if (errors.length) throw new Error(`teacher submission merge failed: ${errors.join("; ")}`);

  const pendingTeacherAssignments = plan.assignments.filter((assignment) => !submissionByAssignment.has(assignment.assignment_id));
  const completedBundles = [];
  const pendingAdjudicationBundles = [];
  const adjudicationAssignments = [];
  const adjudicatorWorkloads = new Map(roster.workers.filter((worker) => worker.roles.includes("adjudicator")).map((worker) => [worker.worker_id, 0]));
  const adjudicatorPackets = new Map();

  for (const sample of [...samples].sort((left, right) => left.sample_id.localeCompare(right.sample_id))) {
    const assignments = plan.assignments.filter((assignment) => assignment.sample_id === sample.sample_id).sort((left, right) => left.slot.localeCompare(right.slot));
    const sampleSubmissions = assignments.map((assignment) => submissionByAssignment.get(assignment.assignment_id)).filter(Boolean);
    // 两份独立提交齐全才判定一致或派仲裁；部分提交继续保留在待办清单。
    if (sampleSubmissions.length !== 2) continue;
    const minimizedSample = minimizeSampleForAnnotation(sample);
    const labels = sampleSubmissions.map((submission) => structuredClone(submission.label));
    const bundle = { bundle_id: assignments[0].bundle_id, synthetic: sample.synthetic, sample: minimizedSample, labels };
    if (!labelsRequireAdjudication(labels[0], labels[1], policy)) {
      const validation = validateAnnotationBundle(bundle, policy);
      if (!validation.valid) throw new Error(`agreed bundle ${bundle.bundle_id} failed validation: ${validation.errors.join("; ")}`);
      completedBundles.push(bundle);
      continue;
    }

    const teacherIds = labels.map((label) => label.labeler_id);
    const adjudicator = chooseAdjudicator(sample, teacherIds, roster, adjudicatorWorkloads, plan.assignment_seed);
    adjudicatorWorkloads.set(adjudicator.worker_id, (adjudicatorWorkloads.get(adjudicator.worker_id) ?? 0) + 1);
    const assignment = {
      adjudication_assignment_id: `adjudication-${hashOrder(plan.plan_id, bundle.bundle_id, adjudicator.worker_id).slice(0, 20)}`,
      bundle_id: bundle.bundle_id,
      sample_id: sample.sample_id,
      adjudicator_id: adjudicator.worker_id,
      source_label_ids: labels.map((label) => label.label_id)
    };
    adjudicationAssignments.push(assignment);
    pendingAdjudicationBundles.push(bundle);
    if (!adjudicatorPackets.has(adjudicator.worker_id)) {
      adjudicatorPackets.set(adjudicator.worker_id, {
        schema_version: "blind-adjudicator-packet-v1",
        plan_id: plan.plan_id,
        authenticated_actor_id: adjudicator.worker_id,
        tasks: []
      });
    }
    adjudicatorPackets.get(adjudicator.worker_id).tasks.push({
      ...assignment,
      sample: minimizedSample,
      source_labels: labels,
      label_template: labelTemplate(adjudicator.worker_id, "adjudicator", minimizedSample.rubric, assignment.source_label_ids)
    });
  }

  const packetManifests = [...adjudicatorPackets.entries()].map(([adjudicatorId, packet]) => ({
    adjudicator_id: adjudicatorId,
    file: `adjudicator-packets/${adjudicatorId}.json`,
    assignment_count: packet.tasks.length,
    sha256: sha256(artifactText(packet))
  })).sort((left, right) => left.adjudicator_id.localeCompare(right.adjudicator_id));
  const status = pendingTeacherAssignments.length ? "teacher_pending" :
    pendingAdjudicationBundles.length ? "adjudication_pending" : "complete";
  const state = {
    schema_version: "annotation-workflow-state-v1",
    plan_id: plan.plan_id,
    state: status,
    dataset: structuredClone(plan.dataset),
    roster_sha256: rosterSha256,
    completed_bundles: completedBundles,
    pending_teacher_assignments: pendingTeacherAssignments,
    pending_adjudication_bundles: pendingAdjudicationBundles,
    adjudication_assignments: adjudicationAssignments,
    adjudicator_workloads: Object.fromEntries([...adjudicatorWorkloads.entries()].sort()),
    adjudicator_packets: packetManifests
  };
  return { state, adjudicatorPackets };
}

export function finalizeAdjudications({ state, roster, datasetSha256, rosterSha256, submissions, policy }) {
  const rosterValidation = validateAnnotationRoster(roster);
  if (!rosterValidation.valid) throw new Error(`invalid annotation roster: ${rosterValidation.errors.join("; ")}`);
  if (state?.schema_version !== "annotation-workflow-state-v1") throw new Error("invalid annotation workflow state");
  if (state.dataset?.sha256 !== datasetSha256) throw new Error("adjudication dataset hash mismatch");
  if (state.roster_sha256 !== rosterSha256) throw new Error("adjudication roster hash mismatch");
  if (state.pending_teacher_assignments.length) throw new Error("teacher assignments remain incomplete");
  if (!Array.isArray(submissions)) throw new Error("adjudication submissions must be an array");

  const assignmentById = new Map(state.adjudication_assignments.map((assignment) => [assignment.adjudication_assignment_id, assignment]));
  const bundleById = new Map(state.pending_adjudication_bundles.map((bundle) => [bundle.bundle_id, bundle]));
  if (assignmentById.size !== state.adjudication_assignments.length) throw new Error("duplicate adjudication assignment id in state");
  if (bundleById.size !== state.pending_adjudication_bundles.length) throw new Error("duplicate pending bundle id in state");
  if (assignmentById.size !== bundleById.size) throw new Error("pending bundles and adjudication assignments do not match");
  const workerById = new Map(roster.workers.map((worker) => [worker.worker_id, worker]));
  for (const assignment of state.adjudication_assignments) {
    const bundle = bundleById.get(assignment.bundle_id);
    if (!bundle || assignment.sample_id !== bundle.sample.sample_id) throw new Error(`adjudication ${assignment.adjudication_assignment_id} references the wrong bundle`);
    const worker = workerById.get(assignment.adjudicator_id);
    const teacherIds = bundle.labels.map((label) => label.labeler_id);
    if (!worker || !workerQualified(worker, bundle.sample, "adjudicator") || teacherIds.includes(worker.worker_id)) {
      throw new Error(`adjudication ${assignment.adjudication_assignment_id} uses an unqualified or non-independent adjudicator`);
    }
    if (assignment.source_label_ids.slice().sort().join("|") !== bundle.labels.map((label) => label.label_id).sort().join("|")) {
      throw new Error(`adjudication ${assignment.adjudication_assignment_id} source labels do not match bundle`);
    }
  }
  const submissionByAssignment = new Map();
  const adjudicationLabelIds = new Set();
  const sourceLabelIds = new Set(state.pending_adjudication_bundles.flatMap((bundle) => bundle.labels.map((label) => label.label_id)));
  const errors = [];
  for (const [index, submission] of submissions.entries()) {
    const path = `adjudications[${index}]`;
    const assignment = assignmentById.get(submission?.adjudication_assignment_id);
    if (!assignment) { errors.push(`${path}.adjudication_assignment_id is unknown`); continue; }
    if (submissionByAssignment.has(assignment.adjudication_assignment_id)) { errors.push(`duplicate adjudication for ${assignment.adjudication_assignment_id}`); continue; }
    if (submission.plan_id !== state.plan_id || submission.bundle_id !== assignment.bundle_id) errors.push(`${path} identity does not match assignment`);
    if (submission.authenticated_actor_id !== assignment.adjudicator_id || submission.label?.labeler_id !== assignment.adjudicator_id) {
      errors.push(`${path} actor or labeler does not own adjudication`);
    }
    if (submission.label?.labeler_role !== "adjudicator") errors.push(`${path}.label.labeler_role must be adjudicator`);
    if (sourceLabelIds.has(submission.label?.label_id) || adjudicationLabelIds.has(submission.label?.label_id)) {
      errors.push(`${path}.label.label_id must be unique`);
    }
    if (submission.label?.label_id) adjudicationLabelIds.add(submission.label.label_id);
    const bundle = bundleById.get(assignment.bundle_id);
    errors.push(...validateAnnotationLabel(submission.label, bundle.sample, `${path}.label`));
    submissionByAssignment.set(assignment.adjudication_assignment_id, submission);
  }
  const missing = state.adjudication_assignments.filter((assignment) => !submissionByAssignment.has(assignment.adjudication_assignment_id));
  if (missing.length) errors.push(`${missing.length} adjudication submissions are missing`);
  if (errors.length) throw new Error(`adjudication finalization failed: ${errors.join("; ")}`);

  const adjudicatedBundles = state.adjudication_assignments.map((assignment) => {
    const bundle = structuredClone(bundleById.get(assignment.bundle_id));
    bundle.adjudication = structuredClone(submissionByAssignment.get(assignment.adjudication_assignment_id).label);
    const validation = validateAnnotationBundle(bundle, policy);
    if (!validation.valid) throw new Error(`adjudicated bundle ${bundle.bundle_id} failed validation: ${validation.errors.join("; ")}`);
    return bundle;
  });
  const bundles = [...state.completed_bundles.map((bundle) => structuredClone(bundle)), ...adjudicatedBundles]
    .sort((left, right) => left.sample.sample_id.localeCompare(right.sample.sample_id));
  if (bundles.length !== state.dataset.records) throw new Error(`final bundle count ${bundles.length} does not match dataset ${state.dataset.records}`);
  // 数量相等不代表样本相同，最终还需核对去重后的完整 ID 集合哈希。
  const finalSampleIds = bundles.map((bundle) => bundle.sample.sample_id);
  if (new Set(finalSampleIds).size !== finalSampleIds.length) throw new Error("final bundles contain duplicate sample IDs");
  if (sha256([...finalSampleIds].sort().join("\n")) !== state.dataset.sample_ids_sha256) throw new Error("final bundle sample ID set does not match annotation plan");
  for (const bundle of bundles) {
    const validation = validateAnnotationBundle(bundle, policy);
    if (!validation.valid) throw new Error(`final bundle ${bundle.bundle_id} failed validation: ${validation.errors.join("; ")}`);
  }
  return {
    schema_version: "annotation-workflow-final-v1",
    plan_id: state.plan_id,
    state: "complete",
    dataset: structuredClone(state.dataset),
    roster_sha256: rosterSha256,
    bundles
  };
}
