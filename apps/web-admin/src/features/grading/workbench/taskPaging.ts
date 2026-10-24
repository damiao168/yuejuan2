import type { ReviewTask } from "../../../api/review";

/** 同考试同题连续显示；组内保持服务端优先级顺序，避免阅卷中频繁切换评分依据。 */
export function groupTasksByQuestion(tasks: ReviewTask[]): ReviewTask[] {
  const groups = new Map<string, Map<string, ReviewTask[]>>();
  for (const task of tasks) {
    const examKey = JSON.stringify([task.exam_id, task.subject || ""]);
    const questions = groups.get(examKey) ?? new Map<string, ReviewTask[]>();
    const questionKey = task.question_id || task.question_no;
    const group = questions.get(questionKey) ?? []; group.push(task); questions.set(questionKey, group); groups.set(examKey, questions);
  }
  return [...groups.values()].flatMap((questions) => [...questions.values()].flat());
}

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
  questionId?: string;
  subject?: string;
}): ReviewTask[] {
  const text = options.keyword.trim().toLowerCase();
  return groupTasksByQuestion(tasks).filter((task) => {
    const activeStatuses = options.canManageTasks ? ["pending", "assigned", "in_progress", "returned"] : ["assigned", "in_progress", "returned"];
    const statusMatched = options.taskFilter === "all" || (options.taskFilter === "active" ? activeStatuses.includes(task.status) : task.status === options.taskFilter);
    const examMatched = !options.initialExamId || task.exam_id === options.initialExamId;
    const scopeMatched = (!options.questionId || task.question_id === options.questionId) && (!options.subject || task.subject === options.subject);
    // 服务端把这些字段以单个空格拼接后做字面查询；省略的名称/学科按空字符串还原，保留分隔空格。
    const keywordMatched = !text || [task.id, task.anonymous_code, task.question_no, task.source, task.exam_name, task.subject]
      .map((value) => value ?? "").join(" ").toLowerCase().includes(text);
    return statusMatched && examMatched && scopeMatched && keywordMatched;
  });
}

export function followingVisibleActionableTask(tasks: ReviewTask[], selectedTaskId: string, options: Parameters<typeof visibleTasks>[1], wrap: boolean): ReviewTask | undefined {
  // 先在完整列表定位锚点再筛选；已提交任务可能已不可见，但仍决定下一题从哪里开始。
  const ordered = groupTasksByQuestion(tasks);
  const index = ordered.findIndex((task) => task.id === selectedTaskId);
  const actionable = (items: ReviewTask[]) => visibleTasks(items, options)
    .find((task) => task.id !== selectedTaskId && !["submitted", "completed"].includes(task.status));
  return actionable(ordered.slice(index + 1)) ?? (wrap ? actionable(ordered.slice(0, Math.max(index, 0))) : undefined);
}

export function precedingVisibleTask(tasks: ReviewTask[], selectedTaskId: string, options: Parameters<typeof visibleTasks>[1]): ReviewTask | undefined {
  const ordered = groupTasksByQuestion(tasks);
  const index = ordered.findIndex(task => task.id === selectedTaskId);
  if (index <= 0) return undefined;
  // 上一份允许回看刚提交的答卷；保持考试、题目和搜索范围，不回绕或重新领取任务。
  const previous = visibleTasks(ordered.slice(0, index), { ...options, taskFilter: options.taskFilter === "active" ? "all" : options.taskFilter })
    .filter(task => !["cancelled", "pending"].includes(task.status));
  return previous[previous.length - 1];
}
