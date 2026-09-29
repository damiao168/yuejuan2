import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { normalizeText } from "../evidenceVerifier.js";

const here = dirname(fileURLToPath(import.meta.url));
export const DEFAULT_ANNOTATION_POLICY = join(here, "../../config/annotation-policy.json");
const STATUSES = new Set(["matched", "partial", "missing"]);
const LABEL_FIELDS = new Set([
  "label_id", "labeler_id", "labeler_role", "label_timestamp", "score", "label_confidence", "rationale", "point_decisions"
]);
const POINT_DECISION_FIELDS = new Set(["rubric_point_id", "status", "score", "evidence"]);

export function loadAnnotationPolicy(path = DEFAULT_ANNOTATION_POLICY) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function validateLabel(label, sample, labelName) {
  const errors = [];
  if (!label || typeof label !== "object" || Array.isArray(label)) return [`${labelName} must be an object`];
  const allowedLabelFields = new Set(LABEL_FIELDS);
  if (label.labeler_role === "adjudicator") allowedLabelFields.add("source_label_ids");
  for (const field of Object.keys(label)) {
    if (!allowedLabelFields.has(field)) errors.push(`${labelName}.${field} is not allowed`);
  }
  if (label.labeler_role !== "adjudicator" && label.source_label_ids !== undefined) {
    errors.push(`${labelName}.source_label_ids is allowed only for adjudicators`);
  }
  if (!label.label_id) errors.push(`${labelName}.label_id is required`);
  if (!/^[a-z0-9][a-z0-9_-]{2,63}$/i.test(label.labeler_id ?? "")) errors.push(`${labelName}.labeler_id must be pseudonymous`);
  if (label.labeler_role !== "teacher" && label.labeler_role !== "adjudicator") errors.push(`${labelName}.labeler_role is invalid`);
  if (Number.isNaN(Date.parse(label.label_timestamp))) errors.push(`${labelName}.label_timestamp is invalid`);
  if (!Number.isFinite(label.score) || label.score < 0 || label.score > sample.max_score) errors.push(`${labelName}.score is out of range`);
  if (!Number.isFinite(label.label_confidence) || label.label_confidence < 0 || label.label_confidence > 1) {
    errors.push(`${labelName}.label_confidence must be between 0 and 1`);
  }
  if (typeof label.rationale !== "string" || label.rationale.trim().length === 0) errors.push(`${labelName}.rationale is required`);
  if (!Array.isArray(label.point_decisions)) return [...errors, `${labelName}.point_decisions must be an array`];

  const rubricPoints = new Map(sample.rubric.points.map((point) => [point.id, point]));
  const seen = new Set();
  let pointTotal = 0;
  label.point_decisions.forEach((decision, index) => {
    const path = `${labelName}.point_decisions[${index}]`;
    if (!decision || typeof decision !== "object" || Array.isArray(decision)) {
      errors.push(`${path} must be an object`);
      return;
    }
    for (const field of Object.keys(decision)) {
      if (!POINT_DECISION_FIELDS.has(field)) errors.push(`${path}.${field} is not allowed`);
    }
    const rubricPoint = rubricPoints.get(decision.rubric_point_id);
    if (!rubricPoint) errors.push(`${path}.rubric_point_id is unknown`);
    if (seen.has(decision.rubric_point_id)) errors.push(`${path}.rubric_point_id is duplicated`);
    seen.add(decision.rubric_point_id);
    if (!STATUSES.has(decision.status)) errors.push(`${path}.status is invalid`);
    if (!Number.isFinite(decision.score) || decision.score < 0 || (rubricPoint && decision.score > rubricPoint.score)) {
      errors.push(`${path}.score is out of range`);
    }
    if (decision.status === "missing" && decision.score !== 0) errors.push(`${path} missing status must score zero`);
    if (decision.status === "matched" && rubricPoint && decision.score !== rubricPoint.score) errors.push(`${path} matched status must receive full point score`);
    if (decision.status === "partial" && rubricPoint && !(decision.score > 0 && decision.score < rubricPoint.score)) {
      errors.push(`${path} partial status must be between zero and full point score`);
    }
    if (!Array.isArray(decision.evidence)) errors.push(`${path}.evidence must be an array`);
    const evidence = decision.evidence ?? [];
    if (decision.status !== "missing" && evidence.length === 0) errors.push(`${path} scored decisions require evidence`);
    if (decision.status === "missing" && evidence.length > 0) errors.push(`${path} missing decisions cannot contain evidence`);
    for (const excerpt of evidence) {
      if (typeof excerpt !== "string" || !normalizeText(sample.answer_text).includes(normalizeText(excerpt)) || normalizeText(excerpt).length === 0) {
        errors.push(`${path}.evidence is not found in answer_text`);
      }
    }
    pointTotal += Number.isFinite(decision.score) ? decision.score : 0;
  });
  for (const pointId of rubricPoints.keys()) {
    if (!seen.has(pointId)) errors.push(`${labelName} did not classify rubric point: ${pointId}`);
  }
  if (Math.abs(pointTotal - label.score) > 1e-9) errors.push(`${labelName}.score must equal point decision total`);
  return errors;
}

