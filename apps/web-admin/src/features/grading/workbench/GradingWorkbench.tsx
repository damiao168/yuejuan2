import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { App, Button, Drawer } from "antd";
import { PanelLeft } from "lucide-react";
import { removeReviewDraftFallback } from "../../../auth/reviewDraftFallback";
import {
  verifyEvidence
} from "../../../api/review";
import { isReviewTaskContextScopeCurrent, reviewTaskContextKeys, useReviewTaskContextScope } from "../../../query/reviewTaskContext";
import type { GoldPaperNominationCandidate } from "../../gold-papers";
import {
  createDraftSnapshot,
  formatError
} from "./gradingWorkbench.model";
import { WorkbenchTaskContent } from "./components/WorkbenchTaskContent";
import { groupTaskRefs, RegionScoreEditor } from "./components/RegionScoreEditor";
import { WorkbenchHeader } from "./components/WorkbenchHeader";
import { WorkbenchOverlays } from "./components/WorkbenchOverlays";
import { type GradingManagementView } from "./components/GradingManagementNav";
import { useGradingKeyboard } from "./hooks/useGradingKeyboard";
import { useGradingPrefetch } from "./hooks/useGradingPrefetch";
import { useExamScoring } from "./hooks/useExamScoring";
import type { ScoringReviewFilter } from "./scoringReviewCategories";
import { useAnswerViewer } from "./hooks/useAnswerViewer";
import { useMathWorkbenchEvidence } from "./hooks/useMathWorkbenchEvidence";
import { useGradingQueue } from "./hooks/useGradingQueue";
import { useGradingScoreShortcuts } from "./hooks/useGradingScoreShortcuts";
import { useGradingQueueActions } from "./hooks/useGradingQueueActions";
import { useGradingReviewActions } from "./hooks/useGradingReviewActions";
import { useGradingMathActions } from "./hooks/useGradingMathActions";
import type { MathStepSelection } from "./mathWorkbenchEvidence";
import { canAdoptWorkbenchGrade, mathRequestDisabledReason, reviewDraftState, selectWorkbenchGrade, visibleMathSelection } from "./workbenchReviewState";
import { isChoiceReview, requiresExplicitSecondOpinion } from "./reviewContext";
import "./grading-management.css";
import { WorkbenchTaskQueue } from "./components/WorkbenchTaskQueue";
import { WorkbenchManagement } from "./components/WorkbenchManagement";
import { useWorkbenchTaskState } from "./hooks/useWorkbenchTaskState";
import { useWorkbenchTaskSession } from "./hooks/useWorkbenchTaskSession";
import { useUnsavedChangesGuard } from "../../../router/useUnsavedChangesGuard";
import { AssignmentPreview } from "./components/AssignmentPreview";
import type { ReviewTask } from "../../../api/review";
import { usePersonalReviewBookmarks } from "./hooks/usePersonalReviewBookmarks";
import { ReviewBookmarkControls, ReviewBookmarksDrawer } from "./components/ReviewBookmarks";
import { TaskContentContainer } from "./components/WorkbenchContainers";
import { FrozenQuestionReference, type FrozenReferenceTab } from "./components/FrozenQuestionReference";
import { useLiveScoreDraft } from "./hooks/useLiveScoreDraft";
import { ReviewStatisticsPanel } from "./components/ReviewStatisticsPanel";

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
  canManageScores?: boolean;
  onNavigate?: (path: string) => void;
}

