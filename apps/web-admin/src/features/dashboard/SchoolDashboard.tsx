import { Button } from "antd";
import { ArrowRight, Check, Clock3, GraduationCap, UserCog, UsersRound } from "lucide-react";
import type { DashboardActiveExam, DashboardActivity, DashboardOrganizationStatistics, DashboardStatistics } from "../../api/dashboard";
import { StatusTag } from "../../components/StatusTag";
import { examStatusLabels, examStatusTone, examSubjectLabel } from "../../constants/examStatus";
import type { StatusTone } from "../../types";

export interface DashboardWorkItem {
  label: string;
  detail: string;
  value: number;
  unit: string;
  action: string;
  path: string;
  tone: StatusTone;
}

function examAction(exam: DashboardActiveExam) {
  const encodedId = encodeURIComponent(exam.id);
  const issueCount = exam.failed_count + exam.quality_issue_count + exam.unmatched_count;
  if (["draft", "configured", "ready"].includes(exam.status)) {
    return { label: "完善试卷", path: `/exams/${encodedId}/paper` };
  }
  if (["collecting", "processing"].includes(exam.status)) {
    return { label: issueCount ? "处理答卷" : "继续导入", path: `/exams/${encodedId}/capture` };
  }
  if (["grading", "reviewing"].includes(exam.status)) {
    return { label: "继续阅卷", path: `/exams/${encodedId}/grading` };
  }
  if (exam.status === "finalized") return { label: "发布成绩", path: `/exams/${encodedId}/scores` };
  if (exam.status === "published") return { label: "查看成绩", path: `/exams/${encodedId}/scores` };
  return { label: "查看考试", path: `/exams/${encodedId}/overview` };
}

function MembersSection({ statistics, onNavigate }: { statistics: DashboardOrganizationStatistics; onNavigate: (path: string) => void }) {
  const items = [
    {
      label: "学生名册",
      value: `${statistics.active_student_count.toLocaleString("zh-CN")} 人`,
      detail: "在籍学生",
      action: "管理学生",
      path: "/members/students",
      icon: <UsersRound size={18} />
    },
    {
      label: "年级与班级",
      value: `${statistics.grade_count} 个年级 · ${statistics.class_count} 个班`,
      detail: statistics.empty_class_count ? `${statistics.empty_class_count} 个班尚无学生` : "班级名册已建立",
      action: "管理班级",
      path: "/members/classes",
      icon: <GraduationCap size={18} />
    },
    {
      label: "阅卷教师",
      value: `${statistics.teacher_count} 名教师`,
      detail: `${statistics.grader_count} 名阅卷人员`,
      action: "管理人员",
      path: "/members/teachers",
      icon: <UserCog size={18} />
    }
  ];

  return (
    <section className="school-dashboard-section members-section">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><span>01</span><div><h2>成员管理</h2><p>维护组织、学生与考试人员基础数据</p></div></div>
        <Button type="link" onClick={() => onNavigate("/members/students")}>查看成员管理 <ArrowRight size={14} /></Button>
      </div>
      <div className="member-summary-grid">
        {items.map((item) => (
          <button type="button" key={item.label} onClick={() => onNavigate(item.path)}>
            <span className="member-summary-icon">{item.icon}</span>
            <span className="member-summary-copy"><small>{item.label}</small><strong>{item.value}</strong><em>{item.detail}</em></span>
            <span className="member-summary-action">{item.action}<ArrowRight size={14} /></span>
          </button>
        ))}
      </div>
    </section>
  );
}

function ExamsSection({ exams, activeExamCount, onNavigate }: { exams: DashboardActiveExam[]; activeExamCount: number; onNavigate: (path: string) => void }) {
  return (
    <section className="school-dashboard-section exams-section">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><span>02</span><div><h2>考试管理</h2><p>进行中 {activeExamCount} 场 · 查看当前阶段并继续下一项工作</p></div></div>
        <Button type="link" onClick={() => onNavigate("/exams")}>全部考试 <ArrowRight size={14} /></Button>
      </div>
      {exams.length ? (
        <div className="dashboard-table-wrap">
          <table className="dashboard-operation-table exam-operation-table">
            <thead><tr><th>考试</th><th>科目</th><th>当前阶段</th><th className="number-cell">答卷</th><th className="number-cell">异常</th><th>下一步</th></tr></thead>
            <tbody>{exams.slice(0, 5).map((exam) => {
              const action = examAction(exam);
              const issueCount = exam.failed_count + exam.quality_issue_count + exam.unmatched_count;
              return (
                <tr key={exam.id}>
                  <td><button type="button" className="table-primary-link" onClick={() => onNavigate(`/exams/${encodeURIComponent(exam.id)}/overview`)}>{exam.name}</button></td>
                  <td>{examSubjectLabel(exam.subject)}</td>
                  <td><StatusTag tone={examStatusTone(exam.status)}>{examStatusLabels[exam.status] ?? "进行中"}</StatusTag></td>
                  <td className="number-cell">{exam.submission_count || "—"}</td>
                  <td className={`number-cell ${issueCount ? "has-issue" : ""}`}>{issueCount}</td>
                  <td><button type="button" className="table-action-link" onClick={() => onNavigate(action.path)}>{action.label}<ArrowRight size={14} /></button></td>
                </tr>
              );
            })}</tbody>
          </table>
        </div>
      ) : (
        <div className="dashboard-inline-empty"><Check size={16} /><span><strong>暂无进行中考试</strong><small>创建考试后，可从这里直接继续下一步。</small></span></div>
      )}
    </section>
  );
}

