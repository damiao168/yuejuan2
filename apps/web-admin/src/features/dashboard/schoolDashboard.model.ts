import type {
  DashboardActiveExam,
  DashboardAppealStatistics,
  DashboardGradingProgress,
  DashboardOrganizationStatistics,
  DashboardReviewTeacher,
  DashboardStatistics
} from "../../api/dashboard";

/*
 * 学校工作台的纯数据模型：把 /api/v1/dashboard/summary 整理成首页各区块的行和文案，界面口径见
 * docs/stories/STORY-080-school-workbench-priorities.md。STORY-080 的新字段在旧后端缺失、来源失败时为 null，
 * 两者都表示“未知”：只能隐藏行或去掉分母，不能当成 0 显示。
 */

export function formatCount(value: number): string {
  return value.toLocaleString("zh-CN");
}

function isPositive(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value) && value > 0;
}

/** 异常 = 处理失败 + 图像质量 + 未匹配学生。后端三类独立计数，同一份答卷可能计入多类，所以总和可能大于答卷份数。 */
export function examIssueCount(exam: DashboardActiveExam): number {
  return exam.failed_count + exam.quality_issue_count + exam.unmatched_count;
}

export function examAction(exam: DashboardActiveExam) {
  const encodedId = encodeURIComponent(exam.id);
  const issueCount = examIssueCount(exam);
  if (["draft", "configured"].includes(exam.status)) {
    return { label: "完善试卷", path: `/exams/${encodedId}/paper` };
  }
  if (["ready", "preparation_confirmed"].includes(exam.status)) return { label: "导入答题卡", path: `/exams/${encodedId}/capture` };
  if (["collecting", "processing"].includes(exam.status)) {
    return { label: issueCount ? "处理答卷" : "继续导入", path: `/exams/${encodedId}/capture` };
  }
  if (exam.status === "reviewing") return { label: "处理人工复核", path: `/exams/${encodedId}/grading` };
  if (exam.status === "grading") {
    return { label: "继续阅卷", path: `/exams/${encodedId}/grading` };
  }
  if (exam.status === "finalized") return { label: "发布成绩", path: `/exams/${encodedId}/scores` };
  if (exam.status === "published") return { label: "查看成绩", path: `/exams/${encodedId}/scores` };
  return { label: "查看考试", path: `/exams/${encodedId}/overview` };
}

/** blocking 会卡住出成绩，waiting 是学生在等，releasable 可以发布，foundation 是基础数据缺口（不计入状态结论）。 */
export type WorkItemLevel = "blocking" | "waiting" | "releasable" | "foundation";

export interface WorkItemRow {
  key: string;
  label: string;
  count: number;
  unit: string;
  examLabel: string;
  impact: string;
  action: string;
  path: string;
  level: WorkItemLevel;
}

type WorkItemCopy = Omit<WorkItemRow, "count" | "examLabel" | "path">;

const workItemCopy = {
  failed: { key: "failed", label: "答卷处理失败", unit: "份", impact: "会阻断后续阅卷", action: "查看并重试", level: "blocking" },
  unmatched: { key: "unmatched", label: "答卷未匹配学生", unit: "份", impact: "无法计入学生成绩", action: "确认学生身份", level: "blocking" },
  quality: { key: "quality", label: "图像质量需要处理", unit: "份", impact: "可能影响文字识别和切题", action: "查看质量问题", level: "blocking" },
  arbitration: { key: "arbitration", label: "待人工复核", unit: "项", impact: "未确认最终得分", action: "开始复核", level: "blocking" },
  appealAssignment: { key: "appeal-assignment", label: "成绩复核申请待分派", unit: "条", impact: "学生在等复核结果", action: "去分派", level: "waiting" },
  appealDecision: { key: "appeal-decision", label: "成绩复核待裁决", unit: "条", impact: "复核教师已提交意见", action: "去裁决", level: "waiting" },
  finalized: { key: "finalized", label: "成绩待发布", unit: "场", impact: "阅卷已完成，检查后即可发布", action: "去发布", level: "releasable" },
  emptyClass: { key: "empty-class", label: "班级没有学生", unit: "个班", impact: "这些班级的答卷无法匹配学生", action: "导入学生", level: "foundation" },
  unassignedTeacher: { key: "unassigned-teacher", label: "教师未设置任教学科", unit: "名", impact: "按学科查看阅卷进度时看不到这些教师", action: "设置学科", level: "foundation" }
} satisfies Record<string, WorkItemCopy>;

