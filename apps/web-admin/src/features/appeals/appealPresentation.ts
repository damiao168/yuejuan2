import type { Appeal } from "../../api/appeals";
import type { StatusTone } from "../../types";

export const statusLabels: Record<string, string> = { submitted: "待分派", under_review: "复核中", need_more_info: "继续复核", accepted: "申诉成立", rejected: "维持原分", score_adjusted: "已调整分数", closed: "已完成" };
const reasonLabels: Record<string, string> = { recognition_error: "答题内容识别不完整", missing_step_credit: "作答步骤疑似漏评", rubric_disagreement: "评分标准适用有异议", calculation_error: "得分记录或合计有误", annotation_issue: "批注与实际扣分不一致", other: "其他明确评分问题" };
export const recommendationLabels: Record<string, string> = { accept: "申诉成立", reject: "维持原分", adjust_score: "建议改分", need_more_info: "需继续复核" };
export const recommendationOptions = Object.entries(recommendationLabels).map(([value, label]) => ({ value, label }));

export function formatTime(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
}

export function formatScore(value?: number | null) {
  return value === undefined || value === null || !Number.isFinite(value) ? "-" : Number(value.toFixed(2)).toString();
}

export function statusTone(status: string): StatusTone {
  if (["accepted", "score_adjusted", "closed"].includes(status)) return "success";
  if (status === "rejected") return "neutral";
  if (["submitted", "need_more_info"].includes(status)) return "warning";
  return "processing";
}

export function readableReason(value: string) {
  const text = value.trim();
  if (reasonLabels[text]) return reasonLabels[text];
  const lower = text.toLowerCase();
  if (lower.includes("grading does not match") || lower.includes("published rubric")) return "评分标准适用有异议";
  if (lower.includes("ocr") || lower.includes("recognition")) return "答题内容识别不完整";
  if (lower.includes("missing") || lower.includes("step")) return "作答步骤疑似漏评";
  if (lower.includes("calculation") || lower.includes("total")) return "得分记录或合计有误";
  return text || "其他明确评分问题";
}

export function readableReasonDetail(value: string) {
  if (value.trim().toLowerCase().includes("grading does not match the published rubric")) return "学生认为本题评分未按已发布的评分标准执行，请核对作答过程与各评分点。";
  return value.trim();
}

export function evidenceNumber(appeal: Appeal | null, key: string) {
  const value = appeal?.evidence?.final_grade?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function rubricScore(item: Record<string, unknown>) {
  if (typeof item.score === "number") return item.score;
  if (typeof item.points === "number") return item.points;
  return undefined;
}

export function rubricItems(appeal: Appeal | null) {
  const points = appeal?.evidence?.rubric?.points;
  if (!Array.isArray(points)) return [];
  return points.flatMap((point, index) => {
    if (!point || typeof point !== "object") return [];
    const item = point as Record<string, unknown>;
    const label = [item.description, item.name, item.code].find((value): value is string => typeof value === "string" && Boolean(value.trim())) ?? `评分点 ${index + 1}`;
    return [{ label, score: rubricScore(item) }];
  });
}

export function assignmentProgress(appeal: Appeal) {
  if (!appeal.assigned_to) return "待分派";
  return appeal.teacher_recommendation ? "已提交复核意见" : "待教师反馈";
}
