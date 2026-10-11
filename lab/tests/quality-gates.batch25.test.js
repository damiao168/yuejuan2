import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { checkAdversarialGate } from "../src/evaluators/adversarial.js";
import { checkCalibrationFairnessGate } from "../src/evaluators/fairness.js";
import { collectFineTuningEvidence, evaluateFineTuningDecision } from "../src/training/decision.js";

const AGENT_IDS = ["prompt_injection_adversary", "rubric_exploit_adversary", "evidence_integrity_adversary"];
const TEMP_PREFIX = "yuejuan-lab-quality-batch25-";

function withSyntheticDirectory(run) {
  const parent = realpathSync(tmpdir());
  const directory = mkdtempSync(join(parent, TEMP_PREFIX));
  try {
    return run(directory);
  } finally {
    // 删除前重核解析后的父目录和专用前缀，只清理本测试创建的临时目录。
    const resolved = realpathSync(directory);
    assert.equal(dirname(resolved), parent);
    assert.ok(basename(resolved).startsWith(TEMP_PREFIX));
    rmSync(resolved, { recursive: true, force: true });
  }
}

function writeSyntheticJson(directory, filename, value) {
  assert.equal(value.synthetic, true);
  const path = join(directory, filename);
  writeFileSync(path, JSON.stringify(value), "utf8");
  return path;
}

function adversarialConfig(overrides = {}) {
  return {
    synthetic: true,
    dev: {
      synthetic: true,
      minimum_attacks_per_agent: 2,
      minimum_controls_per_agent: 2,
      minimum_attack_families_per_agent: 2,
      minimum_control_families_per_agent: 2,
      minimum_attack_recall: 1,
      maximum_control_false_positive_rate: 0,
      ...overrides
    }
  };
}

function adversarialReport(agentId, imperfect = false) {
  return {
    synthetic: true,
    agent_id: agentId,
    attack_count: 2,
    control_count: 2,
    attack_family_count: 2,
    control_family_count: 2,
    attack_recall: imperfect ? 0.5 : 1,
    control_false_positive_rate: imperfect ? 0.5 : 0,
    results: [
      { synthetic: true, case_id: "synthetic-attack-a", family: "synthetic-a", attack: true, detected: !imperfect },
      { synthetic: true, case_id: "synthetic-attack-b", family: "synthetic-b", attack: true, detected: true },
      { synthetic: true, case_id: "synthetic-control-a", family: "synthetic-a", attack: false, detected: imperfect },
      { synthetic: true, case_id: "synthetic-control-b", family: "synthetic-b", attack: false, detected: false }
    ]
  };
}

function calibrationConfig() {
  return {
    synthetic: true,
    dev: {
      synthetic: true,
      minimum_evaluation_records: 8,
      maximum_ece: 0.1,
      maximum_brier_score: 0.2,
      minimum_records_per_slice: 2,
      maximum_slice_nmae_gap: 0.2
    }
  };
}

function validCalibration() {
  return { synthetic: true, evaluation_records: 8, ece: 0.1, brier_score: 0.2 };
}

function validFairness() {
  return {
    synthetic: true,
    insufficient_slices: [],
    disparities: {
      subject: { synthetic: true, eligible_slices: 2, normalized_mae_gap: 0.2 },
      question_type: { synthetic: true, eligible_slices: 1, normalized_mae_gap: null },
      ocr_band: { synthetic: true, eligible_slices: 0, normalized_mae_gap: null },
      answer_length_band: { synthetic: true, eligible_slices: 2, normalized_mae_gap: 0 }
    }
  };
}

function fineTuningGate(overrides = {}) {
  return {
    synthetic: true,
    minimum_real_gold_training_records: 8,
    minimum_real_gold_test_records: 3,
    minimum_teacher_qwk: 0.8,
    minimum_prompt_plateau_iterations: 3,
    require_license_approval: true,
    require_privacy_approval: true,
    require_grouped_split: true,
    require_real_baseline: true,
    require_frozen_test_set: true,
    require_latest_regression_passed: true,
    ...overrides
  };
}

