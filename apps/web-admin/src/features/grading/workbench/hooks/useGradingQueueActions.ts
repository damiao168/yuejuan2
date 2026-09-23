import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import { ApiClientError } from "../../../../api/client";
import { assignReviewTask, batchAssignReviewTasks, claimNextReviewTask, releaseReviewTask, type ReviewTask } from "../../../../api/review";
import { formatError } from "../gradingWorkbench.model";
import type { TaskFilter } from "../gradingWorkbench.types";
import type { WorkbenchContext } from "../gradingWorkbench.types";

interface UseGradingQueueActionsOptions {
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
  pendingNextRef: MutableRefObject<string | null>;
  suppressAutoSelectRef: MutableRefObject<boolean>;
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
  const assignSelectedTasks = async () => {
    const available = new Set(options.assignableTasks.map((task) => task.id));
    const taskIds = options.assignmentTaskIds.filter((taskId) => available.has(taskId));
    const selectedTasks = options.assignableTasks.filter((task) => taskIds.includes(task.id));
    if (!options.assignmentUserId || taskIds.length === 0) {
      options.notify.warning("请选择阅卷员和需要分配的任务");
      return;
    }
    options.setActioning("assign-tasks");
    try {
      const updated = taskIds.length === 1
        ? [(await assignReviewTask(selectedTasks[0].id, options.assignmentUserId, selectedTasks[0].revision)).task]
        : (await batchAssignReviewTasks(selectedTasks, options.assignmentUserId)).tasks;
      const updatedById = new Map(updated.map((task) => [task.id, task]));
      options.setTasks((current) => current.map((task) => updatedById.get(task.id) ?? task));
      options.setCtx((current) => current && updatedById.has(current.task.id)
        ? { ...current, task: updatedById.get(current.task.id)! }
        : current);
      options.setAssignmentTaskIds([]);
      await options.loadTasks();
      options.notify.success(`已将 ${updated.length} 份任务分配给 ${options.graderNames[options.assignmentUserId] ?? "阅卷员"}`);
    } catch (currentError) {
      options.notify.error(formatError(currentError));
    } finally {
      options.setActioning(null);
    }
  };

  const claimTask = async () => {
    const target = options.ctx?.task ?? options.filteredTasks.find((task) => ["assigned", "returned"].includes(task.status));
    options.setActioning("claim-task");
    try {
      const result = await claimNextReviewTask(options.initialExamId || target?.exam_id || "", target?.question_id || "");
      options.setTasks((current) => current.some((task) => task.id === result.task.id)
        ? current.map((task) => task.id === result.task.id ? result.task : task)
        : [result.task, ...current]);
      options.setSelectedTaskId(result.task.id);
      await options.refreshTaskAggregate();
    } catch (currentError) {
      if (currentError instanceof ApiClientError && currentError.code === "grader_qualification_required" && target?.question_id) {
        options.setCalibrationQuestionId(target.question_id);
        options.notify.warning("该题属于高风险阅卷，请先完成本题校准");
      } else {
        options.notify.error(formatError(currentError));
      }
    } finally {
      options.setActioning(null);
    }
  };

  const goNext = async () => {
    if (options.nextTask) {
      options.setSelectedTaskId(options.nextTask.id);
      return;
    }
    if (options.hasMoreTasks) {
      if (options.pageError) {
        options.notify.info("后续任务加载失败，请在任务列表重试");
      } else {
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
    await options.runAction("release", async () => {
      const result = await releaseReviewTask(options.selectedTaskId);
      options.setTasks((current) => current.map((item) => item.id === result.task.id ? result.task : item));
      options.suppressAutoSelectRef.current = true;
      options.setSelectedTaskId("");
    }, "已放回队列，草稿已保留");
  };

  return { assignSelectedTasks, claimTask, goNext, releaseCurrentTask };
}
