import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { App } from "antd";
import { removeReviewDraftFallback, saveReviewDraftFallback } from "../../../auth/reviewDraftFallback";
import {
  renewReviewTask,
  verifyEvidence
} from "../../../api/review";
import { isReviewTaskContextScopeCurrent, reviewTaskContextKeys, useReviewTaskContextScope } from "../../../query/reviewTaskContext";
import type { GoldPaperNominationCandidate } from "../../gold-papers";
import {
  createDraftSnapshot,
  createInitialDraft,
  formatError
} from "./gradingWorkbench.model";
import type { DraftConflictResolution } from "./draftConflict";
import type {
  DraftSaveStatus,
  ScoreDraft,
  WorkbenchContext
} from "./gradingWorkbench.types";
import { WorkbenchTaskContent } from "./components/WorkbenchTaskContent";
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
import type { MathStepSelection } from "./mathWorkbenchEvidence";
import { commitWorkbenchDraft, readQueuedWorkbenchContext, restoreWorkbenchDraft } from "./workbenchDraftLifecycle";
import { canAdoptWorkbenchGrade, reviewDraftState, selectWorkbenchGrade, visibleMathSelection } from "./workbenchReviewState";
import { requiresExplicitSecondOpinion } from "./reviewContext";
import { useUnsavedChangesGuard } from "../../../router/useUnsavedChangesGuard";
import { AssignmentPreview } from "./components/AssignmentPreview";
import type { ReviewTask } from "../../../api/review";

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

function mathRequestDisabledReason(canGrade: boolean, canEditDraft: boolean) {
  // 无评分权限时先解释身份限制，不能被当前任务只读的原因覆盖。
  if (!canGrade) return "当前身份无重新生成数学建议的权限；请按冻结细则人工评分，需要重新生成时请联系管理员。";
  if (!canEditDraft) return "当前任务只读；请先领取属于你的可编辑任务，再重新生成建议。";
  return undefined;
}

