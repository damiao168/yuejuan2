export const scoringModeOptions = [
  ["RULE_AUTO", "规则自动"], ["AI_ASSIST", "AI 建议"], ["AI_FAST_CONFIRM", "AI 快速确认"],
  ["HUMAN_PRIMARY", "人工主评"], ["DUAL_HUMAN", "双人阅卷"], ["MANUAL_ONLY", "仅人工"]
].map(([value, label]) => ({ value, label }));
