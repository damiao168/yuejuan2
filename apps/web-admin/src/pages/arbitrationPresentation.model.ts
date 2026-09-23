import type { RubricPoint } from "../api/papers";
import type { ArbitrationTask } from "../api/review";
import type { StatusTone } from "../types";

export const statusLabels: Record<string, string> = {
  pending: "待分配",
  assigned: "已分配",
  submitted: "已提交"
};

export function formatTime(value?: string) {
  if (!value) {
    return "-";
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

export function formatScore(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return "-";
  }
  return Number(value.toFixed(1)).toString();
}

export function taskTone(status: string): StatusTone {
  if (status === "submitted") {
    return "success";
  }
  if (status === "pending") {
    return "warning";
  }
  if (status === "assigned") {
    return "processing";
  }
  return "neutral";
}

export function pointLabel(point: RubricPoint) {
  return `${point.description || point.id} (${formatScore(point.score)} 分)`;
}

export function activeStatusMatched(task: ArbitrationTask) {
  return task.status === "pending" || task.status === "assigned";
}
