import test from "node:test";
import assert from "node:assert/strict";
import { compareModelSelectionReports, qualifyPilotModelSelection, selectDiverseBenchmarkSamples } from "../src/evaluators/modelSelection.js";

function report(candidateId, { mock = false, completed = 4, failed = 0, mae = 0.5, p95 = 100000 } = {}) {
  return {
    synthetic: true,
    candidate_id: candidateId,
    synthetic_only: true,
    model_info: { mock },
    evidence_scope: "synthetic_development",
    dataset: { sha256: "same", total_samples: 4, source_id: "lab_synthetic", evidence_scope: "synthetic_development" },
    quality: { mae, exact_agreement: 0.75, schema_validity_rate: 1, evidence_validity_rate: 1 },
    operations: { completed, failed, latency_ms_p95: p95 }
  };
}

test("selection excludes failed model even when completed-sample MAE looks perfect", () => {
  const decision = compareModelSelectionReports([
    report("rules", { mock: true, mae: 0 }),
    report("4b"),
    report("8b", { completed: 1, failed: 3, mae: 0, p95: 108000 })
  ]);
  assert.equal(decision.selected_local_llm, "4b");
  assert.equal(decision.candidates.find((candidate) => candidate.candidate_id === "8b").eligible, false);
  assert.equal(decision.candidates.find((candidate) => candidate.candidate_id === "rules").role, "deterministic_reference");
  assert.equal(decision.evidence_scope, "synthetic_development");
});

test("external benchmark cannot qualify as Pilot real model selection", () => {
  const external = {
    synthetic_only: false,
    evidence_scope: "external_real_benchmark",
    dataset_source_id: "jorgpt",
    dataset_sha256: "test-hash",
    selected_local_llm: "qwen3_4b",
    decision_status: "external_benchmark_only"
  };
  const manifest = {
    source: { source_id: "jorgpt", evidence_scope: "external_real_benchmark" },
    files: { test: { sha256: "test-hash" } }
  };
  const result = qualifyPilotModelSelection(external, manifest);
  assert.equal(result.passed, false);
  assert.ok(result.reasons.some((reason) => reason.includes("pilot_in_domain_gold")));
});

test("Pilot model selection is bound to in-domain source and frozen test hash", () => {
  const decision = {
    synthetic_only: false,
    evidence_scope: "pilot_in_domain_gold",
    dataset_source_id: "school_gold_v1",
    dataset_sha256: "frozen-test",
    selected_local_llm: "qwen3_4b",
    decision_status: "pilot_real_gold_selection"
  };
  const manifest = {
    source: { source_id: "school_gold_v1", evidence_scope: "pilot_in_domain_gold" },
    files: { test: { sha256: "frozen-test" } }
  };
  assert.deepEqual(qualifyPilotModelSelection(decision, manifest), { passed: true, reasons: [] });
  decision.dataset_sha256 = "other";
  assert.equal(qualifyPilotModelSelection(decision, manifest).passed, false);
});

test("selection rejects reports from different datasets", () => {
  const second = report("8b");
  second.dataset.sha256 = "different";
  assert.throws(() => compareModelSelectionReports([report("4b"), second]), /same dataset hash/);
});

test("smoke subsets select distinct questions first and cannot win selection", () => {
  const samples = [
    { sample_id: "q2-a", question_id: "q2", rubric_version: "v1" },
    { sample_id: "q1-a", question_id: "q1", rubric_version: "v1" },
    { sample_id: "q1-b", question_id: "q1", rubric_version: "v1" },
    { sample_id: "q3-a", question_id: "q3", rubric_version: "v1" }
  ];
  assert.deepEqual(selectDiverseBenchmarkSamples(samples, 3).map((sample) => sample.sample_id), ["q1-a", "q2-a", "q3-a"]);
  const subset = report("4b");
  subset.dataset.complete_dataset = false;
  const full = report("8b", { mae: 1 });
  const decision = compareModelSelectionReports([subset, full]);
  assert.equal(decision.candidates.find((candidate) => candidate.candidate_id === "4b").eligible, false);
  assert.equal(decision.selected_local_llm, "8b");
});

test("selection excludes candidates with missing ranking metrics", () => {
  const incomplete = report("4b");
  delete incomplete.quality.mae;
  const decision = compareModelSelectionReports([report("rules", { mock: true }), incomplete]);
  assert.equal(decision.selected_local_llm, null);
  assert.equal(decision.candidates.find((candidate) => candidate.candidate_id === "4b").eligible, false);
});

test("selection rejects duplicate or missing candidate identities", () => {
  assert.throws(() => compareModelSelectionReports([report("same"), report("same")]), /unique non-empty/);
  assert.throws(() => compareModelSelectionReports([report("4b"), report("")]), /unique non-empty/);
});

