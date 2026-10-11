import { describe, expect, it } from "vitest";
import type { CalibrationMetrics, CalibrationPolicy } from "../../api/calibration";
import { calibrationThresholdChecks, formatCriterionValue, formatMetric } from "./calibrationPresentation";

const policy: CalibrationPolicy = {
  id: "policy", exam_id: "exam", question_id: "question", archetype_code: "extended_response", max_score: 10,
  minimum_samples: 3, maximum_mae: 1, minimum_exact_agreement: 0.6, minimum_within_one_agreement: 0.9,
  minimum_criterion_agreement: 0.8, maximum_severe_rate: 0, severe_error_threshold: 3,
  qualification_validity_days: 30, revision: 4, created_at: "", updated_at: ""
};
const metrics: CalibrationMetrics = {
  sample_count: 3, criterion_sample_count: 3, mae: 1, exact_agreement: 0.6,
  within_one_agreement: 0.9, criterion_agreement: 0.8, severe_disagreement_rate: 0
};

describe("阅卷员校准阈值展示", () => {
  it("包含阈值边界，不把等于阈值判为未通过", () => {
    expect(calibrationThresholdChecks(metrics, policy).every((check) => check.passed)).toBe(true);
    expect(calibrationThresholdChecks({ ...metrics, mae: 1.001, severe_disagreement_rate: 0.001 }, policy)
      .filter((check) => !check.passed).map((check) => check.key)).toEqual(["mae", "severe"]);
  });

  it("缺失配置不自行判定，采分点数据缺失仅在已配置门槛时不通过", () => {
    expect(calibrationThresholdChecks(metrics)).toEqual([]);
    const missing = { ...metrics, criterion_agreement: undefined };
    expect(calibrationThresholdChecks(missing, policy).find((check) => check.key === "criterion")?.passed).toBe(false);
    expect(calibrationThresholdChecks(missing, { ...policy, minimum_criterion_agreement: undefined })
      .find((check) => check.key === "criterion")?.passed).toBe(true);
  });

  it("保留零分并明确缺失采分点数据", () => {
    expect(formatMetric("criterion", undefined)).toBe("无采分点数据");
    expect(formatMetric("mae", 0)).toBe("0.00");
    expect(formatMetric("exact", 0.625)).toBe("63%");
    expect(formatCriterionValue(0)).toBe("0");
    expect(formatCriterionValue(null)).toBe("未选择");
    expect(formatCriterionValue({ selected: true })).toBe('{"selected":true}');
  });
});
