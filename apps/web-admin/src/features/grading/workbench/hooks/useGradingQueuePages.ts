import { useCallback, useRef, useState, type Dispatch, type RefObject, type SetStateAction } from "react";
import { listReviewTasks, getReviewTask, type ReviewTask, type ReviewTaskFilter } from "../../../../api/review";
import type { ReviewTaskAggregate } from "@edugrade/sdk";
import { hashQueryParam } from "../../../../router/query";
import { appendTaskPage } from "../taskPaging";
import { formatError } from "../gradingWorkbench.model";
import type { TaskFilter } from "../gradingWorkbench.types";

interface QueuePageOptions {
  canManageTasks: boolean;
  canWork: boolean;
  currentUserId: string;
  initialExamId: string;
  personalScope: boolean;
  queueScope: "mine" | "all";
  activeScopeKey: string;
  activeScopeKeyRef: RefObject<string>;
  serverKeyword: string;
  filtersRef: RefObject<{ keyword: string; taskFilter: TaskFilter }>;
  taskListRequestRef: RefObject<number>;
  allSelectionRequestRef: RefObject<number>;
  queueReadyRef: RefObject<boolean>;
  selectingAllRef: RefObject<boolean>;
  pendingNextRef: RefObject<string | null>;
  pendingPageRef: RefObject<Promise<ReviewTask[]> | null>;
  setSelectingAll: Dispatch<SetStateAction<boolean>>;
  setTaskFilter: Dispatch<SetStateAction<TaskFilter>>;
  setSelectedTaskId: Dispatch<SetStateAction<string>>;
  filterQuery: ReviewTaskFilter;
  scopeQuery: ReviewTaskFilter;
}

// 深链补读的任务也受原考试和个人队列限制；请求代次由调用方在补读返回后核对。
function assertRequestedTaskScope(task: ReviewTask, { canManageTasks, canWork, currentUserId, initialExamId, personalScope }: Pick<QueuePageOptions, "canManageTasks" | "canWork" | "currentUserId" | "initialExamId" | "personalScope">, queueScope: "mine" | "all") {
  const personalQueue = personalScope || (canManageTasks && canWork && queueScope === "mine");
  if ((initialExamId && task.exam_id !== initialExamId) || (personalQueue && task.assigned_to !== currentUserId)) {
    throw new Error("此任务不在当前考试或已转派，请返回我的工作查看最新任务");
  }
}

// 分页与汇总共享请求代次；筛选输入和全选接管队列时由上层同步作废，迟到页不能重新追加。
export function useGradingQueuePages({ canManageTasks, canWork, currentUserId, initialExamId, personalScope,
  queueScope, activeScopeKey, activeScopeKeyRef, serverKeyword, filtersRef, taskListRequestRef,
  allSelectionRequestRef, queueReadyRef, selectingAllRef, pendingNextRef, pendingPageRef,
  setSelectingAll, setTaskFilter, setSelectedTaskId, filterQuery, scopeQuery }: QueuePageOptions) {
  const [taskCatalogue, setTaskCatalogue] = useState<ReviewTask[]>([]);
  const [matchedAggregate, setMatchedAggregate] = useState<ReviewTaskAggregate | null>(null);
  const [unassignedCount, setUnassignedCount] = useState<number | null>(null);
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [taskAggregate, setTaskAggregate] = useState<ReviewTaskAggregate | null>(null);
  const requestedTaskRef = useRef(hashQueryParam("task"));
  const [loading, setLoading] = useState({ tasks: true, more: false });
  const loadingTasks = loading.tasks;
  const loadingMoreTasks = loading.more;
  const [nextTaskCursor, setNextTaskCursor] = useState("");
  const [hasMoreTasks, setHasMoreTasks] = useState(false);
  const [pageError, setPageError] = useState<string | null>(null);
  const [taskError, setTaskError] = useState<string | null>(null);
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
  }, [activeScopeKey, canManageTasks, canWork, currentUserId, initialExamId, personalScope, queueScope, filterQuery, scopeQuery, serverKeyword, activeScopeKeyRef, allSelectionRequestRef, filtersRef, pendingNextRef, pendingPageRef, queueReadyRef, selectingAllRef, setSelectedTaskId, setSelectingAll, setTaskFilter, taskListRequestRef]);

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
  }, [activeScopeKey, hasMoreTasks, initialExamId, nextTaskCursor, filterQuery, serverKeyword, activeScopeKeyRef, filtersRef, pendingPageRef, queueReadyRef, taskListRequestRef]);

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
  }, [activeScopeKey, canManageTasks, queueScope, scopeQuery, filterQuery, serverKeyword, activeScopeKeyRef, filtersRef, queueReadyRef, taskListRequestRef]);

  return { taskCatalogue, setTaskCatalogue, matchedAggregate, setMatchedAggregate, unassignedCount, setUnassignedCount,
    tasks, setTasks, taskAggregate, setTaskAggregate, loadingTasks, loadingMoreTasks, setLoading,
    nextTaskCursor, setNextTaskCursor, hasMoreTasks, setHasMoreTasks, pageError, setPageError, taskError,
    loadTasks, loadMoreTasks, refreshTaskAggregate };
}
