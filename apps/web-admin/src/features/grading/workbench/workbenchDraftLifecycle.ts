import type { RefObject } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { ApiClientError } from "../../../api/client";
import { loadReviewDraftFallback, removeReviewDraftFallback } from "../../../auth/reviewDraftFallback";
import { saveReviewDraft } from "../../../api/review";
import { isReviewTaskContextScopeCurrent, reviewTaskContextKeys, type ReviewTaskContextScope } from "../../../query/reviewTaskContext";
import { createInitialDraft, fallbackSnapshot, serverDraftSnapshot } from "./gradingWorkbench.model";
import { loadTaskContext } from "./gradingTaskContext";
import type { DraftFallbackSnapshot, DraftSaveStatus, PrefetchedTaskBundle, WorkbenchContext } from "./gradingWorkbench.types";
import type { TaskDraftSaveQueue } from "./hooks/taskDraftSaveQueue";

interface ContextReadOptions {
  queryClient: QueryClient;
  queryScope: ReviewTaskContextScope;
  taskId: string;
  draftKey: string;
  requestId: number;
  requestRef: RefObject<number>;
  saves: TaskDraftSaveQueue;
  prefetched: Map<string, Promise<PrefetchedTaskBundle>>;
  discardConflictingDraft: boolean;
  canViewOriginalImage: boolean;
}

export async function readQueuedWorkbenchContext(options: Readonly<ContextReadOptions>): Promise<WorkbenchContext | null> {
  const hadPendingSave = options.saves.hasPending(options.draftKey);
  // 同一任务要先等旧保存落定，再读取服务端草稿；等待期间换题的请求不能清当前缓存。
  await options.saves.whenIdle(options.draftKey);
  if (options.requestId !== options.requestRef.current || !isReviewTaskContextScopeCurrent(options.queryScope)) return null;
  if (hadPendingSave || options.discardConflictingDraft) {
    options.queryClient.removeQueries({ queryKey: reviewTaskContextKeys.task(options.queryScope, options.taskId) });
    options.prefetched.delete(options.taskId);
  }
  const prefetched = options.prefetched.get(options.taskId);
  options.prefetched.delete(options.taskId);
  const next = prefetched
    ? (await prefetched).context
    : await loadTaskContext(options.queryClient, options.queryScope, options.taskId);
  if (options.requestId !== options.requestRef.current || !isReviewTaskContextScopeCurrent(options.queryScope)) return null;
  return next;
}

function serverSaveStatus(isConflicted: boolean, mayPersistDraft: boolean, hasServerDraft: boolean): DraftSaveStatus {
  if (isConflicted) return "conflict";
  if (!mayPersistDraft) return "readonly";
  return hasServerDraft ? "saved" : "idle";
}

interface DraftRestoreOptions {
  taskId: string;
  currentUserId: string;
  canViewOriginalImage: boolean;
  isConflicted: boolean;
}

export function restoreWorkbenchDraft(next: WorkbenchContext, options: Readonly<DraftRestoreOptions>) {
  const reviewerOwnsTask = next.task.assigned_to === options.currentUserId;
  // 盲质检样本不参与编辑草稿或续租；查看任务不等于拥有它的编辑状态。
  const mayPersistDraft = reviewerOwnsTask && next.reviewContext.claim.can_renew;
  const initial = createInitialDraft(next);
  const serverDraft = reviewerOwnsTask ? next.reviewContext.draft : null;
  const server = serverDraftSnapshot(initial, serverDraft, options.canViewOriginalImage);
  const localDraft = mayPersistDraft ? loadReviewDraftFallback<unknown>(options.currentUserId, options.taskId) : null;
  const localSnapshot = localDraft ? fallbackSnapshot(localDraft.snapshot, initial) : null;
  const useLocal = Boolean(localDraft && localSnapshot && (options.isConflicted || localDraft.updatedAt > server.updatedAt));
  let restored = server.snapshot;
  let status = serverSaveStatus(options.isConflicted, mayPersistDraft, Boolean(serverDraft));
  if (useLocal && localSnapshot) {
    restored = localSnapshot;
    if (restored.viewer.mode === "original" && !options.canViewOriginalImage) {
      restored = { ...restored, viewer: { ...restored.viewer, mode: "segment" } };
    }
    status = options.isConflicted ? "conflict" : "offline";
  } else if (localDraft) {
    removeReviewDraftFallback(options.currentUserId, options.taskId);
  }
  return { restored, status, server, mayPersistDraft };
}

