import type { AiGrade } from "../../../api/review";
import type { AnswerSegment } from "../../../api/submissions";
import { appQueryClient } from "../../../query/client";
import { reviewTaskContextQueryOptions } from "../../../query/reviewTaskContext";
import { questionFromContext, secondOpinionMetadata } from "./reviewContext";
import type { PrefetchedTaskBundle, WorkbenchContext } from "./gradingWorkbench.types";

function resolveTaskAISuggestion(value: unknown): { grade?: AiGrade; warning?: string } {
  if (!value || typeof value !== "object") return {};
  const candidate = value as Partial<AiGrade>;
  if (candidate.delivery_mode === "shadow_only") return { warning: "本场考试暂未开放 AI 建议，请按评分细则人工评分。" };
  if (candidate.status === "failed") return { warning: "AI 建议生成失败，已切换为人工阅卷；本任务仍可正常提交。" };
  const complete =
    typeof candidate.id === "string" && candidate.id.length > 0 &&
    typeof candidate.suggested_score === "number" &&
    typeof candidate.max_score === "number" &&
    typeof candidate.confidence === "number" &&
    Array.isArray(candidate.matched_points) &&
    Array.isArray(candidate.missing_points) &&
    Array.isArray(candidate.evidence) &&
    Array.isArray(candidate.risk_flags);
  return complete
    ? { grade: candidate as AiGrade }
    : { warning: "AI 建议数据不完整，已切换为人工阅卷；本任务仍可正常提交。" };
}

export async function loadTaskContext(taskId: string, allowOriginalImage: boolean): Promise<WorkbenchContext> {
  const reviewContext = await appQueryClient.fetchQuery(reviewTaskContextQueryOptions(taskId));
  const task = reviewContext.task;
  // 初次加载仍遵守第二意见的显式揭示规则，不能因预取任务而提前填入 AI 建议。
  const metadata = secondOpinionMetadata(reviewContext, false);
  const suggestion = metadata ? resolveTaskAISuggestion(metadata) : {};
  const history = metadata ? (reviewContext.ai_second_opinion?.history ?? []).flatMap((item) => {
    const resolved = resolveTaskAISuggestion(item);
    return resolved.grade?.answer_segment_id === task.answer_segment_id ? [resolved.grade] : [];
  }) : [];
  const question = questionFromContext(reviewContext);
  const artifact = reviewContext.answer_artifact;
  const segment = {
    id: task.answer_segment_id,
    tenant_id: task.tenant_id,
    submission_id: task.submission_id,
    submission_page_id: "",
    question_id: task.question_id,
    question_no: task.question_no,
    bbox: [],
    source: artifact.source || "context",
    status: artifact.status,
    created_at: task.created_at,
    confidence: artifact.confidence
  } as AnswerSegment;
  const recognizedText = reviewContext.automation_result?.recognized_answer || artifact.ocr_text || artifact.raw_answer || "";
  return {
    task,
    reviewContext,
    segment,
    question,
    pages: [],
    ocrTasks: [],
    ocrResults: [],
    aiGrades: history.length ? history : suggestion.grade ? [suggestion.grade] : [],
    automationResult: reviewContext.automation_result,
    warnings: suggestion.warning ? [suggestion.warning] : [],
    ocrText: recognizedText,
    segmentImageUrl: artifact.segment_image_url,
    originalImageUrl: allowOriginalImage ? artifact.original_image_url : undefined
  };
}

export async function fetchTaskBundle(taskId: string, currentUserId: string, initialExamId: string, allowOriginalImage: boolean): Promise<PrefetchedTaskBundle> {
  const context = await loadTaskContext(taskId, allowOriginalImage);
  if (initialExamId && context.task.exam_id !== initialExamId) throw new Error("该阅卷任务不属于当前考试，已停止加载。");
  if (context.task.assigned_to !== currentUserId) throw new Error("该阅卷任务已不再分配给当前用户。");
  return { task: context.task, context };
}
