import { Tag } from "antd";
import type { ReactNode } from "react";
import type { Appeal } from "../../api/appeals";
import type { SchoolClass, Student } from "../../api/org";
import { StatusTag } from "../../components/StatusTag";
import { EmptyState, LoadingState } from "../../components/PageState";
import { examSubjectLabel } from "../../constants/examStatus";
import type { ProductExperience } from "../../router/experience";
import { evidenceNumber, formatScore, formatTime, readableReason, readableReasonDetail, recommendationLabels, rubricItems, statusLabels, statusTone } from "./appealPresentation";

type HeadingProps = Readonly<{
  appeal: Appeal;
  mode: ProductExperience;
  examName?: string;
  student?: Student;
  schoolClass?: SchoolClass;
}>;

export function AppealCasePanel({ loading, hasSelection, children }: Readonly<{ loading: boolean; hasSelection: boolean; children: ReactNode }>) {
  // 切换详情期间展示加载状态，不让旧申诉的操作区继续可见。
  if (loading) return <section className="appeal-case-panel"><LoadingState label="正在读取答卷与复核信息" /></section>;
  if (!hasSelection) return <section className="appeal-case-panel"><EmptyState title="请选择复核申请" description="从左侧列表选择一条记录，核对作答、评分标准与处理依据。" /></section>;
  return <section className="appeal-case-panel">{children}</section>;
}

function caseSubtitle({ appeal, mode, student, schoolClass }: HeadingProps) {
  // 教师只展示匿名编号，不将管理员身份字段拼入复核标题。
  if (mode === "teacher") return `匿名答卷 ${appeal.anonymous_code || "未编号"}`;
  const className = schoolClass?.name ? ` · ${schoolClass.name}` : "";
  return `${student?.name ?? "学生信息未匹配"}${className} · 提交于 ${formatTime(appeal.created_at)}`;
}

export function AppealCaseHeading(props: HeadingProps) {
  const { appeal, examName } = props;
  return <header className="appeal-case-head">
    <div>
      <h2>{examName ?? appeal.exam_name ?? "考试"} · {appeal.subject ? examSubjectLabel(appeal.subject) : "未标注学科"} · {appeal.question_no || "整卷"}</h2>
      <p>{caseSubtitle(props)}</p>
    </div>
    <StatusTag tone={statusTone(appeal.status)}>{statusLabels[appeal.status] ?? "处理中"}</StatusTag>
  </header>;
}

function AppealRubric({ appeal }: Readonly<{ appeal: Appeal }>) {
  const items = rubricItems(appeal);
  if (!items.length) return <p className="muted">暂无结构化评分要点，请按本题正式评分标准复核。</p>;
  return <div className="appeal-rubric-list">
    {items.map((item, index) => <div key={`${item.label}-${index}`}>
      <span>{item.label}</span><strong>{item.score === undefined ? "" : `${formatScore(item.score)} 分`}</strong>
    </div>)}
  </div>;
}

type EvidenceProps = Readonly<{ appeal: Appeal; mode: ProductExperience; workerNames: Record<string, string> }>;

export function AppealEvidence({ appeal, mode, workerNames }: EvidenceProps) {
  return <main className="appeal-evidence-main">
    <section>
      <h3>申诉内容</h3>
      <div className="appeal-reason-box"><Tag color="blue">{readableReason(appeal.reason)}</Tag><p>{readableReasonDetail(appeal.reason)}</p></div>
    </section>
    <section>
      <h3>答题区域</h3>
      <div className="appeal-answer-grid">
        <div><span>原卷作答</span><p>{appeal.evidence?.raw_answer || "暂无可展示的原卷答题内容"}</p></div>
        <div><span>文字识别结果</span><p>{appeal.evidence?.ocr_text || "暂无识别文本，请以原卷作答为准"}</p></div>
      </div>
    </section>
    <section><h3>评分标准</h3><AppealRubric appeal={appeal} /></section>
    {mode === "admin" ? <section>
      <h3>原处理结果</h3>
      <div className="appeal-result-strip">
        <div><span>原最终分</span><strong>{formatScore(evidenceNumber(appeal, "score"))} / {formatScore(evidenceNumber(appeal, "max_score"))}</strong></div>
        <div><span>复核教师</span><strong>{appeal.assigned_to ? workerNames[appeal.assigned_to] ?? "已分派" : "等待系统分派"}</strong></div>
        <div><span>复核意见</span><strong>{appeal.teacher_recommendation ? recommendationLabels[appeal.teacher_recommendation] ?? "已反馈" : "待反馈"}</strong></div>
      </div>
      {appeal.teacher_recommendation_reason ? <p className="appeal-review-note">{appeal.teacher_recommendation_reason}</p> : null}
    </section> : null}
  </main>;
}