// 这里只切换报告元数据来核对状态优先级；合成夹具不是 Pilot 评测证据。
test("selection status preserves eligibility priority and evidence scope fallbacks", () => {
  const cases = [
    ["pilot_in_domain_gold", "pilot_real_gold_selection"],
    ["external_real_benchmark", "external_benchmark_only"],
    ["synthetic_development", "preliminary_synthetic_baseline"],
    ["unknown_scope", "preliminary_synthetic_baseline"]
  ];
  for (const [scope, expected] of cases) {
    const scoped = (candidateId, options) => ({ ...report(candidateId, options), evidence_scope: scope });
    const eligible = compareModelSelectionReports([scoped("4b"), scoped("reference", { mock: true })]);
    assert.equal(eligible.decision_status, expected);
    assert.equal(eligible.selected_local_llm, "4b");
    const none = compareModelSelectionReports([
      scoped("failed", { failed: 1 }), scoped("reference", { mock: true })
    ]);
    assert.equal(none.decision_status, "no_eligible_local_model");
    assert.equal(none.selected_local_llm, null);
  }
});

test("selection ranks by MAE then latency while keeping complete ties and candidate display order", () => {
  const reports = [
    report("reference", { mock: true, mae: 0 }),
    report("slow", { mae: 0.2, p95: 900 }),
    report("fast", { mae: 0.2, p95: 700 }),
    report("lower-mae", { mae: 0.1, p95: 1000 })
  ];
  const decision = compareModelSelectionReports(reports);
  assert.equal(decision.selected_local_llm, "lower-mae");
  assert.deepEqual(decision.candidates.map((candidate) => candidate.candidate_id), reports.map((item) => item.candidate_id));
  assert.equal(compareModelSelectionReports(reports.slice(0, 3)).selected_local_llm, "fast");
  assert.equal(compareModelSelectionReports([report("z-first"), report("a-second")]).selected_local_llm, "z-first");
});

test("benchmark sampling keeps round order across exhausted groups and nullish rubric fallbacks", () => {
  const samples = Object.freeze([
    ["q2-a", "q2"], ["q1-a", "q1"], ["q1-b", "q1"], ["q1-c", "q1"],
    ["q3-a", "q3"], ["q2-b", "q2"], ["q1-d", "q1"]
  ].map(([sample_id, question_id]) => Object.freeze({ synthetic: true, sample_id, question_id, rubric_version: "v1" })));
  const selected = selectDiverseBenchmarkSamples(samples, 6);
  assert.deepEqual(selected.map((sample) => sample.sample_id), ["q1-a", "q2-a", "q3-a", "q1-b", "q2-b", "q1-c"]);
  assert.deepEqual(selectDiverseBenchmarkSamples(samples, 5).map((sample) => sample.sample_id), ["q1-a", "q2-a", "q3-a", "q1-b", "q2-b"]);
  assert.equal(selected[0], samples[1]);
  assert.deepEqual(samples.map((sample) => sample.sample_id), ["q2-a", "q1-a", "q1-b", "q1-c", "q3-a", "q2-b", "q1-d"]);

  const fallbackSamples = [
    { synthetic: true, sample_id: "nested", question_id: "q", rubric_version: null, rubric: { rubric_version: "v2" } },
    { synthetic: true, sample_id: "unknown-question", question_id: null, rubric_version: "v1" },
    { synthetic: true, sample_id: "top", question_id: "q", rubric_version: "v1", rubric: { rubric_version: "v2" } },
    { synthetic: true, sample_id: "nested-next", question_id: "q", rubric: { rubric_version: "v2" } }
  ];
  assert.deepEqual(selectDiverseBenchmarkSamples(fallbackSamples, 3).map((sample) => sample.sample_id), ["top", "nested", "unknown-question"]);
});

test("benchmark sampling preserves shallow copy fast paths and invalid limit errors", () => {
  const samples = [
    { synthetic: true, sample_id: "q2", question_id: "q2" },
    { synthetic: true, sample_id: "q1", question_id: "q1" }
  ];
  for (const limit of [undefined, null, samples.length, samples.length + 1]) {
    const selected = selectDiverseBenchmarkSamples(samples, limit);
    assert.notEqual(selected, samples);
    assert.deepEqual(selected, samples);
    assert.equal(selected[0], samples[0]);
  }
  for (const limit of [0, -1, 1.5, Number.NaN, "2", true]) {
    assert.throws(() => selectDiverseBenchmarkSamples(samples, limit), {
      message: "benchmark sample limit must be a positive integer"
    });
  }
});
