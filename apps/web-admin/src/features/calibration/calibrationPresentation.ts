import type { CalibrationMetrics, CalibrationPolicy } from "../../api/calibration";

export function calibrationThresholdChecks(metrics: CalibrationMetrics, policy?: CalibrationPolicy) {
  // 未设置采分点门槛时不据此拦截；已设置门槛但缺失数据时，按不达标处理。
  if (!policy) return [];
  return [
    { key: "mae", label: "平均绝对误差", value: metrics.mae, passed: metrics.mae <= policy.maximum_mae },
    { key: "exact", label: "完全一致率", value: metrics.exact_agreement, passed: metrics.exact_agreement >= policy.minimum_exact_agreement },
    { key: "within-one", label: "误差不超过 1 分", value: metrics.within_one_agreement, passed: metrics.within_one_agreement >= policy.minimum_within_one_agreement },
    { key: "criterion", label: "采分点一致率", value: metrics.criterion_agreement, passed: policy.minimum_criterion_agreement === undefined || (metrics.criterion_agreement ?? -1) >= policy.minimum_criterion_agreement },
    { key: "severe", label: "严重偏差率", value: metrics.severe_disagreement_rate, passed: metrics.severe_disagreement_rate <= policy.maximum_severe_rate }
  ];
}

export function formatMetric(key: string, value: number | undefined) {
  if (value === undefined) return "无采分点数据";
  return key === "mae" ? value.toFixed(2) : `${Math.round(value * 100)}%`;
}

export function formatCriterionValue(value: unknown) {
  if (value === undefined || value === null || value === "") return "未选择";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
