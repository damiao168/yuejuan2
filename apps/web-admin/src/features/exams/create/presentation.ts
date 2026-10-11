import { examTypeOptions } from "../../../constants/examCatalog";
export { examTypeOptions } from "../../../constants/examCatalog";

export const gradingPresentation = {
  ai_assisted: { title: "AI 建议与教师确认", description: "AI 为符合条件的题目生成建议，教师逐题确认最终分数；规则自动评分以各题生效策略为准。" },
  auto_objective_only: { title: "仅客观题自动评分", description: "客观题自动完成，主观题由教师评分。" },
  human_review_required: { title: "全部人工确认", description: "系统提供识别辅助，主观题评分均由教师确认。" },
  double_mark: { title: "双评", description: "两位教师独立评分。" },
  blind_double_mark: { title: "盲双评", description: "两位教师独立评分，互不可见对方结果。" }
} as const;

export const publishPolicyOptions = [
  { label: "阅卷完成并由管理员确认后发布", value: "after_admin_approval" }
];

export function examTypeLabel(value: string) {
  return examTypeOptions.find((item) => item.value === value)?.label ?? "其他考试类型";
}

export function gradingLabel(value: string) {
  return gradingPresentation[value as keyof typeof gradingPresentation]?.title ?? "其他阅卷方式";
}

export function publishPolicyLabel(value: string) {
  return publishPolicyOptions.find((item) => item.value === value)?.label ?? "其他发布方式";
}
