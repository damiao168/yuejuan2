import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { App, Button, Drawer } from "antd";
import { BookmarkCheck, LogOut, PanelLeft } from "lucide-react";
import { BackmarkQueue } from "../../../components/BackmarkQueue";
import { RegradeQueue } from "../../../components/RegradeQueue";
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
import { groupTaskRefs, RegionScoreEditor } from "./components/RegionScoreEditor";
import { TaskRail } from "./components/TaskRail";
import { WorkbenchHeader } from "./components/WorkbenchHeader";
import { WorkbenchOverlays } from "./components/WorkbenchOverlays";
import { ExamScoringPanel } from "./components/ExamScoringPanel";
import { GradingManagementNav, type GradingManagementView } from "./components/GradingManagementNav";
import { ReviewerProgressView } from "./components/ReviewerProgressView";
import { useGradingKeyboard } from "./hooks/useGradingKeyboard";
import { useGradingPrefetch } from "./hooks/useGradingPrefetch";
import { useExamScoring } from "./hooks/useExamScoring";
import type { ScoringReviewFilter } from "./scoringReviewCategories";
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
import { isChoiceReview, requiresExplicitSecondOpinion } from "./reviewContext";
import "./grading-management.css";
import { useUnsavedChangesGuard } from "../../../router/useUnsavedChangesGuard";
import { AssignmentPreview } from "./components/AssignmentPreview";
import type { ReviewTask } from "../../../api/review";
import { usePersonalReviewBookmarks } from "./hooks/usePersonalReviewBookmarks";
import { ReviewBookmarkControls, ReviewBookmarksDrawer } from "./components/ReviewBookmarks";

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

function TaskRailContainer({ managed, open, onClose, children }: { managed: boolean; open: boolean; onClose: () => void; children: ReactNode }) {
  if (managed) return <>{children}</>;
  return <Drawer title="阅卷任务" open={open} width={420} onClose={onClose} destroyOnHidden={false} className="grading-task-drawer" styles={{ body: { padding: 0 } }}>{children}</Drawer>;
}

