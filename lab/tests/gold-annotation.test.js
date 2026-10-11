import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  buildGoldRecord,
  checkAgreementGate,
  computeInterRaterAgreement,
  loadAnnotationPolicy,
  validateAnnotationBundle,
  validateAnnotationLabel
} from "../src/annotations/gold.js";

const bundles = readFileSync("evals/annotations/synthetic-double-label.jsonl", "utf8").split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));

const syntheticPolicy = {
  score_dispute_tolerance: 0,
  evidence_disagreement_requires_adjudication: true,
  normalized_score_bins: 4,
  adjacent_fraction: 0.25
};

function syntheticBundle() {
  const first = {
    label_id: "label-a",
    labeler_id: "teacher-a",
    labeler_role: "teacher",
    label_timestamp: "2026-10-06T00:00:00Z",
    score: 3,
    label_confidence: 0.9,
    rationale: "synthetic rationale",
    point_decisions: [
      { rubric_point_id: "p1", status: "matched", score: 2, evidence: ["alpha"] },
      { rubric_point_id: "p2", status: "partial", score: 1, evidence: ["beta"] }
    ]
  };
  return {
    bundle_id: "synthetic-gold-boundaries",
    synthetic: true,
    sample: {
      sample_id: "synthetic-gold-boundaries",
      synthetic: true,
      max_score: 4,
      answer_text: "alpha beta",
      rubric: { rubric_version: "synthetic-rubric-v1", points: [{ id: "p1", score: 2 }, { id: "p2", score: 2 }] }
    },
    labels: [first, { ...structuredClone(first), label_id: "label-b", labeler_id: "teacher-b" }]
  };
}

test("synthetic double labels validate and disputed case is adjudicated", () => {
  const validations = bundles.map((bundle) => validateAnnotationBundle(bundle));
  assert.ok(validations.every((result) => result.valid));
  assert.equal(validations[1].adjudication_required, true);
  assert.equal(validations[0].adjudication_required, false);
});

test("gold record selects adjudication for disputed labels", () => {
  const gold = buildGoldRecord(bundles[1]);
  assert.equal(gold.gold_score, 1);
  assert.equal(gold.gold_provenance.adjudicated, true);
  assert.equal(gold.gold_provenance.adjudication_label_id, "adjudication-002");
});

test("bundle rejects self double-scoring", () => {
  const copy = structuredClone(bundles[0]);
  copy.labels[1].labeler_id = copy.labels[0].labeler_id;
  const result = validateAnnotationBundle(copy);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("different labelers")));
});

test("bundle rejects evidence outside the answer", () => {
  const copy = structuredClone(bundles[0]);
  copy.labels[0].point_decisions[0].evidence = ["invented evidence"];
  const result = validateAnnotationBundle(copy);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("not found")));
});

test("malformed label returns validation errors instead of throwing", () => {
  const copy = structuredClone(bundles[0]);
  delete copy.labels[1].point_decisions;
  const result = validateAnnotationBundle(copy);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("point_decisions")));
});

test("labels reject hidden model fields and malformed point entries", () => {
  const hiddenModelField = structuredClone(bundles[0]);
  hiddenModelField.labels[0].model_score = 2;
  let result = validateAnnotationBundle(hiddenModelField);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("model_score is not allowed")));

  const malformedPoint = structuredClone(bundles[0]);
  malformedPoint.labels[0].point_decisions[0] = null;
  result = validateAnnotationBundle(malformedPoint);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("must be an object")));
});

test("agreement metrics are normalized and pilot gate rejects tiny synthetic set", () => {
  const policy = loadAnnotationPolicy();
  const metrics = computeInterRaterAgreement(bundles, policy);
  assert.equal(metrics.double_scored_count, 3);
  assert.ok(metrics.qwk >= -1 && metrics.qwk <= 1);
  assert.ok(metrics.normalized_mae >= 0 && metrics.normalized_mae <= 1);
  assert.equal(checkAgreementGate(metrics, "dev", policy).passed, true);
  const pilot = checkAgreementGate(metrics, "pilot", policy);
  assert.equal(pilot.passed, false);
  assert.ok(pilot.reasons.some((reason) => reason.includes("double_scored_count")));
});

test("label validation keeps field, score, evidence and total errors in order", () => {
  const bundle = syntheticBundle();
  const label = bundle.labels[0];
  label.model_score = 5;
  label.source_label_ids = ["label-a", "label-b"];
  label.score = 5;
  label.label_confidence = 2;
  label.rationale = "";
  // 同时触发多类错误，保护报告顺序以及教师不能携带仲裁引用的边界。
  label.point_decisions = [
    { rubric_point_id: "p1", status: "matched", score: 1, evidence: ["outside", ""], model_score: 2 },
    { rubric_point_id: "p1", status: "missing", score: 1, evidence: ["alpha"] }
  ];
  assert.deepEqual(validateAnnotationLabel(label, bundle.sample), [
    "label.model_score is not allowed",
    "label.source_label_ids is not allowed",
    "label.source_label_ids is allowed only for adjudicators",
    "label.score is out of range",
    "label.label_confidence must be between 0 and 1",
    "label.rationale is required",
    "label.point_decisions[0].model_score is not allowed",
    "label.point_decisions[0] matched status must receive full point score",
    "label.point_decisions[0].evidence is not found in answer_text",
    "label.point_decisions[0].evidence is not found in answer_text",
    "label.point_decisions[1].rubric_point_id is duplicated",
    "label.point_decisions[1] missing status must score zero",
    "label.point_decisions[1] missing decisions cannot contain evidence",
    "label did not classify rubric point: p2",
    "label.score must equal point decision total"
  ]);
});

