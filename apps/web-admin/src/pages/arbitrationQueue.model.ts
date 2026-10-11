import type { Question } from "../api/papers";
import type { ArbitrationTask } from "../api/review";
import { activeStatusMatched } from "./arbitrationPresentation.model";

export type ArbitrationScopeFilter = "mine" | "all";
export type ArbitrationStatusFilter = "active" | "pending" | "assigned" | "submitted";

export interface ArbitrationDetailState {
  task: ArbitrationTask;
  question?: Question;
  warnings: string[];
}

export interface ArbitrationDecisionDraft {
  finalScore: number | null;
  reason: string;
  studentFeedback: string;
}

export const arbitrationStatusOptions: { label: string; value: ArbitrationStatusFilter }[] = [
  { label: "未完成（待分配+已分配）", value: "active" },
  { label: "待分配", value: "pending" },
  { label: "已分配", value: "assigned" },
  { label: "已提交", value: "submitted" }
];

export const arbitrationScopeOptions: { label: string; value: ArbitrationScopeFilter }[] = [
  { label: "我的任务", value: "mine" },
  { label: "全部任务", value: "all" }
];

export function arbitrationTaskAssignee(personalScope: boolean, currentUserId: string, scope: ArbitrationScopeFilter, actorId: string): string | undefined {
  if (personalScope) return currentUserId;
  if (scope === "mine") return actorId;
  return undefined;
}

export function chooseArbitrationTaskId(tasks: readonly ArbitrationTask[], requestedId: string, selectedId: string): string {
  // 深链优先，其次保留仍在队列中的选择，最后才自动选首项。
  if (requestedId) return requestedId;
  if (tasks.some((task) => task.id === selectedId)) return selectedId;
  return tasks[0]?.id ?? "";
}

export function filterArbitrationTasks(tasks: readonly ArbitrationTask[], examNames: Readonly<Record<string, string>>, initialExamId: string, statusFilter: ArbitrationStatusFilter, keyword: string): ArbitrationTask[] {
  const text = keyword.trim().toLowerCase();
  return tasks.filter((task) => {
    const statusMatched = statusFilter === "active" ? activeStatusMatched(task) : task.status === statusFilter;
    const examLabel = examNames[task.exam_id] ?? task.exam_id;
    const keywordMatched = !text || task.id.toLowerCase().includes(text) || task.anonymous_code.toLowerCase().includes(text) || task.question_no.toLowerCase().includes(text) || task.exam_id.toLowerCase().includes(text) || examLabel.toLowerCase().includes(text);
    return (!initialExamId || task.exam_id === initialExamId) && statusMatched && keywordMatched;
  });
}
