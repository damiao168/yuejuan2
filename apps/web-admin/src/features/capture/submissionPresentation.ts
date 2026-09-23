import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../../api/client";
import type { OcrTask, QualityIssue } from "../../api/submissions";
import type { StatusTone } from "../../types";
import type { SubmissionView } from "./captureQueries";

type QualityFilter = "all" | "blurry" | "missing_page" | "duplicate_page" | "ocr_failed" | "needs_manual_handling";

export interface Filters {
  search: string;
  quality: QualityFilter;
  issue: "all" | "failed" | "unmatched" | "quality";
}

export const qualityFilterOptions: { label: string; value: QualityFilter }[] = [
  { label: "全部问题类型", value: "all" },
  { label: "模糊", value: "blurry" },
  { label: "缺页", value: "missing_page" },
  { label: "重复页", value: "duplicate_page" },
  { label: "识别失败", value: "ocr_failed" },
  { label: "需要人工处理", value: "needs_manual_handling" }
];

export const submissionStatusLabels: Record<string, string> = {
  created: "已创建",
  pages_uploaded: "已上传",
  quality_checked: "质量通过",
  ready_for_ocr: "待识别",
  rejected: "已拒绝"
};

export const pageStatusLabels: Record<string, string> = {
  uploaded: "已上传",
  quality_checked: "质量通过",
  quality_failed: "质量未通过",
  replaced: "已替换"
};

export function pageStatusTone(status: string): StatusTone {
  if (status === "quality_checked") {
    return "success";
  }
  if (status === "quality_failed" || status === "failed" || status === "rejected") {
    return "danger";
  }
  return "neutral";
}

export const qualityStatusLabels: Record<string, string> = {
  unchecked: "未检查",
  passed: "通过",
  failed: "未通过"
};

const ocrStatusLabels: Record<string, string> = {
  queued: "排队中",
  processing: "处理中",
  completed: "已完成",
  failed: "失败"
};

export function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn(`API 请求失败 ${error.status} ${error.code}`, error);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function formatTime(value?: string) {
  if (!value) {
    return "-";
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

export function compactFileName(name: string) {
  return name.replace(/\.[^.]+$/, "").trim().slice(0, 64) || name.slice(0, 64);
}

export function sourceTypeFor(file: File) {
  const name = file.name.toLowerCase();
  return file.type === "application/pdf" || name.endsWith(".pdf") ? "pdf_upload" : "image_upload";
}

export function statusTone(status: string): StatusTone {
  if (status === "ready_for_ocr" || status === "quality_checked") {
    return "success";
  }
  if (status === "rejected") {
    return "danger";
  }
  if (status === "created") {
    return "neutral";
  }
  return "processing";
}

export function ocrTone(task?: OcrTask): StatusTone {
  if (!task) {
    return "neutral";
  }
  if (task.status === "completed" && !task.requires_human_review) {
    return "success";
  }
  if (task.status === "failed") {
    return "danger";
  }
  if (task.requires_human_review) {
    return "warning";
  }
  return "processing";
}

export function latestTask(tasks: OcrTask[]) {
  return [...tasks].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0];
}

export function summaryString(row: SubmissionView, key: string) {
  const value = row.submission.summary?.[key];
  return typeof value === "string" ? value : "";
}

export function summaryNumber(row: SubmissionView, key: string) {
  const value = row.submission.summary?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function summaryBoolean(row: SubmissionView, key: string) {
  return row.submission.summary?.[key] === true;
}

type SubmissionProcessingState = "pending" | "processing" | "completed" | "failed";

export function processingState(row: SubmissionView): SubmissionProcessingState {
  const task = latestTask(row.ocrTasks);
  const taskStatus = task?.status ?? summaryString(row, "latest_ocr_status");
  const segmentCount = row.segments.length || summaryNumber(row, "segment_count");
  if (row.submission.quality_status === "failed" || taskStatus === "failed") {
    return "failed";
  }
  if (taskStatus === "completed" && segmentCount > 0) {
    return "completed";
  }
  if (taskStatus === "queued" || taskStatus === "processing") {
    return "processing";
  }
  return "pending";
}

export function ocrLabel(tasks: OcrTask[]) {
  const task = latestTask(tasks);
  if (!task) {
    return "未触发";
  }
  const suffix = task.requires_human_review ? " / 需人工" : "";
  return `${ocrStatusLabels[task.status] ?? "未知状态"}${suffix}`;
}

export function derivedIssues(row: SubmissionView): QualityIssue[] {
  const issues = [...(row.submission.quality_issues ?? [])];
  for (const page of row.pages) {
    issues.push(...(page.quality_issues ?? []).map((issue) => ({ code: issue.code, message: `第 ${page.page_no} 页：${getSafeUserText(issue.message, "图片质量检查未通过")}` })));
  }
  if (row.ocrTasks.some((task) => task.status === "failed")) {
    issues.push({ code: "ocr_failed", message: "文字识别失败" });
  }
  if (summaryNumber(row, "ocr_failed_count") > 0 && !issues.some((issue) => issue.code === "ocr_failed")) {
    issues.push({ code: "ocr_failed", message: "文字识别失败" });
  }
  if (row.ocrTasks.some((task) => task.requires_human_review) || summaryBoolean(row, "latest_ocr_requires_human_review")) {
    issues.push({ code: "needs_manual_handling", message: "识别结果需要人工处理" });
  }
  if (row.segments.some((segment) => segment.status === "needs_manual_review" || segment.status === "rejected") || summaryNumber(row, "manual_segment_count") > 0) {
    issues.push({ code: "needs_manual_handling", message: "切分结果需要人工处理" });
  }
  return issues;
}

export function matchesQualityFilter(row: SubmissionView, filter: QualityFilter) {
  if (filter === "all") {
    return true;
  }
  const issues = derivedIssues(row);
  if (filter === "missing_page") {
    return issues.some((issue) => issue.code === "missing_page" || issue.code === "page_count_mismatch" || issue.code === "no_pages");
  }
  if (filter === "ocr_failed") {
    return issues.some((issue) => issue.code === "ocr_failed") || row.ocrTasks.some((task) => task.status === "failed");
  }
  if (filter === "needs_manual_handling") {
    return issues.some((issue) => issue.code === "needs_manual_handling");
  }
  return issues.some((issue) => issue.code === filter);
}

export function issueTone(code: string): StatusTone {
  if (code === "missing_page" || code === "page_count_mismatch" || code === "no_pages" || code === "ocr_failed") {
    return "danger";
  }
  if (code === "needs_manual_handling" || code === "blurry" || code === "duplicate_page") {
    return "warning";
  }
  return "neutral";
}