function TaskContentContainer({ managed, open, onClose, children }: { managed: boolean; open: boolean; onClose: () => void; children: ReactNode }) {
  if (!managed) return <>{children}</>;
  return <Drawer title="答卷详情" open={open} width="min(1440px, 96vw)" onClose={onClose} destroyOnHidden className="grading-inspection-drawer">{open ? children : null}</Drawer>;
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
  const heldGroupTask = useRef("");
  const queue = useGradingQueue({ canManageTasks, canWork, currentUserId, initialExamId, personalScope, onInfo: reportNoNextTask, heldGroupTask });
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
  const [taskDrawerOpen, setTaskDrawerOpen] = useState(false);
  // 未指定考试或没有评分权限时直接显示任务列表；可管理本场评分时先看概览。
  const [managementView, setManagementView] = useState<GradingManagementView>(initialExamId && canGrade ? "overview" : "assignment");
  // 换考试或账户后回到可用入口，不沿用上一场展开的结果和设置视图。
  useEffect(() => { setManagementView(initialExamId && canGrade ? "overview" : "assignment"); }, [initialExamId, canGrade, currentUserId, currentTenantId]);
  const [taskPreviewOpen, setTaskPreviewOpen] = useState(false);
  const [bookmarksOpen, setBookmarksOpen] = useState(false);
  const bookmarks = usePersonalReviewBookmarks(queryScope, canWork ? selectedTaskId : "");
  const [draft, setDraft] = useState<ScoreDraft>(() => createInitialDraft(null));
  const [regionDirty, setRegionDirty] = useState(false);
  const [regionBusy, setRegionBusy] = useState(false);
  const regionEditing = Boolean(ctx?.gradingGroupImageUrl && ctx.gradingGroupId && groupTaskRefs(ctx).length >= 2);
  heldGroupTask.current = regionEditing ? ctx!.task.id : "";
  const updateRegionState = useCallback((dirty: boolean, busy: boolean) => { setRegionDirty(dirty); setRegionBusy(busy); }, []);
  const [quickSubmit, setQuickSubmit] = useState(false);
  const [aiSuggestionEntry, setAISuggestionEntry] = useState<HTMLDivElement | null>(null);
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
  // 整区成员没有单题 ROI，不能把共享裁图当作本题数学步骤和批注坐标的来源。
  const legacyMath = Boolean(ctx && !isChoiceReview(ctx.reviewContext) && !ctx.aiGrades.some(grade => grade.raw_output?.schema_version === "grading-image-v3"));
  const math = useMathWorkbenchEvidence(ctx?.task.answer_segment_id ?? "", ctx?.answerScope === "grading_group" || !legacyMath ? "" : subjectCode, ctx?.reviewContext.question_snapshot.id ?? "");
  const { prefetchedTaskRef, prefetchedPreviewRef } = useGradingPrefetch({
    queryScope,
    canWork: canWork && (!canManageTasks || managementView === "work" || taskPreviewOpen),
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
  const selectedGrade = useMemo(() => selectWorkbenchGrade(ctx?.aiGrades ?? [], subjectCode, math.state, math.dirty, ctx), [ctx, math.dirty, math.state, subjectCode]);
  const canAdoptAiScore = canAdoptWorkbenchGrade(selectedGrade, ctx, mathRequesting, subjectCode, math.state, math.dirty);
  const currentMathSelection = visibleMathSelection(mathSelection, math.state, math.dirty);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切题必须清除数学建议请求提示及选择；不能将上一题的忙碌状态沿用到新题。
  useEffect(() => { setMathRequestNotice(""); setMathSelection(null); setMathRequesting(false); }, [selectedTaskId]);
  const maxScore = ctx?.question?.score ?? selectedGrade?.max_score ?? 0;
  const rubricPoints = ctx?.question?.rubric?.points ?? [];
  const scoreShortcuts = useGradingScoreShortcuts({ draft, setDraft, rubricPoints, onInfo: (text) => message.info(text) });
  const resetScoreShortcuts = scoreShortcuts.reset;
  const { ownsSelectedTask, canEditDraft, unsyncedDraft, canSubmit } = reviewDraftState({
    ctx, currentUserId, canWork, hasSession, resolvingConflict, hasConflict: Boolean(draftConflict),
    hydrated: draftHydrated, saveStatus: draftSaveStatus, actioning,
    snapshot: createDraftSnapshot(draft, viewerMode, scale, rotation, offset, autoFit), lastSavedSnapshot: lastSavedDraft.current
  });
  useUnsavedChangesGuard({ dirty: (regionEditing ? regionDirty : unsyncedDraft) || math.dirty, busy: Boolean(actioning) || bookmarks.busy || regionBusy, description: "当前评分或证据校正尚未保存，离开后只能恢复已成功保存的版本。" });
  const canUndoScoreChange = scoreShortcuts.canUndo;
  const examScoring = useExamScoring({ initialExamId, currentUserId, currentTenantId, canGrade, onTasksChanged: loadTasks });
  // 启动评分后仍进入同一结果视图；明细读取返回不能重新弹出已经离开的管理页。
  useEffect(() => { if (canManageTasks && examScoring.detailOpen) setManagementView("results"); }, [canManageTasks, examScoring.detailOpen]);

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
  }, [canViewOriginalImage, currentUserId, initialExamId, invalidateViewerContent, prepareViewerContext, restoreViewer, queryClient, queryScope, prefetchedTaskRef, resetScoreShortcuts, setSelectedTaskId]);

  useEffect(() => {
    const updateOnlineState = () => setOnline(navigator.onLine);
    window.addEventListener("online", updateOnlineState);
    window.addEventListener("offline", updateOnlineState);
    return () => {
      window.removeEventListener("online", updateOnlineState);
      window.removeEventListener("offline", updateOnlineState);
    };
  }, []);

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
    resetQueue();
    actionGeneration.current += 1;
    actionLock.current = false;
    mathActionLock.current = false;
    setActioning(null);
    setRegionDirty(false); setRegionBusy(false);
    setAssignmentPreview(null);
    setTaskDrawerOpen(false); setTaskPreviewOpen(false); setManagementView(initialExamId && canGrade ? "overview" : "assignment"); setBookmarksOpen(false);
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
  }, [canGrade, currentUserId, currentTenantId, initialExamId, queryScope, prepareViewerContext, resetQueue, prefetchedTaskRef, prefetchedPreviewRef]);

  useEffect(() => {
    // 同账号解锁会更新会话代次但不改变 loadTasks 的引用；清队列后仍需重新读取。
    if (!isReviewTaskContextScopeCurrent(queryScope)) return;
    void loadTasks();
  }, [loadTasks, queryScope]);

  useEffect(() => {
    void loadGraders();
  }, [loadGraders]);

  const managing = canManageTasks && managementView !== "work";
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
    ctx, canGrade: canGrade && ctx?.answerScope !== "grading_group", canEditDraft, viewerMode, math, activeTask, mathActionLock,
    setCtx, setMathRequesting, setMathRequestNotice, setMathSelection, changeViewerMode,
    notifyInfo: (text) => message.info(text)
  });

  useGradingKeyboard({
    quickSubmit,
    hasTask: Boolean(ctx),
    canEditDraft: canEditDraft && !regionEditing,
    canSubmit: canSubmit && !regionEditing,
    canReturn: canReturn && !regionEditing,
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
    if (actioning || regionBusy) { message.info("正在保存、提交或分配，请等待结果后切换。"); return; }
    if (regionEditing && regionDirty) {
      const taskId = selectedTaskId;
      modal.confirm({ title: "本区还有未保存的评分，继续切换？", content: "已经保存或提交的各题结果会保留，尚未保存的评分将丢失。", okText: "放弃未保存内容并切换", cancelText: "继续评分", onOk: () => { if (isCurrentScope() && activeTask.current === taskId) change(); } });
      return;
    }
    if (bookmarks.busy) { message.info("正在保存标记，请稍后切换。"); return; }
    if (!regionEditing && unsyncedDraft && !math.dirty) {
      modal.confirm({ title: "评分草稿尚未同步，继续切换？", content: "未同步内容保留本机兜底；重新进入时仍需核对服务端保存状态。", okText: "继续切换", cancelText: "等待保存", onOk: () => { if (isCurrentScope()) change(); } });
      return;
    }
    if (!math.dirty) { change(); return; }
    const taskId = selectedTaskId;
    modal.confirm({ title: "放弃未保存的证据校正并切换？", content: "当前证据校正尚未保存。评分草稿以服务端或本机兜底保存状态为准。", okText: "放弃并切换", cancelText: "继续校正", onOk: () => { if (isCurrentScope() && activeTask.current === taskId) change(); } });
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
      {canManageTasks ? <GradingManagementNav view={managementView} hasExamOverview={Boolean(initialExamId) && canGrade} canViewResults={Boolean(initialExamId) && canGrade}
        canOpenResults={Boolean(examScoring.summary?.run)} resultsOpen={examScoring.detailOpen} resultsLoading={examScoring.actioning === "scoring-detail"}
        hasOwnWork={canWork && (myProgress?.total ?? 0) > 0}
        canManageSettings={Boolean(initialExamId) && canGrade}
        onViewChange={value => changeQueueScope(() => { examScoring.setDetailOpen(false); examScoring.closeImage(); setTaskPreviewOpen(false); setManagementView(value); setQueueScope(value === "work" ? "mine" : "all"); })}
        onOpenResults={() => openResults()} /> : null}
      {/* 概览与独立结果视图共用控制器，切换管理视图时保留已选筛选。 */}
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
        onOpenAssignments={managing ? () => changeQueueScope(() => { examScoring.setDetailOpen(false); setManagementView("assignment"); setQueueScope("all"); }) : undefined}
        onOpenResults={canManageTasks ? openResults : undefined}
        showResults={managing && managementView === "results"}
        showSettings={managing && managementView === "settings"}
        showOverview={managing && managementView === "overview"}
        showFastConfirmEntry={!managing || managementView === "overview"}
        fastConfirmTriggerTarget={managing ? undefined : aiSuggestionEntry}
      />
      {managing && managementView === "progress" ? <ReviewerProgressView reviewers={reviewerProgress}
        progressComplete={Boolean(taskAggregate) && !loadingTasks && !taskError} loading={loadingTasks} error={taskError} gradersError={gradersError}
        onRefresh={async () => { await Promise.all([loadTasks(), loadGraders()]); }} /> : null}
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
        draftStatusText={regionEditing ? regionBusy ? "正在保存评分" : regionDirty ? "本区有未保存评分" : "评分按题保存" : undefined}
        loading={loadingTasks || contextLoading}
        actioning={actioning}
        onRefresh={async () => changeQueueScope(() => { void refreshCurrent(); })}
        onNext={async () => changeQueueScope(() => { void goNext(); })}
        onClaim={async () => changeQueueScope(() => { void claimTask(); })}
        onRelease={async () => changeQueueScope(() => { void releaseCurrentTask(); })}
        onResolveConflict={openDraftConflict}
        onReloadConflict={() => loadContext(selectedTaskId, true)}
        readonlyLabel={ctx?.task.status === "submitted" || ctx?.task.status === "completed" ? "已提交·只读" : canManageTasks ? "管理员只读" : "任务只读"}
        canSubmit={canSubmit && !regionEditing}
        onSubmit={!regionEditing && !managing && canEditDraft && draftSaveStatus !== "readonly" ? () => void submitGrade() : undefined}
        taskLabel={!managing && ctx ? `${regionEditing ? "填空整区" : ctx.task.question_no || "当前题目"} · ${ctx.task.anonymous_code || "匿名答卷"}` : undefined}
        scoreLabel={!regionEditing && !managing && ctx ? typeof draft.score === "number" ? `${draft.score.toFixed(1)} / ${maxScore}` : "未评分" : undefined}
        personalActions={<>
          {!managing ? <Button icon={<PanelLeft size={16} />} onClick={() => changeQueueScope(() => setTaskDrawerOpen(true))}>任务</Button> : null}
          {canWork ? <ReviewBookmarkControls scope={queryScope} controller={bookmarks} /> : null}
        </>}
      /> : null}

      {bookmarks.error && !bookmarksOpen ? <div className="bookmark-inline-status" role="status">{bookmarks.error}{bookmarks.uncertainTask ? <Button size="small" loading={bookmarks.busy} onClick={() => void bookmarks.retry()}>重试原操作</Button> : null}</div> : null}

      {!managing || managementView === "assignment" ? <section className={`grading-workspace${managing ? " grading-management-workspace" : ""}`}>
        <TaskRailContainer managed={managing} open={taskDrawerOpen} onClose={() => setTaskDrawerOpen(false)}>
        {!managing && canWork ? <div className="grading-task-tools"><Button block icon={<BookmarkCheck size={16} />} onClick={() => changeQueueScope(() => { setTaskDrawerOpen(false); setBookmarksOpen(true); })}>已标记试卷</Button></div> : null}
        <TaskRail
          scope={queue}
          fixedExam={Boolean(initialExamId)}
          onExamChange={(id) => changeQueueScope(() => { queue.setExamFilter(id); queue.setQuestionFilter(""); })}
          onSubjectChange={(subject) => changeQueueScope(() => { queue.setSubjectFilter(subject); queue.setExamFilter(""); queue.setQuestionFilter(""); })}
          onQuestionChange={(id) => changeQueueScope(() => queue.setQuestionFilter(id))}
          onUnassignedChange={(value) => changeQueueScope(() => queue.setUnassignedOnly(value))}
          canManageTasks={managing}
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
            changeQueueScope(() => { pendingNextRef.current = null; suppressAutoSelectRef.current = false; setSelectedTaskId(taskId); setTaskDrawerOpen(false); if (managing) setTaskPreviewOpen(true); });
          }}
          onLoadTasks={loadTasks}
          onLoadMoreTasks={loadMoreTasks}
        />
        {!managing && canWork ? <section className="grading-task-secondary" aria-label="其他评分任务">
          {ownsSelectedTask ? <div><Button icon={<LogOut size={16} />} disabled={Boolean(actioning) || bookmarks.busy || (regionEditing ? regionBusy || regionDirty : !["saved", "idle"].includes(draftSaveStatus))} loading={actioning === "release"} onClick={() => changeQueueScope(() => { void releaseCurrentTask(); })}>放回任务队列</Button><p>先保存草稿，再将当前答卷放回任务列表。</p></div> : null}
          <h3>其他评分任务</h3>
          {/* 质量复评和新版细则重评各有独立候选结果，不能作为个人标记改变普通评分。 */}
          <div><BackmarkQueue canWork={canWork} label="质量检查重评" /><p>管理员安排的独立复评，用于检查评分质量。</p></div>
          <div><RegradeQueue canWork={canWork} label="细则更新重评" /><p>使用更新后的评分细则重新评分。</p></div>
        </section> : null}
        </TaskRailContainer>

        <TaskContentContainer managed={managing} open={taskPreviewOpen} onClose={() => changeQueueScope(() => setTaskPreviewOpen(false))}>
        <WorkbenchTaskContent
          context={ctx} loading={contextLoading} error={contextError} hasTasks={tasks.length > 0}
          canManageTasks={managing} onRetry={() => void loadContext(selectedTaskId)}
          aiSuggestionEntryRef={managing ? undefined : setAISuggestionEntry}
          selectedGrade={selectedGrade} subjectCode={subjectCode} canAdoptAiScore={canAdoptAiScore}
          regionEditor={regionEditing && ctx ? <RegionScoreEditor context={ctx} currentUserId={currentUserId}
            canGrade={canGrade && canWork && hasSession && !draftConflict} draft={draft} setDraft={setDraft}
            isCurrentScope={isCurrentScope} onStateChange={updateRegionState}
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
            }} /> : undefined}
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
            draft, setDraft, quickSubmit, onQuickSubmitChange: setQuickSubmit, maxScore, rubricPoints,
            selectedGrade, canEditDraft, canSubmit, canReturn, canManageTasks, actioning,
            onAdoptAiScore: () => void adoptAiScore(), onAdoptAiFeedback: adoptAiFeedback, canAdoptAiScore, onMarkDispute: markDispute, onSubmit: submitGrade
            , hidePrimarySubmit: !managing
          }}
        />
        </TaskContentContainer>
      </section> : null}
      {canWork ? <ReviewBookmarksDrawer scope={queryScope} controller={bookmarks} open={bookmarksOpen} initialExamId={initialExamId} onClose={() => setBookmarksOpen(false)} /> : null}
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
