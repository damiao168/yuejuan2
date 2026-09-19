import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { isFormulaEvidenceSubject } from "../grading/workbench/mathEvidenceSubjects";
import type { StatusTone } from "../../types";

export const rubricStatusOptions = [
  { label: "草稿", value: "draft" },
  { label: "待审批", value: "pending_review" },
  { label: "已批准", value: "approved" },
  { label: "锁定", value: "locked" }
];

const formulaEvidenceQuestionTypes = new Set(["formula", "calculation", "numeric"]);

export function labelFrom(options: { label: string; value: string }[], value?: string) {
  return options.find((item) => item.value === value)?.label ?? (value ? "未知类型" : "-");
}

export function formatPaperError(error: unknown) {
  if (error instanceof ApiClientError) console.error("请求失败", error.status, error.code, error.message);
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function rubricStatusLabel(status?: string) {
  if (!status) return "未配置";
  return rubricStatusOptions.find((item) => item.value === status)?.label ?? "未知状态";
}

export function rubricTone(status?: string): StatusTone {
  if (status === "locked" || status === "approved") return "success";
  if (status === "pending_review") return "warning";
  return "neutral";
}

export function formulaEvidenceEnabled(subject: string | undefined, questionType: string | undefined) {
  return isFormulaEvidenceSubject(subject) && formulaEvidenceQuestionTypes.has(questionType ?? "");
}
