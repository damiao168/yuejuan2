import { useState } from "react";
import { Button } from "antd";
import { ArrowRight, Check, CircleAlert, CircleCheck, Clock3, Info } from "lucide-react";
import type {
  DashboardActiveExam,
  DashboardActivity,
  DashboardAppealStatistics,
  DashboardGradingProgress,
  DashboardOrganizationStatistics,
  DashboardReviewTeacher,
  DashboardReviewTeacherSummary,
  DashboardStatistics
} from "../../api/dashboard";
import { examSubjectLabel } from "../../constants/examStatus";
import { ReviewTeacherProgress } from "./ReviewTeacherProgress";
import {
  buildSchoolDashboardModel,
  examAction,
  examStageSteps,
  formatCount,
  type ExamProgressRow,
  type ExamProgressView,
  type GradingProgressView,
  type GradingQuestionRow,
  type WorkbenchStatus,
  type WorkItemRow
} from "./schoolDashboard.model";

type Navigate = (path: string) => void;

const statusIcons = { danger: CircleAlert, warning: Clock3, success: CircleCheck, neutral: Info } as const;

function StatusConclusion({ status }: Readonly<{ status: WorkbenchStatus }>) {
  const Icon = statusIcons[status.tone];
  return (
    <section className={`school-dashboard-status is-${status.tone}`} aria-label="当前状态">
      <Icon size={18} aria-hidden="true" />
      <div>
        <p className="school-dashboard-status-text">{status.text}</p>
        {status.note ? <p className="school-dashboard-status-note">{status.note}</p> : null}
      </div>
    </section>
  );
}

