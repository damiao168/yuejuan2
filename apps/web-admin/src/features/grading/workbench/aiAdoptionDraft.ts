import type { AiGrade } from "../../../api/review";
import type { DraftAIAdoption, ScoreDraft } from "./gradingWorkbench.types";

// 本地回退和服务端草稿都可能来自旧版本；缺少或无效的事实不能还原成“未采纳”。
export function restoreDraftAIAdoption(value: unknown): DraftAIAdoption | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const record = value as Partial<DraftAIAdoption>;
  if (typeof record.taskId !== "string" || !record.taskId || typeof record.aiGradeId !== "string" || !record.aiGradeId
    || typeof record.suggestedScore !== "number" || !Number.isFinite(record.suggestedScore) || typeof record.accepted !== "boolean") return undefined;
  return { taskId: record.taskId, aiGradeId: record.aiGradeId, suggestedScore: record.suggestedScore, accepted: record.accepted };
}

export function manualDraftAIAdoption(taskId: string | undefined, grade: AiGrade | undefined): DraftAIAdoption | undefined {
  // 调用方先核对当前输入及展示权限；这里只记录该有效建议对应的手工决定，不能借用历史模型。
  if (!taskId || !grade?.id || grade.grader_type !== "llm_subjective" || grade.status !== "succeeded" || grade.mock !== false
    || !["teacher_suggestion", "teacher_review"].includes(grade.delivery_mode ?? "") || !Number.isFinite(grade.suggested_score)) return undefined;
  return { taskId, aiGradeId: grade.id, suggestedScore: grade.suggested_score, accepted: false };
}

export function submittedDraftAIAdoption(draft: Pick<ScoreDraft, "score" | "aiAdoption">, taskId: string, grade: AiGrade) {
  const choice = draft.aiAdoption;
  if (!choice || typeof choice.accepted !== "boolean" || !manualDraftAIAdoption(taskId, grade)) return undefined;
  // 恢复后建议可能已换版。保留教师分数，但旧建议的采纳事实不能计入新建议的统计。
  if (choice.taskId !== taskId || choice.aiGradeId !== grade.id || choice.suggestedScore !== grade.suggested_score) return undefined;
  return { ai_grade_id: grade.id, accepted: choice.accepted && draft.score === choice.suggestedScore };
}
