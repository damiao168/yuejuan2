import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReviewTask } from "../../../../api/review";
import { listReviewTasks, getReviewTask } from "../../../../api/review";
import { listAllActiveGraders, type ManagedUser } from "../../../../api/users";
import { displayNameOrUsername } from "../../../../auth/session";
import { formatError } from "../gradingWorkbench.model";
import type { ReviewerProgress, TaskFilter } from "../gradingWorkbench.types";
import { appendTaskPage, followingVisibleActionableTask, visibleTasks } from "../taskPaging";
import { hashQueryParam } from "../../../../router/query";
import type { ReviewTaskAggregate } from "@edugrade/sdk";
import { examSubjectLabel } from "../../../../constants/examStatus";

interface UseGradingQueueOptions {
  canManageTasks: boolean;
  canWork: boolean;
  currentUserId: string;
  initialExamId: string;
  personalScope: boolean;
  onInfo: (message: string) => void;
}

// 深链补读的任务也受原考试和个人队列限制；请求代次由调用方在补读返回后核对。
function assertRequestedTaskScope(task: ReviewTask, { canManageTasks, canWork, currentUserId, initialExamId, personalScope }: Omit<UseGradingQueueOptions, "onInfo">, queueScope: "mine" | "all") {
  const personalQueue = personalScope || (canManageTasks && canWork && queueScope === "mine");
  if ((initialExamId && task.exam_id !== initialExamId) || (personalQueue && task.assigned_to !== currentUserId)) {
    throw new Error("此任务不在当前考试或已转派，请返回我的工作查看最新任务");
  }
}

