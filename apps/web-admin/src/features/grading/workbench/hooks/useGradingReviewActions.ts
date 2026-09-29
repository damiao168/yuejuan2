import { Modal } from "antd";
import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import { removeReviewDraftFallback, loadReviewDraftFallback, saveReviewDraftFallback } from "../../../../auth/reviewDraftFallback";
import { type RubricPoint } from "../../../../api/papers";
import {
  returnReviewTask,
  submitHumanGrade,
  type AiGrade,
  type ReviewTask
} from "../../../../api/review";
import { appQueryClient } from "../../../../query/client";
import { reviewTaskContextKeys } from "../../../../query/reviewTaskContext";
import type { GoldPaperNominationCandidate } from "../../../gold-papers";
import { createDraftSnapshot, createInitialDraft, fallbackSnapshot, formatError, serverDraftSnapshot } from "../gradingWorkbench.model";
import { loadTaskContext } from "../gradingTaskContext";
import type { DraftFallbackSnapshot, DraftSaveStatus, ScoreDraft, ViewerMode, WorkbenchContext, PrefetchedTaskBundle } from "../gradingWorkbench.types";
import type { DraftConflictResolution } from "../draftConflict";
import { mathSuggestionState } from "../mathWorkbenchEvidence";
import type { TaskDraftSaveQueue } from "./taskDraftSaveQueue";
import type { useExamScoring } from "./useExamScoring";
import type { useGradingScoreShortcuts } from "./useGradingScoreShortcuts";
import type { useMathWorkbenchEvidence } from "./useMathWorkbenchEvidence";

type StateSetter<T> = Dispatch<SetStateAction<T>>;
type RunAction = (key: string, action: () => Promise<void>, successText: string) => Promise<void>;

interface WorkbenchNotifications {
  info: (text: string) => void;
  warning: (text: string) => void;
  error: (text: string) => void;
}

export interface UseGradingReviewActionsOptions {
  ctx: WorkbenchContext | null;
  setCtx: StateSetter<WorkbenchContext | null>;
  draft: ScoreDraft;
  setDraft: StateSetter<ScoreDraft>;
  draftConflict: DraftConflictResolution | null;
  canSubmit: boolean;
  canReturn: boolean;
  canEditDraft: boolean;
  canAdoptAiScore: boolean;
  canViewOriginalImage: boolean;
  canManageTasks: boolean;
  hasMoreTasks: boolean;
  currentUserId: string;
  selectedTaskId: string;
  nextTask: ReviewTask | undefined;
  selectedGrade: AiGrade | undefined;
  subjectCode: string;
  maxScore: number;
  rubricPoints: RubricPoint[];
  viewerMode: ViewerMode;
  scale: number;
  rotation: number;
  offset: { x: number; y: number };
  autoFit: boolean;
  math: ReturnType<typeof useMathWorkbenchEvidence>;
  scoreShortcuts: ReturnType<typeof useGradingScoreShortcuts>;
  examScoring: ReturnType<typeof useExamScoring>;
  runAction: RunAction;
  refreshCurrent: () => Promise<void>;
  loadMoreTasks: () => Promise<ReviewTask[]>;
  refreshTaskAggregate: () => Promise<void>;
  restoreViewer: (viewer: DraftFallbackSnapshot["viewer"]) => void;
  invalidateViewerContent: () => void;
  setTasks: StateSetter<ReviewTask[]>;
  setSelectedTaskId: StateSetter<string>;
  setDraftHydrated: StateSetter<boolean>;
  setDraftSaveStatus: StateSetter<DraftSaveStatus>;
  setDraftRevision: StateSetter<number>;
  setDraftConflict: StateSetter<DraftConflictResolution | null>;
  setResolvingConflict: StateSetter<boolean>;
  setGoldPaperCandidate: StateSetter<GoldPaperNominationCandidate | null>;
  setMathRequesting: StateSetter<boolean>;
  contextRequestRef: MutableRefObject<number>;
  activeTask: MutableRefObject<string>;
  activeUserId: MutableRefObject<string>;
  liveDraft: MutableRefObject<ScoreDraft>;
  mathActionLock: MutableRefObject<boolean>;
  conflictedDrafts: MutableRefObject<Set<string>>;
  draftSaves: MutableRefObject<TaskDraftSaveQueue>;
  lastSavedDraft: MutableRefObject<string>;
  conflictServerContext: MutableRefObject<WorkbenchContext | null>;
  prefetchedTaskRef: MutableRefObject<Map<string, Promise<PrefetchedTaskBundle>>>;
  pendingNextRef: MutableRefObject<string | null>;
  suppressAutoSelectRef: MutableRefObject<boolean>;
  notify: WorkbenchNotifications;
}

