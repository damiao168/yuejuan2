import { useCallback, useEffect, type Dispatch, type SetStateAction } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { removeReviewDraftFallback, saveReviewDraftFallback } from "../../../../auth/reviewDraftFallback";
import { renewReviewTask } from "../../../../api/review";
import { isReviewTaskContextScopeCurrent, type ReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { createDraftSnapshot, createInitialDraft, formatError } from "../gradingWorkbench.model";
import { commitWorkbenchDraft, readQueuedWorkbenchContext, restoreWorkbenchDraft } from "../workbenchDraftLifecycle";
import type { useAnswerViewer } from "./useAnswerViewer";
import type { useGradingPrefetch } from "./useGradingPrefetch";
import type { WorkbenchTaskState } from "./useWorkbenchTaskState";

interface WorkbenchTaskSessionOptions {
  state: WorkbenchTaskState;
  queryScope: ReviewTaskContextScope;
  currentUserId: string;
  currentTenantId: string;
  initialExamId: string;
  canGrade: boolean;
  canViewOriginalImage: boolean;
  hasSession: boolean;
  selectedTaskId: string;
  setSelectedTaskId: Dispatch<SetStateAction<string>>;
  regionEditing: boolean;
  managing: boolean;
  taskPreviewOpen: boolean;
  viewer: Pick<ReturnType<typeof useAnswerViewer>, "mode" | "scale" | "rotation" | "offset" | "autoFit" | "invalidateContent" | "prepareContext" | "restore">;
  prefetch: ReturnType<typeof useGradingPrefetch>;
  resetScoreShortcuts: () => void;
  resetWorkbenchScope: () => void;
  loadTasks: () => Promise<unknown>;
  loadGraders: () => Promise<unknown>;
  isCurrentScope: () => boolean;
}

// 上下文、草稿和租约共享任务/身份边界；保存仍委托既有 TaskDraftSaveQueue 与 lifecycle helper。
export function useWorkbenchTaskSession({
  state, queryScope, currentUserId, currentTenantId, initialExamId, canGrade, canViewOriginalImage,
  hasSession, selectedTaskId, setSelectedTaskId, regionEditing, managing, taskPreviewOpen,
  viewer, prefetch, resetScoreShortcuts, resetWorkbenchScope, loadTasks, loadGraders, isCurrentScope
}: WorkbenchTaskSessionOptions) {
  const queryClient = useQueryClient();
  const {
    ctx, setCtx, setContextLoading, setContextError, draft, setDraft, activeTask, activeUserId,
    online, setOnline, contextRequestRef, draftRevision, setDraftRevision, draftSaveStatus, setDraftSaveStatus,
    draftHydrated, setDraftHydrated, lastSavedDraft, draftSaves, conflictedDrafts,
    setDraftConflict, setResolvingConflict, conflictServerContext
  } = state;
  const { mode: viewerMode, scale, rotation, offset, autoFit, invalidateContent: invalidateViewerContent,
    prepareContext: prepareViewerContext, restore: restoreViewer } = viewer;
  const { prefetchedTaskRef, prefetchedPreviewRef } = prefetch;

  const loadContext = useCallback(async (taskId: string, discardConflictingDraft = false) => {
    const requestId = ++contextRequestRef.current;
    setDraftConflict(null);
    conflictServerContext.current = null;
    setResolvingConflict(false);
    if (!taskId || !hasSession) {
      invalidateViewerContent();
      setCtx(null);
      setContextError(null);
      setContextLoading(false);
      setDraft(createInitialDraft(null));
      resetScoreShortcuts();
      setDraftHydrated(false);
      setDraftSaveStatus("idle");
      return;
    }
    setContextLoading(true);
    setContextError(null);
    setCtx(null);
    setDraftHydrated(false);
    prepareViewerContext();
    try {
      const draftKey = JSON.stringify([currentUserId, taskId]);
      const next = await readQueuedWorkbenchContext({
        queryClient, queryScope, taskId, draftKey, requestId, requestRef: contextRequestRef, saves: draftSaves.current,
        prefetched: prefetchedTaskRef.current, discardConflictingDraft, canViewOriginalImage
      });
      if (!next || requestId !== contextRequestRef.current) return;
      if (initialExamId && next.task.exam_id !== initialExamId) {
        setSelectedTaskId("");
        setContextError("该阅卷任务不属于当前考试，已停止加载。");
        return;
      }
      const isConflicted = conflictedDrafts.current.has(draftKey) && !discardConflictingDraft;
      const { restored, status, server, mayPersistDraft } = restoreWorkbenchDraft(next, { taskId, currentUserId, canViewOriginalImage, isConflicted });
      setDraftSaveStatus(status);
      if (discardConflictingDraft) {
        removeReviewDraftFallback(currentUserId, taskId);
        conflictedDrafts.current.delete(draftKey);
      }
      setDraft(restored.draft);
      resetScoreShortcuts();
      setDraftRevision(server.revision);
      draftSaves.current.observeRevision(draftKey, server.revision);
      restoreViewer(restored.viewer);
      setCtx(next);
      lastSavedDraft.current = JSON.stringify(server.snapshot);
      setDraftHydrated(mayPersistDraft);
    } catch (currentError) {
      if (requestId === contextRequestRef.current && isReviewTaskContextScopeCurrent(queryScope)) setContextError(formatError(currentError));
    } finally {
      if (requestId === contextRequestRef.current && isReviewTaskContextScopeCurrent(queryScope)) setContextLoading(false);
    }
  }, [canViewOriginalImage, currentUserId, initialExamId, invalidateViewerContent, prepareViewerContext, restoreViewer, queryClient, queryScope, prefetchedTaskRef, resetScoreShortcuts, setSelectedTaskId, contextRequestRef, setDraftConflict, conflictServerContext, setResolvingConflict, hasSession, setCtx, setContextError, setContextLoading, setDraft, setDraftHydrated, setDraftSaveStatus, conflictedDrafts, setDraftRevision, draftSaves, lastSavedDraft]);

  useEffect(() => {
    const updateOnlineState = () => setOnline(navigator.onLine);
    window.addEventListener("online", updateOnlineState);
    window.addEventListener("offline", updateOnlineState);
    return () => {
      window.removeEventListener("online", updateOnlineState);
      window.removeEventListener("offline", updateOnlineState);
    };
  }, [setOnline]);

  const draftConflicted = draftSaveStatus === "conflict";
  // biome-ignore lint/correctness/useExhaustiveDependencies: 保存版本变化后重算待保存差异；相同快照直接返回，不能删除版本触发而遗漏排队修改。
  useEffect(() => {
    // 整区编辑器逐行显式保存，避免当前题自动保存与该行手动保存竞争草稿版本。
    if (regionEditing) return;
    if (!draftHydrated || ctx?.task.assigned_to !== currentUserId || ctx.task.status === "submitted") return;
    const snapshotValue = createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit);
    const snapshot = JSON.stringify(snapshotValue);
    if (snapshot === lastSavedDraft.current) return;
    saveReviewDraftFallback(currentUserId, ctx.task.id, snapshotValue);
    if (draftConflicted) return;
    const taskId = ctx.task.id;
    const draftKey = JSON.stringify([currentUserId, taskId]);
    const generation = contextRequestRef.current;
    const commit = (leavingTask = false) => commitWorkbenchDraft({
      queryClient, queryScope, currentUserId, taskId, draftKey, generation, snapshot, snapshotValue, online,
      activeTask, contextRequestRef, conflictedDrafts, draftSaves, prefetchedTaskRef,
      lastSavedDraft, setDraftRevision, setDraftSaveStatus
    }, leavingTask);
    const timer = window.setTimeout(() => { void commit(); }, 1200);
    return () => {
      window.clearTimeout(timer);
      // 同账号切换题目时补存上一题；换账号不触发旧账号的离开保存。
      if (activeUserId.current === currentUserId && activeTask.current !== taskId) void commit(true);
    };
  }, [autoFit, ctx, currentUserId, draft, draftHydrated, draftRevision, draftConflicted, offset, online, rotation, scale, viewerMode, queryClient, queryScope, prefetchedTaskRef, regionEditing]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 身份、考试或会话查询域变化都使评分编辑失效；这些失效键不能因回调未直接读取而删除。
  useEffect(() => {
    resetWorkbenchScope();
    contextRequestRef.current += 1;
    prefetchedTaskRef.current.clear();
    prefetchedPreviewRef.current.clear();
    setCtx(null);
    setContextError(null);
    setContextLoading(false);
    prepareViewerContext();
    setDraft(createInitialDraft(null));
    setDraftHydrated(false);
    setDraftSaveStatus("idle");
    lastSavedDraft.current = "";
  }, [canGrade, currentUserId, currentTenantId, initialExamId, queryScope, prepareViewerContext, resetWorkbenchScope, prefetchedTaskRef, prefetchedPreviewRef]);

  useEffect(() => {
    // 同账号解锁会更新会话代次但不改变 loadTasks 的引用；清队列后仍需重新读取。
    if (!isReviewTaskContextScopeCurrent(queryScope)) return;
    void loadTasks();
  }, [loadTasks, queryScope]);

  useEffect(() => {
    void loadGraders();
  }, [loadGraders]);

  useEffect(() => {
    // 管理员默认只加载概览和任务列表；明确打开答卷后才读取单题材料和相关工具。
    void loadContext(managing && !taskPreviewOpen ? "" : selectedTaskId);
  }, [loadContext, managing, selectedTaskId, taskPreviewOpen]);

  useEffect(() => {
    if (
      !selectedTaskId ||
      !ctx?.reviewContext.claim.can_renew ||
      ctx.task.assigned_to !== currentUserId ||
      !["assigned", "in_progress", "returned"].includes(ctx.task.status)
    ) return;
    let active = true;
    const renew = () => {
      if (!active || !isCurrentScope() || activeTask.current !== selectedTaskId) return;
      void renewReviewTask(selectedTaskId).catch(() => {
        if (active && isCurrentScope() && activeTask.current === selectedTaskId) setDraftSaveStatus("error");
      });
    };
    // 打开可续租且归属当前用户的任务时立即续租，之后每五分钟延续；只读质检不续租。
    renew();
    const timer = window.setInterval(renew, 5 * 60 * 1000);
    return () => { active = false; window.clearInterval(timer); };
  }, [ctx, currentUserId, selectedTaskId, isCurrentScope, activeTask, setDraftSaveStatus]);

  return { loadContext };
}