export function validateAnnotationLabel(label, sample, labelName = "label") {
  return validateLabel(label, sample, labelName);
}

function evidenceSignature(label) {
  return label.point_decisions
    .map((decision) => `${decision.rubric_point_id}:${decision.status}:${decision.score}:${(decision.evidence ?? []).map(normalizeText).sort().join("|")}`)
    .sort()
    .join(";");
}

// 总分接近仍可能有采分点分歧；证据差异是否仲裁由策略单独控制。
export function labelsRequireAdjudication(first, second, policy = loadAnnotationPolicy()) {
  if (Math.abs(first.score - second.score) > policy.score_dispute_tolerance) return true;
  const pointSignature = (label) => label.point_decisions
    .map((decision) => `${decision.rubric_point_id}:${decision.status}:${decision.score}`)
    .sort().join(";");
  if (pointSignature(first) !== pointSignature(second)) return true;
  return policy.evidence_disagreement_requires_adjudication && evidenceSignature(first) !== evidenceSignature(second);
}

export function validateAnnotationBundle(bundle, policy = loadAnnotationPolicy()) {
  const errors = [];
  if (!bundle?.bundle_id) errors.push("bundle_id is required");
  if (!bundle?.sample?.sample_id) errors.push("sample.sample_id is required");
  if (!bundle?.sample?.rubric?.rubric_version) errors.push("sample.rubric.rubric_version is required");
  if (bundle?.synthetic !== bundle?.sample?.synthetic) errors.push("bundle synthetic flag must match sample");
  if (!Array.isArray(bundle?.labels) || bundle.labels.length !== 2) {
    errors.push("exactly two independent labels are required");
    return { valid: false, errors, adjudication_required: true };
  }
  const firstErrors = validateLabel(bundle.labels[0], bundle.sample, "labels[0]");
  const secondErrors = validateLabel(bundle.labels[1], bundle.sample, "labels[1]");
  errors.push(...firstErrors, ...secondErrors);
  if (bundle.labels[0].labeler_id === bundle.labels[1].labeler_id) errors.push("independent labels require different labelers");
  const labelsHaveDecisions = Array.isArray(bundle.labels[0]?.point_decisions) && Array.isArray(bundle.labels[1]?.point_decisions) &&
    bundle.labels.every((label) => label.point_decisions.every((decision) => decision && typeof decision === "object" && !Array.isArray(decision)));
  const adjudicationRequired = labelsHaveDecisions ? labelsRequireAdjudication(bundle.labels[0], bundle.labels[1], policy) : true;
  if (adjudicationRequired && !bundle.adjudication) errors.push("adjudication is required for label disagreement");
  if (bundle.adjudication) {
    errors.push(...validateLabel(bundle.adjudication, bundle.sample, "adjudication"));
    if (bundle.adjudication.labeler_role !== "adjudicator") errors.push("adjudication labeler_role must be adjudicator");
    if (bundle.labels.some((label) => label.labeler_id === bundle.adjudication.labeler_id)) {
      errors.push("adjudicator must be independent from both labelers");
    }
    if (!Array.isArray(bundle.adjudication.source_label_ids) || bundle.adjudication.source_label_ids.length !== 2) {
      errors.push("adjudication.source_label_ids must reference both labels");
    } else {
      const expected = bundle.labels.map((label) => label.label_id).sort();
      if (bundle.adjudication.source_label_ids.slice().sort().join("|") !== expected.join("|")) {
        errors.push("adjudication.source_label_ids do not match labels");
      }
    }
  }
  return { valid: errors.length === 0, errors, adjudication_required: adjudicationRequired };
}

