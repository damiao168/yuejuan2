const answerLabels: Record<string, string> = {
  value: "答案值", text: "答案", answer: "答案", standard_answer: "标准答案",
  answers: "各空答案", blanks: "各空答案", unit: "单位", equivalent_answers: "允许写法"
};

// 只转换已知展示结构，保存仍使用接口原值；未知扩展需保留原始内容供人工核对。
export function recognizedAnswerText(value: unknown, questionType?: string, depth = 0): string {
  if (value == null || typeof value === "string" && !value.trim()) return "未识别到答案";
  if (depth > 8) return "复杂答案，请展开原始内容核对";
  if (typeof value === "boolean" && questionType === "true_false") return value ? "正确（√）" : "错误（×）";
  if (Array.isArray(value)) {
    if (questionType === "multiple_choice" || questionType === "single_choice") return value.map(item => recognizedAnswerText(item, undefined, depth + 1)).join("、");
    return value.map((item, index) => `${questionType === "fill_blank" ? `第 ${index + 1} 空` : `写法 ${index + 1}`}：${recognizedAnswerText(item, undefined, depth + 1)}`).join("\n\n");
  }
  if (typeof value === "object") {
    const entries = Object.entries(value).filter(([key]) => answerLabels[key]);
    return entries.length ? entries.map(([key, item]) => `${answerLabels[key]}：${recognizedAnswerText(item, ["answers", "blanks"].includes(key) ? "fill_blank" : undefined, depth + 1)}`).join("\n\n") : "结构化答案，请展开原始内容核对";
  }
  return String(value);
}

export function answerNeedsRawDetails(value: unknown, depth = 0): boolean {
  if (depth > 8) return true;
  if (Array.isArray(value)) return value.some(item => answerNeedsRawDetails(item, depth + 1));
  return Boolean(value && typeof value === "object" && Object.entries(value).some(([key, item]) => !answerLabels[key] || answerNeedsRawDetails(item, depth + 1)));
}
