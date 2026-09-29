export const PILOT_REQUIREMENTS = Object.freeze([
  ["capability_boundary_safe", "Capability matrix forbids final grade publication"],
  ["model_integrity_verified", "Selected model artifact integrity is verified"],
  ["local_regression_passed", "Frozen local-model regression passes"],
  ["real_dataset_governed", "Governed real Gold dataset exists"],
  ["real_model_selection_passed", "Model selection passed on real Gold data"],
  ["teacher_agreement_pilot_passed", "Teacher agreement pilot gate passes"],
  ["adversarial_pilot_passed", "Adversarial pilot breadth and quality gate passes"],
  ["calibration_fairness_pilot_passed", "Calibration and fairness pilot gate passes"],
  ["operational_latency_passed", "Local one-request latency stays within the pilot ceiling"]
]);

// 这里只聚合调用方提供的检查事实；通过结果允许影子试点，不授权发布最终成绩。
export function evaluatePilotReadiness(evidence) {
  const checks = PILOT_REQUIREMENTS.map(([id, description]) => ({
    id,
    description,
    passed: evidence[id] === true,
    detail: evidence.details?.[id] ?? null
  }));
  const blockers = checks.filter((check) => !check.passed);
  return {
    decision: blockers.length ? "NOT_READY" : "READY_FOR_SHADOW_PILOT",
    ready: blockers.length === 0,
    checks,
    blocker_count: blockers.length,
    blockers: blockers.map((check) => ({ id: check.id, description: check.description, detail: check.detail }))
  };
}
