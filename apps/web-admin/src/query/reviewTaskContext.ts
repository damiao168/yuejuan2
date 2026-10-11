import { useMemo } from "react";
import { queryOptions, useQuery, type QueryClient } from "@tanstack/react-query";
import { getBusinessCommandSession, isBusinessCommandSessionCurrent } from "../api/businessCommand";
import { ApiClientError } from "../api/client";
import { getReviewTaskContext } from "../api/review";

export interface ReviewTaskContextScope {
  session: ReturnType<typeof getBusinessCommandSession>;
  examId: string;
  allowOriginalImage: boolean;
}

export function createReviewTaskContextScope(examId = "", allowOriginalImage = false): ReviewTaskContextScope {
  return { session: getBusinessCommandSession(), examId, allowOriginalImage };
}

export function useReviewTaskContextScope(examId = "", allowOriginalImage = false) {
  const session = getBusinessCommandSession();
  return useMemo(() => ({ session, examId, allowOriginalImage }), [session, examId, allowOriginalImage]);
}

export function isReviewTaskContextScopeCurrent(scope: ReviewTaskContextScope): boolean {
  return isBusinessCommandSessionCurrent(scope.session);
}

export function assertReviewTaskContextScope(scope: ReviewTaskContextScope): void {
  if (!isReviewTaskContextScopeCurrent(scope)) throw new ApiClientError(401, "session_scope_changed", "登录状态已变化，请重新打开阅卷任务");
}

export const reviewTaskContextKeys = {
  all: ["review-task-context"] as const,
  // 同一任务包含当前人的草稿和查看权限；旧会话的清缓存不能碰到重登后的结果。
  task: (scope: ReviewTaskContextScope, taskId: string) => ["review-task-context", scope.session.scope, scope.session.generation, taskId] as const,
  detail: (scope: ReviewTaskContextScope, taskId: string) => [...reviewTaskContextKeys.task(scope, taskId), scope.examId, scope.allowOriginalImage] as const
};

export function reviewTaskContextQueryOptions(scope: ReviewTaskContextScope, taskId: string) {
  return queryOptions({
    queryKey: reviewTaskContextKeys.detail(scope, taskId),
    queryFn: async ({ signal }) => {
      assertReviewTaskContextScope(scope);
      const { context } = await getReviewTaskContext(taskId, signal);
      assertReviewTaskContextScope(scope);
      // 同一管理员进入教师体验时也不缓存整卷地址；菜单限制不能代替数据边界。
      return scope.allowOriginalImage ? context : { ...context, answer_artifact: { ...context.answer_artifact, original_image_url: undefined } };
    },
    enabled: Boolean(taskId),
    staleTime: 10_000
  });
}

export function useReviewTaskContext(scope: ReviewTaskContextScope, taskId: string) {
  return useQuery(reviewTaskContextQueryOptions(scope, taskId));
}

export function invalidateReviewTaskContext(queryClient: QueryClient, scope: ReviewTaskContextScope, taskId: string) {
  return queryClient.invalidateQueries({ queryKey: reviewTaskContextKeys.task(scope, taskId) });
}
