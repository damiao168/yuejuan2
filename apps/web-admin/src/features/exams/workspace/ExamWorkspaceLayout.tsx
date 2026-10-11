import { Suspense, useMemo, type ReactNode } from "react";
import { Alert, Button, Select } from "antd";
import { MessageCircle } from "lucide-react";
import { ExamStageIndicator, QualityIndicator, StatusBadge, WorkspaceLayout } from "@edugrade/ui";
import { examStatusLabels, examStatusTone, examSubjectLabel } from "../../../constants/examStatus";
import type { ExamWorkspaceProjection } from "../../../api/workspace";
import { examBusinessStages, sectionBusinessStage } from "./businessStages";
import { preparationSteps } from "../preparation/preparationSteps";
import { ErrorBoundary } from "../../../components/ErrorBoundary";
import { LoadingState } from "../../../components/PageState";
import { useSchoolAssistantContext } from "../../school-assistant/SchoolAssistantProvider";
import { managementExamRoute } from "../management/managementPresentation";

const sectionNames: Record<string, string> = {
  overview: "考试准备", settings: "准备检查与确认", capture: "答卷导入", processing: "识别处理",
  grading: "阅卷", quality: "复核与异常", scores: "成绩与报告", appeals: "申诉处理", reports: "成绩分析"
};
const preparationSections = new Set(["overview", "settings", ...preparationSteps.map((step) => step.route)]);

export function ExamWorkspaceLayout({ data, section, moduleContent, onNavigate }: Readonly<{ data: ExamWorkspaceProjection; section: string; moduleContent?: ReactNode; onNavigate: (path: string) => void }>) {
  const currentSectionName = sectionNames[section] ?? preparationSteps.find((step) => step.route === section)?.label ?? "考试工作区";
  const businessStages = useMemo(() => examBusinessStages(data), [data]);
  const inPreparation = preparationSections.has(section);
  const assistant = useSchoolAssistantContext();
  const readOnlyNotice = data.warnings.some((warning) => warning.code === "archive_state_unavailable")
    ? "归档状态暂无法确认，当前学科只读；请稍后刷新检查。"
    : "考试已归档，当前学科只读；已发布成绩继续按原权限访问。";
  // 分区页面按需加载或报错时只替换内容区，保留考试信息和导航，避免切换步骤时整页闪动。
  const module = <ErrorBoundary resetKey={`${data.exam_id}:${section}`}><Suspense fallback={<LoadingState label={`正在加载${currentSectionName}`} />}>{moduleContent}</Suspense></ErrorBoundary>;
  const content = <div className={`exam-workspace-content${inPreparation ? " is-preparation" : ""}`} data-stage={sectionBusinessStage[section]}>
    {data.read_only || data.exam_status === "archived" ? <Alert className="drawer-alert" type="info" showIcon message={readOnlyNotice} /> : null}
    {moduleContent ? <div className="exam-workspace-module" aria-label={currentSectionName}>{module}</div> : <main className="eg-workspace-panel eg-workspace-section-empty"><div><span>考试工作区</span><h2>{currentSectionName}</h2><p>该环节暂时没有独立页面，请从左侧菜单选择可用入口。</p></div></main>}
  </div>;
  const subjectExams = data.subject_exams ?? [];
  const workspaceName = data.exam_session_name || data.exam_name;
  const preparationLinks = [...preparationSteps, { route: "settings", label: "确认准备完成" }];
  const currentStage = businessStages.find((stage) => stage.state === "current");
  return <div className={`exam-workspace-canvas${inPreparation ? " is-preparation" : ""}`}><WorkspaceLayout
    header={<header className="exam-context-header">
      <div className="exam-context-leading"><div className="exam-context-title"><h1>{workspaceName}</h1>{subjectExams.length > 1 ? <label className="exam-workspace-subject"><span>当前学科</span><Select aria-label="切换考试学科" value={data.exam_id} options={subjectExams.map((exam) => ({ label: `${examSubjectLabel(exam.subject)} · ${exam.total_score} 分`, value: exam.exam_id }))} onChange={(examId) => onNavigate(`/exams/${encodeURIComponent(examId)}/${section}`)} /></label> : <span>{data.subject_summary.label} · {data.subject_summary.total_score} 分</span>}</div></div>
      <div className="exam-context-status">
        <Button size="small" onClick={() => onNavigate(managementExamRoute({ kind: data.exam_session_id ? "session" : "standalone", id: data.exam_session_id || data.exam_id }))}>整场总览</Button>
        {assistant ? <Button className="exam-assistant-trigger" type="text" size="small" icon={<MessageCircle size={15} />} onClick={() => assistant.open({ exam_id: data.exam_id, exam_name: `${workspaceName} · ${data.subject_summary.label}`, revision: data.revision, origin_module: "exam_workspace", origin_section: section })}>问助手</Button> : null}
        <ExamStageIndicator stage={currentStage} /><QualityIndicator blockers={data.blockers.length} warnings={data.warnings.length} /><StatusBadge tone={examStatusTone(data.exam_status)}>{examStatusLabels[data.exam_status] ?? "未知状态"}</StatusBadge>
      </div>
    </header>}
    stageRail={null}
  >
    {inPreparation ? <div className="exam-preparation-shell"><nav className="exam-preparation-nav" aria-label="考试准备"><strong>考试准备</strong>{preparationLinks.map((link, index) => <button key={link.route} type="button" className={section === link.route ? "is-active" : ""} aria-current={section === link.route ? "step" : undefined} onClick={() => onNavigate(`/exams/${encodeURIComponent(data.exam_id)}/${link.route}`)}><span className="exam-preparation-step-number" aria-hidden="true">{index + 1}</span><span>{link.label}</span></button>)}</nav>{content}</div> : content}
  </WorkspaceLayout></div>;
}