export interface WorkItemSource {
  statistics: DashboardStatistics;
  exams: readonly DashboardActiveExam[];
  appealStatistics?: DashboardAppealStatistics;
  organizationStatistics?: DashboardOrganizationStatistics;
  /** 持有 org:manage 时才显示两行基础数据。 */
  canManageOrganization: boolean;
}

/*
 * 与考试相关的行：数量取 statistics 总数；“考试”列和单场/多场入口按 active_exams 的每场数量判断。
 * 两者口径都只含未发布、未归档的考试，所以涉及场数就是每场数量大于 0 的考试数。
 * 旧后端没有每场字段（如 pending_arbitration_count）时，无法判断涉及哪几场，“考试”列显示“—”并使用多场入口。
 */
function examScopedRow(
  copy: WorkItemCopy,
  total: number | undefined,
  exams: readonly DashboardActiveExam[],
  countOf: (exam: DashboardActiveExam) => number | null | undefined,
  singlePath: (encodedExamId: string) => string,
  multiPath: string
): WorkItemRow | undefined {
  if (!isPositive(total)) return undefined;
  let involved = 0;
  let top: { exam: DashboardActiveExam; count: number } | undefined;
  for (const exam of exams) {
    const count = countOf(exam);
    if (!isPositive(count)) continue;
    involved += 1;
    // 数量相同时保留列表中靠前的一场（后端按创建时间倒序），刷新时考试名不会来回跳。
    if (!top || count > top.count) top = { exam, count };
  }
  if (involved === 1 && top) {
    return { ...copy, count: total, examLabel: top.exam.name, path: singlePath(encodeURIComponent(top.exam.id)) };
  }
  const examLabel = top ? `${top.exam.name} 等 ${involved} 场` : "—";
  return { ...copy, count: total, examLabel, path: multiPath };
}

function plainRow(copy: WorkItemCopy, total: number | undefined, path: string): WorkItemRow | undefined {
  return isPositive(total) ? { ...copy, count: total, examLabel: "—", path } : undefined;
}

/** “需要我处理”的行，固定按“阻断出成绩、学生在等、可以发布、基础数据缺口”排列；数量为 0 或未知的行不返回。 */
export function buildWorkItems(source: WorkItemSource): WorkItemRow[] {
  const { statistics, exams, appealStatistics, organizationStatistics } = source;
  const rows = [
    examScopedRow(workItemCopy.failed, statistics.failed_submission_count, exams, exam => exam.failed_count, id => `/exams/${id}/capture`, "/capture?issue=failed"),
    examScopedRow(workItemCopy.unmatched, statistics.unmatched_submission_count, exams, exam => exam.unmatched_count, id => `/exams/${id}/capture`, "/capture?issue=unmatched"),
    examScopedRow(workItemCopy.quality, statistics.quality_issue_submission_count, exams, exam => exam.quality_issue_count, id => `/exams/${id}/capture`, "/capture?issue=quality"),
    examScopedRow(workItemCopy.arbitration, statistics.pending_arbitration_count, exams, exam => exam.pending_arbitration_count, () => "/arbitration?status=pending", "/arbitration?status=pending"),
    // 申诉发生在成绩发布之后，没有按考试拆分的数量，“考试”列固定为“—”；没有 appeal_statistics 时两行都不出现。
    plainRow(workItemCopy.appealAssignment, appealStatistics?.pending_assignment_count, "/appeals"),
    plainRow(workItemCopy.appealDecision, appealStatistics?.pending_decision_count, "/appeals"),
    examScopedRow(workItemCopy.finalized, statistics.finalized_exam_count, exams, exam => (exam.status === "finalized" ? 1 : 0), id => `/exams/${id}/scores`, "/scores?status=finalized"),
    // 首页不再常驻成员卡片；基础数据缺口只提示能处理它的人（org:manage）。
    ...(source.canManageOrganization ? [
      plainRow(workItemCopy.emptyClass, organizationStatistics?.empty_class_count, "/members/students"),
      plainRow(workItemCopy.unassignedTeacher, organizationStatistics?.unassigned_teacher_count, "/members/teachers")
    ] : [])
  ];
  return rows.filter((row): row is WorkItemRow => row !== undefined);
}

