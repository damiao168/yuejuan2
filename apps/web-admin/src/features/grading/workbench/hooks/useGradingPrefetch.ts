import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { isReviewTaskContextScopeCurrent, type ReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { downloadReviewWorkspaceImage, type ReviewTask } from "../../../../api/review";
import { fetchTaskBundle } from "../gradingTaskContext";
import type { PrefetchedTaskBundle } from "../gradingWorkbench.types";

export interface UseGradingPrefetchOptions {
  queryScope: ReviewTaskContextScope;
  canWork: boolean;
  canViewOriginalImage: boolean;
  currentUserId: string;
  initialExamId: string;
  selectedTaskId: string;
  tasks: ReviewTask[];
}

export function useGradingPrefetch({
  queryScope,
  canWork,
  currentUserId,
  initialExamId,
  selectedTaskId,
  tasks
}: UseGradingPrefetchOptions) {
  const queryClient = useQueryClient();
  const prefetchedTaskRef = useRef(new Map<string, Promise<PrefetchedTaskBundle>>());
  const prefetchedPreviewRef = useRef(new Map<string, Promise<Awaited<ReturnType<typeof downloadReviewWorkspaceImage>> | null>>());

  useEffect(() => {
    if (!canWork || tasks.length === 0 || !isReviewTaskContextScopeCurrent(queryScope)) return;
    for (const taskId of prefetchedTaskRef.current.keys()) {
      if (taskId !== selectedTaskId && !tasks.some((task) => task.id === taskId)) prefetchedTaskRef.current.delete(taskId);
    }
    for (const taskId of prefetchedPreviewRef.current.keys()) {
      if (taskId !== selectedTaskId && !tasks.some((task) => task.id === taskId)) prefetchedPreviewRef.current.delete(taskId);
    }
    for (const task of tasks) {
      if (task.assigned_to !== currentUserId || !["assigned", "in_progress", "returned"].includes(task.status) || prefetchedTaskRef.current.has(task.id)) continue;
      const bundlePromise = fetchTaskBundle(queryClient, queryScope, task.id, currentUserId, initialExamId);
      prefetchedTaskRef.current.set(task.id, bundlePromise);
      prefetchedPreviewRef.current.set(
        task.id,
        bundlePromise
          .then((bundle) => isReviewTaskContextScopeCurrent(queryScope) && bundle.context.segmentImageUrl ? downloadReviewWorkspaceImage(bundle.context.segmentImageUrl) : null)
          .catch(() => null)
      );
      void bundlePromise.catch(() => {
        // 旧预取失败不能删除同一任务已经重新发起的新请求。
        if (prefetchedTaskRef.current.get(task.id) === bundlePromise) {
          prefetchedTaskRef.current.delete(task.id);
          prefetchedPreviewRef.current.delete(task.id);
        }
      });
    }
  }, [canWork, currentUserId, initialExamId, selectedTaskId, tasks, queryClient, queryScope]);

  return { prefetchedTaskRef, prefetchedPreviewRef };
}
