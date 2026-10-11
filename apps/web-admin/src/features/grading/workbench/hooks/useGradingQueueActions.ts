import { useRef, type Dispatch, type RefObject, type SetStateAction } from "react";
import { ApiClientError } from "../../../../api/client";
import { assignReviewTask, batchAssignReviewTasks, claimNextReviewTask, releaseReviewTask, type ReviewTask } from "../../../../api/review";
import { formatError } from "../gradingWorkbench.model";
import type { TaskFilter, WorkbenchContext } from "../gradingWorkbench.types";
import type { AssignmentBatch } from "../assignmentPlan";

interface UseGradingQueueActionsOptions {
  isCurrentScope: () => boolean;
  canManageTasks: boolean;
  initialExamId: string;
  ctx: WorkbenchContext | null;
  filteredTasks: ReviewTask[];
  assignableTasks: ReviewTask[];
  assignmentTaskIds: string[];
  assignmentUserId: string;
  graderNames: Record<string, string>;
  nextTask?: ReviewTask;
  hasMoreTasks: boolean;
  pageError: string | null;
  selectedTaskId: string;
  ownsSelectedTask: boolean;
  pendingNextRef: RefObject<string | null>;
  suppressAutoSelectRef: RefObject<boolean>;
  setTasks: Dispatch<SetStateAction<ReviewTask[]>>;
  setCtx: Dispatch<SetStateAction<WorkbenchContext | null>>;
  setAssignmentTaskIds: Dispatch<SetStateAction<string[]>>;
  setSelectedTaskId: Dispatch<SetStateAction<string>>;
  setTaskFilter: (filter: TaskFilter) => void;
  setActioning: (action: string | null) => void;
  setCalibrationQuestionId: (questionId: string) => void;
  loadTasks: () => Promise<void>;
  loadMoreTasks: () => Promise<ReviewTask[]>;
  refreshTaskAggregate: () => Promise<void>;
  runAction: (key: string, action: () => Promise<void>, successText: string) => Promise<void>;
  notify: {
    info: (text: string) => void;
    warning: (text: string) => void;
    success: (text: string) => void;
    error: (text: string) => void;
  };
}

