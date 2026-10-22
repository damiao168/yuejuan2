const legacyMultipleChoiceKeys = ["allow_partial", "score_per_correct_option", "wrong_option_penalty", "minimum_score"];

export function usesLegacyMultipleChoiceScoring(config: Record<string, unknown>) {
  // 没有固定得分字段时保留历史按选项计分；只有教师明确切换后才改变规则含义。
  return config.partial_score === undefined && config.wrong_answer_score === undefined
    && legacyMultipleChoiceKeys.some((key) => config[key] !== undefined);
}

export function withMultipleChoiceScoreDefaults(config: Record<string, unknown>) {
  if (usesLegacyMultipleChoiceScoring(config)) return config;
  return { ...config, partial_score: config.partial_score === undefined ? 0 : config.partial_score,
    wrong_answer_score: config.wrong_answer_score === undefined ? 0 : config.wrong_answer_score };
}
