import type { EducationStage, EligibilityAxis, ExamRiskTier, SubjectCode } from "../../api/scoringAssurance";

export type EligibilitySelection = Partial<EligibilityAxis>;

export const eligibilitySubjects: Array<{ value: SubjectCode; label: string }> = [
  { value: "chinese", label: "语文" }, { value: "mathematics", label: "数学" }, { value: "english", label: "英语" },
  { value: "physics", label: "物理" }, { value: "chemistry", label: "化学" }, { value: "biology", label: "生物" },
  { value: "history", label: "历史" }, { value: "geography", label: "地理" }, { value: "ethics_politics", label: "道德与法治 / 思想政治" }
];
export const eligibilityStages: Array<{ value: EducationStage; label: string }> = [{ value: "junior", label: "初中" }, { value: "senior", label: "高中" }];
export const eligibilityArchetypes = [
  ["selected_response", "选择题"], ["exact_text", "精确文本作答"], ["numeric_expression", "数值作答"], ["structured_steps", "步骤题"],
  ["short_constructed", "简答题"], ["extended_response", "长作答"], ["diagram_graph", "图表题"], ["table_experiment", "表格 / 实验题"]
].map(([value, label]) => ({ value, label }));
export const eligibilityRisks: Array<{ value: ExamRiskTier; label: string }> = [
  { value: "R1", label: "风险等级 1（R1）" }, { value: "R2", label: "风险等级 2（R2）" }, { value: "R3", label: "风险等级 3（R3）" }
];

export function eligibilitySelection(scope: { stage?: string; subject?: string; archetype?: string; risk?: string }): EligibilitySelection {
  // 组合绑定允许通配和作文等业务题型；准入只接受规范具体题型，不能据名称猜映射或补另一范围的默认值。
  return {
    education_stage: eligibilityStages.find((item) => item.value === scope.stage)?.value,
    subject_code: eligibilitySubjects.find((item) => item.value === scope.subject)?.value,
    archetype_code: eligibilityArchetypes.find((item) => item.value === scope.archetype)?.value,
    risk_tier: eligibilityRisks.find((item) => item.value === scope.risk)?.value
  };
}

export function isCompleteEligibilitySelection(axis: EligibilitySelection): axis is EligibilityAxis {
  return eligibilitySubjects.some((item) => item.value === axis.subject_code) &&
    eligibilityStages.some((item) => item.value === axis.education_stage) &&
    eligibilityArchetypes.some((item) => item.value === axis.archetype_code) &&
    eligibilityRisks.some((item) => item.value === axis.risk_tier);
}

export function eligibilityScopeLabel(axis: EligibilitySelection): string {
  return [
    eligibilityStages.find((item) => item.value === axis.education_stage)?.label || "学段待选择",
    eligibilitySubjects.find((item) => item.value === axis.subject_code)?.label || "学科待选择",
    eligibilityArchetypes.find((item) => item.value === axis.archetype_code)?.label || "具体题型待选择",
    eligibilityRisks.find((item) => item.value === axis.risk_tier)?.label || "风险级别待选择"
  ].join(" · ");
}