function WorkItemsSection({ rows, onNavigate }: Readonly<{ rows: WorkItemRow[]; onNavigate: Navigate }>) {
  return (
    <section className="school-dashboard-section work-items-section" aria-labelledby="school-work-items-title">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><div><h2 id="school-work-items-title">需要我处理</h2></div></div>
      </div>
      {rows.length ? (
        <div className="dashboard-table-wrap">
          <table className="dashboard-operation-table work-item-table">
            <thead><tr><th scope="col">事项</th><th scope="col">考试</th><th scope="col" className="number-cell">数量</th><th scope="col">影响</th><th scope="col">操作</th></tr></thead>
            <tbody>{rows.map(row => (
              <tr key={row.key}>
                {/* 级别圆点只是视觉提示；顺序和“影响”列已说明轻重，读屏不重复朗读。 */}
                <th scope="row"><span className={`work-item-indicator ${row.level}`} aria-hidden="true" />{row.label}</th>
                <td>{row.examLabel}</td>
                <td className="number-cell"><strong>{formatCount(row.count)}</strong> {row.unit}</td>
                <td className="secondary-cell">{row.impact}</td>
                <td><button type="button" className="table-action-link" onClick={() => onNavigate(row.path)}>{row.action}<ArrowRight size={14} aria-hidden="true" /></button></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      ) : (
        <div className="dashboard-inline-empty"><Check size={16} aria-hidden="true" /><span><strong>当前没有需要处理的事</strong><small>新的答卷异常、复核申请和发布任务会显示在这里。</small></span></div>
      )}
    </section>
  );
}

function stepState(index: number, current: number) {
  if (index < current) return "is-done";
  return index === current ? "is-current" : "is-todo";
}

function ExamStageSteps({ stepIndex }: Readonly<{ stepIndex: number | null }>) {
  // 未知状态不猜所在步骤，只说明仍在进行。
  if (stepIndex === null) return <span className="exam-stage-unknown">进行中</span>;
  return (
    <ol className="exam-stage-steps" aria-label="考试阶段">
      {examStageSteps.map((label, index) => (
        <li key={label} className={stepState(index, stepIndex)} aria-current={index === stepIndex ? "step" : undefined}>
          {label}{index < stepIndex ? <span className="dashboard-sr-only">（已完成）</span> : null}
        </li>
      ))}
    </ol>
  );
}

function ExamProgressTableRow({ row, onNavigate }: Readonly<{ row: ExamProgressRow; onNavigate: Navigate }>) {
  const { exam } = row;
  const action = examAction(exam);
  return (
    <tr>
      <th scope="row">
        <button type="button" className="table-primary-link" title={exam.name} onClick={() => onNavigate(`/exams/${encodeURIComponent(exam.id)}/overview`)}>{exam.name}</button>
        {exam.subject ? <small className="exam-progress-subject">{examSubjectLabel(exam.subject)}</small> : null}
      </th>
      <td><ExamStageSteps stepIndex={row.stepIndex} /></td>
      <td>{row.progressText}</td>
      <td className={`number-cell ${row.issueCount ? "has-issue" : ""}`}>{formatCount(row.issueCount)}</td>
      <td><button type="button" className="table-action-link" onClick={() => onNavigate(action.path)}>{action.label}<ArrowRight size={14} aria-hidden="true" /></button></td>
    </tr>
  );
}

function ExamProgressSection({ view, onNavigate }: Readonly<{ view: ExamProgressView; onNavigate: Navigate }>) {
  return (
    <section className="school-dashboard-section exam-progress-section" aria-labelledby="school-exam-progress-title">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><div><h2 id="school-exam-progress-title">考试进度</h2></div></div>
        <Button type="link" onClick={() => onNavigate("/exams")}>全部考试 <ArrowRight size={14} aria-hidden="true" /></Button>
      </div>
      {view.rows.length ? (
        <div className="dashboard-table-wrap">
          <table className="dashboard-operation-table exam-progress-table">
            <thead><tr><th scope="col">考试</th><th scope="col">阶段</th><th scope="col">当前进度</th><th scope="col" className="number-cell">异常</th><th scope="col">下一步</th></tr></thead>
            <tbody>{view.rows.map(row => <ExamProgressTableRow key={row.exam.id} row={row} onNavigate={onNavigate} />)}</tbody>
          </table>
        </div>
      ) : null}
      {view.empty ? (
        <div className="dashboard-inline-empty"><Check size={16} aria-hidden="true" /><span><strong>{view.empty.title}</strong>{view.empty.description ? <small>{view.empty.description}</small> : null}</span></div>
      ) : null}
      {/* 考试列表不读取地址里的状态筛选，这里再放入口只会和标题右侧“全部考试”重复，所以只给提示文字。 */}
      {view.preparingCount ? (
        <div className="exam-preparing-line">
          <span>{`另有 ${formatCount(view.preparingCount)} 场考试在准备中`}</span>
        </div>
      ) : null}
    </section>
  );
}

const collapsedQuestionRows = 5;

function GradingQuestionProgress({ rows, windowLabel }: Readonly<{ rows: GradingQuestionRow[]; windowLabel?: string }>) {
  const [expanded, setExpanded] = useState(false);
  const visible = expanded ? rows : rows.slice(0, collapsedQuestionRows);
  return (
    <section className="dashboard-subsection grading-question-progress" aria-labelledby="grading-question-progress-title">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><div><h3 id="grading-question-progress-title">按题</h3></div></div>
      </div>
      <div className="dashboard-table-wrap">
        <table className="dashboard-operation-table grading-question-table">
          <thead><tr>
            <th scope="col">考试</th><th scope="col">题号</th><th scope="col">进度</th><th scope="col" className="number-cell">剩余</th>
            <th scope="col" className="number-cell">{windowLabel ? `${windowLabel}已阅` : "近期已阅"}</th><th scope="col">预计剩余时间</th>
          </tr></thead>
          <tbody>{visible.map(row => (
            <tr key={row.key}>
              <td>{row.examName}</td>
              <td>{row.questionNo}</td>
              <td>
                <div className="grading-question-completion">
                  <span>{formatCount(row.doneCount)}/{formatCount(row.taskCount)}</span>
                  {row.taskCount > 0 ? <progress value={row.doneCount} max={row.taskCount} aria-label={`${row.examName} 第 ${row.questionNo} 题阅卷进度`} /> : null}
                </div>
              </td>
              <td className="number-cell">{formatCount(row.remainingCount)}</td>
              <td className="number-cell">{formatCount(row.recentDoneCount)}</td>
              <td>{row.etaText}</td>
            </tr>
          ))}</tbody>
        </table>
      </div>
      {rows.length > collapsedQuestionRows ? (
        <Button type="link" className="grading-question-toggle" onClick={() => setExpanded(value => !value)}>{expanded ? "收起" : "展开全部"}</Button>
      ) : null}
      <p className="dashboard-footnote">按小题阅卷任务计数，双评时每位教师各计一项；预计时间按近 1 小时速度粗略估算，速度变化时会偏差。</p>
    </section>
  );
}

function GradingProgressSection({ view, reviewTeachers, reviewTeacherSummary }: Readonly<{
  view: GradingProgressView;
  reviewTeachers?: DashboardReviewTeacher[];
  reviewTeacherSummary?: DashboardReviewTeacherSummary;
}>) {
  if (!view.visible) return null;
  return (
    <section className="school-dashboard-section grading-progress-section" aria-labelledby="school-grading-progress-title">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><div><h2 id="school-grading-progress-title">阅卷进度</h2></div></div>
      </div>
      {/* 阅卷任务来源失败或旧后端没有 grading_progress 时不显示按题；“阅卷待办暂时无法统计”告警由页面统一展示。 */}
      {view.questions?.length ? <GradingQuestionProgress rows={view.questions} windowLabel={view.windowLabel} /> : null}
      {reviewTeacherSummary ? <ReviewTeacherProgress teachers={reviewTeachers ?? []} summary={reviewTeacherSummary} /> : null}
    </section>
  );
}

function RecentActivitySection({ activities, onNavigate }: Readonly<{ activities: DashboardActivity[]; onNavigate: Navigate }>) {
  if (!activities.length) return null;
  return (
    <section className="school-dashboard-section recent-activity-section" aria-labelledby="school-recent-activity-title">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><div><h2 id="school-recent-activity-title">最近动态</h2><p>仅展示会影响考试推进的重要变化</p></div></div>
      </div>
      <div className="dashboard-link-list">
        {activities.slice(0, 5).map(activity => (
          <button type="button" key={activity.id} onClick={() => activity.drilldown_path && onNavigate(activity.drilldown_path)} disabled={!activity.drilldown_path}>
            <Clock3 size={15} aria-hidden="true" />
            <span>
              <strong>{activity.title || "考试进展已更新"}</strong>
              <small>{activity.summary || "查看相关考试的最新状态"} · {new Date(activity.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })}</small>
            </span>
            {activity.drilldown_path ? <ArrowRight size={14} aria-hidden="true" /> : null}
          </button>
        ))}
      </div>
    </section>
  );
}

export interface SchoolDashboardProps {
  statistics: DashboardStatistics;
  organizationStatistics?: DashboardOrganizationStatistics;
  exams: DashboardActiveExam[];
  gradingProgress?: DashboardGradingProgress | null;
  appealStatistics?: DashboardAppealStatistics;
  warnings?: readonly string[];
  // 动态来源失败时后端的 nil 切片会序列化为 null，不能直接读 length。
  activities?: DashboardActivity[] | null;
  canManageOrganization?: boolean;
  /** 只对学校管理员传入；其他身份不显示“按老师”。 */
  reviewTeachers?: DashboardReviewTeacher[];
  reviewTeacherSummary?: DashboardReviewTeacherSummary;
  onNavigate: Navigate;
}

/** 学校工作台首页。统计口径和缺失字段的降级规则在 schoolDashboard.model.ts，这里只负责渲染和跳转。 */
export function SchoolDashboard({
  statistics, organizationStatistics, exams, gradingProgress, appealStatistics, warnings, activities,
  canManageOrganization = false, reviewTeachers, reviewTeacherSummary, onNavigate
}: Readonly<SchoolDashboardProps>) {
  const model = buildSchoolDashboardModel({
    statistics,
    organizationStatistics,
    exams,
    gradingProgress,
    appealStatistics,
    canManageOrganization,
    reviewTeachers,
    hasWarnings: Boolean(warnings?.length)
  });
  return (
    <div className="school-dashboard-sections">
      <StatusConclusion status={model.status} />
      <WorkItemsSection rows={model.workItems} onNavigate={onNavigate} />
      <ExamProgressSection view={model.examProgress} onNavigate={onNavigate} />
      <GradingProgressSection view={model.gradingProgress} reviewTeachers={reviewTeachers} reviewTeacherSummary={reviewTeacherSummary} />
      <RecentActivitySection activities={activities ?? []} onNavigate={onNavigate} />
    </div>
  );
}
