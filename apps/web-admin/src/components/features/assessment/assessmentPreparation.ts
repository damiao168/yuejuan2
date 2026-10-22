import type { ReadinessScoringPreview } from "../../../api/configuration";

export const preparationScoringModeLabels: Record<string, string> = {
  RULE_AUTO: "规则自动判分", AI_ASSIST: "系统建议分数，教师逐题确认", AI_FAST_CONFIRM: "系统建议分数，教师快速确认",
  HUMAN_PRIMARY: "教师人工评分", DUAL_HUMAN: "两名教师独立评分", MANUAL_ONLY: "仅人工评分"
};

export function preparationScoringModeLabel(mode: string) {
  return Object.prototype.hasOwnProperty.call(preparationScoringModeLabels, mode) ? preparationScoringModeLabels[mode] : "评分方式待核对";
}

export function preparationScoringSummary(preview: ReadinessScoringPreview[]) {
  const counts = new Map<string, number>();
  for (const item of preview) {
    if (item.blocking_reason) continue;
    const label = preparationScoringModeLabel(item.processing_mode ?? item.scoring_policy.mode);
    counts.set(label, (counts.get(label) ?? 0) + 1);
  }
  return [...counts].map(([label, count]) => `${label} ${count} 题`).join("；");
}

export const preparationScoringSourceLabels: Record<ReadinessScoringPreview["source"], string> = {
  default: "系统匹配", saved: "单题设置", snapshot: "已冻结方案"
};
