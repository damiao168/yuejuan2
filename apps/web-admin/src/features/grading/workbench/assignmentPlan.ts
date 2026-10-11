import type { ReviewTask } from "../../../api/review";
import { examSubjectLabel } from "../../../constants/examStatus";

export interface AssignmentBatch { graderId: string; tasks: ReviewTask[] }
export function assignmentGroupKey(task: ReviewTask) { return JSON.stringify([task.exam_id, task.question_id]); }
export function assignmentGroups(tasks: ReviewTask[]) {
  const groups = new Map<string, ReviewTask[]>();
  for (const task of tasks) { const key = assignmentGroupKey(task); const group = groups.get(key) ?? []; group.push(task); groups.set(key, group); }
  return [...groups.entries()].map(([key, group]) => {
    const firstTask = group[0];
    const examLabel = firstTask.exam_name || `考试 ${firstTask.exam_id.slice(0, 8)}`;
    const subjectLabel = firstTask.subject ? examSubjectLabel(firstTask.subject) : "学科未标注";
    return { key, tasks: group, label: `${examLabel} · ${subjectLabel} · 第 ${firstTask.question_no} 题` };
  });
}

/** 分配预览固定任务 ID 和修订号；按题平均分给明确选择的阅卷员，不跨题猜测资格。 */
export function buildAssignmentPlan(tasks: ReviewTask[], targets: Record<string, string[]>): AssignmentBatch[] {
  const batches = new Map<string, ReviewTask[]>();
  for (const group of assignmentGroups(tasks)) {
    const graders = [...new Set(targets[group.key] ?? [])];
    if (!graders.length) continue;
    group.tasks.forEach((task, index) => { const grader = graders[index % graders.length]; const assigned = batches.get(grader) ?? []; assigned.push(task); batches.set(grader, assigned); });
  }
  return [...batches].map(([graderId, tasks]) => ({ graderId, tasks }));
}
