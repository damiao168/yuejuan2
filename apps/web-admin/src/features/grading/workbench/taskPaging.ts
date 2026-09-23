import type { ReviewTask } from "../../../api/review";

export function appendTaskPage(current: ReviewTask[], page: ReviewTask[]): ReviewTask[] {
  const incoming = new Map(page.map((task) => [task.id, task]));
  const merged = current.map((task) => incoming.get(task.id) ?? task);
  const known = new Set(current.map((task) => task.id));
  for (const task of page) {
    if (known.has(task.id)) continue;
    known.add(task.id);
    merged.push(task);
  }
  return merged;
}

export function visibleTasks(tasks: ReviewTask[], options: {
  canManageTasks: boolean;
  initialExamId: string;
  keyword: string;
  taskFilter: string;
}): ReviewTask[] {
  const text = options.keyword.trim().toLowerCase();
  return tasks.filter((task) => {
    const activeStatuses = options.canManageTasks ? ["pending", "assigned", "in_progress", "returned"] : ["assigned", "in_progress", "returned"];
    const statusMatched = options.taskFilter === "all" || (options.taskFilter === "active" ? activeStatuses.includes(task.status) : task.status === options.taskFilter);
    const examMatched = !options.initialExamId || task.exam_id === options.initialExamId;
    const keywordMatched = !text || [task.id, task.anonymous_code, task.question_no, task.source]
      .some((value) => value.toLowerCase().includes(text));
    return statusMatched && examMatched && keywordMatched;
  });
}

export function followingVisibleActionableTask(tasks: ReviewTask[], selectedTaskId: string, options: Parameters<typeof visibleTasks>[1], wrap: boolean): ReviewTask | undefined {
  const index = tasks.findIndex((task) => task.id === selectedTaskId);
  const actionable = (items: ReviewTask[]) => visibleTasks(items, options)
    .find((task) => task.id !== selectedTaskId && !["submitted", "completed"].includes(task.status));
  return actionable(tasks.slice(index + 1)) ?? (wrap ? actionable(tasks.slice(0, Math.max(index, 0))) : undefined);
}
