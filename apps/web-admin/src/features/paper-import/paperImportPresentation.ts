import type { StatusBadgeTone } from "@edugrade/ui";
import { getPaperImportUserMessage } from "../../api/client";
import type { PaperImportIssue, PaperImportJob, PaperImportSource } from "../../api/papers";
import { hasNoExamContentDetected, isPaperImportCancelled, paperImportReviewIssues } from "./materials";

export const importRoleOptions = [
  { label: "自动判断", value: "auto" },
  { label: "题目", value: "question" },
  { label: "答案", value: "answer" },
  { label: "解析", value: "solution" },
  { label: "评分标准", value: "rubric" },
  { label: "混合资料", value: "mixed" },
  { label: "未知", value: "unknown" }
];

const detectedRoleLabels = {
  question: "题目", answer: "答案", solution: "解析", rubric: "评分标准", mixed: "混合内容", unknown: "识别中"
};

export function importSourceStatus(source: PaperImportSource, cancelled: boolean): { tone: StatusBadgeTone; label: string } {
  if (cancelled) return { tone: "neutral", label: "已停止" };
  if (source.processing_status === "processed") return { tone: "success", label: "已识别" };
  if (source.processing_status === "failed") return { tone: "danger", label: "失败" };
  return { tone: "processing", label: "处理中" };
}

export function importSourceDescription(source: PaperImportSource) {
  const confidence = source.role_confidence
    ? " · 资料类型判断 " + Math.round(source.role_confidence * 100) + "%（不代表逐字准确率）"
    : "";
  return "识别内容：" + (detectedRoleLabels[source.detected_role] ?? "识别中") + confidence;
}

export function isImportStructureConfirmed(job: PaperImportJob) {
  return job.structure_confirmed_generation === job.generation;
}

export function importVisibleIssues(job: PaperImportJob): Pick<PaperImportIssue, "message" | "certainty" | "question_no">[] {
  // 重新识别期间仍保留上一轮题目，但暂不展示旧问题，避免把历史告警当作本轮结果。
  if (isPaperImportCancelled(job) || job.status === "processing") return [];
  if (hasNoExamContentDetected(job)) {
    return (job.structured_issues ?? []).filter((issue) => issue.code === "NO_EXAM_CONTENT_DETECTED");
  }
  if (job.structured_issues?.length) return paperImportReviewIssues(job);
  return (job.issues ?? []).map((message) => ({ message, certainty: "unknown" }));
}

export function importExcludedPageFurniture(job: PaperImportJob) {
  if (isPaperImportCancelled(job) || job.status === "processing") return [];
  return (job.structured_issues ?? []).filter((issue) => issue.code === "PAGE_FURNITURE_EXCLUDED");
}

export function importReviewDescription(job: PaperImportJob) {
  if (isImportStructureConfirmed(job)) return "试卷结构已采用，请到小题与分值核对答案、分值和评分配置";
  if (job.status === "review_required") {
    if (hasNoExamContentDetected(job)) return "未识别到与考试有关的题目、答案、解析或评分标准，请检查是否上传了无关图片或错误文件";
    return "已识别题目 " + (job.question_candidates?.length ?? job.questions.length)
      + "、答案 " + (job.answer_candidates?.length ?? 0)
      + "、解析 " + (job.solution_candidates?.length ?? 0)
      + "、评分标准 " + (job.rubric_candidates?.length ?? 0);
  }
  if (job.status === "applied") return "已确认并写入当前考试";
  if (job.status === "failed") return getPaperImportUserMessage(job.issues[0], "考试资料识别失败，请检查资料后重试");
  if (job.status === "cancelled") return "识别任务已手动停止，已上传资料仍然保留";
  return "正在识别考试资料中的题目、答案与解析";
}

export function importRetainedDraftDescription(status: PaperImportJob["status"]) {
  if (status === "processing") return "本次补充资料正在单独处理；完成后会按题号合并，以下历史结果暂时只读。";
  if (status === "cancelled") return "本次补充识别已停止，不影响以下历史结果。可选择“仅解析本次补充”继续。";
  return "本次补充资料处理失败，不影响以下历史结果。";
}

export function importProgressTiming(startedAt: string | undefined) {
  if (!startedAt) return "";
  return "已处理约 " + Math.max(1, Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000)) + " 秒";
}
