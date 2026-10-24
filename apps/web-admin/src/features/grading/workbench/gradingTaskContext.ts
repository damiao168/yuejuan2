import type { AiGrade } from "../../../api/review";
import type { AnswerSegment } from "../../../api/submissions";
import type { QueryClient } from "@tanstack/react-query";
import { assertReviewTaskContextScope, reviewTaskContextQueryOptions, type ReviewTaskContextScope } from "../../../query/reviewTaskContext";
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

export async function loadTaskContext(queryClient: QueryClient, scope: ReviewTaskContextScope, taskId: string): Promise<WorkbenchContext> {
  assertReviewTaskContextScope(scope);
  const reviewContext = await queryClient.fetchQuery(reviewTaskContextQueryOptions(scope, taskId));
  assertReviewTaskContextScope(scope);
  const task = reviewContext.task;
  // 初次加载仍遵守第二意见的显式揭示规则，不能因预取任务而提前填入 AI 建议。
  const metadata = secondOpinionMetadata(reviewContext, false);
  // 当前失败由服务端绑定答案和冻结评分版本；不能回退到更早的成功建议作为本次输出。
  const suggestion = reviewContext.ai_failure ? { warning: "AI 建议未完成，请人工评分。" }
    : metadata ? resolveTaskAISuggestion(metadata) : {};
  const history = metadata ? (reviewContext.ai_second_opinion?.history ?? []).flatMap((item) => {
    const resolved = resolveTaskAISuggestion(item);
    return resolved.grade?.answer_segment_id === task.answer_segment_id ? [resolved.grade] : [];
  }) : [];
  // 当前答题片段的有效历史优先；历史为空时才使用当前已允许展示的建议。
  let aiGrades: AiGrade[] = [];
  if (history.length) aiGrades = history;
  else if (suggestion.grade) aiGrades = [suggestion.grade];
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
  const recognizedText = artifact.source === "image_observation" ? artifact.ocr_text || ""
    : reviewContext.automation_result?.recognized_answer || artifact.ocr_text || artifact.raw_answer || "";
  return {
    task,
    reviewContext,
    segment,
    question,
    pages: [],
    ocrTasks: [],
    ocrResults: [],
    aiGrades,
    automationResult: reviewContext.automation_result,
    warnings: suggestion.warning ? [suggestion.warning] : [],
    ocrText: recognizedText,
    segmentImageUrl: artifact.segment_image_url,
    answerScope: artifact.answer_scope ?? "question",
    gradingGroupImageUrl: artifact.grading_group_image_url,
    gradingGroupId: artifact.grading_group_id,
    originalImageUrl: scope.allowOriginalImage ? artifact.original_image_url : undefined
  };
}

export async function fetchTaskBundle(queryClient: QueryClient, scope: ReviewTaskContextScope, taskId: string, currentUserId: string, initialExamId: string): Promise<PrefetchedTaskBundle> {
  const context = await loadTaskContext(queryClient, scope, taskId);
  if (initialExamId && context.task.exam_id !== initialExamId) throw new Error("该阅卷任务不属于当前考试，已停止加载。");
  if (context.task.assigned_to !== currentUserId) throw new Error("该阅卷任务已不再分配给当前用户。");
  return { task: context.task, context };
}