export type WorkbenchStatusTone = "danger" | "warning" | "success" | "neutral";

export interface WorkbenchStatus {
  tone: WorkbenchStatusTone;
  text: string;
  /** warnings 非空时的第二行。 */
  note?: string;
}

function workItemPhrase(row: WorkItemRow): string {
  return `${formatCount(row.count)} ${row.unit}${row.label}`;
}

function leadingPhrases(rows: readonly WorkItemRow[]): string {
  return rows.slice(0, 2).map(workItemPhrase).join("，") + (rows.length > 2 ? "等" : "");
}

function primaryConclusion(rows: readonly WorkItemRow[], gradingExamCount: number | undefined): WorkbenchStatus {
  const blocking = rows.filter(row => row.level === "blocking");
  if (blocking.length) return { tone: "danger", text: `${blocking.length} 件事会卡住出成绩：${leadingPhrases(blocking)}` };
  // 基础数据缺口不影响本次出成绩，不进入结论。
  const pending = rows.filter(row => row.level === "waiting" || row.level === "releasable");
  if (pending.length) return { tone: "warning", text: `有 ${pending.length} 件事等你处理：${leadingPhrases(pending)}` };
  const grading = isPositive(gradingExamCount) ? `，${formatCount(gradingExamCount)} 场考试正在阅卷` : "";
  return { tone: "success", text: `目前没有卡住出成绩的事${grading}` };
}

/** 状态结论按行数计数，不按份数相加；有告警时部分来源缺失，结论不能显示成绿色。 */
export function buildStatusConclusion(rows: readonly WorkItemRow[], options: Readonly<{ hasWarnings: boolean; gradingExamCount?: number }>): WorkbenchStatus {
  const conclusion = primaryConclusion(rows, options.gradingExamCount);
  if (!options.hasWarnings) return conclusion;
  return {
    ...conclusion,
    tone: conclusion.tone === "success" ? "neutral" : conclusion.tone,
    note: "部分统计暂时不可用，结论可能不完整。"
  };
}

export const examStageSteps = ["试卷", "采集", "阅卷", "复核", "发布"] as const;

// 准备中的考试还没有答卷，首页收成一行。这里按状态自己计数：statistics.preparing_exam_count 不含 preparation_confirmed。
const preparingStatuses = new Set(["draft", "configured", "ready", "preparation_confirmed"]);
// active_exams 本应不含已发布、已归档；若出现也不进表格，避免把它们当成未知状态显示。
const closedStatuses = new Set(["published", "archived"]);
const stepIndexByStatus = new Map<string, number>([
  ["collecting", 1], ["processing", 1], ["grading", 2], ["reviewing", 3], ["finalized", 4]
]);
const examProgressRowLimit = 6;

export interface ExamProgressRow {
  exam: DashboardActiveExam;
  /** examStageSteps 中当前步的下标；未知状态为 null，按“进行中”显示并排在最后。 */
  stepIndex: number | null;
  progressText: string;
  issueCount: number;
  needsAttention: boolean;
}

export interface ExamProgressView {
  rows: ExamProgressRow[];
  preparingCount: number;
  /** 表格没有行时的空状态文案。 */
  empty?: { title: string; description?: string };
}

