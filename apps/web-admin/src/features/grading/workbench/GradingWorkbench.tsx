import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Modal } from "antd";
import { ApiClientError, getSafeUserText } from "../../../api/client";
import { loadReviewDraftFallback, removeReviewDraftFallback, saveReviewDraftFallback } from "../../../auth/reviewDraftFallback";
import { displayNameOrUsername } from "../../../auth/session";
import {
  assignReviewTask,
  batchAssignReviewTasks,
  claimNextReviewTask,
  listReviewTasks,
  getReviewTask,
  returnReviewTask,
  renewReviewTask,
  releaseReviewTask,
  saveReviewDraft,
  submitHumanGrade,
  verifyEvidence,
  createSubjectiveAiGrade,
  type ReviewTask,
  type RubricSelection
} from "../../../api/review";
import { listManagedUsers, type ManagedUser } from "../../../api/users";
import { EmptyState, ErrorState, LoadingState } from "../../../components/PageState";
import { appQueryClient } from "../../../query/client";
import { reviewTaskContextKeys } from "../../../query/reviewTaskContext";
import { CalibrationDrawer } from "../../calibration";
import {
  GoldPaperManagerDrawer,
  GoldPaperNominationDrawer,
  type GoldPaperNominationCandidate
} from "../../gold-papers";
import {
  createDraftSnapshot,
  createInitialDraft,
  fallbackSnapshot,
  formatError,
  latestGrade,
  sourceDescriptions,
  sourceLabels
} from "./gradingWorkbench.model";
import { loadTaskContext } from "./gradingTaskContext";
import type {
  DraftFallbackSnapshot,
  DraftSaveStatus,
  ReviewerProgress,
  ScoreDraft,
  ScoreDraftChange,
  TaskFilter,
  ViewerMode,
  WorkbenchContext
} from "./gradingWorkbench.types";
import { AnswerEvidencePane } from "./components/AnswerEvidencePane";
import { EvidenceInspector } from "./components/EvidenceInspector";
import { ScoreEditor } from "./components/ScoreEditor";
import { TaskRail } from "./components/TaskRail";
import { WorkbenchHeader } from "./components/WorkbenchHeader";
import { ExamScoringPanel } from "./components/ExamScoringPanel";
import { useGradingKeyboard } from "./hooks/useGradingKeyboard";
import { hashQueryParam } from "../../../router/query";
import { useGradingPrefetch } from "./hooks/useGradingPrefetch";
import { useExamScoring } from "./hooks/useExamScoring";
import { useAnswerViewer } from "./hooks/useAnswerViewer";
import { useMathWorkbenchEvidence } from "./hooks/useMathWorkbenchEvidence";
import { mathEvidenceBinding, mathSuggestionState, selectMathStep, type MathStepSelection } from "./mathWorkbenchEvidence";
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
  const [taskFilter, setTaskFilter] = useState<TaskFilter>("active");
  const [queueScope, setQueueScope] = useState<"mine" | "all">(canManageTasks ? "all" : "mine");
  const [keyword, setKeyword] = useState("");
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [selectedTaskId, setSelectedTaskId] = useState("");
  const requestedTaskRef = useRef(hashQueryParam("task"));
  const actionLock = useRef(false);
  const [loadingTasks, setLoadingTasks] = useState(true);
  const [loadingMoreTasks, setLoadingMoreTasks] = useState(false);
  const [nextTaskCursor, setNextTaskCursor] = useState("");
  const [hasMoreTasks, setHasMoreTasks] = useState(false);
  const [taskError, setTaskError] = useState<string | null>(null);
  const [graders, setGraders] = useState<ManagedUser[]>([]);
  const [gradersError, setGradersError] = useState<string | null>(null);
  const [assignmentUserId, setAssignmentUserId] = useState("");
  const [assignmentTaskIds, setAssignmentTaskIds] = useState<string[]>([]);
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
  const liveDraft = useRef(draft);
  liveDraft.current = draft;
  const [goldPaperManagerOpen, setGoldPaperManagerOpen] = useState(false);
  const [calibrationQuestionId, setCalibrationQuestionId] = useState("");
  const [goldPaperCandidate, setGoldPaperCandidate] = useState<GoldPaperNominationCandidate | null>(null);
  const [online, setOnline] = useState(() => typeof navigator === "undefined" || navigator.onLine);
  const taskListRequestRef = useRef(0);
  const contextRequestRef = useRef(0);
  const suppressAutoSelectRef = useRef(false);
  const [draftRevision, setDraftRevision] = useState(0);
  const [draftSaveStatus, setDraftSaveStatus] = useState<DraftSaveStatus>("idle");
  const [draftHydrated, setDraftHydrated] = useState(false);
  const lastSavedDraft = useRef("");
  const lastScoreDraftChange = useRef<ScoreDraftChange | null>(null);
  const [hasLastScoreDraftChange, setHasLastScoreDraftChange] = useState(false);

  const filteredTasks = useMemo(() => {
    const text = keyword.trim().toLowerCase();
    return tasks.filter((task) => {
      const activeStatuses = canManageTasks ? ["pending", "assigned", "in_progress", "returned"] : ["assigned", "in_progress", "returned"];
      const statusMatched = taskFilter === "all" || (taskFilter === "active" ? activeStatuses.includes(task.status) : task.status === taskFilter);
      const examMatched = !initialExamId || task.exam_id === initialExamId;
      const keywordMatched =
        !text ||
        task.id.toLowerCase().includes(text) ||
        task.anonymous_code.toLowerCase().includes(text) ||
        task.question_no.toLowerCase().includes(text) ||
        task.source.toLowerCase().includes(text);
      return examMatched && statusMatched && keywordMatched;
    });
  }, [canManageTasks, initialExamId, keyword, taskFilter, tasks]);

  const assignableTasks = useMemo(
    () => filteredTasks.filter((task) => !["submitted", "completed", "in_progress"].includes(task.status)),
    [filteredTasks]
  );
  const graderOptions = useMemo(
    () => graders.map((grader) => {
      const name = displayNameOrUsername(grader.display_name, grader.username);
      const username = grader.username.trim();
      return {
        value: grader.id,
        label: !username || name === username ? name : `${name} · ${username}`
      };
    }),
    [graders]
  );
  const graderNames = useMemo(
    () => Object.fromEntries(graders.map((grader) => [grader.id, displayNameOrUsername(grader.display_name, grader.username)])),
    [graders]
  );

  const reviewerProgress = useMemo<ReviewerProgress[]>(() => {
    const names = new Map<string, string>();
    if (canManageTasks) {
      graders.forEach((grader) => names.set(grader.id, displayNameOrUsername(grader.display_name, grader.username)));
    } else {
      names.set(currentUserId, "我的阅卷");
    }

    const totals = new Map<string, { total: number; completed: number; active: number }>();
    names.forEach((_name, id) => {
      totals.set(id, { total: 0, completed: 0, active: 0 });
    });
    tasks.forEach((task) => {
      const reviewerId = task.assigned_to;
      if (!reviewerId) return;
      if (!names.has(reviewerId)) names.set(reviewerId, "未知阅卷员");
      const current = totals.get(reviewerId) ?? { total: 0, completed: 0, active: 0 };
      current.total += 1;
      if (["submitted", "completed"].includes(task.status)) {
        current.completed += 1;
      } else {
        current.active += 1;
      }
      totals.set(reviewerId, current);
    });

    return Array.from(totals.entries())
      .map(([id, counts]) => ({
        id,
        name: names.get(id) ?? "阅卷员",
        ...counts,
        percent: counts.total > 0 ? Math.round((counts.completed / counts.total) * 100) : 0
      }))
      .sort((left, right) => right.total - left.total || left.name.localeCompare(right.name));
  }, [canManageTasks, currentUserId, graders, tasks]);

  const myProgress = useMemo(
    () => reviewerProgress.find((reviewer) => reviewer.id === currentUserId),
    [currentUserId, reviewerProgress]
  );
  const remainingCount = useMemo(
    () => filteredTasks.filter((task) => !["submitted", "completed"].includes(task.status)).length,
    [filteredTasks]
  );

  useEffect(() => {
    if (loadingTasks || filteredTasks.some((task) => task.id === selectedTaskId)) {
      return;
    }
    if (!selectedTaskId && suppressAutoSelectRef.current) {
      return;
    }
    setSelectedTaskId(filteredTasks[0]?.id ?? "");
  }, [filteredTasks, loadingTasks, selectedTaskId]);

  useEffect(() => {
    const available = new Set(assignableTasks.map((task) => task.id));
    setAssignmentTaskIds((current) => current.filter((taskId) => available.has(taskId)));
  }, [assignableTasks]);

  const selectedIndex = useMemo(() => filteredTasks.findIndex((task) => task.id === selectedTaskId), [filteredTasks, selectedTaskId]);
  const prefetchTasks = useMemo(() => {
    const actionable = (task: ReviewTask) => task.id !== selectedTaskId && !["submitted", "completed"].includes(task.status);
    const ordered = [...filteredTasks.slice(selectedIndex + 1), ...filteredTasks.slice(0, Math.max(selectedIndex, 0))]
      .filter(actionable);
    return ordered.slice(0, 2);
  }, [filteredTasks, selectedIndex, selectedTaskId]);
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
  const nextTask = useMemo(() => {
    const actionable = (task: ReviewTask) => task.id !== selectedTaskId && !["submitted", "completed"].includes(task.status);
    return filteredTasks.slice(selectedIndex + 1).find(actionable) ?? filteredTasks.find(actionable);
  }, [filteredTasks, selectedIndex, selectedTaskId]);
  const selectedGrade = useMemo(() => (subjectCode === "mathematics" ? latestGrade((ctx?.aiGrades ?? []).filter((grade) => mathSuggestionState(grade, math.state, math.dirty).current)) : undefined)
    ?? latestGrade(ctx?.aiGrades ?? []), [ctx?.aiGrades, math.dirty, math.state, subjectCode]);
  const canAdoptAiScore = Boolean(selectedGrade && selectedGrade.delivery_mode !== "shadow_only" && !mathRequesting
    && !requiresExplicitSecondOpinion(ctx!.reviewContext) && (subjectCode !== "mathematics" || mathSuggestionState(selectedGrade, math.state, math.dirty).current));
  const currentMathSelection = mathSelection && !math.dirty && !["conflict", "loading", "unavailable"].includes(math.state.phase)
    && math.state.understanding && mathSelection.binding === mathEvidenceBinding(math.state.understanding) ? mathSelection : null;
  useEffect(() => { setMathRequestNotice(""); setMathSelection(null); setMathRequesting(false); }, [selectedTaskId]);
  const maxScore = ctx?.question?.score ?? selectedGrade?.max_score ?? 0;
  const rubricPoints = ctx?.question?.rubric?.points ?? [];
  const ownsSelectedTask = Boolean(ctx?.task.assigned_to && ctx.task.assigned_to === currentUserId);
  const canEditDraft = canWork && hasSession && ownsSelectedTask && Boolean(ctx && ["assigned", "in_progress", "returned"].includes(ctx.task.status));
  const canSubmit = canEditDraft && !actioning && draft.score !== null;
  const canUndoScoreChange = hasLastScoreDraftChange;
  const loadTasks = useCallback(async () => {
    const requestId = ++taskListRequestRef.current;
    setLoadingTasks(true);
    setTaskError(null);
    try {
      const teacherScope = personalScope ? { assigned_to: currentUserId } : {};
      const personalQueue = personalScope || (canManageTasks && canWork && queueScope === "mine");
      const result = await listReviewTasks({
        ...(personalQueue ? { assigned_to: currentUserId } : teacherScope),
        ...(initialExamId ? { exam_id: initialExamId } : {}),
        limit: 50
      });
      if (requestId !== taskListRequestRef.current) return;
      const scopedTasks = initialExamId ? result.tasks.filter((task) => task.exam_id === initialExamId) : result.tasks;
      const requestedId = requestedTaskRef.current;
      if (requestedId && !scopedTasks.some((task) => task.id === requestedId)) {
        const { task } = await getReviewTask(requestedId);
        if (requestId !== taskListRequestRef.current) return;
        if ((initialExamId && task.exam_id !== initialExamId) || (personalQueue && task.assigned_to !== currentUserId)) throw new Error("此任务不在当前考试或已转派，请返回我的工作查看最新任务");
        scopedTasks.unshift(task);
      }
      if (requestedId) { setSelectedTaskId(requestedId); setTaskFilter("all"); requestedTaskRef.current = ""; }
      setTasks(scopedTasks);
      setNextTaskCursor(result.next_cursor ?? "");
      setHasMoreTasks(Boolean(result.has_more));
      setSelectedTaskId((current) => requestedId || (scopedTasks.some((task) => task.id === current)
        ? current
        : scopedTasks.find((task) => ["assigned", "in_progress", "returned"].includes(task.status))?.id || ""));
    } catch (currentError) {
      if (requestId !== taskListRequestRef.current) return;
      setTasks([]);
      setNextTaskCursor("");
      setHasMoreTasks(false);
      setSelectedTaskId("");
      setTaskError(formatError(currentError));
    } finally {
      if (requestId === taskListRequestRef.current) setLoadingTasks(false);
    }
  }, [canManageTasks, canWork, currentUserId, initialExamId, personalScope, queueScope]);

  const examScoring = useExamScoring({ initialExamId, currentUserId, currentTenantId, canGrade, onTasksChanged: loadTasks });

  const loadMoreTasks = useCallback(async () => {
    if (!hasMoreTasks || !nextTaskCursor || loadingMoreTasks) return;
    setLoadingMoreTasks(true);
    try {
      const personalQueue = personalScope || (canManageTasks && canWork && queueScope === "mine");
      const result = await listReviewTasks({
        ...(personalQueue ? { assigned_to: currentUserId } : {}),
        ...(initialExamId ? { exam_id: initialExamId } : {}),
        limit: 50,
        cursor: nextTaskCursor
      });
      setTasks((current) => {
        const known = new Set(current.map((task) => task.id));
        return [...current, ...result.tasks.filter((task) => !known.has(task.id))];
      });
      setNextTaskCursor(result.next_cursor ?? "");
      setHasMoreTasks(Boolean(result.has_more));
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setLoadingMoreTasks(false);
    }
  }, [canManageTasks, canWork, currentUserId, hasMoreTasks, initialExamId, loadingMoreTasks, message, nextTaskCursor, personalScope, queueScope]);

  const loadGraders = useCallback(async () => {
    if (!canManageTasks) {
      setGraders([]);
      setGradersError(null);
      return;
    }
    try {
      const result = await listManagedUsers({ limit: 200 });
      const available = result.users.filter((user) => user.status === "active" && user.roles.includes("grader"));
      setGraders(available);
      setGradersError(available.length ? null : "当前没有可分配的有效阅卷员账号");
      setAssignmentUserId((current) => available.some((user) => user.id === current) ? current : "");
    } catch (currentError) {
      setGraders([]);
      setAssignmentUserId("");
      setGradersError(formatError(currentError));
    }
  }, [canManageTasks]);

  const loadContext = useCallback(async (taskId: string) => {
    const requestId = ++contextRequestRef.current;
    if (!taskId || !hasSession) {
      invalidateViewerContent();
      setCtx(null);
      setContextError(null);
      setContextLoading(false);
      setDraft(createInitialDraft(null));
      lastScoreDraftChange.current = null;
      setHasLastScoreDraftChange(false);
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
      const prefetched = prefetchedTaskRef.current.get(taskId);
      prefetchedTaskRef.current.delete(taskId);
      const next = prefetched
        ? (await prefetched).context
        : await loadTaskContext(taskId, canViewOriginalImage);
      if (initialExamId && next.task.exam_id !== initialExamId) {
        setSelectedTaskId("");
        setContextError("该阅卷任务不属于当前考试，已停止加载。");
        return;
      }
      const reviewerOwnsTask = next.task.assigned_to === currentUserId;
      // Blind quality samples use the ordinary workbench but deliberately do
      // not participate in the review task's draft or lease endpoints.
      const mayPersistDraft = reviewerOwnsTask && next.reviewContext.claim.can_renew;
      const draftResult = { draft: reviewerOwnsTask ? next.reviewContext.draft : null };
      if (requestId !== contextRequestRef.current) return;
      const initial = createInitialDraft(next);
      let restored = initial;
      let revision = 0;
      let restoredViewer: DraftFallbackSnapshot["viewer"] = { mode: "segment", scale: 1, rotation: 0, offset: { x: 0, y: 0 }, fit: true };
      let serverUpdatedAt = 0;
      if (draftResult.draft) {
        restored = {
          ...initial,
          score: draftResult.draft.score ?? null,
          comments: draftResult.draft.comments,
          privateNote: draftResult.draft.private_note,
          studentFeedback: draftResult.draft.student_feedback,
          rubricSelections: Object.fromEntries(draftResult.draft.rubric_selections.map((item) => [item.point_id, item.score]))
        };
        revision = draftResult.draft.revision;
        const viewer = draftResult.draft.viewer_state;
        const savedMode = ["segment", "original", "ocr"].includes(String(viewer.mode)) ? viewer.mode as ViewerMode : "segment";
        const mode = savedMode === "original" && !canViewOriginalImage ? "segment" : savedMode;
        const scale = Number(viewer.scale ?? 1);
        const rotation = Number(viewer.rotation ?? 0);
        const savedOffset = viewer.offset as { x?: number; y?: number } | undefined;
        restoredViewer = { mode, scale, rotation, offset: { x: Number(savedOffset?.x ?? 0), y: Number(savedOffset?.y ?? 0) }, fit: viewer.fit !== false };
        serverUpdatedAt = Date.parse(draftResult.draft.updated_at) || 0;
      }
      const serverSnapshot = createDraftSnapshot(restored, restoredViewer.mode, restoredViewer.scale, restoredViewer.rotation, restoredViewer.offset, restoredViewer.fit);
      const localDraft = mayPersistDraft ? loadReviewDraftFallback<unknown>(currentUserId, taskId) : null;
      const localSnapshot = localDraft ? fallbackSnapshot(localDraft.snapshot, initial) : null;
      const useLocalDraft = Boolean(localDraft && localSnapshot && localDraft.updatedAt > serverUpdatedAt);
      if (useLocalDraft && localSnapshot) {
        restored = localSnapshot.draft;
        restoredViewer = localSnapshot.viewer.mode === "original" && !canViewOriginalImage
          ? { ...localSnapshot.viewer, mode: "segment" }
          : localSnapshot.viewer;
        setDraftSaveStatus("offline");
      } else {
        if (localDraft) removeReviewDraftFallback(currentUserId, taskId);
        setDraftSaveStatus(mayPersistDraft ? (draftResult.draft ? "saved" : "idle") : "readonly");
      }
      setDraft(restored);
      lastScoreDraftChange.current = null;
      setHasLastScoreDraftChange(false);
      setDraftRevision(revision);
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
    const timer = window.setTimeout(async () => {
      if (!online) {
        setDraftSaveStatus("offline");
        return;
      }
      setDraftSaveStatus("saving");
      try {
        const selections = Object.entries(draft.rubricSelections).filter(([, value]) => Number(value) > 0).map(([point_id, value]) => ({ point_id, score: Number(value) }));
        const result = await saveReviewDraft(ctx.task.id, {
          score: draft.score,
          rubric_selections: selections,
          comments: draft.comments,
          private_note: draft.privateNote,
          student_feedback: draft.studentFeedback,
          viewer_state: { mode: viewerMode, scale, rotation, offset, fit: autoFit },
          expected_revision: draftRevision,
          client_updated_at: new Date().toISOString()
        });
        setDraftRevision(result.draft.revision);
        lastSavedDraft.current = snapshot;
        removeReviewDraftFallback(currentUserId, ctx.task.id);
        setDraftSaveStatus("saved");
      } catch (currentError) {
        setDraftSaveStatus(currentError instanceof ApiClientError && currentError.status === 409 ? "conflict" : (!navigator.onLine ? "offline" : "error"));
      }
    }, 1200);
    return () => window.clearTimeout(timer);
  }, [autoFit, ctx, currentUserId, draft, draftHydrated, draftRevision, offset, online, rotation, scale, viewerMode]);

  useEffect(() => {
    suppressAutoSelectRef.current = false;
    taskListRequestRef.current += 1;
    contextRequestRef.current += 1;
    prefetchedTaskRef.current.clear();
    prefetchedPreviewRef.current.clear();
    setTasks([]);
    setNextTaskCursor("");
    setHasMoreTasks(false);
    setSelectedTaskId("");
    setAssignmentTaskIds([]);
    setCtx(null);
    setContextError(null);
    setContextLoading(false);
    prepareViewerContext();
    setDraft(createInitialDraft(null));
    setDraftHydrated(false);
    setDraftSaveStatus("idle");
    lastSavedDraft.current = "";
  }, [currentUserId, initialExamId, prepareViewerContext]);

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

  const assignSelectedTasks = async () => {
    const available = new Set(assignableTasks.map((task) => task.id));
    const taskIds = assignmentTaskIds.filter((taskId) => available.has(taskId));
    const selectedTasks = assignableTasks.filter((task) => taskIds.includes(task.id));
    if (!assignmentUserId || taskIds.length === 0) {
      message.warning("请选择阅卷员和需要分配的任务");
      return;
    }
    setActioning("assign-tasks");
    try {
      const updated = taskIds.length === 1
        ? [(await assignReviewTask(selectedTasks[0].id, assignmentUserId, selectedTasks[0].revision)).task]
        : (await batchAssignReviewTasks(selectedTasks, assignmentUserId)).tasks;
      const updatedById = new Map(updated.map((task) => [task.id, task]));
      setTasks((current) => current.map((task) => updatedById.get(task.id) ?? task));
      setCtx((current) => current && updatedById.has(current.task.id)
        ? { ...current, task: updatedById.get(current.task.id)! }
        : current);
      setAssignmentTaskIds([]);
      message.success(`已将 ${updated.length} 份任务分配给 ${graderNames[assignmentUserId] ?? "阅卷员"}`);
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(null);
    }
  };

  const claimTask = async () => {
    const target = ctx?.task ?? filteredTasks.find((task) => ["assigned", "returned"].includes(task.status));
    setActioning("claim-task");
    try {
      const result = await claimNextReviewTask(initialExamId || target?.exam_id || "", target?.question_id || "");
      setTasks((current) => current.some((task) => task.id === result.task.id)
        ? current.map((task) => task.id === result.task.id ? result.task : task)
        : [result.task, ...current]);
      setSelectedTaskId(result.task.id);
    } catch (currentError) {
      if (currentError instanceof ApiClientError && currentError.code === "grader_qualification_required" && target?.question_id) {
        setCalibrationQuestionId(target.question_id);
        message.warning("该题属于高风险阅卷，请先完成本题校准");
      } else {
        message.error(formatError(currentError));
      }
    } finally {
      setActioning(null);
    }
  };

  const goNext = async () => {
    if (nextTask) {
      setSelectedTaskId(nextTask.id);
      return;
    }
    if (!canManageTasks) {
      setSelectedTaskId("");
      message.info("当前没有更多已分配给你的阅卷任务");
      return;
    }
    setSelectedTaskId("");
    setTaskFilter("pending");
    message.info("没有更多已分配任务；请先把待分配任务交给阅卷员");
  };

  const releaseCurrentTask = async () => {
    if (!selectedTaskId || !ownsSelectedTask) return;
    await runAction("release", async () => {
      const result = await releaseReviewTask(selectedTaskId);
      setTasks((current) => current.map((item) => item.id === result.task.id ? result.task : item));
      suppressAutoSelectRef.current = true;
      setSelectedTaskId("");
    }, "已放回队列，草稿已保留");
  };

  const submitGrade = async (nominateAsGold = false) => {
    if (!ctx) {
      message.error("请先选择任务");
      return;
    }
    if (!canSubmit || draft.score === null) { message.error("请先填写最终分，0分也需要明确输入"); return; }
    if (rubricPoints.length && Math.abs(draft.score - rubricPoints.reduce((sum, point) => sum + (draft.rubricSelections[point.id] ?? 0), 0)) > 0.001 && (!draft.reason.trim() || draft.reason === "教师复核完成")) {
      message.error("最终分与评分细则合计不同，请填写人工调整原因"); return;
    }
    const score = Number(draft.score);
    if (!Number.isFinite(score) || score < 0 || score > maxScore) {
      message.error("最终分必须在 0 到题目满分之间");
      return;
    }
    await runAction(
      "submit",
      async () => {
        const submittedTaskId = ctx.task.id;
        const nextTaskId = nextTask?.id ?? "";
        const selections: RubricSelection[] = Object.entries(draft.rubricSelections)
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
        if (nominateAsGold) {
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
        removeReviewDraftFallback(currentUserId, submittedTaskId);
        appQueryClient.removeQueries({ queryKey: reviewTaskContextKeys.detail(submittedTaskId) });
        contextRequestRef.current += 1;
        invalidateViewerContent();
        setTasks((current) => current.map((task) => task.id === submittedTaskId ? { ...task, status: "submitted" } : task));
        setCtx(null);
        setSelectedTaskId(nextTaskId);
        setDraft(createInitialDraft(null));
        lastScoreDraftChange.current = null;
        setHasLastScoreDraftChange(false);
        setDraftHydrated(false);
        setDraftSaveStatus("idle");
        lastSavedDraft.current = "";
        await Promise.all([loadTasks(), examScoring.loadSummary()]);
        if (!nextTaskId && !canManageTasks) {
          message.info("当前没有更多已分配给你的阅卷任务");
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
      message.warning("当前任务没有 AI 建议分");
      return;
    }
    if (selectedGrade.delivery_mode === "shadow_only") {
      message.warning("本场考试未开放 AI 建议，无法采纳");
      return;
    }
    if (!canEditDraft || !canAdoptAiScore) {
      message.warning("当前建议未通过版本核对，请按原图人工判定或刷新证据。");
      return;
    }
    if (subjectCode === "mathematics") {
      if (mathActionLock.current) return;
      const taskId = selectedTaskId;
      const before = JSON.stringify({ score: draft.score, selections: draft.rubricSelections });
      mathActionLock.current = true;
      setMathRequesting(true);
      try {
        const evidence = await math.refresh();
        if (activeTask.current !== taskId) return;
        if (!evidence || !mathSuggestionState(selectedGrade, evidence, math.dirty).current) {
          message.warning("数学证据或图片已更新，旧建议不可采纳。");
          return;
        }
        if (before !== JSON.stringify({ score: liveDraft.current.score, selections: liveDraft.current.rubricSelections })) {
          message.warning("评分草稿已变化，请再次确认后采纳。");
          return;
        }
      } finally {
        mathActionLock.current = false;
        if (activeTask.current === taskId) setMathRequesting(false);
      }
    }
    lastScoreDraftChange.current = {
      score: draft.score,
      rubricSelections: { ...draft.rubricSelections }
    };
    setHasLastScoreDraftChange(true);
    setDraft((current) => ({
      ...current,
      score: selectedGrade.suggested_score,
      studentFeedback: current.studentFeedback || selectedGrade.student_feedback || "",
      privateNote: current.privateNote || selectedGrade.teacher_note || ""
    }));
  };

  const refreshMath = () => {
    if (math.dirty) {
      Modal.confirm({ title: "刷新并放弃未保存的数学校正？", content: "教师评分草稿不会被重置。", onOk: async () => { math.setDirty(false); await math.refresh(); } });
    } else void math.refresh();
  };

  const selectMathEvidenceStep = (stepId: string) => {
    if (!math.state.understanding || math.dirty || ["loading", "conflict", "unavailable"].includes(math.state.phase)) return;
    const selection = selectMathStep(math.state.understanding, stepId);
    if (!selection) { message.info("该步骤暂无可靠位置，请按原图核对。"); return; }
    setMathSelection(selection);
    if (viewerMode !== "segment") changeViewerMode("segment");
  };

  const requestMathSuggestion = async () => {
    if (!ctx || !canGrade || !canEditDraft || requiresExplicitSecondOpinion(ctx.reviewContext) || math.dirty || math.state.phase !== "ready" || mathActionLock.current) return;
    const taskId = ctx.task.id, segmentId = ctx.task.answer_segment_id;
    mathActionLock.current = true;
    setMathRequesting(true);
    setMathRequestNotice("");
    try {
      const evidence = await math.refresh();
      if (activeTask.current !== taskId || evidence?.phase !== "ready") return;
      const result = await createSubjectiveAiGrade(segmentId);
      if (activeTask.current !== taskId) return;
      const latest = await math.refresh();
      if (activeTask.current !== taskId) return;
      setCtx((current) => current?.task.id === taskId ? { ...current, aiGrades: [result.grade, ...current.aiGrades.filter((grade) => grade.id !== result.grade.id)] } : current);
      setMathRequestNotice(latest && mathSuggestionState(result.grade, latest).current ? "新版本数学建议已生成，仍需教师确认。" : "未生成可采纳的当前数学 v2 建议，请继续人工阅卷。");
      void appQueryClient.invalidateQueries({ queryKey: reviewTaskContextKeys.detail(taskId) });
    } catch (cause) {
      if (activeTask.current !== taskId) return;
      setMathRequestNotice(cause instanceof ApiClientError && cause.code === "math_human_review_required" ? "仍有未决评分点或风险，已转人工确认；未生成成功建议。" : cause instanceof ApiClientError && cause.status === 409 ? "数学证据已变化，本次建议未落库，请刷新后核对。" : "数学建议服务暂不可用或未开放，请继续人工阅卷。");
      await math.refresh();
    } finally {
      mathActionLock.current = false;
      if (activeTask.current === taskId) setMathRequesting(false);
    }
  };

  const setScoreFromShortcut = (score: number) => {
    lastScoreDraftChange.current = {
      score: draft.score,
      rubricSelections: { ...draft.rubricSelections }
    };
    setHasLastScoreDraftChange(true);
    setDraft((current) => ({ ...current, score }));
    message.info(`已打分 ${score} 分`);
  };

  const toggleCriterionFromShortcut = (index: number) => {
    const point = rubricPoints[index];
    if (!point) return;
    lastScoreDraftChange.current = {
      score: draft.score,
      rubricSelections: { ...draft.rubricSelections }
    };
    setHasLastScoreDraftChange(true);
    setDraft((current) => {
      const nextScore = (current.rubricSelections[point.id] ?? 0) > 0 ? 0 : point.score;
      const rubricSelections = { ...current.rubricSelections, [point.id]: nextScore };
      const score = rubricPoints.reduce((total, item) => total + (rubricSelections[item.id] ?? 0), 0);
      return { ...current, rubricSelections, score };
    });
    message.info(`已${(draft.rubricSelections[point.id] ?? 0) > 0 ? "取消" : "计入"}评分点 ${index + 1}`);
  };

  const undoLastScoreDraftChange = () => {
    const previous = lastScoreDraftChange.current;
    if (!previous) return;
    setDraft((current) => ({ ...current, ...previous }));
    lastScoreDraftChange.current = null;
    setHasLastScoreDraftChange(false);
    message.info("已撤销本次本地评分修改");
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
    onSetScore: setScoreFromShortcut,
    onToggleCriterion: toggleCriterionFromShortcut,
    onAdoptAi: () => void adoptAiScore(),
    onFlagException: () => confirmReturnTask(true),
    onReturnTask: () => confirmReturnTask(false),
    onUndoScoreChange: undoLastScoreDraftChange,
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
        draftSaveStatus={draftSaveStatus}
        loading={loadingTasks || contextLoading}
        actioning={actioning}
        onRefresh={refreshCurrent}
        onNext={goNext}
        onClaim={claimTask}
        onRelease={releaseCurrentTask}
        onReloadConflict={() => loadContext(selectedTaskId)}
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
      <GoldPaperManagerDrawer open={goldPaperManagerOpen} examId={initialExamId || undefined} onClose={() => setGoldPaperManagerOpen(false)} />
      <CalibrationDrawer
        open={Boolean(calibrationQuestionId)}
        examId={initialExamId || ctx?.task.exam_id || ""}
        questionId={calibrationQuestionId}
        graderId={currentUserId}
        canManagePolicy={canManageTasks}
        onClose={() => setCalibrationQuestionId("")}
        onQualified={() => { message.success("校准已通过，可以重新领取本题任务"); void loadTasks(); }}
      />
      <GoldPaperNominationDrawer candidate={goldPaperCandidate} onClose={() => setGoldPaperCandidate(null)} onCreated={() => setGoldPaperManagerOpen(true)} />
    </div>
  );
}