export function buildGoldRecord(bundle, policy = loadAnnotationPolicy()) {
  const validation = validateAnnotationBundle(bundle, policy);
  if (!validation.valid) throw new Error(`Invalid annotation bundle ${bundle.bundle_id}: ${validation.errors.join("; ")}`);
  // 校验已保证有分歧时存在独立仲裁；无仲裁才使用首份一致标注作为 Gold。
  const selected = bundle.adjudication ?? bundle.labels[0];
  return {
    ...bundle.sample,
    gold_score: selected.score,
    gold_point_decisions: selected.point_decisions,
    gold_rationale: selected.rationale,
    gold_label_confidence: selected.label_confidence,
    gold_provenance: {
      bundle_id: bundle.bundle_id,
      source_label_ids: bundle.labels.map((label) => label.label_id),
      adjudicated: Boolean(bundle.adjudication),
      adjudication_label_id: bundle.adjudication?.label_id ?? null
    }
  };
}

function quadraticWeightedKappa(pairs, bins) {
  if (pairs.length === 0) return 0;
  const size = bins + 1;
  const observed = Array.from({ length: size }, () => Array(size).fill(0));
  const firstHistogram = Array(size).fill(0);
  const secondHistogram = Array(size).fill(0);
  for (const [first, second] of pairs) {
    observed[first][second] += 1;
    firstHistogram[first] += 1;
    secondHistogram[second] += 1;
  }
  let observedWeighted = 0;
  let expectedWeighted = 0;
  for (let first = 0; first < size; first += 1) {
    for (let second = 0; second < size; second += 1) {
      const weight = ((first - second) ** 2) / (bins ** 2);
      observedWeighted += weight * observed[first][second] / pairs.length;
      expectedWeighted += weight * firstHistogram[first] * secondHistogram[second] / (pairs.length ** 2);
    }
  }
  return expectedWeighted === 0 ? 1 : 1 - observedWeighted / expectedWeighted;
}

export function computeInterRaterAgreement(bundles, policy = loadAnnotationPolicy()) {
  const bins = policy.normalized_score_bins;
  // 先除以各题满分再离散到统一档位，避免不同满分题直接混算一致性。
  const normalizedPairs = bundles.map((bundle) => bundle.labels.map((label) => label.score / bundle.sample.max_score));
  const binPairs = normalizedPairs.map(([first, second]) => [Math.round(first * bins), Math.round(second * bins)]);
  let pointTotal = 0;
  let pointMatches = 0;
  for (const bundle of bundles) {
    const secondById = new Map(bundle.labels[1].point_decisions.map((item) => [item.rubric_point_id, item]));
    for (const decision of bundle.labels[0].point_decisions) {
      const second = secondById.get(decision.rubric_point_id);
      pointTotal += 1;
      if (second && second.status === decision.status && Math.abs(second.score - decision.score) < 1e-9) pointMatches += 1;
    }
  }
  const absoluteErrors = normalizedPairs.map(([first, second]) => Math.abs(first - second));
  const biases = normalizedPairs.map(([first, second]) => second - first);
  const mean = (values) => values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : 0;
  return {
    double_scored_count: bundles.length,
    qwk: quadraticWeightedKappa(binPairs, bins),
    exact_agreement: mean(normalizedPairs.map(([first, second]) => first === second ? 1 : 0)),
    adjacent_agreement: mean(absoluteErrors.map((error) => error <= policy.adjacent_fraction ? 1 : 0)),
    normalized_mae: mean(absoluteErrors),
    normalized_score_bias_label2_minus_label1: mean(biases),
    point_agreement: pointTotal ? pointMatches / pointTotal : 0,
    adjudication_rate: mean(bundles.map((bundle) => bundle.adjudication ? 1 : 0))
  };
}

export function checkAgreementGate(metrics, level, policy = loadAnnotationPolicy()) {
  const gate = policy.gates[level];
  if (!gate) throw new Error(`Unknown annotation gate: ${level}`);
  const reasons = [];
  if (metrics.double_scored_count < gate.minimum_double_scored) reasons.push(`double_scored_count ${metrics.double_scored_count} < ${gate.minimum_double_scored}`);
  if (gate.minimum_qwk !== null && metrics.qwk < gate.minimum_qwk) reasons.push(`qwk ${metrics.qwk} < ${gate.minimum_qwk}`);
  if (gate.maximum_normalized_mae !== null && metrics.normalized_mae > gate.maximum_normalized_mae) reasons.push(`normalized_mae ${metrics.normalized_mae} > ${gate.maximum_normalized_mae}`);
  if (gate.minimum_point_agreement !== null && metrics.point_agreement < gate.minimum_point_agreement) reasons.push(`point_agreement ${metrics.point_agreement} < ${gate.minimum_point_agreement}`);
  return { level, passed: reasons.length === 0, reasons };
}