export function useGradingQueueActions(options: UseGradingQueueActionsOptions) {
  const actionRef = useRef<{ isCurrentScope: () => boolean } | null>(null);
  const beginAction = () => {
    if (!options.isCurrentScope() || actionRef.current?.isCurrentScope()) return null;
    const action = { isCurrentScope: options.isCurrentScope };
    actionRef.current = action;
    return action;
  };
  const isCurrentAction = (action: NonNullable<typeof actionRef.current>) => actionRef.current === action && action.isCurrentScope();
  // 新考试可以开始新动作；旧请求只能保留远端结果，不能解开后来动作的本地锁。
  const finishAction = (action: NonNullable<typeof actionRef.current>, clearBusy = true) => {
    if (!isCurrentAction(action)) return;
    actionRef.current = null;
    if (clearBusy) options.setActioning(null);
  };

  const assignSelectedTasks = async () => {
    if (!options.isCurrentScope()) return;
    const available = new Set(options.assignableTasks.map((task) => task.id));
    const taskIds = options.assignmentTaskIds.filter((taskId) => available.has(taskId));
    const selectedTasks = options.assignableTasks.filter((task) => taskIds.includes(task.id));
    if (!options.assignmentUserId || taskIds.length === 0) {
      options.notify.warning("请选择阅卷员和需要分配的任务");
      return;
    }
    const action = beginAction();
    if (!action) return;
    options.setActioning("assign-tasks");
    try {
      const updated = taskIds.length === 1
        ? [(await assignReviewTask(selectedTasks[0].id, options.assignmentUserId, selectedTasks[0].revision)).task]
        : (await batchAssignReviewTasks(selectedTasks, options.assignmentUserId)).tasks;
      if (!isCurrentAction(action)) return;
      const updatedById = new Map(updated.map((task) => [task.id, task]));
      options.setTasks((current) => current.map((task) => updatedById.has(task.id) ? { ...task, ...updatedById.get(task.id)! } : task));
      options.setCtx((current) => current && updatedById.has(current.task.id)
        ? { ...current, task: { ...current.task, ...updatedById.get(current.task.id)! } }
        : current);
      options.setAssignmentTaskIds([]);
      await options.loadTasks();
      if (!isCurrentAction(action)) return;
      options.notify.success(`已将 ${updated.length} 份任务分配给 ${options.graderNames[options.assignmentUserId] ?? "阅卷员"}`);
    } catch (currentError) {
      if (isCurrentAction(action)) options.notify.error(formatError(currentError));
    } finally {
      finishAction(action);
    }
  };

  const assignPlannedTasks = async (plan: AssignmentBatch[]): Promise<boolean> => {
    if (!options.canManageTasks || !plan.length) return false;
    const action = beginAction();
    if (!action) return false;
    options.setActioning("assign-tasks");
    const succeeded = new Set<string>();
    try {
      // 单个阅卷员的一批分配是事务；多名阅卷员之间并非原子操作，失败后保留成功事实。
      for (const batch of plan) {
        if (!isCurrentAction(action)) return false;
        const result = await batchAssignReviewTasks(batch.tasks, batch.graderId);
        if (!isCurrentAction(action)) return false;
        result.tasks.forEach((task) => succeeded.add(task.id));
        const updated = new Map(result.tasks.map((task) => [task.id, task]));
        options.setTasks((current) => current.map((task) => updated.has(task.id) ? { ...task, ...updated.get(task.id)! } : task));
        options.setCtx((current) => current && updated.has(current.task.id) ? { ...current, task: { ...current.task, ...updated.get(current.task.id)! } } : current);
      }
      options.notify.success(`已分配 ${succeeded.size} 份任务给 ${plan.length} 名阅卷员`);
    } catch (error) {
      if (!isCurrentAction(action)) return false;
      options.notify.error(`已成功分配 ${succeeded.size} 份，剩余未完成；${formatError(error)}。请根据刷新后的队列重新预览。`);
    } finally {
      if (isCurrentAction(action)) {
        options.setAssignmentTaskIds((current) => current.filter((id) => !succeeded.has(id)));
        await options.loadTasks();
        finishAction(action);
      }
    }
    return action.isCurrentScope();
  };

  const claimTask = async () => {
    const action = beginAction();
    if (!action) return;
    const target = options.ctx?.task ?? options.filteredTasks.find((task) => ["assigned", "returned"].includes(task.status));
    options.setActioning("claim-task");
    try {
      const result = await claimNextReviewTask(options.initialExamId || target?.exam_id || "", target?.question_id || "");
      // 领取已在服务端发生；切考试或身份后，不把旧任务加入新队列，也不继续发旧范围的汇总请求。
      if (!isCurrentAction(action)) return;
      options.setTasks((current) => current.some((task) => task.id === result.task.id)
        ? current.map((task) => task.id === result.task.id ? { ...task, ...result.task } : task)
        : [result.task, ...current]);
      options.setSelectedTaskId(result.task.id);
      await options.refreshTaskAggregate();
    } catch (currentError) {
      if (!isCurrentAction(action)) return;
      if (currentError instanceof ApiClientError && currentError.code === "grader_qualification_required" && target?.question_id) {
        options.setCalibrationQuestionId(target.question_id);
        options.notify.warning("该题属于高风险阅卷，请先完成本题校准");
      } else {
        options.notify.error(formatError(currentError));
      }
    } finally {
      finishAction(action);
    }
  };

  const goNext = async () => {
    if (!options.isCurrentScope()) return;
    if (options.nextTask) {
      options.setSelectedTaskId(options.nextTask.id);
      return;
    }
    if (options.hasMoreTasks) {
      if (options.pageError) {
        options.notify.info("后续任务加载失败，请在任务列表重试");
      } else {
        // 记住触发翻页的任务，待新页到达后再定位下一份，不能把当前已加载末尾当作全部完成。
        options.pendingNextRef.current = options.selectedTaskId;
        void options.loadMoreTasks();
        options.notify.info("正在加载后续任务，请稍后继续");
      }
      return;
    }
    if (!options.canManageTasks) {
      options.setSelectedTaskId("");
      options.notify.info("当前没有更多已分配给你的阅卷任务");
      return;
    }
    options.setSelectedTaskId("");
    options.setTaskFilter("pending");
    options.notify.info("没有更多已分配任务；请先把待分配任务交给阅卷员");
  };

  const releaseCurrentTask = async () => {
    if (!options.selectedTaskId || !options.ownsSelectedTask) return;
    const action = beginAction();
    if (!action) return;
    try {
      await options.runAction("release", async () => {
        if (!isCurrentAction(action)) return;
        const result = await releaseReviewTask(options.selectedTaskId);
        if (!isCurrentAction(action)) return;
        options.setTasks((current) => current.map((item) => item.id === result.task.id ? { ...item, ...result.task } : item));
        // 主动暂放后留在空选择，避免列表刷新立即把刚放回的任务自动选中。
        options.suppressAutoSelectRef.current = true;
        options.setSelectedTaskId("");
      }, "任务已放回队列；请以服务端草稿状态为准");
    } finally {
      // 暂放的提示由 runAction 维护；它拒绝执行时不能清掉其他动作的忙状态。
      finishAction(action, false);
    }
  };

  return { assignSelectedTasks, assignPlannedTasks, claimTask, goNext, releaseCurrentTask };
}
