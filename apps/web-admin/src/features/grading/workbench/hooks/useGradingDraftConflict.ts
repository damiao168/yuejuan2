import { useQueryClient } from "@tanstack/react-query";
import { removeReviewDraftFallback, loadReviewDraftFallback, saveReviewDraftFallback } from "../../../../auth/reviewDraftFallback";
import { reviewTaskContextKeys } from "../../../../query/reviewTaskContext";
import { createDraftSnapshot, createInitialDraft, fallbackSnapshot, formatError, serverDraftSnapshot } from "../gradingWorkbench.model";
import { loadTaskContext } from "../gradingTaskContext";
import type { DraftFallbackSnapshot } from "../gradingWorkbench.types";
import type { UseGradingReviewActionsOptions } from "./gradingReviewActions.types";

type ConflictOptions = Pick<UseGradingReviewActionsOptions,
  "ctx" | "currentUserId" | "draft" | "viewerMode" | "scale" | "rotation" | "offset" | "autoFit" | "setResolvingConflict" | "queryScope" | "prefetchedTaskRef" | "activeTask" | "activeUserId" | "canViewOriginalImage" | "conflictServerContext" | "setDraftConflict" | "notify" | "draftConflict" | "conflictedDrafts" | "draftSaves" | "setCtx" | "setDraftRevision" | "lastSavedDraft" | "setDraft" | "restoreViewer" | "setDraftSaveStatus"> & { isCurrentSession: () => boolean };

// 冲突读取和合并只操作仍属于本账号、考试与当前任务的草稿；合并续用服务端最新修订号。
export function useGradingDraftConflict({ ctx, currentUserId, draft, viewerMode, scale, rotation, offset, autoFit, setResolvingConflict, queryScope, prefetchedTaskRef, activeTask, activeUserId, canViewOriginalImage, conflictServerContext, setDraftConflict, notify, draftConflict, conflictedDrafts, draftSaves, setCtx, setDraftRevision, lastSavedDraft, setDraft, restoreViewer, setDraftSaveStatus, isCurrentSession }: ConflictOptions) {
  const queryClient = useQueryClient();
  const openDraftConflict = async () => {
    if (!isCurrentSession() || activeTask.current !== ctx?.task.id) return;
    const taskId = ctx.task.id;
    const userId = currentUserId;
    const initial = createInitialDraft(ctx);
    const fallback = loadReviewDraftFallback<unknown>(userId, taskId);
    const local = (fallback && fallbackSnapshot(fallback.snapshot, initial))
      ?? createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit);
    setResolvingConflict(true);
    try {
      if (!isCurrentSession()) return;
      queryClient.removeQueries({ queryKey: reviewTaskContextKeys.task(queryScope, taskId) });
      prefetchedTaskRef.current.delete(taskId);
      const latestContext = await loadTaskContext(queryClient, queryScope, taskId);
      if (activeTask.current !== taskId || activeUserId.current !== userId || !isCurrentSession()) return;
      const server = serverDraftSnapshot(
        createInitialDraft(latestContext),
        latestContext.task.assigned_to === userId ? latestContext.reviewContext.draft : null,
        canViewOriginalImage
      );
      conflictServerContext.current = latestContext;
      setDraftConflict({ taskId, serverRevision: server.revision, local, server: server.snapshot });
    } catch (currentError) {
      if (activeTask.current === taskId && activeUserId.current === userId && isCurrentSession()) {
        notify.error(formatError(currentError));
      }
    } finally {
      if (activeTask.current === taskId && activeUserId.current === userId && isCurrentSession()) setResolvingConflict(false);
    }
  };

  const applyDraftConflict = (merged: DraftFallbackSnapshot) => {
    if (!isCurrentSession()) return;
    const conflict = draftConflict;
    const latestContext = conflictServerContext.current;
    if (!conflict || !latestContext || conflict.taskId !== activeTask.current || latestContext.task.id !== conflict.taskId) return;
    const draftKey = JSON.stringify([currentUserId, conflict.taskId]);
    const serverSnapshot = JSON.stringify(conflict.server);
    const mergedSnapshot = JSON.stringify(merged);
    conflictedDrafts.current.delete(draftKey);
    // 合并结果以服务端最新修订号续存；旧版本号不能再次提交，否则会把已解决的冲突重新带回去。
    draftSaves.current.observeRevision(draftKey, conflict.serverRevision);
    if (mergedSnapshot === serverSnapshot) removeReviewDraftFallback(currentUserId, conflict.taskId);
    else saveReviewDraftFallback(currentUserId, conflict.taskId, merged);
    setCtx(latestContext);
    setDraftRevision(conflict.serverRevision);
    lastSavedDraft.current = serverSnapshot;
    setDraft(merged.draft);
    restoreViewer(merged.viewer);
    setDraftConflict(null);
    conflictServerContext.current = null;
    setDraftSaveStatus(mergedSnapshot === serverSnapshot ? "saved" : "saving");
    setResolvingConflict(false);
  };

  return { openDraftConflict, applyDraftConflict };
}