export function useGradingReviewActions({
  ctx,
  setCtx,
  draft,
  setDraft,
  draftConflict,
  canSubmit,
  canReturn,
  canEditDraft,
  canAdoptAiScore,
  canViewOriginalImage,
  canManageTasks,
  hasMoreTasks,
  currentUserId,
  selectedTaskId,
  nextTask,
  selectedGrade,
  subjectCode,
  maxScore,
  rubricPoints,
  viewerMode,
  scale,
  rotation,
  offset,
  autoFit,
  math,
  scoreShortcuts,
  examScoring,
  runAction,
  refreshCurrent,
  loadMoreTasks,
  refreshTaskAggregate,
  restoreViewer,
  invalidateViewerContent,
  setTasks,
  setSelectedTaskId,
  setDraftHydrated,
  setDraftSaveStatus,
  setDraftRevision,
  setDraftConflict,
  setResolvingConflict,
  setGoldPaperCandidate,
  setMathRequesting,
  contextRequestRef,
  activeTask,
  activeUserId,
  liveDraft,
  mathActionLock,
  conflictedDrafts,
  draftSaves,
  lastSavedDraft,
  conflictServerContext,
  prefetchedTaskRef,
  pendingNextRef,
  suppressAutoSelectRef,
  notify
}: UseGradingReviewActionsOptions) {
  const submitGrade = async (nominateAsGold = false) => {
    if (!ctx) {
      notify.error("请先选择任务");
      return;
    }
    if (!canSubmit || draft.score === null) { notify.error("请先填写最终分，0分也需要明确输入"); return; }
    if (rubricPoints.length && Math.abs(draft.score - rubricPoints.reduce((sum, point) => sum + (draft.rubricSelections[point.id] ?? 0), 0)) > 0.001 && (!draft.reason.trim() || draft.reason === "教师复核完成")) {
      notify.error("最终分与评分细则合计不同，请填写人工调整原因"); return;
    }
    const score = Number(draft.score);
    if (!Number.isFinite(score) || score < 0 || score > maxScore) {
      notify.error("最终分必须在 0 到题目满分之间");
      return;
    }
    await runAction(
      "submit",
      async () => {
        const submittedTaskId = ctx.task.id;
        const nextTaskId = nextTask?.id ?? "";
        const selections = Object.entries(draft.rubricSelections)
          .filter(([, value]) => Number(value) > 0)
          .map(([point_id, value]) => ({ point_id, score: Number(value) }));
        const result = await submitHumanGrade(submittedTaskId, {
          expected_revision: ctx.reviewContext.expected_revision,
          score,
          rubric_selections: selections,
          comments: draft.comments,
          private_note: draft.privateNote,
          student_feedback: draft.studentFeedback,
          reason: draft.reason || "教师复核完成"
        });
        removeReviewDraftFallback(currentUserId, submittedTaskId);
        // 查询缓存和任务队列属于当前登录会话；旧账号的迟到响应不能清掉或改写新账号的状态。
        if (activeUserId.current !== currentUserId) return;
        appQueryClient.removeQueries({ queryKey: reviewTaskContextKeys.detail(submittedTaskId) });
        prefetchedTaskRef.current.delete(submittedTaskId);
        setTasks((current) => current.map((task) => task.id === submittedTaskId ? { ...task, status: "submitted" } : task));

        // 提交期间用户仍可切换题目；只有当前仍显示刚提交的任务时，才清空编辑器并推进到下一题。
        const stillActive = activeTask.current === submittedTaskId;
        if (nominateAsGold && stillActive) {
          setGoldPaperCandidate({
            examId: ctx.task.exam_id,
            questionId: ctx.task.question_id,
            questionNo: ctx.task.question_no,
            submissionId: ctx.task.submission_id,
            answerImageUrl: ctx.originalImageUrl ?? ctx.segmentImageUrl,
            referenceScore: score,
            maxScore,
            explanation: draft.comments,
            sourceGradeId: result.human_grade.id,
            rubricPoints: rubricPoints.map((point) => ({ id: point.id, description: point.description, score: point.score, required: point.required })),
            rubricSelections: { ...draft.rubricSelections }
          });
        }
        if (stillActive) {
          contextRequestRef.current += 1;
          invalidateViewerContent();
          setCtx(null);
          if (!nextTaskId && hasMoreTasks) {
            pendingNextRef.current = submittedTaskId;
            suppressAutoSelectRef.current = true;
            void loadMoreTasks();
          }
          setSelectedTaskId(nextTaskId);
          setDraft(createInitialDraft(null));
          scoreShortcuts.reset();
          setDraftHydrated(false);
          setDraftSaveStatus("idle");
          lastSavedDraft.current = "";
        }
        await refreshTaskAggregate();
        if (activeUserId.current !== currentUserId) return;
        await examScoring.loadSummary();
        if (stillActive && activeUserId.current === currentUserId && !activeTask.current && !nextTaskId && !canManageTasks && !hasMoreTasks) {
          notify.info("当前没有更多已分配给你的阅卷任务");
        }
      },
      "人工评分已提交"
    );
  };

  const markDispute = async () => {
    if (!ctx) {
      return;
    }
    const reason = draft.disputeReason.trim() || "教师标记争议，需要重新评阅";
    await runAction(
      "return",
      async () => {
        await returnReviewTask(ctx.task.id, reason, ctx.task.revision);
        await refreshCurrent();
      },
      "已标记争议并退回重评"
    );
  };

  const adoptAiScore = async () => {
    if (!selectedGrade) {
      notify.warning("当前任务没有 AI 建议分");
      return;
    }
    if (selectedGrade.delivery_mode === "shadow_only") {
      notify.warning("本场考试未开放 AI 建议，无法采纳");
      return;
    }
    if (!canEditDraft || !canAdoptAiScore) {
      notify.warning("当前建议未通过版本核对，请按原图人工判定或刷新证据。");
      return;
    }
    if (subjectCode === "mathematics") {
      if (mathActionLock.current) return;
      const taskId = selectedTaskId;
      // 刷新证据期间教师仍可修改草稿；采纳建议前要同时确认任务、证据版本和本地评分快照仍未变化。
      const before = JSON.stringify({ score: draft.score, selections: draft.rubricSelections });
      mathActionLock.current = true;
      setMathRequesting(true);
      try {
        const evidence = await math.refresh();
        if (activeTask.current !== taskId) return;
        if (!evidence || !mathSuggestionState(selectedGrade, evidence, math.dirty).current) {
          notify.warning("数学证据或图片已更新，旧建议不可采纳。");
          return;
        }
        if (before !== JSON.stringify({ score: liveDraft.current.score, selections: liveDraft.current.rubricSelections })) {
          notify.warning("评分草稿已变化，请再次确认后采纳。");
          return;
        }
      } finally {
        mathActionLock.current = false;
        if (activeTask.current === taskId) setMathRequesting(false);
      }
    }
    scoreShortcuts.rememberCurrent();
    setDraft((current) => ({
      ...current,
      score: selectedGrade.suggested_score,
      studentFeedback: current.studentFeedback || selectedGrade.student_feedback || "",
      privateNote: current.privateNote || selectedGrade.teacher_note || ""
    }));
  };

  const openDraftConflict = async () => {
    if (!ctx || activeTask.current !== ctx.task.id) return;
    const taskId = ctx.task.id;
    const userId = currentUserId;
    const initial = createInitialDraft(ctx);
    const fallback = loadReviewDraftFallback<unknown>(userId, taskId);
    const local = (fallback && fallbackSnapshot(fallback.snapshot, initial))
      ?? createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit);
    setResolvingConflict(true);
    try {
      appQueryClient.removeQueries({ queryKey: reviewTaskContextKeys.detail(taskId) });
      prefetchedTaskRef.current.delete(taskId);
      const latestContext = await loadTaskContext(taskId, canViewOriginalImage);
      if (activeTask.current !== taskId || activeUserId.current !== userId) return;
      const server = serverDraftSnapshot(
        createInitialDraft(latestContext),
        latestContext.task.assigned_to === userId ? latestContext.reviewContext.draft : null,
        canViewOriginalImage
      );
      conflictServerContext.current = latestContext;
      setDraftConflict({ taskId, serverRevision: server.revision, local, server: server.snapshot });
    } catch (currentError) {
      if (activeTask.current === taskId && activeUserId.current === userId) {
        notify.error(formatError(currentError));
      }
    } finally {
      if (activeTask.current === taskId && activeUserId.current === userId) setResolvingConflict(false);
    }
  };

  const applyDraftConflict = (merged: DraftFallbackSnapshot) => {
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

  const confirmReturnTask = (asException: boolean) => {
    if (!canReturn || !canEditDraft || !ctx) return;
    Modal.confirm({
      title: asException ? "标记异常并退回重评？" : "退回重新评阅？",
      content: asException
        ? "该答卷会进入重评流程，不会被丢弃。"
        : "该答卷会退出你的队列并进入重评流程。",
      okText: "确认退回",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: () => markDispute()
    });
  };

  return { submitGrade, markDispute, adoptAiScore, openDraftConflict, applyDraftConflict, confirmReturnTask };
}
