import { useEffect, useState } from "react";
import type { Exam } from "../../../api/exams";
import { listClasses, listGrades } from "../../../api/org";

export type ExamEducationStage = "junior" | "senior" | "unknown";

/**
 * 按考试关联班级所属年级判断学段，用于发布时的排名默认值。读取失败、未关联班级或初高中混合时
 * 返回 unknown / junior，调用方按义务教育学段的更严格规则处理，不能因读不到就默认公开排名。
 */
export function useExamEducationStage(exam: Exam | undefined, enabled: boolean): ExamEducationStage {
  const [result, setResult] = useState<{ examId: string; stage: ExamEducationStage } | null>(null);
  const examId = exam?.id ?? "";
  const schoolId = exam?.school_id ?? "";
  const sessionGradeId = exam?.exam_session_grade_id ?? "";
  const classIds = (exam?.class_ids ?? []).join(",");

  useEffect(() => {
    if (!enabled || !examId) return;
    let cancelled = false;
    void (async () => {
      try {
        const [grades, classes] = await Promise.all([listGrades(schoolId || undefined), classIds ? listClasses() : Promise.resolve({ classes: [] })]);
        const wanted = new Set(classIds ? classIds.split(",") : []);
        const gradeIds = new Set(classes.classes.filter(item => wanted.has(item.id)).map(item => item.grade_id));
        if (sessionGradeId) gradeIds.add(sessionGradeId);
        const stages = new Set(grades.grades.filter(grade => gradeIds.has(grade.id)).map(grade => grade.education_stage));
        const stage: ExamEducationStage = stages.has("junior") ? "junior" : stages.has("senior") ? "senior" : "unknown";
        if (!cancelled) setResult({ examId, stage });
      } catch {
        if (!cancelled) setResult({ examId, stage: "unknown" });
      }
    })();
    return () => { cancelled = true; };
  }, [enabled, examId, schoolId, sessionGradeId, classIds]);

  return result?.examId === examId ? result.stage : "unknown";
}
