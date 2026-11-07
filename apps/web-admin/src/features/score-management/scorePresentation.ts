import type { QualityCheckResult, RosterReport } from "../../api/scores";
import type { Submission } from "../../api/submissions";
import { examStatusLabels, examSubjectLabel } from "../../constants/examStatus";
import type { ProductExperience } from "../../router/experience";
import type { StatusTone } from "../../types";

export interface ScoreSummary {
  expectedStudents: number;
  receivedSubmissions: number;
  completedGrades: number;
  absentStudents: number;
  unresolvedRoster: number;
  unfinishedReviews: number;
  pendingArbitrations: number;
  ocrFailures: number;
  canPublish: boolean;
}

export const statusLabels: Record<string, string> = {
  calculating: "计算中",
  pending_confirmation: "待确认",
  confirmed: "已确认",
  pending_publish: "待发布",
  published: "已发布",
  locked: "已锁定"
};

export const sourceLabels: Record<string, string> = {
  single_review: "人工单评",
  rule_auto: "规则自动",
  arbitration: "仲裁",
  average: "双评平均",
  first: "第一评",
  second: "第二评",
  higher: "高分优先",
  lower: "低分优先"
};

export const rosterStatusLabels: Record<string, string> = {
  graded: "已评分",
  submitted: "已交卷",
  absent: "缺考",
  unmatched: "未匹配待处理",
  missing_pages: "缺页待处理"
};

export const rosterResolutionLabels: Record<string, string> = {
  graded: "成绩已汇总",
  absent: "未匹配到答卷，自动按缺考处理",
  missing_submission: "未收到匹配答卷，请核查漏扫或未识别答卷",
  grading_incomplete: "已交卷，仍有小题未完成评分",
  awaiting_aggregation: "评分已完成，发布时自动汇总总分",
  duplicate_submission: "同一学生关联多份答卷",
  student_unidentified: "答卷尚未关联学生",
  student_identity_unresolved: "答卷身份待核对",
  student_not_in_roster: "答卷学生不在本场应考名册",
  absent_has_submission: "已收到答卷，请重新核对匹配状态",
  missing_pages: "实收页数少于应收页数或质量检查失败"
};

export function formatScore(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) return "-";
  return Number(value.toFixed(2)).toString();
}

export function statusTone(status: string): StatusTone {
  if (status === "published" || status === "locked") return "success";
  if (status === "confirmed" || status === "pending_publish") return "processing";
  if (status === "pending_confirmation") return "warning";
  return "neutral";
}

export function rosterStatusTone(status: string): StatusTone {
  if (status === "graded") return "success";
  if (status === "submitted") return "processing";
  if (status === "absent") return "neutral";
  return "danger";
}

export function rosterReconciliationStatus(unresolved: number): { tone: StatusTone; label: string } {
  if (unresolved === 0) return { tone: "success", label: "对账完成" };
  return { tone: "danger", label: `${unresolved} 项待处理` };
}

export function scoreWorkspacePresentation(mode: ProductExperience) {
  if (mode === "teacher") {
    return {
      shellClass: "score-shell read-only", workspaceClass: "score-workspace read-only", title: "班级成绩",
      description: "查看当前授权考试的班级成绩与阅卷完成情况。", summaryTitle: "当前状态",
      emptyGrades: "该考试暂无成绩，请等待阅卷完成。"
    };
  }
  return {
    shellClass: "score-shell", workspaceClass: "score-workspace", title: "成绩发布",
    description: "选择考试，处理完待办后发布成绩，学生即可查看。", summaryTitle: "基础成绩检查",
    emptyGrades: "该考试还没有汇总的成绩。阅卷完成后，发布时会自动汇总。"
  };
}

export function scoreExamOptionLabel(exam: { name: string; subject: string }): string {
  const subject = examSubjectLabel(exam.subject);
  const parts = exam.name.trim().split(/\s*[·•]\s*/u);
  // 创建多科考试时名称已经带学科，选择器只补一次；保持各考次原有名称和 ID。
  return parts[parts.length - 1] === subject ? exam.name : `${exam.name} · ${subject}`;
}

export type ScorePublicationState = Readonly<{
  examId: string;
  loadingExams: boolean;
  loadingScores: boolean;
  error: string | null;
  examStatus?: string;
  releaseVersion?: number;
}>;

// 重读期间不能把保留的旧快照显示成当前发布事实；基础成绩和门户版本分别判断。
export function basePublicationLabel(state: ScorePublicationState): string {
  if (!state.examId) return "请选择考试";
  if (state.loadingExams || state.loadingScores) return "正在核对";
  if (state.error) return "状态暂不可用";
  if (state.examStatus === "published") return "已公开并锁定";
  return "尚未公开";
}

export function portalPublicationLabel(state: ScorePublicationState): string {
  if (!state.examId) return "请选择考试";
  if (state.loadingScores) return "正在核对";
  if (state.error) return "状态暂不可用";
  if (state.releaseVersion !== undefined) return `当前版本 V${state.releaseVersion}`;
  return "尚未向学生发布";
}

export function summaryStatus(
  mode: ProductExperience,
  examStatus: string | undefined,
  publishedOrLocked: boolean,
  canPublish: boolean
): { tone: StatusTone; label: string } {
  // 教师只查看考试流程状态，不能把管理员的发布门禁状态当成可执行权限。
  if (mode === "teacher") {
    return { tone: "neutral", label: examStatus === undefined ? "未选择" : examStatusLabels[examStatus] ?? "未知状态" };
  }
  if (publishedOrLocked) return { tone: "success", label: "已锁定" };
  if (canPublish) return { tone: "success", label: "可公开" };
  return { tone: "danger", label: "待处理" };
}

export function qualityCheckStatus(quality: QualityCheckResult | null): { tone: StatusTone; label: string } | null {
  if (!quality) return null;
  if (quality.quality.passed) return { tone: "success", label: "检查通过" };
  return { tone: "danger", label: `${quality.quality.issues.filter((issue) => issue.blocking).length} 项须处理` };
}

function issueCount(quality: QualityCheckResult | null, code: string) {
  return quality?.quality.issues.find((issue) => issue.code === code)?.count ?? 0;
}

// 页面人数优先采用名册汇总；能否发布始终以服务端质量门禁结果为准。
export function createSummary(
  submissions: Submission[],
  completedGradeCount: number,
  quality: QualityCheckResult | null,
  roster: RosterReport | null
): ScoreSummary {
  return {
    expectedStudents: roster?.summary.expected ?? submissions.length,
    receivedSubmissions: roster?.summary.received ?? submissions.length,
    completedGrades: completedGradeCount,
    absentStudents: roster?.summary.absent ?? 0,
    unresolvedRoster: roster?.summary.unresolved ?? 0,
    unfinishedReviews: issueCount(quality, "unfinished_review_tasks"),
    pendingArbitrations: issueCount(quality, "unfinished_arbitration_tasks"),
    ocrFailures: issueCount(quality, "ocr_failed_unhandled"),
    canPublish: Boolean(quality?.can_publish)
  };
}
