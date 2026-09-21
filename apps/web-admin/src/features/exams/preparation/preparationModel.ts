import type { ReadinessCheck } from "../../../api/configuration";

export type PreparationGroupKey = "students" | "materials" | "grading" | "checks";

export interface PreparationTaskGroup {
  key: PreparationGroupKey;
  label: string;
  description: string;
  routeSection?: "students" | "paper";
  checks: ReadinessCheck[];
  passed: number;
  total: number;
  complete: boolean;
}

export function preparationTaskGroups(checks: ReadinessCheck[], gradingLabel: string): PreparationTaskGroup[] {
  const students = checks.filter((check) => check.section === "students");
  const materials = checks.filter((check) => ["paper", "questions", "template"].includes(check.section));
  const build = (
    key: PreparationGroupKey,
    label: string,
    description: string,
    groupChecks: ReadinessCheck[],
    routeSection?: "students" | "paper"
  ): PreparationTaskGroup => ({
    key,
    label,
    description,
    routeSection,
    checks: groupChecks,
    passed: groupChecks.filter((check) => check.passed).length,
    total: groupChecks.length,
    complete: groupChecks.length > 0 && groupChecks.every((check) => check.passed)
  });

  return [
    build("students", "学生范围", "确定参加考试的班级与学生", students, "students"),
    build("materials", "考试资料", "试卷、答案、评分标准与答题卡", materials, "paper"),
    { key: "grading", label: "阅卷方式", description: gradingLabel, checks: [], passed: 1, total: 1, complete: true },
    build("checks", "开考检查", "完成全部真实检查项后确认准备", checks)
  ];
}

