import type { QualityCheckResult, RosterReport } from "../../api/scores";
import type { Submission } from "../../api/submissions";
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
  absent: "已标记缺考",
  unmatched: "未匹配待处理",
  missing_pages: "缺页待处理"
};

export const rosterResolutionLabels: Record<string, string> = {
  graded: "成绩已汇总",
  absent: "已人工确认缺考",
  missing_submission: "未收到答卷（请核查缺考或漏扫）",
  grading_incomplete: "答卷已匹配，评分尚未完成",
  duplicate_submission: "同一学生关联多份答卷",
  student_unidentified: "答卷尚未关联学生",
  student_not_in_roster: "答卷学生不在本场应考名册",
  absent_has_submission: "已标记缺考但存在答卷",
  missing_pages: "实收页数少于应收页数或质量检查失败"
};

export function formatScore(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) return "-";
  return Number(value.toFixed(1)).toString();
}

export function statusTone(status: string): StatusTone {
  if (status === "published" || status === "locked") return "success";
  if (status === "confirmed" || status === "pending_publish") return "processing";
  if (status === "pending_confirmation") return "warning";
  return "neutral";
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
