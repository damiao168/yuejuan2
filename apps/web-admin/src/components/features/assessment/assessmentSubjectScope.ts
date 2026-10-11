import type { SubjectCode } from "../../../api/assessment";

const subjectAliases = new Map<string, SubjectCode>([
  ["chinese", "chinese"], ["语文", "chinese"],
  ["mathematics", "mathematics"], ["math", "mathematics"], ["数学", "mathematics"],
  ["english", "english"], ["英语", "english"],
  ["physics", "physics"], ["物理", "physics"],
  ["chemistry", "chemistry"], ["化学", "chemistry"],
  ["biology", "biology"], ["生物", "biology"],
  ["history", "history"], ["历史", "history"],
  ["geography", "geography"], ["地理", "geography"],
  ["ethics_politics", "ethics_politics"], ["politics", "ethics_politics"], ["civics", "ethics_politics"],
  ["道德与法治", "ethics_politics"], ["思想政治", "ethics_politics"], ["政治", "ethics_politics"]
]);

// 与后台 assessment.NormalizeSubjectCode 使用同一组别名；未知或综合学科返回空值，继续允许逐题选择学科。
export function normalizeAssessmentSubject(value: string): SubjectCode | undefined {
  return subjectAliases.get(value.trim().toLowerCase());
}