export interface WorkbenchDraftCommitOptions {
  queryClient: QueryClient;
  queryScope: ReviewTaskContextScope;
  currentUserId: string;
  taskId: string;
  draftKey: string;
  generation: number;
  snapshot: string;
  snapshotValue: DraftFallbackSnapshot;
  online: boolean;
  activeTask: RefObject<string>;
  contextRequestRef: RefObject<number>;
  conflictedDrafts: RefObject<Set<string>>;
  draftSaves: RefObject<TaskDraftSaveQueue>;
  prefetchedTaskRef: RefObject<Map<string, Promise<PrefetchedTaskBundle>>>;
  lastSavedDraft: RefObject<string>;
  setDraftRevision: (revision: number) => void;
  setDraftSaveStatus: (status: DraftSaveStatus) => void;
}

function isCurrentDraft(options: Readonly<WorkbenchDraftCommitOptions>): boolean {
  return isReviewTaskContextScopeCurrent(options.queryScope) && options.generation === options.contextRequestRef.current && options.activeTask.current === options.taskId;
}

function isDraftConflict(error: unknown): boolean {
  return error instanceof ApiClientError && error.status === 409;
}

function failedSaveStatus(error: unknown): DraftSaveStatus {
  if (isDraftConflict(error)) return "conflict";
  return navigator.onLine ? "error" : "offline";
}

async function persistWorkbenchDraft(options: Readonly<WorkbenchDraftCommitOptions>): Promise<number | null> {
  return options.draftSaves.current.enqueue(options.draftKey, async (expectedRevision) => {
    if (!isReviewTaskContextScopeCurrent(options.queryScope)) return null;
    const pending = loadReviewDraftFallback<unknown>(options.currentUserId, options.taskId);
    if (!pending || JSON.stringify(pending.snapshot) !== options.snapshot || options.conflictedDrafts.current.has(options.draftKey)) return null;
    const draft = options.snapshotValue.draft;
    const result = await saveReviewDraft(options.taskId, {
      score: draft.score,
      rubric_selections: Object.entries(draft.rubricSelections)
        .filter(([, value]) => Number(value) > 0)
        .map(([point_id, value]) => ({ point_id, score: Number(value) })),
      comments: draft.comments,
      private_note: draft.privateNote,
      student_feedback: draft.studentFeedback,
      viewer_state: { ...options.snapshotValue.viewer },
      expected_revision: expectedRevision,
      client_updated_at: new Date().toISOString()
    });
    // 远端成功不能撤销；账号已变化时保留原草稿供恢复，不修改新会话的缓存或预取表。
    if (!isReviewTaskContextScopeCurrent(options.queryScope)) return result.draft.revision;
    options.queryClient.removeQueries({ queryKey: reviewTaskContextKeys.task(options.queryScope, options.taskId) });
    options.prefetchedTaskRef.current.delete(options.taskId);
    const latest = loadReviewDraftFallback<unknown>(options.currentUserId, options.taskId);
    // 迟到保存只能删掉它提交的那一版；新编辑的内存草稿必须留给后续队列。
    if (latest && JSON.stringify(latest.snapshot) === options.snapshot) removeReviewDraftFallback(options.currentUserId, options.taskId);
    return result.draft.revision;
  });
}

export async function commitWorkbenchDraft(options: Readonly<WorkbenchDraftCommitOptions>, leavingTask = false): Promise<void> {
  if (!isReviewTaskContextScopeCurrent(options.queryScope) || (!leavingTask && options.activeTask.current !== options.taskId) || options.conflictedDrafts.current.has(options.draftKey)) return;
  if (!options.online) {
    if (!leavingTask) options.setDraftSaveStatus("offline");
    return;
  }
  if (!leavingTask) options.setDraftSaveStatus("saving");
  try {
    const savedRevision = await persistWorkbenchDraft(options);
    if (savedRevision === null) return;
    applySavedWorkbenchDraft(options, savedRevision);
  } catch (error) {
    recordFailedWorkbenchDraft(options, error);
  }
}

function applySavedWorkbenchDraft(options: Readonly<WorkbenchDraftCommitOptions>, savedRevision: number): void {
  const latest = loadReviewDraftFallback<unknown>(options.currentUserId, options.taskId);
  const stillCurrent = !latest || JSON.stringify(latest.snapshot) === options.snapshot;
  if (isCurrentDraft(options)) {
    options.lastSavedDraft.current = options.snapshot;
    options.setDraftRevision(savedRevision);
    options.setDraftSaveStatus(stillCurrent ? "saved" : "saving");
  }
}

function recordFailedWorkbenchDraft(options: Readonly<WorkbenchDraftCommitOptions>, error: unknown): void {
  if (!isReviewTaskContextScopeCurrent(options.queryScope)) return;
  // 冲突属于原任务，即使用户已经换题也要记住；当前编辑器的提示只接收本代次结果。
  if (isDraftConflict(error)) options.conflictedDrafts.current.add(options.draftKey);
  if (isCurrentDraft(options)) options.setDraftSaveStatus(failedSaveStatus(error));
}