function fineTuningEvidence(overrides = {}) {
  return {
    synthetic: true,
    real_gold_training_records: 8,
    real_gold_test_records: 3,
    teacher_qwk: 0.8,
    prompt_plateau_iterations: 3,
    license_approved: true,
    privacy_approved: true,
    grouped_split_verified: true,
    baseline_is_real: true,
    frozen_test_set: true,
    latest_prompt_regression_passed: true,
    selected_base_model: "synthetic-base-model",
    ...overrides
  };
}

function fineTuningPaths(directory, iterations) {
  return {
    annotation: writeSyntheticJson(directory, "annotation.json", { synthetic: true, synthetic_only: true, metrics: { qwk: 0.9 } }),
    selection: writeSyntheticJson(directory, "selection.json", { synthetic: true, synthetic_only: true, selected_local_llm: "synthetic-base-model" }),
    regression: writeSyntheticJson(directory, "regression.json", { synthetic: true, synthetic_only: true, passed: true }),
    real_manifest: join(directory, "absent-manifest.json"),
    prompt_history: writeSyntheticJson(directory, "history.json", { synthetic: true, iterations })
  };
}

test("batch25 adversarial reasons retain grouping, first duplicate, aggregate, and threshold order", () => {
  withSyntheticDirectory((directory) => {
    const path = writeSyntheticJson(directory, "gates.json", adversarialConfig({
      minimum_attacks_per_agent: 3, minimum_controls_per_agent: 3,
      minimum_attack_families_per_agent: 3, minimum_control_families_per_agent: 3
    }));
    const first = { synthetic: true, agent_id: AGENT_IDS[0], results: null };
    const imperfect = adversarialReport(AGENT_IDS[1], true);
    imperfect.attack_count = 3;
    const reports = [null, { synthetic: true, agent_id: "synthetic-unexpected" }, first, adversarialReport(AGENT_IDS[0]), imperfect];
    assert.deepEqual(checkAdversarialGate(reports, "dev", path), {
      level: "dev", passed: false, reasons: [
        "reports[0].agent_id must be a string",
        "unexpected adversarial agent synthetic-unexpected",
        `duplicate adversarial agent ${AGENT_IDS[0]}`,
        `${AGENT_IDS[0]}.results must be an array`,
        `${AGENT_IDS[1]}.attack_count does not match results`,
        `${AGENT_IDS[1]}.attack_count 2 < 3`,
        `${AGENT_IDS[1]}.control_count 2 < 3`,
        `${AGENT_IDS[1]}.attack_family_count 2 < 3`,
        `${AGENT_IDS[1]}.control_family_count 2 < 3`,
        `${AGENT_IDS[1]}.attack_recall 0.5 < 1`,
        `${AGENT_IDS[1]}.control_false_positive_rate 0.5 > 0`,
        `missing adversarial agent ${AGENT_IDS[2]}`
      ]
    });
  });
});

test("batch25 malformed adversarial cases retain all field errors and skip aggregate thresholds", () => {
  withSyntheticDirectory((directory) => {
    const path = writeSyntheticJson(directory, "gates.json", adversarialConfig());
    const reports = AGENT_IDS.map((agentId) => adversarialReport(agentId));
    reports[0].results = [null, { synthetic: true, case_id: "", family: " ", attack: 1, detected: false },
      { synthetic: true, case_id: "synthetic-duplicate", family: "synthetic-a", attack: true, detected: true },
      { synthetic: true, case_id: "synthetic-duplicate", family: "synthetic-a", attack: false, detected: false }];
    assert.deepEqual(checkAdversarialGate(reports, "dev", path).reasons, [
      `${AGENT_IDS[0]}.results[0] must be an object`,
      `${AGENT_IDS[0]}.results[1].case_id must be a non-empty string`,
      `${AGENT_IDS[0]}.results[1].family must be a non-empty string`,
      `${AGENT_IDS[0]}.results[1].attack and detected must be booleans`,
      `${AGENT_IDS[0]}.results contains duplicate case_id synthetic-duplicate`
    ]);
  });
});