export function GradingWorkbench({ canWork, canManageTasks, canViewOriginalImage, canGrade, canVerifyEvidence, canReturn, currentUserId, currentTenantId, initialExamId = "", personalScope = false }: Readonly<GradingWorkbenchProps>) {
  const { message, modal } = App.useApp();
  const queryClient = useQueryClient();
  const queryScope = useReviewTaskContextScope(initialExamId, canViewOriginalImage);
  const activeQueryScope = useRef<typeof queryScope | null>(queryScope);
  activeQueryScope.current = queryScope;
  // 会话未变也可能切考试或查看权限；旧动作只能回填它发起时的工作台。
  const isCurrentScope = useCallback(() => activeQueryScope.current === queryScope && isReviewTaskContextScopeCurrent(queryScope), [queryScope]);
  useEffect(() => {
    activeQueryScope.current = queryScope;
    return () => { if (activeQueryScope.current === queryScope) activeQueryScope.current = null; };
  }, [queryScope]);
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
  const actionGeneration = useRef(0);
  const [ctx, setCtx] = useState<WorkbenchContext | null>(null);
  const [contextLoading, setContextLoading] = useState(false);
  const [contextError, setContextError] = useState<string | null>(null);
  const [draft, setDraft] = useState<ScoreDraft>(() => createInitialDraft(null));
  const [quickSubmit, setQuickSubmit] = useState(false);
  const [actioning, setActioning] = useState<string | null>(null);
  const [assignmentPreview, setAssignmentPreview] = useState<ReviewTask[] | null>(null);
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
    queryScope,
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
    onKeyDown: onViewerKeyDown,
    onImageLoad
  } = useAnswerViewer({ context: ctx, canViewOriginalImage, prefetchedPreviewRef, contentRevision: math.state.understanding?.artifact.input_hash });
  const selectedGrade = useMemo(() => selectWorkbenchGrade(ctx?.aiGrades ?? [], subjectCode, math.state, math.dirty), [ctx?.aiGrades, math.dirty, math.state, subjectCode]);
  const canAdoptAiScore = canAdoptWorkbenchGrade(selectedGrade, ctx, mathRequesting, subjectCode, math.state, math.dirty);
  const currentMathSelection = visibleMathSelection(mathSelection, math.state, math.dirty);
  useEffect(() => { setMathRequestNotice(""); setMathSelection(null); setMathRequesting(false); }, [selectedTaskId]);
  const maxScore = ctx?.question?.score ?? selectedGrade?.max_score ?? 0;
  const rubricPoints = ctx?.question?.rubric?.points ?? [];
  const scoreShortcuts = useGradingScoreShortcuts({ draft, setDraft, rubricPoints, onInfo: (text) => message.info(text) });
  const { ownsSelectedTask, canEditDraft, unsyncedDraft, canSubmit } = reviewDraftState({
    ctx, currentUserId, canWork, hasSession, resolvingConflict, hasConflict: Boolean(draftConflict),
    hydrated: draftHydrated, saveStatus: draftSaveStatus, actioning,
    snapshot: createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit), lastSavedSnapshot: lastSavedDraft.current
  });
  useUnsavedChangesGuard({ dirty: unsyncedDraft || math.dirty, busy: Boolean(actioning), description: "当前评分或证据修改尚未同步到服务端，离开后只能恢复已成功保存的版本。" });
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
      scoreShortcuts.reset();
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
  }, [canViewOriginalImage, currentUserId, hasSession, initialExamId, invalidateViewerContent, prepareViewerContext, restoreViewer, queryClient, queryScope]);

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
    if (!draftHydrated || ctx?.task.assigned_to !== currentUserId || ctx.task.status === "submitted") return;
    const snapshotValue = createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit);
    const snapshot = JSON.stringify(snapshotValue);
    if (snapshot === lastSavedDraft.current) return;
    saveReviewDraftFallback(currentUserId, ctx.task.id, snapshotValue);
    if (draftSaveStatus === "conflict") return;
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
  }, [autoFit, ctx, currentUserId, draft, draftHydrated, draftRevision, offset, online, rotation, scale, viewerMode, queryClient, queryScope]);

  useEffect(() => {
    resetQueue();
    actionGeneration.current += 1;
    actionLock.current = false;
    mathActionLock.current = false;
    setActioning(null);
    setAssignmentPreview(null);
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
  }, [currentUserId, currentTenantId, initialExamId, prepareViewerContext, resetQueue, queryScope]);

  useEffect(() => {
    // 同账号解锁会更新会话代次但不改变 loadTasks 的引用；清队列后仍需重新读取。
    if (!isReviewTaskContextScopeCurrent(queryScope)) return;
    void loadTasks();
  }, [loadTasks, queryScope]);

  useEffect(() => {
    void loadGraders();
  }, [loadGraders]);

  useEffect(() => {
    void loadContext(selectedTaskId);
  }, [loadContext, selectedTaskId]);

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
  }, [ctx, currentUserId, selectedTaskId, isCurrentScope]);

  const refreshCurrent = async () => {
    if (selectedTaskId && isCurrentScope() && activeTask.current === selectedTaskId) {
      await queryClient.invalidateQueries({ queryKey: reviewTaskContextKeys.task(queryScope, selectedTaskId) });
      if (!isCurrentScope() || activeTask.current !== selectedTaskId) return;
      await loadContext(selectedTaskId);
      if (!isCurrentScope() || activeTask.current !== selectedTaskId) return;
      await loadTasks();
      if (isCurrentScope() && activeTask.current === selectedTaskId && !math.dirty) await math.refresh();
    }
  };

  const runAction = async (key: string, action: () => Promise<void>, successText: string) => {
    if (actionLock.current || !isCurrentScope()) return;
    const generation = ++actionGeneration.current;
    actionLock.current = true;
    setActioning(key);
    try {
      await action();
      if (generation === actionGeneration.current && isCurrentScope()) message.success(successText);
    } catch (currentError) {
      if (generation === actionGeneration.current && isCurrentScope()) message.error(formatError(currentError));
    } finally {
      // 旧动作不能解开新会话的操作锁，也不能把新操作的提示清空。
      if (generation === actionGeneration.current && isCurrentScope()) {
        actionLock.current = false;
        setActioning(null);
      }
    }
  };
  const { assignPlannedTasks, claimTask, goNext, releaseCurrentTask } = useGradingQueueActions({
    isCurrentScope,
    canManageTasks,
    initialExamId: queue.effectiveExamId,
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
    isCurrentScope,
    queryScope,
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
  const { submitGrade, markDispute, adoptAiScore, adoptAiFeedback, openDraftConflict, applyDraftConflict, confirmReturnTask } = reviewActions;
  const { refreshMath, selectMathEvidenceStep, requestMathSuggestion } = useGradingMathActions({
    queryScope, isCurrentScope,
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

  const changeQueueScope = (change: () => void) => {
    if (actioning) { message.info("正在提交或分配，请等待结果后切换。"); return; }
    if (!math.dirty) { change(); return; }
    const taskId = selectedTaskId;
    modal.confirm({ title: "放弃未保存的证据校正并切换？", content: "当前证据校正尚未保存。评分草稿以服务端或本机兜底保存状态为准。", okText: "放弃并切换", cancelText: "继续校正", onOk: () => { if (isCurrentScope() && activeTask.current === taskId) change(); } });
  };

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
          scope={queue}
          fixedExam={Boolean(initialExamId)}
          onExamChange={(id) => changeQueueScope(() => { queue.setExamFilter(id); queue.setQuestionFilter(""); })}
          onSubjectChange={(subject) => changeQueueScope(() => { queue.setSubjectFilter(subject); queue.setExamFilter(""); queue.setQuestionFilter(""); })}
          onQuestionChange={(id) => changeQueueScope(() => queue.setQuestionFilter(id))}
          onUnassignedChange={(value) => changeQueueScope(() => queue.setUnassignedOnly(value))}
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
          onQueueScopeChange={(scope) => changeQueueScope(() => setQueueScope(scope))}
          onTaskFilterChange={(filter) => changeQueueScope(() => setTaskFilter(filter))}
          onKeywordChange={(text) => changeQueueScope(() => setKeyword(text))}
          onAssignmentUserChange={setAssignmentUserId}
          onSelectAllAssignments={(selected) => setAssignmentTaskIds(selected ? assignableTasks.map((task) => task.id) : [])}
          onToggleAssignment={(taskId, selected) => setAssignmentTaskIds((current) => selected ? [...new Set([...current, taskId])] : current.filter((id) => id !== taskId))}
          onPreviewAssignments={() => setAssignmentPreview(assignableTasks.filter((task) => assignmentTaskIds.includes(task.id)).map((task) => ({ ...task })))}
          onSelectTask={(taskId) => {
            changeQueueScope(() => { pendingNextRef.current = null; suppressAutoSelectRef.current = false; setSelectedTaskId(taskId); });
          }}
          onLoadTasks={loadTasks}
          onLoadMoreTasks={loadMoreTasks}
        />

        <WorkbenchTaskContent
          context={ctx} loading={contextLoading} error={contextError} hasTasks={tasks.length > 0}
          canManageTasks={canManageTasks} onRetry={() => void loadContext(selectedTaskId)}
          selectedGrade={selectedGrade} subjectCode={subjectCode} canAdoptAiScore={canAdoptAiScore}
          answer={{
            preview, previewLoading, viewerMode, scale, autoFit, imageSize, rotation, offset, dragging,
            viewportRef, canViewOriginalImage, canEditDraft, rubricPoints,
            onChangeViewerMode: changeViewerMode, onZoom: zoomViewer, onRotate: rotateViewer,
            onFit: () => applyViewerFit(imageSize, true), onPointerDown, onPointerMove, onPointerUp,
            onImageLoad, onKeyDown: onViewerKeyDown, mathSelection: currentMathSelection
          }}
          evidence={{
            draft, selectedGrade, maxScore, canEditDraft, canVerifyEvidence, actioning, math,
            onRefreshMath: refreshMath, onSelectMathStep: selectMathEvidenceStep,
            canRequestMath: canGrade && canEditDraft && !requiresExplicitSecondOpinion(ctx!.reviewContext),
            mathDisabledReason: mathRequestDisabledReason(canGrade, canEditDraft),
            requestingMath: mathRequesting, onRequestMath: () => void requestMathSuggestion(), mathRequestNotice,
            onVerifyEvidence: () => {
              if (!ctx || !selectedGrade || !isCurrentScope()) return Promise.resolve();
              const taskId = ctx.task.id;
              const requestId = contextRequestRef.current;
              return runAction("evidence", async () => {
                const result = await verifyEvidence(selectedGrade.id);
                // 同一会话内仍可换题或刷新版本；旧建议的校验结果不能写进新题的证据面板。
                if (!isCurrentScope() || activeTask.current !== taskId || contextRequestRef.current !== requestId) return;
                setCtx((current) => current?.task.id === taskId ? { ...current, evidenceJob: result.job } : current);
              }, "证据校验完成");
            }
          }}
          editor={{
            draft, setDraft, quickSubmit, onQuickSubmitChange: setQuickSubmit, maxScore, rubricPoints,
            selectedGrade, canEditDraft, canSubmit, canReturn, canManageTasks, actioning,
            onAdoptAiScore: () => void adoptAiScore(), onAdoptAiFeedback: adoptAiFeedback, canAdoptAiScore, onMarkDispute: markDispute, onSubmit: submitGrade
          }}
        />
      </section>
      {assignmentPreview ? <AssignmentPreview tasks={assignmentPreview} defaultGrader={assignmentUserId} options={graderOptions} unassignedCount={queue.unassignedCount} busy={actioning === "assign-tasks"} onCancel={() => setAssignmentPreview(null)} onConfirm={assignPlannedTasks} /> : null}
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
