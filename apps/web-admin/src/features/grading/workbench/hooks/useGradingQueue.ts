import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReviewTask } from "../../../../api/review";
import { listReviewTasks, getReviewTask } from "../../../../api/review";
import { listManagedUsers, type ManagedUser } from "../../../../api/users";
import { displayNameOrUsername } from "../../../../auth/session";
import { formatError } from "../gradingWorkbench.model";
import type { ReviewerProgress, TaskFilter } from "../gradingWorkbench.types";
import { appendTaskPage, followingVisibleActionableTask, visibleTasks } from "../taskPaging";
import { hashQueryParam } from "../../../../router/query";
import type { ReviewTaskAggregate } from "@edugrade/sdk";

interface UseGradingQueueOptions {
  canManageTasks: boolean;
  canWork: boolean;
  currentUserId: string;
  initialExamId: string;
  personalScope: boolean;
  onInfo: (message: string) => void;
}

export function useGradingQueue({ canManageTasks, canWork, currentUserId, initialExamId, personalScope, onInfo }: UseGradingQueueOptions) {
  const [taskFilter, setTaskFilter] = useState<TaskFilter>("active");
  const [queueScope, setQueueScope] = useState<"mine" | "all">(canManageTasks ? "all" : "mine");
  const [keyword, setKeyword] = useState("");
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
  const suppressAutoSelectRef = useRef(false);

  const filteredTasks = useMemo(() => visibleTasks(tasks, { canManageTasks, initialExamId, keyword, taskFilter }),
    [canManageTasks, initialExamId, keyword, taskFilter, tasks]);
  const assignableTasks = useMemo(
    () => filteredTasks.filter((task) => !["submitted", "completed", "in_progress"].includes(task.status)),
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
    const requestId = ++taskListRequestRef.current;
    pendingNextRef.current = null;
    pendingPageRef.current = null;
    setLoading((current) => ({ ...current, tasks: true }));
    setTaskAggregate(null);
    setLoading((current) => ({ ...current, more: false }));
    setTaskError(null);
    setPageError(null);
    try {
      const personalQueue = personalScope || (canManageTasks && canWork && queueScope === "mine");
      const result = await listReviewTasks({ ...(personalQueue ? { assigned_to: currentUserId } : {}), ...(initialExamId ? { exam_id: initialExamId } : {}), limit: 50 });
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
      setTaskAggregate(result.aggregate ?? null);
      setNextTaskCursor(result.next_cursor ?? "");
      setHasMoreTasks(Boolean(result.has_more));
      setSelectedTaskId((current) => requestedId || ((scopedTasks.some((task) => task.id === current) || (result.has_more && Boolean(current)))
        ? current : scopedTasks.find((task) => ["assigned", "in_progress", "returned"].includes(task.status))?.id || ""));
    } catch (currentError) {
      if (requestId !== taskListRequestRef.current) return;
      setTasks([]); setTaskAggregate(null); setNextTaskCursor(""); setHasMoreTasks(false); setSelectedTaskId(""); setTaskError(formatError(currentError));
    } finally {
      if (requestId === taskListRequestRef.current) setLoading((current) => ({ ...current, tasks: false }));
    }
  }, [canManageTasks, canWork, currentUserId, initialExamId, personalScope, queueScope]);

  const loadMoreTasks = useCallback((): Promise<ReviewTask[]> => {
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
        const personalQueue = personalScope || (canManageTasks && canWork && queueScope === "mine");
        const result = await listReviewTasks({ ...(personalQueue ? { assigned_to: currentUserId } : {}), ...(initialExamId ? { exam_id: initialExamId } : {}), limit: 50, cursor });
        if (requestId !== taskListRequestRef.current) return [];
        const scopedTasks = initialExamId ? result.tasks.filter((task) => task.exam_id === initialExamId) : result.tasks;
        setTasks((current) => appendTaskPage(current, scopedTasks));
        if (result.aggregate) setTaskAggregate(result.aggregate);
        setNextTaskCursor(result.next_cursor ?? "");
        setHasMoreTasks(Boolean(result.has_more));
        if (result.has_more && (!result.next_cursor || result.next_cursor === cursor)) setPageError("任务分页位置未推进，请刷新任务列表");
        return scopedTasks;
      } catch (currentError) {
        if (requestId === taskListRequestRef.current) setPageError(formatError(currentError));
        return [];
      } finally {
        if (requestId === taskListRequestRef.current) setLoading((current) => ({ ...current, more: false }));
      }
    })();
    pendingPageRef.current = pending;
    void pending.finally(() => { if (pendingPageRef.current === pending) pendingPageRef.current = null; });
    return pending;
  }, [canManageTasks, canWork, currentUserId, hasMoreTasks, initialExamId, nextTaskCursor, personalScope, queueScope]);

  const refreshTaskAggregate = useCallback(async () => {
    const requestId = taskListRequestRef.current;
    const personalQueue = personalScope || (canManageTasks && canWork && queueScope === "mine");
    try {
      const result = await listReviewTasks({ ...(personalQueue ? { assigned_to: currentUserId } : {}), ...(initialExamId ? { exam_id: initialExamId } : {}), limit: 1 });
      if (requestId === taskListRequestRef.current && result.aggregate) setTaskAggregate(result.aggregate);
    } catch {
      // A later full refresh will retry. Keep the last confirmed aggregate.
    }
  }, [canManageTasks, canWork, currentUserId, initialExamId, personalScope, queueScope]);

  useEffect(() => {
    if (hasMoreTasks && !loadingTasks && !loadingMoreTasks && !pageError) void loadMoreTasks();
  }, [hasMoreTasks, loadingTasks, loadingMoreTasks, pageError, loadMoreTasks]);
  // 仍有后续页时不回绕队首，先补齐队列再判断下一份可处理任务。
  const nextTask = useMemo(() => followingVisibleActionableTask(tasks, selectedTaskId,
    { canManageTasks, initialExamId, keyword, taskFilter }, !hasMoreTasks),
    [canManageTasks, hasMoreTasks, initialExamId, keyword, selectedTaskId, taskFilter, tasks]);
  useEffect(() => {
    const fromTaskId = pendingNextRef.current;
    if (!fromTaskId || loadingTasks || loadingMoreTasks) return;
    const following = followingVisibleActionableTask(tasks, fromTaskId,
      { canManageTasks, initialExamId, keyword, taskFilter }, !hasMoreTasks);
    if (following) {
      pendingNextRef.current = null;
      suppressAutoSelectRef.current = false;
      setSelectedTaskId(following.id);
    } else if (!hasMoreTasks && !pageError) {
      pendingNextRef.current = null;
      if (!canManageTasks) onInfo("当前没有更多已分配给你的阅卷任务");
    }
  }, [canManageTasks, hasMoreTasks, initialExamId, keyword, loadingMoreTasks, loadingTasks, onInfo, pageError, taskFilter, tasks]);

  const loadGraders = useCallback(async () => {
    if (!canManageTasks) { setGraders([]); setGradersError(null); return; }
    try {
      const result = await listManagedUsers({ limit: 200 });
      const available = result.users.filter((user) => user.status === "active" && user.roles.includes("grader"));
      setGraders(available);
      setGradersError(available.length ? null : "当前没有可分配的有效阅卷员账号");
      setAssignmentUserId((current) => available.some((user) => user.id === current) ? current : "");
    } catch (currentError) {
      setGraders([]); setAssignmentUserId(""); setGradersError(formatError(currentError));
    }
  }, [canManageTasks]);

  const reset = useCallback(() => {
    suppressAutoSelectRef.current = false;
    taskListRequestRef.current += 1;
    pendingPageRef.current = null;
    setTasks([]); setTaskAggregate(null); setNextTaskCursor(""); setHasMoreTasks(false);
    setLoading((current) => ({ ...current, more: false }));
    pendingNextRef.current = null;
    setSelectedTaskId("");
    setAssignmentTaskIds([]);
  }, []);

  return {
    taskFilter, setTaskFilter, queueScope, setQueueScope, keyword, setKeyword, tasks, setTasks, taskAggregate,
    selectedTaskId, setSelectedTaskId, loadingTasks, loadingMoreTasks, hasMoreTasks, pageError, taskError,
    nextTaskCursor, graders, gradersError, assignmentUserId, setAssignmentUserId, assignmentTaskIds,
    setAssignmentTaskIds, filteredTasks, assignableTasks, graderOptions, graderNames, reviewerProgress,
    myProgress, remainingCount, selectedIndex, prefetchTasks, nextTask, pendingNextRef, suppressAutoSelectRef,
    loadTasks, loadMoreTasks, refreshTaskAggregate, loadGraders, reset
  };
}
