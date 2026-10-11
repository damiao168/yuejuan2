/**
 * 按评分语义定义颜色。界面代码应引用这些含义，
 * 不要为成绩状态临时挑选红色、琥珀色或蓝色。
 */
export const gradingColors = {
  correct: "var(--eg-grading-correct)",
  partial: "var(--eg-grading-partial)",
  incorrect: "var(--eg-grading-incorrect)",
  aiSuggestion: "var(--eg-grading-ai-suggestion)",
  lowConfidence: "var(--eg-grading-low-confidence)",
  disagreement: "var(--eg-grading-disagreement)",
  humanOverride: "var(--eg-grading-human-override)"
} as const;
