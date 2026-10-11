import type { QualityCheckResult, RosterReport } from "../../api/scores";
import type { Submission } from "../../api/submissions";
import { examStatusLabels } from "../../constants/examStatus";
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

export function rosterStatusTone(status: string): StatusTone {
  if (status === "graded") return "success";
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
    description: "核对成绩与发布门禁后，分别管理基础成绩查询和学生门户版本。", summaryTitle: "基础成绩检查",
    emptyGrades: "该考试暂无成绩。请先完成阅卷，再点击『汇总最终成绩』生成成绩。"
  };
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
  return "门户无当前版本";
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
