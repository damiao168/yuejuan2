import { useMemo, type ReactNode } from "react";
import { Button, Select } from "antd";
import { ArrowLeft, ArrowRight } from "lucide-react";
import { StatusBadge, WorkspaceLayout, WorkspaceStageRail } from "@edugrade/ui";
import { examStatusLabels, examStatusTone } from "../../../constants/examStatus";
import { examSubjectLabel } from "../../../constants/examStatus";
import type { Exam } from "../../../api/exams";
import type { ExamWorkspaceProjection } from "../../../api/workspace";
import { examBusinessStages, sectionBusinessStage } from "./businessStages";

const sectionNames: Record<string, string> = {
  overview: "考试设置", students: "学生范围", paper: "考试资料", questions: "小题与分值",
  template: "答题卡设置", settings: "考试设置", capture: "答卷导入", processing: "识别处理",
  grading: "阅卷", quality: "复核与异常", scores: "成绩与报告", appeals: "申诉处理", reports: "成绩分析"
};
const preparationSections = new Set(["overview", "students", "paper", "questions", "template", "settings"]);

function Overview({ data, onNavigate }: { data: ExamWorkspaceProjection; onNavigate: (path: string) => void }) {
  return <main className="exam-legacy-overview">
    <div><span>准备概览</span><h2>继续完成这场考试</h2><p>按当前状态处理最重要的下一步；开考前任务已集中到准备工作区。</p></div>
    <div className="exam-next-actions">{data.next_actions.slice(0, 3).map((action) => <button type="button" key={action.code} aria-label={action.label} onClick={() => onNavigate(action.route)}><span><strong>{action.label}</strong><small>{action.description}</small></span><ArrowRight size={16} /></button>)}{!data.next_actions.length ? <p>当前没有待处理任务。</p> : null}</div>
    <Button type="primary" onClick={() => onNavigate(`/exams/${encodeURIComponent(data.exam_id)}/settings`)}>查看全部准备任务</Button>
  </main>;
}

export function ExamWorkspaceLayout({ data, section, subjectExams = [], moduleContent, onNavigate }: { data: ExamWorkspaceProjection; section: string; subjectExams?: Exam[]; moduleContent?: ReactNode; onNavigate: (path: string) => void }) {
  const currentSectionName = sectionNames[section] ?? "考试工作区";
  const businessStages = useMemo(() => examBusinessStages(data), [data]);
  const inPreparation = preparationSections.has(section);
  const showPreparationReturn = inPreparation && !["overview", "settings"].includes(section);
  const content = <div className={`exam-workspace-content${inPreparation ? " is-preparation" : ""}`} data-stage={sectionBusinessStage[section]}>
    {section === "overview" ? <Overview data={data} onNavigate={onNavigate} /> : moduleContent ? <div className="exam-workspace-module" aria-label={currentSectionName}>{moduleContent}</div> : <main className="eg-workspace-panel eg-workspace-section-empty"><div><span>考试工作区</span><h2>{currentSectionName}</h2><p>该环节暂时没有独立页面，请从阶段导航选择可用入口。</p></div></main>}
  </div>;
  const currentExam = subjectExams.find((exam) => exam.id === data.exam_id);
  const workspaceName = currentExam?.exam_session_name || data.exam_name;
  return <div className="exam-workspace-canvas"><WorkspaceLayout
    header={<header className="exam-context-header"><div className="exam-context-leading">{showPreparationReturn ? <Button className="exam-workspace-back" icon={<ArrowLeft size={17} />} onClick={() => onNavigate(`/exams/${encodeURIComponent(data.exam_id)}/settings`)}>返回考试设置</Button> : null}<div className="exam-context-title"><h1>{workspaceName}</h1>{subjectExams.length > 1 ? <label className="exam-workspace-subject"><span>当前学科</span><Select aria-label="切换考试学科" value={data.exam_id} options={subjectExams.map((exam) => ({ label: `${examSubjectLabel(exam.subject)} · ${exam.total_score} 分`, value: exam.id }))} onChange={(examId) => onNavigate(`/exams/${encodeURIComponent(examId)}/${section}`)} /></label> : <span>{data.subject_summary.label} · {data.subject_summary.total_score} 分</span>}</div></div><StatusBadge tone={examStatusTone(data.exam_status)}>{examStatusLabels[data.exam_status] ?? "未知状态"}</StatusBadge></header>}
    stageRail={<WorkspaceStageRail stages={businessStages} onNavigate={onNavigate} />}
  >
    {inPreparation ? <div className="exam-preparation-shell">{content}</div> : content}
  </WorkspaceLayout></div>;
}
