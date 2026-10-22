import type { CalibrationMetrics, CalibrationPolicy } from "../../api/calibration";

export function calibrationThresholdChecks(metrics: CalibrationMetrics, policy?: CalibrationPolicy) {
  // 未设置采分点门槛时不据此拦截；已设置门槛但缺失数据时，按不达标处理。
  if (!policy) return [];
  return [
    { key: "mae", label: "平均分差（分）", value: metrics.mae, passed: metrics.mae <= policy.maximum_mae },
    { key: "exact", label: "分数完全相同的比例", value: metrics.exact_agreement, passed: metrics.exact_agreement >= policy.minimum_exact_agreement },
    { key: "within-one", label: "分差不超过 1 分的比例", value: metrics.within_one_agreement, passed: metrics.within_one_agreement >= policy.minimum_within_one_agreement },
    { key: "criterion", label: "采分点判断相同的比例", value: metrics.criterion_agreement, passed: policy.minimum_criterion_agreement === undefined || (metrics.criterion_agreement ?? -1) >= policy.minimum_criterion_agreement },
    { key: "severe", label: "严重分差的比例", value: metrics.severe_disagreement_rate, passed: metrics.severe_disagreement_rate <= policy.maximum_severe_rate }
  ];
}

export function formatMetric(key: string, value: number | undefined) {
  if (value === undefined) return "无采分点数据";
  return key === "mae" ? value.toFixed(2) : `${Math.round(value * 100)}%`;
}

export function formatCriterionValue(value: unknown) {
  if (value === undefined || value === null || value === "") return "未选择";
  if (typeof value === "object") return JSON.stringify(value);
  // 明确区分可直接转换的值；结构化采分点仍保留上面的 JSON 表示。
  switch (typeof value) {
    case "string": return value;
    case "number":
    case "boolean":
    case "bigint":
    case "symbol":
    case "function": return String(value);
    default: return "未选择";
  }
}
