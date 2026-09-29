import { subjectScore } from "./createExamDraft";
import type { CreateExamDraft } from "./types";

export interface CreateExamValidationIssue {
  field: string;
  message: string;
}

export function createExamFieldId(field: string) {
  return `exam-create-${field.replace(/[^a-zA-Z0-9_-]/g, "-")}`;
}

export function validateCreateExam(draft: CreateExamDraft): CreateExamValidationIssue[] {
  const issues: CreateExamValidationIssue[] = [];
  if (!draft.name.trim()) issues.push({ field: "name", message: "请填写考试名称" });
  if (!draft.examType) issues.push({ field: "examType", message: "请选择考试类型" });
  if (!draft.schoolId) issues.push({ field: "schoolId", message: "请选择考试所属学校" });
  if (!draft.gradeId) issues.push({ field: "gradeId", message: "请选择考试年级" });
  if (!draft.classIds.length) issues.push({ field: "classIds", message: "请至少选择一个参考班级" });
  if (draft.creationMode === "template" && !draft.templateId) issues.push({ field: "templateId", message: "请选择一个考试方案" });
  if (!draft.subjects.length) issues.push({ field: "subjects", message: "请至少选择一个考试科目" });
  for (const subject of draft.subjects) {
    const prefix = `subject.${subject.subject}`;
    if (subject.totalScore <= 0) issues.push({ field: `${prefix}.totalScore`, message: "满分必须大于 0" });
    if (subject.durationMinutes <= 0) issues.push({ field: `${prefix}.durationMinutes`, message: "考试时间必须大于 0" });
    if (subject.candidateRule === "subject_selected_classes" && !subject.classIds.length) {
      issues.push({ field: `${prefix}.classIds`, message: "请为该科目选择参考班级" });
    }
    // 先上传资料的流程允许稍后补题目；已有模板分区时，才要求分区合计与满分一致。
    if (subject.sections.length > 0) {
      for (const section of subject.sections) {
        if (!section.title.trim() || !section.questionType || section.questionCount <= 0 || section.scorePerQuestion <= 0) {
          issues.push({ field: prefix, message: "模板中的试卷分区不完整" });
          break;
        }
      }
      if (Math.abs(subjectScore(subject) - subject.totalScore) > 0.001) {
        issues.push({ field: `${prefix}.totalScore`, message: "模板题目分值合计必须等于科目满分" });
      }
    }
  }
  if (!draft.gradingMode) issues.push({ field: "gradingMode", message: "请选择阅卷方式" });
  return issues;
}
