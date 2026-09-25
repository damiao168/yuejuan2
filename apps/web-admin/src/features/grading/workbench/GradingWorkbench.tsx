import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App } from "antd";
import { ApiClientError, getSafeUserText } from "../../../api/client";
import { loadReviewDraftFallback, removeReviewDraftFallback, saveReviewDraftFallback } from "../../../auth/reviewDraftFallback";
import {
  renewReviewTask,
  saveReviewDraft,
  verifyEvidence
} from "../../../api/review";
import { EmptyState, ErrorState, LoadingState } from "../../../components/PageState";
import { appQueryClient } from "../../../query/client";
import { reviewTaskContextKeys } from "../../../query/reviewTaskContext";
import type { GoldPaperNominationCandidate } from "../../gold-papers";
import {
  createDraftSnapshot,
  createInitialDraft,
  fallbackSnapshot,
  formatError,
  latestGrade,
  serverDraftSnapshot,
  sourceDescriptions,
  sourceLabels
} from "./gradingWorkbench.model";
import { loadTaskContext } from "./gradingTaskContext";
import type { DraftConflictResolution } from "./draftConflict";
import type {
  DraftSaveStatus,
  ScoreDraft,
  WorkbenchContext
} from "./gradingWorkbench.types";
import { AnswerEvidencePane } from "./components/AnswerEvidencePane";
import { EvidenceInspector } from "./components/EvidenceInspector";
import { ScoreEditor } from "./components/ScoreEditor";
import { TaskRail } from "./components/TaskRail";
import { WorkbenchHeader } from "./components/WorkbenchHeader";
import { WorkbenchOverlays } from "./components/WorkbenchOverlays";
import { ExamScoringPanel } from "./components/ExamScoringPanel";
import { useGradingKeyboard } from "./hooks/useGradingKeyboard";
import { useGradingPrefetch } from "./hooks/useGradingPrefetch";
import { useExamScoring } from "./hooks/useExamScoring";
import { useAnswerViewer } from "./hooks/useAnswerViewer";
import { useMathWorkbenchEvidence } from "./hooks/useMathWorkbenchEvidence";
import { TaskDraftSaveQueue } from "./hooks/taskDraftSaveQueue";
import { useGradingQueue } from "./hooks/useGradingQueue";
import { useGradingScoreShortcuts } from "./hooks/useGradingScoreShortcuts";
import { useGradingQueueActions } from "./hooks/useGradingQueueActions";
import { useGradingReviewActions } from "./hooks/useGradingReviewActions";
import { useGradingMathActions } from "./hooks/useGradingMathActions";
import { mathEvidenceBinding, mathSuggestionState, type MathStepSelection } from "./mathWorkbenchEvidence";
import { requiresExplicitSecondOpinion } from "./reviewContext";

export interface GradingWorkbenchProps {
  canWork: boolean;
  canManageTasks: boolean;
  canViewOriginalImage: boolean;
  canGrade: boolean;
  canVerifyEvidence: boolean;
  canReturn: boolean;
  currentUserId: string;
  currentTenantId: string;
  initialExamId?: string;
  personalScope?: boolean;
}

