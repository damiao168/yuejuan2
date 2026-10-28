export const assessmentArchetypeLabels: Record<string, string> = {
  selected_response: "选择作答", exact_text: "精确文本", numeric_expression: "数值与表达式",
  structured_steps: "分步解答", short_constructed: "简短作答", extended_response: "开放论述",
  diagram_graph: "作图与图像", table_experiment: "表格与实验"
};
export function assessmentArchetypeLabel(code: string) { return code ? assessmentArchetypeLabels[code] ?? "其他作答类型" : "未配置"; }

export const typicalErrorLabels: Record<string, string> = {
  unit_error: "单位错误", condition_error: "条件使用错误", calculation_error: "计算错误", balancing_error: "配平错误",
  alternative_valid_path: "其他有效解法", missing_key_step: "缺少关键步骤", correct_conclusion_insufficient_process: "结论正确但过程不足",
  content_strong_language_weak: "内容充分但语言表达较弱", language_strong_content_off_topic: "语言流畅但内容偏题", structural_imbalance: "结构不均衡",
  insufficient_material_evidence: "材料证据不足", relation_error: "关系判断错误", typical_error: "典型错误"
};
export function typicalErrorLabel(value: string) {
  if (typicalErrorLabels[value]) return typicalErrorLabels[value];
  // 自定义中文说明保留；未知机器标签只作其他类型，原值由调用方放入诊断详情。
  return /^[a-z][a-z0-9_:-]*$/i.test(value) ? "其他典型错误" : value;
}
export const typicalErrorOptions = Object.entries(typicalErrorLabels).map(([value, label]) => ({ value, label }));