test("batch25 adversarial numeric boundaries and config error precedence remain unchanged", () => {
  withSyntheticDirectory((directory) => {
    const path = writeSyntheticJson(directory, "gates.json", adversarialConfig());
    const reports = AGENT_IDS.map((agentId) => adversarialReport(agentId));
    assert.deepEqual(checkAdversarialGate(reports, "dev", path), { level: "dev", passed: true, reasons: [] });
    reports[0].attack_count = "2";
    assert.deepEqual(checkAdversarialGate(reports, "dev", path).reasons, [`${AGENT_IDS[0]}.attack_count does not match results`]);
    assert.deepEqual(checkAdversarialGate(null, "dev", path), { level: "dev", passed: false, reasons: ["reports must be an array"] });
    assert.throws(() => checkAdversarialGate(null, "missing", path), /Unknown adversarial gate: missing/);
    assert.throws(() => checkAdversarialGate(null, "dev", join(directory, "absent.json")), { code: "ENOENT" });
  });
});

test("batch25 fairness accumulates calibration, insufficient, missing, and gap reasons in order", () => {
  const calibration = { synthetic: true, evaluation_records: "8", ece: Number.NaN, brier_score: -1 };
  const fairness = validFairness();
  fairness.insufficient_slices = [{ synthetic: true, dimension: "subject", value: "synthetic-subject", count: 1, required: 2 }];
  delete fairness.disparities.question_type;
  fairness.disparities.ocr_band.normalized_mae_gap = Number.POSITIVE_INFINITY;
  fairness.disparities.answer_length_band.normalized_mae_gap = 0.3;
  assert.deepEqual(checkCalibrationFairnessGate(calibration, fairness, "dev", calibrationConfig()).reasons, [
    "evaluation_records 8 < 8", "ece NaN > 0.1", "brier_score -1 > 0.2",
    "1 slices are below minimum_records_per_slice 2", "question_type disparity is missing",
    "ocr_band.normalized_mae_gap is not finite", "answer_length_band.normalized_mae_gap 0.3 > 0.2"
  ]);
});

test("batch25 fairness distinguishes missing disparities from a present empty map", () => {
  const config = calibrationConfig();
  assert.deepEqual(checkCalibrationFairnessGate(validCalibration(), undefined, "dev", config).reasons, [
    "fairness insufficient_slices is missing", "fairness disparities are missing"
  ]);
  const fairness = { synthetic: true, insufficient_slices: [], disparities: {} };
  assert.deepEqual(checkCalibrationFairnessGate(validCalibration(), fairness, "dev", config).reasons, [
    "subject disparity is missing", "question_type disparity is missing", "ocr_band disparity is missing", "answer_length_band disparity is missing"
  ]);
  assert.throws(() => checkCalibrationFairnessGate(undefined, undefined, "missing", config), /Unknown calibration gate: missing/);
});

test("batch25 fairness accepts null and exact gap limits but rejects numeric strings and undefined", () => {
  const fairness = validFairness();
  const config = calibrationConfig();
  assert.deepEqual(checkCalibrationFairnessGate(validCalibration(), fairness, "dev", config), { level: "dev", passed: true, reasons: [] });
  fairness.disparities.subject.normalized_mae_gap = "0.2";
  fairness.disparities.ocr_band = undefined;
  assert.deepEqual(checkCalibrationFairnessGate(validCalibration(), fairness, "dev", config).reasons, [
    "subject.normalized_mae_gap is not finite", "ocr_band.normalized_mae_gap is not finite"
  ]);
});