export function GradingWorkbench({ canWork, canManageTasks, canViewOriginalImage, canGrade, canVerifyEvidence, canReturn, currentUserId, currentTenantId, initialExamId = "", personalScope = false }: GradingWorkbenchProps) {
  const { message } = App.useApp();
  const hasSession = true;
  const reportNoNextTask = useCallback((text: string) => message.info(text), [message]);
  const queue = useGradingQueue({ canManageTasks, canWork, currentUserId, initialExamId, personalScope, onInfo: reportNoNextTask });
  const {
    taskFilter, setTaskFilter, queueScope, setQueueScope, keyword, setKeyword, tasks, setTasks, taskAggregate,
    selectedTaskId, setSelectedTaskId, loadingTasks, loadingMoreTasks, hasMoreTasks, pageError, taskError,
    gradersError, assignmentUserId, setAssignmentUserId, assignmentTaskIds, setAssignmentTaskIds,
    filteredTasks, assignableTasks, graderOptions, graderNames, reviewerProgress, myProgress, remainingCount,
    prefetchTasks, nextTask, pendingNextRef, suppressAutoSelectRef,
    loadTasks, loadMoreTasks, refreshTaskAggregate, loadGraders, reset: resetQueue
  } = queue;
  const actionLock = useRef(false);
  const [ctx, setCtx] = useState<WorkbenchContext | null>(null);
  const [contextLoading, setContextLoading] = useState(false);
  const [contextError, setContextError] = useState<string | null>(null);
  const [draft, setDraft] = useState<ScoreDraft>(() => createInitialDraft(null));
  const [quickSubmit, setQuickSubmit] = useState(false);
  const [actioning, setActioning] = useState<string | null>(null);
  const [mathRequesting, setMathRequesting] = useState(false);
  const [mathRequestNotice, setMathRequestNotice] = useState("");
  const [mathSelection, setMathSelection] = useState<MathStepSelection | null>(null);
  const mathActionLock = useRef(false);
  const activeTask = useRef(selectedTaskId);
  activeTask.current = selectedTaskId;
  const activeUserId = useRef(currentUserId);
  activeUserId.current = currentUserId;
  const liveDraft = useRef(draft);
  liveDraft.current = draft;
  const [goldPaperManagerOpen, setGoldPaperManagerOpen] = useState(false);
  const [calibrationQuestionId, setCalibrationQuestionId] = useState("");
  const [goldPaperCandidate, setGoldPaperCandidate] = useState<GoldPaperNominationCandidate | null>(null);
  const [online, setOnline] = useState(() => typeof navigator === "undefined" || navigator.onLine);
  const contextRequestRef = useRef(0);
  const [draftRevision, setDraftRevision] = useState(0);
  const [draftSaveStatus, setDraftSaveStatus] = useState<DraftSaveStatus>("idle");
  const [draftHydrated, setDraftHydrated] = useState(false);
  const lastSavedDraft = useRef("");
  const draftSaves = useRef(new TaskDraftSaveQueue());
  const conflictedDrafts = useRef(new Set<string>());
  const [draftConflict, setDraftConflict] = useState<DraftConflictResolution | null>(null);
  const [resolvingConflict, setResolvingConflict] = useState(false);
  const conflictServerContext = useRef<WorkbenchContext | null>(null);

  const subjectCode = ctx?.reviewContext.subject_tool_hints.subject_code ?? "";
  const math = useMathWorkbenchEvidence(ctx?.task.answer_segment_id ?? "", subjectCode, ctx?.reviewContext.question_snapshot.id ?? "");
  const { prefetchedTaskRef, prefetchedPreviewRef } = useGradingPrefetch({
    canWork,
    canViewOriginalImage,
    currentUserId,
    initialExamId,
    selectedTaskId,
    tasks: prefetchTasks
  });
  const {
    preview,
    previewLoading,
    mode: viewerMode,
    scale,
    autoFit,
    imageSize,
    rotation,
    offset,
    dragging,
    viewportRef,
    invalidateContent: invalidateViewerContent,
    prepareContext: prepareViewerContext,
    restore: restoreViewer,
    fit: applyViewerFit,
    changeMode: changeViewerMode,
    zoom: zoomViewer,
    rotate: rotateViewer,
    onPointerDown,
    onPointerMove,
    onPointerUp,
    onImageLoad
  } = useAnswerViewer({ context: ctx, canViewOriginalImage, prefetchedPreviewRef, contentRevision: math.state.understanding?.artifact.input_hash });
  const selectedGrade = useMemo(() => (subjectCode === "mathematics" ? latestGrade((ctx?.aiGrades ?? []).filter((grade) => mathSuggestionState(grade, math.state, math.dirty).current)) : undefined)
    ?? latestGrade(ctx?.aiGrades ?? []), [ctx?.aiGrades, math.dirty, math.state, subjectCode]);
  const canAdoptAiScore = Boolean(selectedGrade && selectedGrade.delivery_mode !== "shadow_only" && !mathRequesting
    && !requiresExplicitSecondOpinion(ctx!.reviewContext) && (subjectCode !== "mathematics" || mathSuggestionState(selectedGrade, math.state, math.dirty).current));
  const currentMathSelection = mathSelection && !math.dirty && !["conflict", "loading", "unavailable"].includes(math.state.phase)
    && math.state.understanding && mathSelection.binding === mathEvidenceBinding(math.state.understanding) ? mathSelection : null;
  useEffect(() => { setMathRequestNotice(""); setMathSelection(null); setMathRequesting(false); }, [selectedTaskId]);
  const maxScore = ctx?.question?.score ?? selectedGrade?.max_score ?? 0;
  const rubricPoints = ctx?.question?.rubric?.points ?? [];
  const scoreShortcuts = useGradingScoreShortcuts({ draft, setDraft, rubricPoints, onInfo: (text) => message.info(text) });
  const ownsSelectedTask = Boolean(ctx?.task.assigned_to && ctx.task.assigned_to === currentUserId);
  const canEditDraft = canWork && hasSession && ownsSelectedTask && !resolvingConflict && !draftConflict && Boolean(ctx && ["assigned", "in_progress", "returned"].includes(ctx.task.status));
  const canSubmit = canEditDraft && draftSaveStatus !== "conflict" && !actioning && draft.score !== null;
  const canUndoScoreChange = scoreShortcuts.canUndo;
  const examScoring = useExamScoring({ initialExamId, currentUserId, currentTenantId, canGrade, onTasksChanged: loadTasks });

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
      scoreShortcuts.reset();
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
      // A return to the same task must observe its last in-flight save before
      // hydrating the server revision and local fallback.
      const draftKey = JSON.stringify([currentUserId, taskId]);
      const hadPendingSave = draftSaves.current.hasPending(draftKey);
      await draftSaves.current.whenIdle(draftKey);
      if (requestId !== contextRequestRef.current) return;
      if (hadPendingSave || discardConflictingDraft) {
        appQueryClient.removeQueries({ queryKey: reviewTaskContextKeys.detail(taskId) });
        prefetchedTaskRef.current.delete(taskId);
      }
      const prefetched = prefetchedTaskRef.current.get(taskId);
      prefetchedTaskRef.current.delete(taskId);
      const next = prefetched
        ? (await prefetched).context
        : await loadTaskContext(taskId, canViewOriginalImage);
      if (requestId !== contextRequestRef.current) return;
      if (initialExamId && next.task.exam_id !== initialExamId) {
        setSelectedTaskId("");
        setContextError("该阅卷任务不属于当前考试，已停止加载。");
        return;
      }
      const reviewerOwnsTask = next.task.assigned_to === currentUserId;
      // Blind quality samples use the ordinary workbench but deliberately do
      // not participate in the review task's draft or lease endpoints.
      const mayPersistDraft = reviewerOwnsTask && next.reviewContext.claim.can_renew;
      const initial = createInitialDraft(next);
      const draftResult = { draft: reviewerOwnsTask ? next.reviewContext.draft : null };
      const server = serverDraftSnapshot(initial, draftResult.draft, canViewOriginalImage);
      let restored = server.snapshot.draft;
      let restoredViewer = server.snapshot.viewer;
      const revision = server.revision;
      const serverSnapshot = server.snapshot;
      const localDraft = mayPersistDraft ? loadReviewDraftFallback<unknown>(currentUserId, taskId) : null;
      const localSnapshot = localDraft ? fallbackSnapshot(localDraft.snapshot, initial) : null;
      const isConflicted = conflictedDrafts.current.has(draftKey) && !discardConflictingDraft;
      const useLocalDraft = Boolean(localDraft && localSnapshot && (isConflicted || localDraft.updatedAt > server.updatedAt));
      if (useLocalDraft && localSnapshot) {
        restored = localSnapshot.draft;
        restoredViewer = localSnapshot.viewer.mode === "original" && !canViewOriginalImage
          ? { ...localSnapshot.viewer, mode: "segment" }
          : localSnapshot.viewer;
        setDraftSaveStatus(isConflicted ? "conflict" : "offline");
      } else {
        if (localDraft) removeReviewDraftFallback(currentUserId, taskId);
        setDraftSaveStatus(isConflicted ? "conflict" : mayPersistDraft ? (draftResult.draft ? "saved" : "idle") : "readonly");
      }
      if (discardConflictingDraft) {
        removeReviewDraftFallback(currentUserId, taskId);
        conflictedDrafts.current.delete(draftKey);
      }
      setDraft(restored);
      scoreShortcuts.reset();
      setDraftRevision(revision);
      draftSaves.current.observeRevision(draftKey, revision);
      restoreViewer(restoredViewer);
      setCtx(next);
      lastSavedDraft.current = JSON.stringify(serverSnapshot);
      setDraftHydrated(mayPersistDraft);
    } catch (currentError) {
      if (requestId === contextRequestRef.current) setContextError(formatError(currentError));
    } finally {
      if (requestId === contextRequestRef.current) setContextLoading(false);
    }
  }, [canViewOriginalImage, currentUserId, hasSession, initialExamId, invalidateViewerContent, prepareViewerContext, restoreViewer]);

  useEffect(() => {
    const updateOnlineState = () => setOnline(navigator.onLine);
    window.addEventListener("online", updateOnlineState);
    window.addEventListener("offline", updateOnlineState);
    return () => {
      window.removeEventListener("online", updateOnlineState);
      window.removeEventListener("offline", updateOnlineState);
    };
  }, []);

  useEffect(() => {
    if (!draftHydrated || !ctx || ctx.task.assigned_to !== currentUserId || ctx.task.status === "submitted") return;
    const snapshotValue = createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit);
    const snapshot = JSON.stringify(snapshotValue);
    if (snapshot === lastSavedDraft.current) return;
    saveReviewDraftFallback(currentUserId, ctx.task.id, snapshotValue);
    if (draftSaveStatus === "conflict") return;
    const taskId = ctx.task.id;
    const draftKey = JSON.stringify([currentUserId, taskId]);
    const generation = contextRequestRef.current;
    const commit = async (leavingTask = false) => {
      if ((!leavingTask && activeTask.current !== taskId) || conflictedDrafts.current.has(draftKey)) return;
      if (!online) {
        if (!leavingTask) setDraftSaveStatus("offline");
        return;
      }
      if (!leavingTask) setDraftSaveStatus("saving");
      try {
        const savedRevision = await draftSaves.current.enqueue(draftKey, async (expectedRevision) => {
          // A queued intermediate edit is superseded by the newest local copy.
          const pending = loadReviewDraftFallback<unknown>(currentUserId, taskId);
          if (!pending || JSON.stringify(pending.snapshot) !== snapshot || conflictedDrafts.current.has(draftKey)) return null;
          const selections = Object.entries(draft.rubricSelections).filter(([, value]) => Number(value) > 0).map(([point_id, value]) => ({ point_id, score: Number(value) }));
          const result = await saveReviewDraft(taskId, {
            score: draft.score,
            rubric_selections: selections,
            comments: draft.comments,
            private_note: draft.privateNote,
            student_feedback: draft.studentFeedback,
            viewer_state: { mode: viewerMode, scale, rotation, offset, fit: autoFit },
            expected_revision: expectedRevision,
            client_updated_at: new Date().toISOString()
          });
          appQueryClient.removeQueries({ queryKey: reviewTaskContextKeys.detail(taskId) });
          prefetchedTaskRef.current.delete(taskId);
          const latest = loadReviewDraftFallback<unknown>(currentUserId, taskId);
          const stillCurrent = Boolean(latest && JSON.stringify(latest.snapshot) === snapshot);
          if (stillCurrent) removeReviewDraftFallback(currentUserId, taskId);
          return result.draft.revision;
        });
        if (savedRevision === null) return;
        const latest = loadReviewDraftFallback<unknown>(currentUserId, taskId);
        const stillCurrent = !latest || JSON.stringify(latest.snapshot) === snapshot;
        if (generation === contextRequestRef.current && activeTask.current === taskId) {
          lastSavedDraft.current = snapshot;
          setDraftRevision(savedRevision);
          setDraftSaveStatus(stillCurrent ? "saved" : "saving");
        }
      } catch (currentError) {
        if (currentError instanceof ApiClientError && currentError.status === 409) conflictedDrafts.current.add(draftKey);
        if (generation === contextRequestRef.current && activeTask.current === taskId) {
          setDraftSaveStatus(currentError instanceof ApiClientError && currentError.status === 409 ? "conflict" : (!navigator.onLine ? "offline" : "error"));
        }
      }
    };
    const timer = window.setTimeout(() => { void commit(); }, 1200);
    return () => {
      window.clearTimeout(timer);
      if (activeUserId.current === currentUserId && activeTask.current !== taskId) void commit(true);
    };
  }, [autoFit, ctx, currentUserId, draft, draftHydrated, draftRevision, offset, online, rotation, scale, viewerMode]);

  useEffect(() => {
    resetQueue();
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
  }, [currentUserId, initialExamId, prepareViewerContext, resetQueue]);

  useEffect(() => {
    void loadTasks();
  }, [loadTasks]);

  useEffect(() => {
    void loadGraders();
  }, [loadGraders]);

  useEffect(() => {
    void loadContext(selectedTaskId);
  }, [loadContext, selectedTaskId]);

  useEffect(() => {
    if (
      !selectedTaskId ||
      !ctx ||
      !ctx.reviewContext.claim.can_renew ||
      ctx.task.assigned_to !== currentUserId ||
      !["assigned", "in_progress", "returned"].includes(ctx.task.status)
    ) return;
    const renew = () => void renewReviewTask(selectedTaskId).catch(() => setDraftSaveStatus("error"));
    // Establish the lease as soon as an assigned task is opened. Waiting for
    // the first interval leaves the task without a claim during the initial
    // editing window and makes release/renew state inconsistent.
    renew();
    const timer = window.setInterval(renew, 5 * 60 * 1000);
    return () => window.clearInterval(timer);
  }, [ctx, currentUserId, selectedTaskId]);

  const refreshCurrent = async () => {
    if (selectedTaskId) {
      await appQueryClient.invalidateQueries({ queryKey: reviewTaskContextKeys.detail(selectedTaskId) });
      await loadContext(selectedTaskId);
      await loadTasks();
      if (!math.dirty) await math.refresh();
    }
  };

  const runAction = async (key: string, action: () => Promise<void>, successText: string) => {
    if (actionLock.current) return;
    actionLock.current = true;
    setActioning(key);
    try {
      await action();
      message.success(successText);
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      actionLock.current = false;
      setActioning(null);
    }
  };
  const { assignSelectedTasks, claimTask, goNext, releaseCurrentTask } = useGradingQueueActions({
    canManageTasks,
    initialExamId,
    ctx,
    filteredTasks,
    assignableTasks,
    assignmentTaskIds,
    assignmentUserId,
    graderNames,
    nextTask,
    hasMoreTasks,
    pageError,
    selectedTaskId,
    ownsSelectedTask,
    pendingNextRef,
    suppressAutoSelectRef,
    setTasks,
    setCtx,
    setAssignmentTaskIds,
    setSelectedTaskId,
    setTaskFilter,
    setActioning,
    setCalibrationQuestionId,
    loadTasks,
    loadMoreTasks,
    refreshTaskAggregate,
    runAction,
    notify: {
      info: (text) => message.info(text),
      warning: (text) => message.warning(text),
      success: (text) => message.success(text),
      error: (text) => message.error(text)
    }
  });

  const reviewActions = useGradingReviewActions({
    ctx, setCtx, draft, setDraft, draftConflict, canSubmit, canReturn, canEditDraft,
    canAdoptAiScore, canViewOriginalImage, canManageTasks, hasMoreTasks, currentUserId, selectedTaskId,
    nextTask, selectedGrade, subjectCode, maxScore, rubricPoints, viewerMode, scale, rotation, offset, autoFit,
    math, scoreShortcuts, examScoring, runAction, refreshCurrent, loadMoreTasks, refreshTaskAggregate,
    restoreViewer, invalidateViewerContent, setTasks, setSelectedTaskId, setDraftHydrated,
    setDraftSaveStatus, setDraftRevision, setDraftConflict, setResolvingConflict, setGoldPaperCandidate,
    setMathRequesting, contextRequestRef, activeTask, activeUserId,
    liveDraft, mathActionLock, conflictedDrafts, draftSaves, lastSavedDraft, conflictServerContext,
    prefetchedTaskRef, pendingNextRef, suppressAutoSelectRef,
    notify: {
      info: (text) => message.info(text),
      warning: (text) => message.warning(text),
      error: (text) => message.error(text)
    }
  });
  const { submitGrade, markDispute, adoptAiScore, openDraftConflict, applyDraftConflict, confirmReturnTask } = reviewActions;
  const { refreshMath, selectMathEvidenceStep, requestMathSuggestion } = useGradingMathActions({
    ctx, canGrade, canEditDraft, viewerMode, math, activeTask, mathActionLock,
    setCtx, setMathRequesting, setMathRequestNotice, setMathSelection, changeViewerMode,
    notifyInfo: (text) => message.info(text)
  });

  useGradingKeyboard({
    quickSubmit,
    hasTask: Boolean(ctx),
    canEditDraft,
    canSubmit,
    canReturn,
    canUndoScoreChange,
    hasAiSuggestion: canAdoptAiScore,
    maxScore,
    rubricPointCount: rubricPoints.length,
    onSubmit: () => void submitGrade(),
    onSetScore: scoreShortcuts.setScore,
    onToggleCriterion: scoreShortcuts.toggleCriterion,
    onAdoptAi: () => void adoptAiScore(),
    onFlagException: () => confirmReturnTask(true),
    onReturnTask: () => confirmReturnTask(false),
    onUndoScoreChange: scoreShortcuts.undo,
    onZoom: zoomViewer
  });

  return (
    <div className="grading-shell">
      <ExamScoringPanel
        initialExamId={initialExamId}
        canGrade={canGrade}
        canWork={canWork}
        canManageTasks={canManageTasks}
        currentQuestionId={ctx?.task.question_id ?? ""}
        scoring={examScoring}
        tasks={tasks}
        currentUserId={currentUserId}
        onTasksChanged={loadTasks}
        onOpenGoldPapers={() => setGoldPaperManagerOpen(true)}
        onOpenCalibration={setCalibrationQuestionId}
      />
      <WorkbenchHeader
        canWork={canWork}
        canManageTasks={canManageTasks}
        queueScope={queueScope}
        hasContext={Boolean(ctx)}
        ownsSelectedTask={ownsSelectedTask}
        myProgress={myProgress}
        reviewerProgress={reviewerProgress}
        remainingCount={remainingCount}
        progressComplete={Boolean(taskAggregate) && !loadingTasks && !taskError}
        draftSaveStatus={draftSaveStatus}
        loading={loadingTasks || contextLoading}
        actioning={actioning}
        onRefresh={refreshCurrent}
        onNext={goNext}
        onClaim={claimTask}
        onRelease={releaseCurrentTask}
        onResolveConflict={openDraftConflict}
        onReloadConflict={() => loadContext(selectedTaskId, true)}
      />

      <section className="grading-workspace">
        <TaskRail
          canManageTasks={canManageTasks}
          canWork={canWork}
          queueScope={queueScope}
          taskFilter={taskFilter}
          keyword={keyword}
          tasks={tasks}
          filteredTasks={filteredTasks}
          assignableTasks={assignableTasks}
          assignmentTaskIds={assignmentTaskIds}
          assignmentUserId={assignmentUserId}
          graderOptions={graderOptions}
          graderNames={graderNames}
          gradersError={gradersError}
          selectedTaskId={selectedTaskId}
          loadingTasks={loadingTasks}
          taskError={taskError}
          hasMoreTasks={hasMoreTasks}
          pageError={pageError}
          loadingMoreTasks={loadingMoreTasks}
          actioning={actioning}
          onQueueScopeChange={setQueueScope}
          onTaskFilterChange={setTaskFilter}
          onKeywordChange={setKeyword}
          onAssignmentUserChange={setAssignmentUserId}
          onSelectAllAssignments={(selected) => setAssignmentTaskIds(selected ? assignableTasks.map((task) => task.id) : [])}
          onToggleAssignment={(taskId, selected) => setAssignmentTaskIds((current) => selected ? [...new Set([...current, taskId])] : current.filter((id) => id !== taskId))}
          onAssignSelected={assignSelectedTasks}
          onSelectTask={(taskId) => {
            pendingNextRef.current = null;
            suppressAutoSelectRef.current = false;
            setSelectedTaskId(taskId);
          }}
          onLoadTasks={loadTasks}
          onLoadMoreTasks={loadMoreTasks}
        />

        {contextLoading ? (
          <main className="grading-main-empty">
            <LoadingState label="正在加载答卷" />
          </main>
        ) : contextError ? (
          <main className="grading-main-empty">
            <ErrorState message={contextError} onRetry={() => void loadContext(selectedTaskId)} />
          </main>
        ) : !ctx ? (
          <main className="grading-main-empty">
            <EmptyState
              title="请选择一份答卷"
              description={tasks.length > 0
                ? canManageTasks ? "从左侧队列选择需要检查的答卷。" : "从左侧任务队列选择一份答卷开始阅卷。"
                : canManageTasks ? "当前没有待处理任务。" : "当前没有已分配任务，请等待管理员分配。"}
            />
          </main>
        ) : (
          <main className="grading-main">
            {ctx.warnings.length > 0 ? <Alert type="warning" showIcon message="AI 辅助不可用" description={ctx.warnings.map((warning) => getSafeUserText(warning, "智能辅助暂时不可用")).join("；")} /> : null}
            <section className="grading-reason-banner"><div><span>为什么需要我处理？</span><strong>{sourceLabels[ctx.task.source] ?? "本题需要人工确认"}</strong><p>{ctx.warnings[0] ? getSafeUserText(ctx.warnings[0], "智能辅助暂时不可用，请人工确认。") : sourceDescriptions[ctx.task.source] ?? "请结合学生原始答案和评分细则完成确认。"}</p></div>{selectedGrade && !requiresExplicitSecondOpinion(ctx.reviewContext) ? <div><span>系统建议</span><strong>{subjectCode === "mathematics" && !canAdoptAiScore ? "待核对 / 已失效" : `${selectedGrade.suggested_score} / ${selectedGrade.max_score}`}</strong></div> : null}</section>

            <section className="grading-panels">
              <AnswerEvidencePane
                context={ctx}
                preview={preview}
                previewLoading={previewLoading}
                viewerMode={viewerMode}
                scale={scale}
                autoFit={autoFit}
                imageSize={imageSize}
                rotation={rotation}
                offset={offset}
                dragging={dragging}
                viewportRef={viewportRef}
                canViewOriginalImage={canViewOriginalImage}
                canEditDraft={canEditDraft}
                rubricPoints={rubricPoints}
                onChangeViewerMode={changeViewerMode}
                onZoom={zoomViewer}
                onRotate={rotateViewer}
                onFit={() => applyViewerFit(imageSize, true)}
                onPointerDown={onPointerDown}
                onPointerMove={onPointerMove}
                onPointerUp={onPointerUp}
                onImageLoad={onImageLoad}
                mathSelection={currentMathSelection}
              />

              <aside className="grading-inspector">
                <EvidenceInspector
                  context={ctx}
                  draft={draft}
                  selectedGrade={selectedGrade}
                  maxScore={maxScore}
                  canEditDraft={canEditDraft}
                  canVerifyEvidence={canVerifyEvidence}
                  actioning={actioning}
                  math={math}
                  onRefreshMath={refreshMath}
                  onSelectMathStep={selectMathEvidenceStep}
                  canRequestMath={canGrade && canEditDraft && !requiresExplicitSecondOpinion(ctx.reviewContext)}
                  requestingMath={mathRequesting}
                  onRequestMath={() => void requestMathSuggestion()}
                  mathRequestNotice={mathRequestNotice}
                  onVerifyEvidence={() => runAction(
                    "evidence",
                    async () => {
                      const result = await verifyEvidence(selectedGrade!.id);
                      setCtx((current) => (current ? { ...current, evidenceJob: result.job } : current));
                    },
                    "证据校验完成"
                  )}
                />
                <ScoreEditor
                  context={ctx}
                  draft={draft}
                  setDraft={setDraft}
                  quickSubmit={quickSubmit}
                  onQuickSubmitChange={setQuickSubmit}
                  maxScore={maxScore}
                  rubricPoints={rubricPoints}
                  selectedGrade={selectedGrade}
                  canEditDraft={canEditDraft}
                  canSubmit={canSubmit}
                  canReturn={canReturn}
                  canManageTasks={canManageTasks}
                  actioning={actioning}
                  onAdoptAiScore={() => void adoptAiScore()}
                  canAdoptAiScore={canAdoptAiScore}
                  onMarkDispute={markDispute}
                  onSubmit={submitGrade}
                />
              </aside>
            </section>

          </main>
        )}
      </section>
      <WorkbenchOverlays
        goldPaperManagerOpen={goldPaperManagerOpen}
        initialExamId={initialExamId}
        onCloseGoldPaperManager={() => setGoldPaperManagerOpen(false)}
        calibrationQuestionId={calibrationQuestionId}
        calibrationExamId={initialExamId || ctx?.task.exam_id || ""}
        currentUserId={currentUserId}
        canManageTasks={canManageTasks}
        onCloseCalibration={() => setCalibrationQuestionId("")}
        onCalibrationQualified={() => { message.success("校准已通过，可以重新领取本题任务"); void loadTasks(); }}
        goldPaperCandidate={goldPaperCandidate}
        onCloseGoldPaperNomination={() => setGoldPaperCandidate(null)}
        onGoldPaperCreated={() => setGoldPaperManagerOpen(true)}
        draftConflict={draftConflict}
        resolvingConflict={resolvingConflict}
        onClearDraftConflict={() => setDraftConflict(null)}
        onApplyDraftConflict={applyDraftConflict}
      />
    </div>
  );
}