function examProgressText(exam: DashboardActiveExam, stepIndex: number | null): string {
  if (stepIndex === 1) {
    // roster_count 为 null（名册来源不可用）或 0（尚无冻结名册）时没有可信分母，只显示已收份数。
    if (isPositive(exam.roster_count) && typeof exam.roster_submitted_count === "number") {
      return `已收 ${formatCount(exam.roster_submitted_count)}/${formatCount(exam.roster_count)} 人`;
    }
    return `已收 ${formatCount(exam.submission_count)} 份`;
  }
  if (stepIndex === 2 || stepIndex === 3) {
    // 旧后端不返回任务数时同样显示“暂无阅卷任务”，不显示成“已阅 0/0 项”。
    const reviewed = isPositive(exam.review_task_count) && typeof exam.review_done_count === "number"
      ? `已阅 ${formatCount(exam.review_done_count)}/${formatCount(exam.review_task_count)} 项`
      : "暂无阅卷任务";
    const arbitration = isPositive(exam.pending_arbitration_count) ? `，待复核 ${formatCount(exam.pending_arbitration_count)} 项` : "";
    return reviewed + arbitration;
  }
  if (stepIndex === 4) return "阅卷完成，待发布";
  return "—";
}

// 离发布越近排越前：待发布、复核、阅卷、采集；未知状态最后。
function stageRank(stepIndex: number | null): number {
  return stepIndex === null ? examStageSteps.length : examStageSteps.length - 1 - stepIndex;
}

function createdTime(exam: DashboardActiveExam): number {
  const time = Date.parse(exam.created_at);
  return Number.isNaN(time) ? 0 : time;
}

/** 有异常或待人工复核的在前；再按离发布近的在前；再按创建时间倒序。 */
function compareExamProgress(left: ExamProgressRow, right: ExamProgressRow): number {
  return Number(right.needsAttention) - Number(left.needsAttention)
    || stageRank(left.stepIndex) - stageRank(right.stepIndex)
    || createdTime(right.exam) - createdTime(left.exam);
}

function examProgressEmpty(rowCount: number, preparingCount: number): ExamProgressView["empty"] {
  if (rowCount) return undefined;
  if (preparingCount) return { title: "暂无采集中或阅卷中的考试" };
  return { title: "暂无进行中的考试", description: "创建考试后，可从这里直接继续下一步。" };
}

export function buildExamProgress(exams: readonly DashboardActiveExam[]): ExamProgressView {
  const preparingCount = exams.filter(exam => preparingStatuses.has(exam.status)).length;
  const rows = exams
    .filter(exam => !preparingStatuses.has(exam.status) && !closedStatuses.has(exam.status))
    .map((exam): ExamProgressRow => {
      const stepIndex = stepIndexByStatus.get(exam.status) ?? null;
      const issueCount = examIssueCount(exam);
      return {
        exam,
        stepIndex,
        progressText: examProgressText(exam, stepIndex),
        issueCount,
        needsAttention: issueCount > 0 || isPositive(exam.pending_arbitration_count)
      };
    })
    .sort(compareExamProgress);
  return { rows: rows.slice(0, examProgressRowLimit), preparingCount, empty: examProgressEmpty(rows.length, preparingCount) };
}

export interface GradingQuestionRow {
  key: string;
  examName: string;
  questionNo: string;
  taskCount: number;
  doneCount: number;
  remainingCount: number;
  recentDoneCount: number;
  etaText: string;
}

export interface GradingProgressView {
  /** 某题或某位教师仍有剩余任务时才显示整个“阅卷进度”区块。 */
  visible: boolean;
  /** grading_progress 缺失（旧后端）或为 null（来源失败）时为 undefined，按题表格不显示。 */
  questions?: GradingQuestionRow[];
  windowLabel?: string;
}

export function gradingWindowLabel(windowSeconds: number): string | undefined {
  if (!isPositive(windowSeconds)) return undefined;
  if (windowSeconds % 3600 === 0) return `近 ${windowSeconds / 3600} 小时`;
  if (windowSeconds >= 60) return `近 ${Math.round(windowSeconds / 60)} 分钟`;
  return `近 ${windowSeconds} 秒`;
}