export function useGradingQueue({ canManageTasks, canWork, currentUserId, initialExamId, personalScope, onInfo }: UseGradingQueueOptions) {
  const [taskFilter, setTaskFilter] = useState<TaskFilter>("active");
  const [queueScope, setQueueScope] = useState<"mine" | "all">(canManageTasks ? "all" : "mine");
  const [keyword, setKeyword] = useState(hashQueryParam("q"));
  const [serverKeyword, setServerKeyword] = useState(hashQueryParam("q"));
  const [examFilter, setExamFilter] = useState(initialExamId);
  const [subjectFilter, setSubjectFilter] = useState("");
  const [questionFilter, setQuestionFilter] = useState("");
  const [unassignedOnly, setUnassignedOnly] = useState(false);
  const [taskCatalogue, setTaskCatalogue] = useState<ReviewTask[]>([]);
  const [matchedAggregate, setMatchedAggregate] = useState<ReviewTaskAggregate | null>(null);
  const [unassignedCount, setUnassignedCount] = useState<number | null>(null);
  const [selectingAll, setSelectingAll] = useState(false);
  const selectingAllRef = useRef(false);
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [taskAggregate, setTaskAggregate] = useState<ReviewTaskAggregate | null>(null);
  const [selectedTaskId, setSelectedTaskId] = useState("");
  const requestedTaskRef = useRef(hashQueryParam("task"));
  const [loading, setLoading] = useState({ tasks: true, more: false });
  const loadingTasks = loading.tasks;
  const loadingMoreTasks = loading.more;
  const [nextTaskCursor, setNextTaskCursor] = useState("");
  const [hasMoreTasks, setHasMoreTasks] = useState(false);
  const [pageError, setPageError] = useState<string | null>(null);
  const pendingPageRef = useRef<Promise<ReviewTask[]> | null>(null);
  const pendingNextRef = useRef<string | null>(null);
  const [taskError, setTaskError] = useState<string | null>(null);
  const [graders, setGraders] = useState<ManagedUser[]>([]);
  const [gradersError, setGradersError] = useState<string | null>(null);
  const [assignmentUserId, setAssignmentUserId] = useState("");
  const [assignmentTaskIds, setAssignmentTaskIds] = useState<string[]>([]);
  const taskListRequestRef = useRef(0);
  const allSelectionRequestRef = useRef(0);
  const queueReadyRef = useRef(false);
  const filtersRef = useRef({ taskFilter, queueScope, keyword, examFilter, subjectFilter, questionFilter, unassignedOnly });
  const suppressAutoSelectRef = useRef(false);

  const invalidateFilter = useCallback(() => {
    // 输入先于防抖请求变化；同步作废旧分页和全选，避免它们在新筛选下回填旧范围。
    taskListRequestRef.current += 1;
    allSelectionRequestRef.current += 1;
    queueReadyRef.current = false;
    selectingAllRef.current = false;
    pendingPageRef.current = null;
    pendingNextRef.current = null;
    setSelectingAll(false);
    setAssignmentTaskIds([]);
    setLoading({ tasks: true, more: false });
  }, []);
  const updateFilter = useCallback(<K extends keyof typeof filtersRef.current>(key: K, value: typeof filtersRef.current[K], setValue: (value: typeof filtersRef.current[K]) => void) => {
    const previous = filtersRef.current[key];
    filtersRef.current[key] = value;
    if (key === "keyword" ? String(previous).trim() !== String(value).trim() : previous !== value) invalidateFilter();
    setValue(value);
  }, [invalidateFilter]);
  // 对外筛选 setter 使用包装回调，先同步 ref 并在范围变化时作废旧请求，再更新 state。
  const updateTaskFilter = useCallback((value: TaskFilter) => updateFilter("taskFilter", value, setTaskFilter), [updateFilter]);
  const updateQueueScope = useCallback((value: "mine" | "all") => updateFilter("queueScope", value, setQueueScope), [updateFilter]);
  const updateKeyword = useCallback((value: string) => updateFilter("keyword", value, setKeyword), [updateFilter]);
  const updateExamFilter = useCallback((value: string) => updateFilter("examFilter", value, setExamFilter), [updateFilter]);
  const updateSubjectFilter = useCallback((value: string) => updateFilter("subjectFilter", value, setSubjectFilter), [updateFilter]);
  const updateQuestionFilter = useCallback((value: string) => updateFilter("questionFilter", value, setQuestionFilter), [updateFilter]);
  const updateUnassignedOnly = useCallback((value: boolean) => updateFilter("unassignedOnly", value, setUnassignedOnly), [updateFilter]);

  useEffect(() => { const timer = window.setTimeout(() => setServerKeyword(keyword.trim()), 300); return () => window.clearTimeout(timer); }, [keyword]);
  const effectiveExamId = initialExamId || examFilter;
  const activeScopeKey = JSON.stringify([canManageTasks, canWork, currentUserId, initialExamId, personalScope, queueScope, effectiveExamId, subjectFilter, questionFilter, unassignedOnly, taskFilter, keyword.trim()]);
  const activeScopeKeyRef = useRef(activeScopeKey);
  activeScopeKeyRef.current = activeScopeKey;
  const scopeQuery = useMemo(() => ({
    ...((personalScope || (canManageTasks && canWork && queueScope === "mine")) ? { assigned_to: currentUserId } : {}),
    ...(effectiveExamId ? { exam_id: effectiveExamId } : {}), ...(questionFilter ? { question_id: questionFilter } : {}),
    ...(subjectFilter ? { subject: subjectFilter } : {}),
    ...(serverKeyword ? { q: serverKeyword } : {})
  }), [canManageTasks, canWork, currentUserId, effectiveExamId, personalScope, questionFilter, queueScope, serverKeyword, subjectFilter]);
  const filterQuery = useMemo(() => ({ ...scopeQuery, ...(taskFilter !== "all" ? { status: taskFilter } : {}), ...(unassignedOnly ? { unassigned: true } : {}) }), [scopeQuery, taskFilter, unassignedOnly]);
  // 这里排序的是接口学科代码，保留固定 UTF-16 顺序；中文标签只负责展示，不参与排序。
  const subjectOptions = useMemo(() => [...new Set(taskCatalogue.map((task) => task.subject).filter((subject): subject is string => Boolean(subject)))].sort((left, right) => {
    if (left < right) return -1;
    if (left > right) return 1;
    return 0;
  }).map((value) => ({ value, label: examSubjectLabel(value) })), [taskCatalogue]);
  const examOptions = useMemo(() => [...new Map(taskCatalogue.filter((task) => !subjectFilter || task.subject === subjectFilter).map((task) => {
    const examName = task.exam_name || `考试 ${task.exam_id.slice(0, 8)}`;
    return [task.exam_id, { value: task.exam_id, label: `${examName} · ${task.subject ? examSubjectLabel(task.subject) : "学科未标注"}` }];
  })).values()], [subjectFilter, taskCatalogue]);
  const questionOptions = useMemo(() => [...new Map(taskCatalogue.filter((task) => !effectiveExamId || task.exam_id === effectiveExamId).filter((task) => !subjectFilter || task.subject === subjectFilter).map((task) => {
    const examPrefix = effectiveExamId ? "" : `${task.exam_name || task.exam_id.slice(0, 8)} · `;
    return [task.question_id, { value: task.question_id, label: `${examPrefix}第 ${task.question_no} 题` }];
  })).values()], [effectiveExamId, subjectFilter, taskCatalogue]);

  const filteredTasks = useMemo(() => visibleTasks(tasks, { canManageTasks, initialExamId: effectiveExamId, keyword, taskFilter, questionId: questionFilter, subject: subjectFilter }),
    [canManageTasks, effectiveExamId, keyword, questionFilter, subjectFilter, taskFilter, tasks]);
  const assignableTasks = useMemo(
    () => filteredTasks.filter((task) => !["submitted", "completed", "in_progress", "cancelled"].includes(task.status) && task.source !== "double_mark_required"),
    [filteredTasks]
  );
  const graderOptions = useMemo(
    () => graders.map((grader) => {
      const name = displayNameOrUsername(grader.display_name, grader.username);
      const username = grader.username.trim();
      return { value: grader.id, label: !username || name === username ? name : `${name} · ${username}` };
    }),
    [graders]
  );
  const graderNames = useMemo(
    () => Object.fromEntries(graders.map((grader) => [grader.id, displayNameOrUsername(grader.display_name, grader.username)])),
    [graders]
  );
  const reviewerProgress = useMemo<ReviewerProgress[]>(() => {
    const names = new Map<string, string>();
    if (canManageTasks) graders.forEach((grader) => names.set(grader.id, displayNameOrUsername(grader.display_name, grader.username)));
    else names.set(currentUserId, "我的阅卷");
    const totals = new Map<string, { total: number; completed: number; active: number }>();
    names.forEach((_name, id) => totals.set(id, { total: 0, completed: 0, active: 0 }));
    (taskAggregate?.reviewers ?? []).forEach((item) => {
      const reviewerId = item.reviewer_id;
      if (!names.has(reviewerId)) names.set(reviewerId, "未知阅卷员");
      totals.set(reviewerId, { total: item.total_count, completed: item.completed_count, active: item.remaining_count });
    });
    return Array.from(totals.entries())
      .map(([id, counts]) => ({ id, name: names.get(id) ?? "阅卷员", ...counts, percent: counts.total > 0 ? Math.round((counts.completed / counts.total) * 100) : 0 }))
      .sort((left, right) => right.total - left.total || left.name.localeCompare(right.name));
  }, [canManageTasks, currentUserId, graders, taskAggregate]);
  const myProgress = useMemo(() => reviewerProgress.find((reviewer) => reviewer.id === currentUserId), [currentUserId, reviewerProgress]);
  const remainingCount = taskAggregate?.remaining_count ?? 0;

  useEffect(() => {
    if (loadingTasks || filteredTasks.some((task) => task.id === selectedTaskId)) return;
    if (selectedTaskId && hasMoreTasks && !tasks.some((task) => task.id === selectedTaskId)) return;
    if (!selectedTaskId && suppressAutoSelectRef.current) return;
    setSelectedTaskId(filteredTasks[0]?.id ?? "");
  }, [filteredTasks, hasMoreTasks, loadingTasks, selectedTaskId, tasks]);
  useEffect(() => {
    const available = new Set(assignableTasks.map((task) => task.id));
    setAssignmentTaskIds((current) => current.filter((taskId) => available.has(taskId)));
  }, [assignableTasks]);

  const selectedIndex = useMemo(() => filteredTasks.findIndex((task) => task.id === selectedTaskId), [filteredTasks, selectedTaskId]);
  const prefetchTasks = useMemo(() => {
    const actionable = (task: ReviewTask) => task.id !== selectedTaskId && !["submitted", "completed"].includes(task.status);
    return [...filteredTasks.slice(selectedIndex + 1), ...filteredTasks.slice(0, Math.max(selectedIndex, 0))]
      .filter(actionable).slice(0, 2);
  }, [filteredTasks, selectedIndex, selectedTaskId]);

  const loadTasks = useCallback(async () => {
    if (activeScopeKeyRef.current !== activeScopeKey || filtersRef.current.keyword.trim() !== serverKeyword) return;
    const requestId = ++taskListRequestRef.current;
    queueReadyRef.current = false;
    allSelectionRequestRef.current += 1;
    selectingAllRef.current = false;
    setSelectingAll(false);
    pendingNextRef.current = null;
    pendingPageRef.current = null;
    setLoading((current) => ({ ...current, tasks: true }));
    setTaskAggregate(null);
    setMatchedAggregate(null); setUnassignedCount(null);
    setLoading((current) => ({ ...current, more: false }));
    setTaskError(null);
    setPageError(null);
    try {
      // 列表与匹配数量在服务端使用同一筛选；进度另按同考试/题目的全状态统计，避免“待处理”筛选把完成数清零。
      const [result, progress, unassigned] = await Promise.all([
        listReviewTasks({ ...filterQuery, limit: 50 }),
        listReviewTasks({ ...scopeQuery, limit: 1 }),
        canManageTasks && queueScope === "all" ? listReviewTasks({ ...filterQuery, unassigned: true, limit: 1 }) : Promise.resolve(null)
      ]);
      if (requestId !== taskListRequestRef.current || activeScopeKeyRef.current !== activeScopeKey) return;
      const scopedTasks = initialExamId ? result.tasks.filter((task) => task.exam_id === initialExamId) : result.tasks;
      const requestedId = requestedTaskRef.current;
      if (requestedId && !scopedTasks.some((task) => task.id === requestedId)) {
        const { task } = await getReviewTask(requestedId);
        if (requestId !== taskListRequestRef.current || activeScopeKeyRef.current !== activeScopeKey) return;
        assertRequestedTaskScope(task, { canManageTasks, canWork, currentUserId, initialExamId, personalScope }, queueScope);
        scopedTasks.unshift(task);
      }
      if (requestedId) { setSelectedTaskId(requestedId); filtersRef.current.taskFilter = "all"; setTaskFilter("all"); requestedTaskRef.current = ""; }
      setTasks(scopedTasks);
      setTaskCatalogue((current) => appendTaskPage(current, scopedTasks));
      setMatchedAggregate(result.aggregate ?? null);
      setUnassignedCount(unassigned?.aggregate?.total_count ?? null);
      setTaskAggregate(progress.aggregate ?? null);
      setNextTaskCursor(result.next_cursor ?? "");
      setHasMoreTasks(Boolean(result.has_more));
      setSelectedTaskId((current) => requestedId || ((scopedTasks.some((task) => task.id === current) || (result.has_more && Boolean(current)))
        ? current : scopedTasks.find((task) => ["assigned", "in_progress", "returned"].includes(task.status))?.id || ""));
      queueReadyRef.current = true;
    } catch (currentError) {
      if (requestId !== taskListRequestRef.current || activeScopeKeyRef.current !== activeScopeKey) return;
      setTasks([]); setTaskAggregate(null); setMatchedAggregate(null); setUnassignedCount(null); setNextTaskCursor(""); setHasMoreTasks(false); setSelectedTaskId(""); setTaskError(formatError(currentError));
    } finally {
      if (requestId === taskListRequestRef.current && activeScopeKeyRef.current === activeScopeKey) setLoading((current) => ({ ...current, tasks: false }));
    }
  }, [activeScopeKey, canManageTasks, canWork, currentUserId, initialExamId, personalScope, queueScope, filterQuery, scopeQuery, serverKeyword]);

  const loadMoreTasks = useCallback((): Promise<ReviewTask[]> => {
    if (!queueReadyRef.current || activeScopeKeyRef.current !== activeScopeKey || filtersRef.current.keyword.trim() !== serverKeyword) return Promise.resolve([]);
    // 自动续页和手动翻页共享同一在途请求，避免同一游标并发追加两次。
    if (pendingPageRef.current) return pendingPageRef.current;
    if (!hasMoreTasks) return Promise.resolve([]);
    if (!nextTaskCursor) { setPageError("后续任务缺少分页位置，请刷新任务列表"); return Promise.resolve([]); }
    const requestId = taskListRequestRef.current;
    const cursor = nextTaskCursor;
    setPageError(null);
    setLoading((current) => ({ ...current, more: true }));
    const pending = (async () => {
      try {
        const result = await listReviewTasks({ ...filterQuery, limit: 50, cursor });
        if (requestId !== taskListRequestRef.current || activeScopeKeyRef.current !== activeScopeKey) return [];
        const scopedTasks = initialExamId ? result.tasks.filter((task) => task.exam_id === initialExamId) : result.tasks;
        setTasks((current) => appendTaskPage(current, scopedTasks));
        setTaskCatalogue((current) => appendTaskPage(current, scopedTasks));
        if (result.aggregate) setMatchedAggregate(result.aggregate);
        setNextTaskCursor(result.next_cursor ?? "");
        setHasMoreTasks(Boolean(result.has_more));
        if (result.has_more && (!result.next_cursor || result.next_cursor === cursor)) setPageError("任务分页位置未推进，请刷新任务列表");
        return scopedTasks;
      } catch (currentError) {
        if (requestId === taskListRequestRef.current && activeScopeKeyRef.current === activeScopeKey) setPageError(formatError(currentError));
        return [];
      } finally {
        if (requestId === taskListRequestRef.current && activeScopeKeyRef.current === activeScopeKey) setLoading((current) => ({ ...current, more: false }));
      }
    })();
    pendingPageRef.current = pending;
    void pending.finally(() => { if (pendingPageRef.current === pending) pendingPageRef.current = null; });
    return pending;
  }, [activeScopeKey, hasMoreTasks, initialExamId, nextTaskCursor, filterQuery, serverKeyword]);

  const refreshTaskAggregate = useCallback(async () => {
    if (!queueReadyRef.current || activeScopeKeyRef.current !== activeScopeKey || filtersRef.current.keyword.trim() !== serverKeyword) return;
    const requestId = taskListRequestRef.current;
    try {
      const [result, matched, unassigned] = await Promise.all([
        listReviewTasks({ ...scopeQuery, limit: 1 }), listReviewTasks({ ...filterQuery, limit: 1 }),
        canManageTasks && queueScope === "all" ? listReviewTasks({ ...filterQuery, unassigned: true, limit: 1 }) : Promise.resolve(null)
      ]);
      if (requestId === taskListRequestRef.current && activeScopeKeyRef.current === activeScopeKey) {
        if (result.aggregate) setTaskAggregate(result.aggregate);
        if (matched.aggregate) setMatchedAggregate(matched.aggregate);
        setUnassignedCount(unassigned?.aggregate?.total_count ?? null);
      }
    } catch {
      // 后续完整刷新会重试；保留最后一次确认的汇总结果。
    }
  }, [activeScopeKey, canManageTasks, queueScope, scopeQuery, filterQuery, serverKeyword]);

  const selectAllMatchingAssignments = async () => {
    if (!canManageTasks || selectingAllRef.current) return;
    // 全选必须基于已确认的当前搜索事实；按钮禁用之外，ref 还拦住同一事件中的快速改词和调用。
    if (!queueReadyRef.current || activeScopeKeyRef.current !== activeScopeKey || filtersRef.current.keyword.trim() !== serverKeyword) {
      onInfo("请先完成当前筛选的任务列表读取，再选择全部匹配任务");
      return;
    }
    const requestId = taskListRequestRef.current;
    const selectionRequestId = ++allSelectionRequestRef.current;
    const isCurrentSelection = () => requestId === taskListRequestRef.current && selectionRequestId === allSelectionRequestRef.current && activeScopeKeyRef.current === activeScopeKey;
    selectingAllRef.current = true; setSelectingAll(true);
    try {
      let cursor: string | undefined;
      let complete: ReviewTask[] = [];
      const seen = new Set<string>();
      do {
        const page = await listReviewTasks({ ...filterQuery, limit: 200, cursor });
        if (!isCurrentSelection()) return;
        complete = appendTaskPage(complete, page.tasks);
        if (!page.has_more) break;
        if (!page.next_cursor || seen.has(page.next_cursor)) throw new Error("任务分页异常，请刷新后重试");
        seen.add(page.next_cursor); cursor = page.next_cursor;
      } while (cursor);
      // 完整分页成功后才把“全部匹配”作为选择事实；中途失败不能悄悄缩成已读几页。
      // 同范围的自动续页也可能仍在途；完整结果接管队列后，旧游标不能再追加或重启分页。
      taskListRequestRef.current += 1;
      pendingPageRef.current = null;
      setLoading({ tasks: false, more: false });
      setPageError(null);
      setTasks(complete); setTaskCatalogue((current) => appendTaskPage(current, complete));
      setAssignmentTaskIds(visibleTasks(complete, { canManageTasks, initialExamId: effectiveExamId, keyword: serverKeyword, taskFilter, questionId: questionFilter, subject: subjectFilter })
        .filter((task) => !["submitted", "completed", "in_progress", "cancelled"].includes(task.status) && task.source !== "double_mark_required").map((task) => task.id));
      setNextTaskCursor(""); setHasMoreTasks(false);
    } catch (error) {
      if (isCurrentSelection()) onInfo(formatError(error));
    } finally {
      if (selectionRequestId === allSelectionRequestRef.current) { selectingAllRef.current = false; setSelectingAll(false); }
    }
  };

  useEffect(() => {
    if (hasMoreTasks && !loadingTasks && !loadingMoreTasks && !pageError) void loadMoreTasks();
  }, [hasMoreTasks, loadingTasks, loadingMoreTasks, pageError, loadMoreTasks]);
  // 仍有后续页时不回绕队首，先补齐队列再判断下一份可处理任务。
  const nextTask = useMemo(() => followingVisibleActionableTask(tasks, selectedTaskId,
    { canManageTasks, initialExamId: effectiveExamId, keyword, taskFilter, questionId: questionFilter, subject: subjectFilter }, !hasMoreTasks),
    [canManageTasks, hasMoreTasks, effectiveExamId, keyword, questionFilter, subjectFilter, selectedTaskId, taskFilter, tasks]);
  useEffect(() => {
    const fromTaskId = pendingNextRef.current;
    if (!fromTaskId || loadingTasks || loadingMoreTasks) return;
    const following = followingVisibleActionableTask(tasks, fromTaskId,
      { canManageTasks, initialExamId: effectiveExamId, keyword, taskFilter, questionId: questionFilter, subject: subjectFilter }, !hasMoreTasks);
    if (following) {
      pendingNextRef.current = null;
      suppressAutoSelectRef.current = false;
      setSelectedTaskId(following.id);
    } else if (!hasMoreTasks && !pageError) {
      pendingNextRef.current = null;
      if (!canManageTasks) onInfo("当前没有更多已分配给你的阅卷任务");
    }
  }, [canManageTasks, hasMoreTasks, effectiveExamId, keyword, questionFilter, subjectFilter, loadingMoreTasks, loadingTasks, onInfo, pageError, taskFilter, tasks]);

  const loadGraders = useCallback(async () => {
    if (!canManageTasks) { setGraders([]); setGradersError(null); return; }
    try {
      const available = await listAllActiveGraders();
      setGraders(available);
      setGradersError(available.length ? null : "当前没有可分配的有效阅卷员账号");
      setAssignmentUserId((current) => available.some((user) => user.id === current) ? current : "");
    } catch (currentError) {
      setGraders([]); setAssignmentUserId(""); setGradersError(formatError(currentError));
    }
  }, [canManageTasks]);

  const reset = useCallback(() => {
    suppressAutoSelectRef.current = false;
    invalidateFilter();
    pendingPageRef.current = null;
    setTasks([]); setTaskAggregate(null); setNextTaskCursor(""); setHasMoreTasks(false);
    setLoading((current) => ({ ...current, more: false }));
    pendingNextRef.current = null;
    setSelectedTaskId("");
    setAssignmentTaskIds([]);
    Object.assign(filtersRef.current, { examFilter: initialExamId, subjectFilter: "", questionFilter: "", unassignedOnly: false });
    setTaskCatalogue([]); setExamFilter(initialExamId); setSubjectFilter(""); setQuestionFilter(""); setUnassignedOnly(false); setMatchedAggregate(null); setUnassignedCount(null);
  }, [initialExamId, invalidateFilter]);

  return {
    taskFilter, setTaskFilter: updateTaskFilter, queueScope, setQueueScope: updateQueueScope, keyword, setKeyword: updateKeyword, tasks, setTasks, taskAggregate,
    selectedTaskId, setSelectedTaskId, loadingTasks, loadingMoreTasks, hasMoreTasks, pageError, taskError,
    nextTaskCursor, graders, gradersError, assignmentUserId, setAssignmentUserId, assignmentTaskIds,
    setAssignmentTaskIds, filteredTasks, assignableTasks, graderOptions, graderNames, reviewerProgress,
    myProgress, remainingCount, selectedIndex, prefetchTasks, nextTask, pendingNextRef, suppressAutoSelectRef,
    loadTasks, loadMoreTasks, refreshTaskAggregate, loadGraders, reset,
    examFilter, effectiveExamId, setExamFilter: updateExamFilter, subjectFilter, setSubjectFilter: updateSubjectFilter, questionFilter, setQuestionFilter: updateQuestionFilter,
    unassignedOnly, setUnassignedOnly: updateUnassignedOnly, subjectOptions, examOptions, questionOptions, matchedAggregate, unassignedCount,
    selectingAll, selectAllMatchingAssignments
  };
}