export function GradingWorkbench({ canWork, canManageTasks, canViewOriginalImage, canGrade, canVerifyEvidence, canReturn, currentUserId, currentTenantId, initialExamId = "", personalScope = false, canManageScores = false, onNavigate }: Readonly<GradingWorkbenchProps>) {
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
  const heldGroupTask = useRef("");
  const queue = useGradingQueue({ canManageTasks, canWork, currentUserId, initialExamId, personalScope, onInfo: reportNoNextTask, heldGroupTask });
  const {
    setTaskFilter, queueScope, setQueueScope, tasks, setTasks, taskAggregate,
    selectedTaskId, setSelectedTaskId, loadingTasks, hasMoreTasks, pageError, taskError,
    assignmentUserId, assignmentTaskIds, setAssignmentTaskIds,
    filteredTasks, assignableTasks, graderOptions, graderNames, reviewerProgress, myProgress, remainingCount,
    prefetchTasks, nextTask, previousTask, pendingNextRef, suppressAutoSelectRef,
    loadTasks, loadMoreTasks, refreshTaskAggregate, loadGraders, reset: resetQueue
  } = queue;
  const actionLock = useRef(false);
  const actionGeneration = useRef(0);
  const taskState = useWorkbenchTaskState(selectedTaskId, currentUserId);
  const {
    ctx, setCtx, contextLoading, contextError, draft, setDraft, activeTask, activeUserId, liveDraft, online,
    contextRequestRef, setDraftRevision, draftSaveStatus, setDraftSaveStatus, draftHydrated, setDraftHydrated,
    lastSavedDraft, draftSaves, conflictedDrafts, draftConflict, setDraftConflict,
    resolvingConflict, setResolvingConflict, conflictServerContext
  } = taskState;
  const [taskDrawerOpen, setTaskDrawerOpen] = useState(false);
  // 未指定考试或没有评分权限时直接显示任务列表；可管理本场评分时先看概览。
  const [managementView, setManagementView] = useState<GradingManagementView>(initialExamId && canGrade ? "overview" : "assignment");
  // biome-ignore lint/correctness/useExhaustiveDependencies: 账户变化也须回到可用管理入口，不能沿用上次展开的界面。
  useEffect(() => { setManagementView(initialExamId && canGrade ? "overview" : "assignment"); }, [initialExamId, canGrade, currentUserId, currentTenantId]);
  const [taskPreviewOpen, setTaskPreviewOpen] = useState(false);
  const [bookmarksOpen, setBookmarksOpen] = useState(false);
  const bookmarks = usePersonalReviewBookmarks(queryScope, canWork ? selectedTaskId : "");
  const [regionDirty, setRegionDirty] = useState(false);
  const [regionBusy, setRegionBusy] = useState(false);
  const regionEditing = Boolean(ctx?.gradingGroupImageUrl && ctx.gradingGroupId && groupTaskRefs(ctx).length >= 2);
  heldGroupTask.current = regionEditing || ctx?.task.status === "submitted" ? ctx!.task.id : "";
  const updateRegionState = useCallback((dirty: boolean, busy: boolean) => { setRegionDirty(dirty); setRegionBusy(busy); }, []);
  const [referenceTab, setReferenceTab] = useState<FrozenReferenceTab | null>(null);
  const [actioning, setActioning] = useState<string | null>(null);
  const [advancing, setAdvancing] = useState(false);
  const [statisticsRevision, setStatisticsRevision] = useState(0);
  const advanceLock = useRef(false);
  const regionSubmit = useRef<(() => Promise<boolean>) | null>(null);
  const registerRegionSubmit = useCallback((submit: (() => Promise<boolean>) | null) => { regionSubmit.current = submit; }, []);
  const [assignmentPreview, setAssignmentPreview] = useState<ReviewTask[] | null>(null);
  const [mathRequesting, setMathRequesting] = useState(false);
  const [mathRequestNotice, setMathRequestNotice] = useState("");
  const [mathSelection, setMathSelection] = useState<MathStepSelection | null>(null);
  const mathActionLock = useRef(false);
  const setEditingDraft = useLiveScoreDraft(liveDraft, setDraft);
  const [goldPaperManagerOpen, setGoldPaperManagerOpen] = useState(false);
  const [calibrationQuestionId, setCalibrationQuestionId] = useState("");
  const [goldPaperCandidate, setGoldPaperCandidate] = useState<GoldPaperNominationCandidate | null>(null);

  const subjectCode = ctx?.reviewContext.subject_tool_hints.subject_code ?? "";
  // 整区成员没有单题 ROI，不能把共享裁图当作本题数学步骤和批注坐标的来源。
  const legacyMath = Boolean(ctx && !isChoiceReview(ctx.reviewContext) && !ctx.aiGrades.some(grade => grade.raw_output?.schema_version === "grading-image-v3"));
  const math = useMathWorkbenchEvidence(ctx?.task.answer_segment_id ?? "", ctx?.answerScope === "grading_group" || !legacyMath ? "" : subjectCode, ctx?.reviewContext.question_snapshot.id ?? "");
  const prefetch = useGradingPrefetch({
    queryScope,
    canWork: canWork && (!canManageTasks || managementView === "work" || taskPreviewOpen),
    canViewOriginalImage,
    currentUserId,
    initialExamId,
    selectedTaskId,
    tasks: prefetchTasks
  });
  const { prefetchedTaskRef, prefetchedPreviewRef } = prefetch;
  const viewer = useAnswerViewer({ context: ctx, canViewOriginalImage, prefetchedPreviewRef, contentRevision: math.state.understanding?.artifact.input_hash });
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
    restore: restoreViewer,
    fit: applyViewerFit,
    changeMode: changeViewerMode,
    zoom: zoomViewer,
    rotate: rotateViewer, resetView: resetImageView,
    onPointerDown,
    onPointerMove,
    onPointerUp,
    onKeyDown: onViewerKeyDown,
    onImageLoad
  } = viewer;
  const selectedGrade = useMemo(() => selectWorkbenchGrade(ctx?.aiGrades ?? [], subjectCode, math.state, math.dirty, ctx), [ctx, math.dirty, math.state, subjectCode]);
  const canAdoptAiScore = canAdoptWorkbenchGrade(selectedGrade, ctx, mathRequesting, subjectCode, math.state, math.dirty);
  const currentMathSelection = visibleMathSelection(mathSelection, math.state, math.dirty);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切题必须清除数学建议请求提示及选择；不能将上一题的忙碌状态沿用到新题。
  useEffect(() => { setMathRequestNotice(""); setMathSelection(null); setMathRequesting(false); setReferenceTab(null); }, [selectedTaskId]);
  const maxScore = ctx?.question?.score ?? selectedGrade?.max_score ?? 0;
  const rubricPoints = ctx?.question?.rubric?.points ?? [];
  const scoreShortcuts = useGradingScoreShortcuts({ draft, setDraft: setEditingDraft, rubricPoints: [], onInfo: (text) => message.info(text) });
  const resetScoreShortcuts = scoreShortcuts.reset;
  const { ownsSelectedTask, canEditDraft, pendingGradeSubmission, canSubmit } = reviewDraftState({
    ctx, currentUserId, canWork, hasSession, resolvingConflict, hasConflict: Boolean(draftConflict),
    hydrated: draftHydrated, saveStatus: draftSaveStatus, actioning,
    snapshot: createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit), lastSavedSnapshot: lastSavedDraft.current
  });
  const hasPendingGradeSubmission = regionEditing ? regionDirty : pendingGradeSubmission;
  const leaveDescription = hasPendingGradeSubmission ? "当前已填写的评分尚未提交，离开不会完成交分。" : "当前证据校正尚未保存，离开后只能恢复已成功保存的版本。";
  useUnsavedChangesGuard({ dirty: hasPendingGradeSubmission || math.dirty,
    busy: (hasPendingGradeSubmission || math.dirty) && (Boolean(actioning) || regionBusy || advancing),
    title: hasPendingGradeSubmission ? "评分尚未提交，继续离开？" : undefined,
    okText: "继续离开", cancelText: "继续阅卷", description: leaveDescription });
  const canUndoScoreChange = scoreShortcuts.canUndo;
  const examScoring = useExamScoring({ initialExamId, currentUserId, currentTenantId, canGrade, onTasksChanged: loadTasks });
  // 启动评分后仍进入同一结果视图；明细读取返回不能重新弹出已经离开的管理页。
  useEffect(() => { if (canManageTasks && examScoring.detailOpen) setManagementView("results"); }, [canManageTasks, examScoring.detailOpen]);

  const managing = canManageTasks && managementView !== "work";
  const resetWorkbenchScope = useCallback(() => {
    resetQueue();
    actionGeneration.current += 1;
    actionLock.current = false;
    mathActionLock.current = false;
    setActioning(null);
    setAdvancing(false); advanceLock.current = false; regionSubmit.current = null;
    setRegionDirty(false); setRegionBusy(false);
    setAssignmentPreview(null);
    setTaskDrawerOpen(false); setTaskPreviewOpen(false); setManagementView(initialExamId && canGrade ? "overview" : "assignment"); setBookmarksOpen(false);
  }, [resetQueue, initialExamId, canGrade]);
  const { loadContext } = useWorkbenchTaskSession({
    state: taskState, queryScope, currentUserId, currentTenantId, initialExamId, canGrade, canViewOriginalImage,
    hasSession, selectedTaskId, setSelectedTaskId, regionEditing, submitting: advancing, managing, taskPreviewOpen,
    viewer, prefetch, resetScoreShortcuts, resetWorkbenchScope, loadTasks, loadGraders, isCurrentScope
  });

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
      if (successText && generation === actionGeneration.current && isCurrentScope()) message.success(successText);
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
    ctx, setCtx, draft, setDraft: setEditingDraft, draftConflict, canSubmit, canReturn, canEditDraft,
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
  const advance = async () => {
    if (advanceLock.current || actionLock.current || regionBusy || contextLoading || loadingTasks || !isCurrentScope()) return;
    const taskId = selectedTaskId;
    if (queue.taskFilter === "reviewed") {
      if (queue.nextReviewedTask) setSelectedTaskId(queue.nextReviewedTask.id);
      else if (hasMoreTasks) {
        pendingNextRef.current = taskId;
        void loadMoreTasks();
        message.info(pageError ? "历史记录加载失败，请在任务列表重试" : "正在加载后续历史记录");
      } else message.info("已到历史记录末尾");
      return;
    }
    advanceLock.current = true; setAdvancing(true);
    try {
      if (ctx && regionEditing && canWork) {
        if (!online || draftConflict || !regionSubmit.current || !await regionSubmit.current()) return;
      } else if (ctx && canEditDraft) {
        if (!online || math.dirty || draftConflict) { message.warning("请先联网并处理草稿冲突或证据校正，再提交下一份。"); return; }
        if (!canSubmit) { message.warning("请填写最终分，0 分也需要明确输入。"); return; }
        // 等已发出的草稿写入收尾；只有正式交分成功，才能离开当前答卷。
        const draftKey = JSON.stringify([currentUserId, taskId]);
        await draftSaves.current.whenIdle(draftKey);
        if (conflictedDrafts.current.has(draftKey)) { message.warning("草稿已被其他会话修改，请先处理冲突。"); return; }
        if (!isCurrentScope() || activeTask.current !== taskId || !await submitGrade()) return;
        setStatisticsRevision(value => value + 1);
      } else if (hasPendingGradeSubmission || ctx && ownsSelectedTask && ["assigned", "in_progress", "returned"].includes(ctx.task.status)) {
        message.warning("当前任务暂不可提交，请先完成读取或处理草稿冲突。"); return;
      }
      if (isCurrentScope() && activeTask.current === taskId) await goNext(ctx && regionEditing ? groupTaskRefs(ctx).map(task => task.id) : []);
    } finally {
      if (isCurrentScope()) { advanceLock.current = false; setAdvancing(false); }
    }
  };
  const { refreshMath, selectMathEvidenceStep, requestMathSuggestion } = useGradingMathActions({
    queryScope, isCurrentScope,
    ctx, canGrade: canGrade && ctx?.answerScope !== "grading_group", canEditDraft, viewerMode, math, activeTask, mathActionLock,
    setCtx, setMathRequesting, setMathRequestNotice, setMathSelection, changeViewerMode,
    notifyInfo: (text) => message.info(text)
  });

  useGradingKeyboard({
    quickSubmit: false, canToggleBookmark: Boolean(bookmarks.state.data && !bookmarks.busy && !bookmarks.uncertainTask && (bookmarks.state.data.marked || bookmarks.state.data.can_edit_reason)),
    hasTask: Boolean(ctx) && (!managing || taskPreviewOpen),
    canEditDraft: canEditDraft && !regionEditing && !actioning && !advancing,
    canSubmit: Boolean(ctx) && canWork && !actioning && !advancing && !managing,
    canReturn: canReturn && !regionEditing,
    canUndoScoreChange,
    hasAiSuggestion: canAdoptAiScore,
    maxScore,
    rubricPointCount: 0,
    onSubmit: () => void advance(),
    onSetScore: scoreShortcuts.setScore,
    onToggleCriterion: scoreShortcuts.toggleCriterion,
    onAdoptAi: () => void adoptAiScore(),
    onToggleBookmark: () => { const bookmark = bookmarks.state.data; if (bookmark) void bookmarks.write(bookmark, bookmark.marked ? null : ""); },
    onReturnTask: () => confirmReturnTask(false),
    onUndoScoreChange: scoreShortcuts.undo,
    onZoom: zoomViewer
  });

  const changeQueueScope = (change: () => void) => {
    if (actioning || advancing || regionBusy) { message.info("正在保存、提交或分配，请等待结果后切换。"); return; }
    if (bookmarks.busy) { message.info("正在保存标记，请稍后切换。"); return; }
    if (!hasPendingGradeSubmission && !math.dirty) { change(); return; }
    const taskId = selectedTaskId;
    modal.confirm({ title: hasPendingGradeSubmission ? "评分尚未提交，继续离开？" : "放弃未保存的证据校正并切换？", content: leaveDescription,
      okText: "继续离开", cancelText: "继续阅卷", onOk: () => { if (isCurrentScope() && activeTask.current === taskId) change(); } });
  };

  const openResults = (filter?: ScoringReviewFilter) => changeQueueScope(() => {
    setTaskPreviewOpen(false);
    setManagementView("results");
    setQueueScope("all");
    if (filter !== undefined) {
      examScoring.setResultType("all");
      examScoring.setResultState(filter === "all" ? "all" : "review");
      examScoring.setResultKeyword("");
      examScoring.setResultReviewFilter(filter);
    }
    void examScoring.showDetail();
  });

  return (
    <div className={`grading-shell${managing ? " grading-management" : " grading-teacher-simple"}`}>
      {!initialExamId && canManageTasks && canManageScores && onNavigate && queue.effectiveExamId ? <div className="grading-publication-action"><Button type="primary" onClick={() => changeQueueScope(() => onNavigate(`/exams/${encodeURIComponent(queue.effectiveExamId)}/scores`))}>核对并发布本场成绩</Button></div> : null}
      <WorkbenchManagement canManageTasks={canManageTasks} managementView={managementView} initialExamId={initialExamId}
        canGrade={canGrade} canWork={canWork} currentUserId={currentUserId} managing={managing} context={ctx}
        scoring={examScoring} queue={queue} onOpenGoldPapers={() => setGoldPaperManagerOpen(true)}
        onOpenCalibration={setCalibrationQuestionId}
        onOpenAssignments={() => changeQueueScope(() => { examScoring.setDetailOpen(false); setManagementView("assignment"); setQueueScope("all"); })}
        onViewChange={value => changeQueueScope(() => { examScoring.setDetailOpen(false); examScoring.closeImage(); setTaskPreviewOpen(false); setManagementView(value); setQueueScope(value === "work" ? "mine" : "all"); })}
        onOpenResults={openResults} />
      <ReviewStatisticsPanel examId={queue.effectiveExamId || ctx?.task.exam_id || ""} tenantId={currentTenantId} actorId={currentUserId} personalScope={personalScope || !canManageTasks || (!managing && queueScope === 'mine')} refreshRevision={statisticsRevision} />
      {!managing ? <WorkbenchHeader
        canWork={canWork}
        canManageTasks={false}
        queueScope={queueScope}
        hasContext={Boolean(ctx)}
        ownsSelectedTask={ownsSelectedTask}
        myProgress={myProgress}
        reviewerProgress={reviewerProgress}
        remainingCount={remainingCount}
        progressComplete={Boolean(taskAggregate) && !loadingTasks && !taskError}
        draftSaveStatus={regionEditing ? regionBusy ? "saving" : regionDirty ? "pending" : "saved" : draftSaveStatus}
        loading={loadingTasks || contextLoading}
        actioning={advancing ? "submit" : actioning}
        onRefresh={async () => changeQueueScope(() => { void refreshCurrent(); })}
        onNext={advance}
        nextLabel={ctx && regionEditing && canWork ? "提交本区并下一份" : ctx && canEditDraft ? "提交并下一份" : "下一份"}
        onPrevious={() => changeQueueScope(() => { if (previousTask) setSelectedTaskId(previousTask.id); })}
        canPrevious={Boolean(previousTask)}
        onOpenReference={setReferenceTab}
        onClaim={async () => changeQueueScope(() => { void claimTask(); })}
        onRelease={async () => changeQueueScope(() => { void releaseCurrentTask(); })}
        onResolveConflict={openDraftConflict}
        onReloadConflict={() => loadContext(selectedTaskId, true)}
        readonlyLabel={ctx?.task.status === "submitted" || ctx?.task.status === "completed" ? "已提交·只读" : canManageTasks ? "管理员只读" : "任务只读"}
        taskLabel={!managing && ctx ? regionEditing ? "填空整区" : ctx.task.question_no || "当前题目" : undefined}
        scoreLabel={!regionEditing && !managing && ctx ? typeof draft.score === "number" ? `${Number(draft.score.toFixed(1))}/${maxScore} 分` : "未评分" : undefined}
        personalActions={<>
          {!managing ? <Button icon={<PanelLeft size={16} />} onClick={() => changeQueueScope(() => setTaskDrawerOpen(true))}>任务</Button> : null}
          {!managing ? <Button onClick={() => changeQueueScope(() => { setTaskFilter("reviewed"); setTaskDrawerOpen(true); })}>历史评阅</Button> : null}
          {canWork ? <ReviewBookmarkControls scope={queryScope} controller={bookmarks} /> : null}
        </>}
      /> : null}

      {bookmarks.error && !bookmarksOpen ? <div className="bookmark-inline-status" role="status">{bookmarks.error}{bookmarks.uncertainTask ? <Button size="small" loading={bookmarks.busy} onClick={() => void bookmarks.retry()}>重试原操作</Button> : null}</div> : null}

      {!managing || managementView === "assignment" ? <section className={`grading-workspace${managing ? " grading-management-workspace" : ""}`}>
        <WorkbenchTaskQueue queue={queue} managing={managing} canWork={canWork} fixedExam={Boolean(initialExamId)}
          open={taskDrawerOpen} onClose={() => setTaskDrawerOpen(false)} onOpenBookmarks={() => setBookmarksOpen(true)}
          onPreviewTask={() => setTaskPreviewOpen(true)} onPreviewAssignments={setAssignmentPreview}
          actioning={actioning} ownsSelectedTask={ownsSelectedTask} bookmarksBusy={bookmarks.busy}
          regionEditing={regionEditing} regionBusy={regionBusy} regionDirty={regionDirty} draftSaveStatus={draftSaveStatus}
          releaseCurrentTask={releaseCurrentTask} changeQueueScope={changeQueueScope} />

        <TaskContentContainer managed={managing} open={taskPreviewOpen} onClose={() => changeQueueScope(() => setTaskPreviewOpen(false))}>
        <WorkbenchTaskContent
          context={ctx} loading={contextLoading} error={contextError} hasTasks={tasks.length > 0}
          canManageTasks={managing} onRetry={() => void loadContext(selectedTaskId)}
          selectedGrade={selectedGrade} subjectCode={subjectCode} canAdoptAiScore={canAdoptAiScore}
          regionEditor={regionEditing && ctx ? <RegionScoreEditor context={ctx} currentUserId={currentUserId}
            canGrade={canWork && hasSession && !draftConflict} draft={draft} setDraft={setDraft}
            isCurrentScope={isCurrentScope} onStateChange={updateRegionState}
            registerSubmit={registerRegionSubmit}
            onDraftSaved={(taskId) => {
              queryClient.removeQueries({ queryKey: reviewTaskContextKeys.task(queryScope, taskId) });
              prefetchedTaskRef.current.delete(taskId);
              removeReviewDraftFallback(currentUserId, taskId);
            }}
            onSubmitted={(task) => {
              // 共区内一题成功只更新该题；不跳答卷，也不重读覆盖其它行未保存的分数。
              queryClient.removeQueries({ queryKey: reviewTaskContextKeys.task(queryScope, task.id) });
              prefetchedTaskRef.current.delete(task.id);
              removeReviewDraftFallback(currentUserId, task.id);
              setTasks(current => current.map(item => item.id === task.id ? task : item));
              setCtx(current => current?.task.id === task.id ? { ...current, task, reviewContext: { ...current.reviewContext, task, expected_revision: task.revision } } : current);
              void refreshTaskAggregate();
              setStatisticsRevision(value => value + 1);
            }} /> : undefined}
          answer={{
            preview, previewLoading, viewerMode, scale, autoFit, imageSize, rotation, offset, dragging,
            viewportRef, canViewOriginalImage, canEditDraft, rubricPoints,
            onChangeViewerMode: changeViewerMode, onZoom: zoomViewer, onRotate: rotateViewer,
            onFit: () => applyViewerFit(imageSize, true), onResetView: resetImageView, onPointerDown, onPointerMove, onPointerUp,
            onImageLoad, onKeyDown: onViewerKeyDown, mathSelection: currentMathSelection
          }}
          evidence={{
            draft, selectedGrade, maxScore, canEditDraft, canVerifyEvidence, actioning, math,
            onRefreshMath: refreshMath, onSelectMathStep: selectMathEvidenceStep,
            canRequestMath: legacyMath && canGrade && canEditDraft && ctx?.answerScope !== "grading_group" && !requiresExplicitSecondOpinion(ctx!.reviewContext),
            mathDisabledReason: ctx?.answerScope === "grading_group" ? "整区作答图尚无本题坐标，请人工阅卷。" : mathRequestDisabledReason(canGrade, canEditDraft),
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
            draft, setDraft: scoreShortcuts.updateDraft, maxScore, rubricPoints,
            selectedGrade, canEditDraft, canReturn, actioning: advancing ? "submit" : actioning,
            bookmarkMarked: bookmarks.state.isSuccess && !bookmarks.busy && !bookmarks.error && !bookmarks.uncertainTask ? bookmarks.state.data?.marked : undefined,
            onAdoptAiScore: () => void adoptAiScore(), canAdoptAiScore, onMarkDispute: markDispute
          }}
        />
        </TaskContentContainer>
      </section> : null}
      {canWork ? <ReviewBookmarksDrawer scope={queryScope} controller={bookmarks} open={bookmarksOpen} initialExamId={initialExamId} onClose={() => setBookmarksOpen(false)} onResumeTask={!managing ? taskId => changeQueueScope(() => {
        // 标记只提供导航，任务上下文仍会重读权限、状态和租约，不能沿用只读材料直接交分。
        if (!isCurrentScope()) return;
        setBookmarksOpen(false); setTaskDrawerOpen(false); setTaskPreviewOpen(false); setSelectedTaskId(taskId);
      }) : undefined} /> : null}
      <Drawer className="grading-reference-drawer" title="题目与评分细则" open={Boolean(referenceTab && ctx)} width="min(1100px, 94vw)" onClose={() => setReferenceTab(null)} destroyOnHidden>{referenceTab && ctx ? <FrozenQuestionReference context={ctx} activeTab={referenceTab} onTabChange={setReferenceTab} /> : null}</Drawer>
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
        rubricPoints={rubricPoints}
        calibrationQuestionDefaults={ctx?.question?.id === calibrationQuestionId ? { questionId: ctx.question.id, archetypeCode: ctx.question.assessment_archetype ?? "", maxScore: ctx.question.score } : undefined}
      />
    </div>
  );
}