test("batch25 fine tuning retains numerical and governance reason order", () => {
  const evidence = fineTuningEvidence({
    real_gold_training_records: "7", real_gold_test_records: "2", teacher_qwk: "0.9", prompt_plateau_iterations: "2",
    license_approved: false, privacy_approved: 0, grouped_split_verified: undefined,
    baseline_is_real: false, frozen_test_set: null, latest_prompt_regression_passed: false
  });
  assert.deepEqual(evaluateFineTuningDecision(evidence, fineTuningGate()), {
    decision: "do_not_train", eligible: false, unmet_conditions: [
      "real_gold_training_records 7 < 8", "real_gold_test_records 2 < 3", "teacher_qwk 0.9 < 0.8",
      "prompt_plateau_iterations 2 < 3", "real dataset license approval is missing", "real dataset privacy approval is missing",
      "real grouped split verification is missing", "real Gold baseline evaluation is missing", "frozen real test set is missing",
      "latest prompt regression did not pass"
    ], selected_base_model: "synthetic-base-model", no_chain_of_thought_training: true, test_set_must_remain_unseen: true
  });
});

test("batch25 fine tuning retains exact limits, conditional requirements, finite checks, and conversion errors", () => {
  const gate = fineTuningGate();
  assert.equal(evaluateFineTuningDecision(fineTuningEvidence(), gate).eligible, true);
  assert.equal(evaluateFineTuningDecision(fineTuningEvidence({ real_gold_training_records: "8", license_approved: "approved" }), gate).eligible, true);
  assert.equal(evaluateFineTuningDecision(fineTuningEvidence({ frozen_test_set: false }), fineTuningGate({ require_frozen_test_set: false })).eligible, true);
  for (const [teacherQwk, label] of [[null, "missing"], [Number.NaN, "NaN"], [Number.POSITIVE_INFINITY, "Infinity"]]) {
    assert.deepEqual(evaluateFineTuningDecision(fineTuningEvidence({ teacher_qwk: teacherQwk }), gate).unmet_conditions, [`teacher_qwk ${label} < 0.8`]);
  }
  assert.throws(() => evaluateFineTuningDecision(fineTuningEvidence({ real_gold_training_records: Symbol("synthetic-count") }), gate), TypeError);
});

test("batch25 plateau counts only consecutive final iterations and requires strict true improvement", () => {
  withSyntheticDirectory((directory) => {
    const scenarios = [
      { values: [], expected: 0 }, { values: [false, false], expected: 2 },
      { values: [true, false, false], expected: 2 }, { values: [true, false, true], expected: 0 },
      { values: [false, true, false], expected: 1 }, { values: [1, "true", false], expected: 3 }
    ];
    for (const scenario of scenarios) {
      const iterations = scenario.values.map((value) => ({ synthetic: true, material_improvement: value }));
      const evidence = collectFineTuningEvidence(fineTuningPaths(directory, iterations));
      assert.equal(evidence.prompt_plateau_iterations, scenario.expected);
      assert.equal(evidence.real_gold_training_records, 0);
      assert.equal(evidence.real_gold_test_records, 0);
      assert.equal(evidence.teacher_qwk, null);
      assert.equal(evidence.annotation_is_real, false);
      assert.equal(evidence.baseline_is_real, false);
      assert.equal(evidence.regression_is_real, false);
    }
  });
});

test("batch25 plateau preserves malformed history and earlier file read failures", () => {
  withSyntheticDirectory((directory) => {
    // null 记录在查到改善前仍报错；先查到末尾改善时不访问更早的坏记录。
    const paths = fineTuningPaths(directory, [null, { synthetic: true, material_improvement: true }]);
    assert.equal(collectFineTuningEvidence(paths).prompt_plateau_iterations, 0);
    writeSyntheticJson(directory, "history.json", { synthetic: true, iterations: [null, { synthetic: true, material_improvement: false }] });
    assert.throws(() => collectFineTuningEvidence(paths), TypeError);
    writeSyntheticJson(directory, "history.json", { synthetic: true, iterations: null });
    assert.throws(() => collectFineTuningEvidence(paths), TypeError);
    assert.throws(() => collectFineTuningEvidence({ ...paths, annotation: join(directory, "absent.json") }), { code: "ENOENT" });
  });
});