function GradingResultsSection({ statistics, workItems, onNavigate }: { statistics: DashboardStatistics; workItems: DashboardWorkItem[]; onNavigate: (path: string) => void }) {
  const metrics = [
    { label: "待阅", value: statistics.pending_review_question_count, unit: "题" },
    { label: "待复核", value: statistics.pending_arbitration_submission_count, unit: "份" },
    { label: "待发布", value: statistics.finalized_exam_count, unit: "场" }
  ];
  return (
    <section className="school-dashboard-section grading-results-section">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><span>03</span><div><h2>阅卷与成绩</h2><p>优先处理阻断阅卷和成绩发布的事项</p></div></div>
        <Button type="link" onClick={() => onNavigate("/grading?status=pending")}>进入阅卷任务 <ArrowRight size={14} /></Button>
      </div>
      <div className="grading-metric-strip">{metrics.map((metric) => <span key={metric.label}><small>{metric.label}</small><strong>{metric.value.toLocaleString("zh-CN")}</strong><em>{metric.unit}</em></span>)}</div>
      {workItems.length ? (
        <div className="dashboard-table-wrap">
          <table className="dashboard-operation-table work-item-table">
            <thead><tr><th>事项</th><th>影响</th><th className="number-cell">数量</th><th>操作</th></tr></thead>
            <tbody>{workItems.map((item) => (
              <tr key={item.label}>
                <td><span className={`work-item-indicator ${item.tone}`} />{item.label}</td>
                <td className="secondary-cell">{item.detail}</td>
                <td className="number-cell"><strong>{item.value}</strong> {item.unit}</td>
                <td><button type="button" className="table-action-link" onClick={() => onNavigate(item.path)}>{item.action}<ArrowRight size={14} /></button></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      ) : (
        <div className="dashboard-inline-empty"><Check size={16} /><span><strong>当前没有需要处理的事项</strong><small>新的答卷异常、复核和发布任务会显示在这里。</small></span></div>
      )}
    </section>
  );
}

function KeyProgressSection({ activities, onNavigate }: { activities: DashboardActivity[]; onNavigate: (path: string) => void }) {
  if (!activities.length) return null;
  return (
    <section className="school-dashboard-section key-progress-section">
      <div className="school-dashboard-section-head">
        <div className="school-dashboard-section-title"><span>04</span><div><h2>关键进展</h2><p>仅展示会影响考试推进的重要变化</p></div></div>
      </div>
      <div className="dashboard-link-list">
        {activities.slice(0, 5).map((activity) => (
          <button type="button" key={activity.id} onClick={() => activity.drilldown_path && onNavigate(activity.drilldown_path)} disabled={!activity.drilldown_path}>
            <Clock3 size={15} />
            <span>
              <strong>{activity.title || "考试进展已更新"}</strong>
              <small>{activity.summary || "查看相关考试的最新状态"} · {new Date(activity.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })}</small>
            </span>
            {activity.drilldown_path ? <ArrowRight size={14} /> : null}
          </button>
        ))}
      </div>
    </section>
  );
}

export function SchoolDashboard({ organizationStatistics, statistics, activeExamCount, exams, workItems, activities, onNavigate }: {
  organizationStatistics: DashboardOrganizationStatistics;
  statistics: DashboardStatistics;
  activeExamCount: number;
  exams: DashboardActiveExam[];
  workItems: DashboardWorkItem[];
  activities: DashboardActivity[];
  onNavigate: (path: string) => void;
}) {
  return (
    <div className="school-dashboard-sections">
      <MembersSection statistics={organizationStatistics} onNavigate={onNavigate} />
      <ExamsSection exams={exams} activeExamCount={activeExamCount} onNavigate={onNavigate} />
      <GradingResultsSection statistics={statistics} workItems={workItems} onNavigate={onNavigate} />
      <KeyProgressSection activities={activities} onNavigate={onNavigate} />
    </div>
  );
}