// 时窗内已阅少于 10 项时速度不稳定，不给估计。
const minimumEtaSample = 10;

/**
 * 预计剩余时间：小时数 = 剩余 ÷ (近期已阅 × 3600 ÷ window_seconds)，只是粗略估计（精确 ETA 属于 STORY-076）。
 * 公式改写成一次除法，整数输入落在 1、24 小时边界时不受浮点误差影响。
 */
export function gradingEtaText(remaining: number, recentDone: number, windowSeconds: number): string {
  if (remaining <= 0) return "已完成";
  if (!Number.isFinite(recentDone) || recentDone < minimumEtaSample) return "样本不足";
  if (!isPositive(windowSeconds)) return "—";
  const hours = (remaining * windowSeconds) / (recentDone * 3600);
  if (hours < 1) return "不到 1 小时";
  if (hours <= 24) return `约 ${Math.ceil(hours)} 小时`;
  return "超过 1 天";
}

export function buildGradingQuestionRows(progress: DashboardGradingProgress, exams: readonly DashboardActiveExam[]): GradingQuestionRow[] {
  const examNames = new Map(exams.map(exam => [exam.id, exam.name]));
  // Go 的空切片可能序列化为 null，按没有题目处理。
  return (progress.questions ?? []).map(question => ({
    key: `${question.exam_id}:${question.question_id}`,
    examName: examNames.get(question.exam_id) ?? "—",
    questionNo: String(question.question_no ?? ""),
    taskCount: question.task_count,
    doneCount: question.done_count,
    remainingCount: question.remaining_count,
    recentDoneCount: question.recent_done_count,
    etaText: gradingEtaText(question.remaining_count, question.recent_done_count, progress.window_seconds)
  }))
    // 剩余多的在前；同数时按考试名、题号排，保证每 30 秒刷新后顺序不变。
    .sort((left, right) => right.remainingCount - left.remainingCount
      || left.examName.localeCompare(right.examName, "zh-CN")
      || left.questionNo.localeCompare(right.questionNo, "zh-CN", { numeric: true }));
}

export function buildGradingProgress(
  progress: DashboardGradingProgress | null | undefined,
  exams: readonly DashboardActiveExam[],
  reviewTeachers?: readonly DashboardReviewTeacher[]
): GradingProgressView {
  const questions = progress ? buildGradingQuestionRows(progress, exams) : undefined;
  const questionRemaining = questions?.some(row => row.remainingCount > 0) ?? false;
  // 按老师仅学校管理员可见，调用方对其他身份不传 reviewTeachers。
  const teacherRemaining = reviewTeachers?.some(teacher => teacher.remaining_count > 0) ?? false;
  return {
    visible: questionRemaining || teacherRemaining,
    questions,
    windowLabel: progress ? gradingWindowLabel(progress.window_seconds) : undefined
  };
}

export interface SchoolDashboardSource extends WorkItemSource {
  gradingProgress?: DashboardGradingProgress | null;
  reviewTeachers?: readonly DashboardReviewTeacher[];
  hasWarnings: boolean;
}

export interface SchoolDashboardModel {
  workItems: WorkItemRow[];
  status: WorkbenchStatus;
  examProgress: ExamProgressView;
  gradingProgress: GradingProgressView;
}

export function buildSchoolDashboardModel(source: SchoolDashboardSource): SchoolDashboardModel {
  const workItems = buildWorkItems(source);
  // 旧后端没有 grading_exam_count；active_exams 与它的口径相同（未发布、未归档），可以直接按状态数。
  const gradingExamCount = source.statistics.grading_exam_count
    ?? source.exams.filter(exam => exam.status === "grading" || exam.status === "reviewing").length;
  return {
    workItems,
    status: buildStatusConclusion(workItems, { hasWarnings: source.hasWarnings, gradingExamCount }),
    examProgress: buildExamProgress(source.exams),
    gradingProgress: buildGradingProgress(source.gradingProgress, source.exams, source.reviewTeachers)
  };
}
