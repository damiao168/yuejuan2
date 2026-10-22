import type { AiGrade } from "../../../api/review";
import type { WorkbenchContext } from "./gradingWorkbench.types";

export interface FastConfirmCandidate { grade: AiGrade; score: number; threshold: number }

// 批量确认的范围有意小于普通教师阅卷。
// 有评分细则、数学证据、高风险或 AI 证据不完整的条目，
// 仍留在完整工作台中逐项判断。
export function fastConfirmCandidate(context: WorkbenchContext, userId: string): FastConfirmCandidate | null {
  // 当前执行已失败时，历史成功建议也不能快捷确认；不要依赖调用方是否生成了警告文案。
  if (context.reviewContext.ai_failure) return null;
  const snapshot = context.reviewContext.question_snapshot;
  if (snapshot.scoring_policy_snapshot.mode !== "AI_FAST_CONFIRM" || !["R1", "R2"].includes(snapshot.risk_tier) || snapshot.subject_code === "mathematics") return null;
  if (context.task.assigned_to !== userId || !["assigned", "in_progress", "returned"].includes(context.task.status)) return null;
  if ((context.question?.rubric?.points?.length ?? 0) > 0 || context.reviewContext.draft?.score != null || !context.segmentImageUrl || context.warnings.length > 0) return null;
  const grade = context.aiGrades[0];
  if (grade?.status !== "succeeded" || grade.delivery_mode === "shadow_only") return null;
  if (!grade || grade.mock || grade.risk_flags.length > 0 || !grade.evidence.some((evidence) => evidence.answer_text?.trim())) return null;
  const threshold = Math.max(0.95, snapshot.scoring_policy_snapshot.confidence_threshold ?? 0);
  if (!Number.isFinite(grade.confidence) || grade.confidence < threshold) return null;
  if (!Number.isFinite(grade.suggested_score) || grade.suggested_score < 0 || grade.suggested_score > (context.question?.score ?? grade.max_score)) return null;
  if (grade.max_score !== (context.question?.score ?? grade.max_score)) return null;
  return { grade, score: grade.suggested_score, threshold };
}