test("point statuses retain score and evidence boundaries", () => {
  const path = "label.point_decisions[0]";
  const cases = [
    { status: "matched", score: 1, evidence: ["alpha"], errors: [`${path} matched status must receive full point score`] },
    { status: "missing", score: 1, evidence: ["alpha"], errors: [`${path} missing status must score zero`, `${path} missing decisions cannot contain evidence`] },
    { status: "partial", score: 0, evidence: [], errors: [`${path} partial status must be between zero and full point score`, `${path} scored decisions require evidence`] },
    { status: "partial", score: 2, evidence: ["alpha"], errors: [`${path} partial status must be between zero and full point score`] },
    {
      rubric_point_id: "unknown", status: "invalid", score: -1, evidence: [],
      errors: [`${path}.rubric_point_id is unknown`, `${path}.status is invalid`, `${path}.score is out of range`, `${path} scored decisions require evidence`, "label did not classify rubric point: p1"]
    }
  ];
  for (const { errors, ...decision } of cases) {
    const bundle = syntheticBundle();
    const label = bundle.labels[0];
    Object.assign(label.point_decisions[0], decision);
    label.score = decision.score + label.point_decisions[1].score;
    assert.deepEqual(validateAnnotationLabel(label, bundle.sample), errors);
  }
});

test("bundle comparison and adjudication retain independent source boundaries", () => {
  const bundle = syntheticBundle();
  bundle.labels[1].point_decisions.reverse();
  assert.deepEqual(validateAnnotationBundle(bundle, syntheticPolicy), { valid: true, errors: [], adjudication_required: false });

  const malformed = syntheticBundle();
  delete malformed.labels[0].point_decisions;
  assert.deepEqual(validateAnnotationBundle(malformed, syntheticPolicy), {
    valid: false,
    errors: ["labels[0].point_decisions must be an array", "adjudication is required for label disagreement"],
    adjudication_required: true
  });

  bundle.labels[1].score = 1;
  bundle.labels[1].point_decisions[1] = { rubric_point_id: "p1", status: "missing", score: 0, evidence: [] };
  assert.deepEqual(validateAnnotationBundle(bundle, syntheticPolicy), {
    valid: false, errors: ["adjudication is required for label disagreement"], adjudication_required: true
  });
  bundle.adjudication = {
    ...structuredClone(bundle.labels[0]), label_id: "adjudication-a", labeler_id: "teacher-c", labeler_role: "adjudicator",
    source_label_ids: ["label-b", "label-a"]
  };
  assert.deepEqual(validateAnnotationBundle(bundle, syntheticPolicy), { valid: true, errors: [], adjudication_required: true });
  bundle.adjudication.labeler_id = "teacher-a";
  bundle.adjudication.source_label_ids = ["label-a", "unknown"];
  assert.deepEqual(validateAnnotationBundle(bundle, syntheticPolicy), {
    valid: false,
    errors: ["adjudicator must be independent from both labelers", "adjudication.source_label_ids do not match labels"],
    adjudication_required: true
  });
  bundle.adjudication.labeler_id = "teacher-c";
  bundle.adjudication.source_label_ids = ["label-a"];
  assert.deepEqual(validateAnnotationBundle(bundle, syntheticPolicy), {
    valid: false, errors: ["adjudication.source_label_ids must reference both labels"], adjudication_required: true
  });
});

test("agreement metrics retain empty results and opposing normalized scores", () => {
  assert.deepEqual(computeInterRaterAgreement([], syntheticPolicy), {
    double_scored_count: 0, qwk: 0, exact_agreement: 0, adjacent_agreement: 0, normalized_mae: 0,
    normalized_score_bias_label2_minus_label1: 0, point_agreement: 0, adjudication_rate: 0
  });
  const first = syntheticBundle();
  first.labels[1].score = 1;
  first.labels[1].point_decisions[0] = { rubric_point_id: "p1", status: "missing", score: 0, evidence: [] };
  const second = syntheticBundle();
  second.labels[0].score = 1;
  second.labels[0].point_decisions[0] = { rubric_point_id: "p1", status: "missing", score: 0, evidence: [] };
  // 两份合成样本的分数方向相反，保护 QWK 矩阵及两组直方图的公开结果。
  assert.deepEqual(computeInterRaterAgreement([first, second], syntheticPolicy), {
    double_scored_count: 2, qwk: -1, exact_agreement: 0, adjacent_agreement: 0, normalized_mae: 0.5,
    normalized_score_bias_label2_minus_label1: 0, point_agreement: 0.5, adjudication_rate: 0
  });
});
