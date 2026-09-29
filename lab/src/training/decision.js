import { existsSync, readFileSync } from "node:fs";

function readJson(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

export function collectFineTuningEvidence(paths = {}) {
  const annotationPath = paths.annotation ?? "evals/reports/annotation-reliability.json";
  const selectionPath = paths.selection ?? "evals/reports/model-selection-decision.json";
  const regressionPath = paths.regression ?? "evals/reports/local-regression-v2.json";
  const realManifestPath = paths.real_manifest ?? "evals/gold/real/manifest.json";
  const promptHistoryPath = paths.prompt_history ?? "config/prompt-optimization-history.json";
  const annotation = readJson(annotationPath);
  const selection = readJson(selectionPath);
  const regression = readJson(regressionPath);
  const promptHistory = readJson(promptHistoryPath);
  const realManifest = existsSync(realManifestPath) ? readJson(realManifestPath) : undefined;
  return {
    evidence_paths: { annotation: annotationPath, selection: selectionPath, regression: regressionPath, real_manifest: realManifestPath, prompt_history: promptHistoryPath },
    real_gold_training_records: realManifest?.splits?.train?.records ?? 0,
    real_gold_test_records: realManifest?.splits?.test?.records ?? 0,
    teacher_qwk: annotation.synthetic_only ? null : annotation.metrics?.qwk ?? null,
    annotation_is_real: annotation.synthetic_only === false,
    baseline_is_real: selection.synthetic_only === false,
    regression_is_real: regression.synthetic_only === false,
    license_approved: realManifest?.license_approved === true,
    privacy_approved: realManifest?.privacy_approved === true,
    grouped_split_verified: realManifest?.leakage_audit?.passed === true,
    frozen_test_set: realManifest?.test_set_frozen === true,
    // 从最新记录往回数连续没有实质改善的迭代，不能用历史累计次数代替平台期。
    prompt_plateau_iterations: promptHistory.iterations.slice().reverse()
      .findIndex((iteration) => iteration.material_improvement === true) < 0
      ? promptHistory.iterations.length
      : promptHistory.iterations.slice().reverse().findIndex((iteration) => iteration.material_improvement === true),
    latest_prompt_regression_passed: regression.passed === true,
    selected_base_model: selection.selected_local_llm
  };
}

export function evaluateFineTuningDecision(evidence, gate) {
  const unmet = [];
  if (evidence.real_gold_training_records < gate.minimum_real_gold_training_records) {
    unmet.push(`real_gold_training_records ${evidence.real_gold_training_records} < ${gate.minimum_real_gold_training_records}`);
  }
  if (evidence.real_gold_test_records < gate.minimum_real_gold_test_records) {
    unmet.push(`real_gold_test_records ${evidence.real_gold_test_records} < ${gate.minimum_real_gold_test_records}`);
  }
  if (!Number.isFinite(evidence.teacher_qwk) || evidence.teacher_qwk < gate.minimum_teacher_qwk) {
    unmet.push(`teacher_qwk ${evidence.teacher_qwk ?? "missing"} < ${gate.minimum_teacher_qwk}`);
  }
  if (evidence.prompt_plateau_iterations < gate.minimum_prompt_plateau_iterations) {
    unmet.push(`prompt_plateau_iterations ${evidence.prompt_plateau_iterations} < ${gate.minimum_prompt_plateau_iterations}`);
  }
  if (gate.require_license_approval && !evidence.license_approved) unmet.push("real dataset license approval is missing");
  if (gate.require_privacy_approval && !evidence.privacy_approved) unmet.push("real dataset privacy approval is missing");
  if (gate.require_grouped_split && !evidence.grouped_split_verified) unmet.push("real grouped split verification is missing");
  if (gate.require_real_baseline && !evidence.baseline_is_real) unmet.push("real Gold baseline evaluation is missing");
  if (gate.require_frozen_test_set && !evidence.frozen_test_set) unmet.push("frozen real test set is missing");
  if (gate.require_latest_regression_passed && !evidence.latest_prompt_regression_passed) unmet.push("latest prompt regression did not pass");
  return {
    decision: unmet.length ? "do_not_train" : "qlora_eligible",
    eligible: unmet.length === 0,
    unmet_conditions: unmet,
    selected_base_model: evidence.selected_base_model,
    no_chain_of_thought_training: true,
    test_set_must_remain_unseen: true
  };
}
