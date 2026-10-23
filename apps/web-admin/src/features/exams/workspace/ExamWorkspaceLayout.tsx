import { Suspense, useEffect, useState, type ReactNode } from "react";
import { Alert, Button, Select } from "antd";
import { Check, MessageCircle } from "lucide-react";
import { WorkspaceLayout } from "@edugrade/ui";
import { examSubjectLabel } from "../../../constants/examStatus";
import type { ExamWorkspaceProjection } from "../../../api/workspace";
import { sectionBusinessStage } from "./businessStages";
import { preparationNavigationSteps, preparationSteps } from "../preparation/preparationSteps";
import { getExamReadiness, type ExamReadiness } from "../../../api/configuration";
import { ErrorBoundary } from "../../../components/ErrorBoundary";
import { LoadingState } from "../../../components/PageState";
import { useSchoolAssistantContext } from "../../school-assistant/SchoolAssistantProvider";
import { managementExamRoute } from "../management/managementPresentation";

const sectionNames: Record<string, string> = {
  overview: "考试准备", settings: "准备检查与确认", capture: "答卷导入", processing: "识别处理",
  grading: "阅卷", quality: "复核与异常", scores: "成绩与报告", appeals: "申诉处理", reports: "成绩分析"
};
const preparationSections = new Set(["overview", "settings", ...preparationSteps.map((step) => step.route)]);
type ReadinessResult = { key: string; scopeKey: string; readiness?: ExamReadiness; failed: boolean };

function PreparationNavigation({ links, section, examId, failed, canRead, onNavigate }: Readonly<{
  links: ReturnType<typeof preparationNavigationSteps>; section: string; examId: string;
  failed: boolean; canRead: boolean; onNavigate: (path: string) => void;
}>) {
  const inCapture = section === "capture" || section === "processing";
  return <div className="exam-preparation-progress"><nav className="exam-preparation-nav" aria-label="考试准备与答卷导入">{links.map((link, index) => {
    const stateLabel = { complete: "已完成", incomplete: "未完成", unknown: "状态待检查" }[link.state];
    let detail = link.detail;
    if (!canRead) detail = "当前账号无法查看准备检查状态";
    if (failed) detail = "准备状态暂无法获取，请刷新后重试";
    return <button key={link.route} type="button" className={`is-${link.state}${section === link.route ? " is-active" : ""}`} aria-label={`${link.label}，${stateLabel}；${detail}`} title={`${stateLabel} · ${detail}`} aria-current={section === link.route ? "step" : undefined} onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/${link.route}`)}><span className="exam-preparation-step-number" aria-hidden="true">{link.state === "complete" ? <Check size={14} /> : index + 1}</span><span>{link.label}</span></button>;
  })}
    {/* 答卷可分批追加，进入页面或导入部分答卷不能标成完成；准备和写入权限仍由导入页校验。 */}
    <button type="button" className={`exam-capture-step${inCapture ? " is-active" : ""}`} aria-label="学生答卷，上传学生填写的答题卡扫描件"
      title="上传学生填写的答题卡扫描件；请先确认准备完成" aria-current={inCapture ? "step" : undefined}
      onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/capture`)}><span className="exam-preparation-step-number" aria-hidden="true">{links.length + 1}</span><span>学生答卷</span></button>
  </nav></div>;
}

function ExamWorkspaceModule({ data, section, name, inPreparation, moduleContent }: Readonly<{
  data: ExamWorkspaceProjection; section: string; name: string; inPreparation: boolean;
  moduleContent?: ReactNode;
}>) {
  const readOnly = data.read_only || data.exam_status === "archived";
  const readOnlyNotice = data.warnings.some((warning) => warning.code === "archive_state_unavailable")
    ? "归档状态暂无法确认，当前学科只读；请稍后刷新检查。"
    : "考试已归档，当前学科只读；已发布成绩继续按原权限访问。";
  // 分区按需加载或报错时只替换内容，保留考试信息和导航；换考试或分区后重置错误边界。
  const module = <ErrorBoundary resetKey={`${data.exam_id}:${section}`}><Suspense fallback={<LoadingState label={`正在加载${name}`} />}>{moduleContent}</Suspense></ErrorBoundary>;
  return <div className={`exam-workspace-content${inPreparation ? " is-preparation" : section === "capture" ? " is-capture" : ""}`} data-stage={sectionBusinessStage[section]}>
    {readOnly ? <Alert className="drawer-alert" type="info" showIcon message={readOnlyNotice} /> : null}
    {moduleContent ? <div className="exam-workspace-module" aria-label={name}>{module}</div> : <main className="eg-workspace-panel eg-workspace-section-empty"><div><span>考试工作区</span><h2>{name}</h2><p>该环节暂时没有独立页面，请从左侧菜单选择可用入口。</p></div></main>}
  </div>;
}

function ExamWorkspaceHeader({ data, section, assistant, preparationNavigation, onNavigate }: Readonly<{
  data: ExamWorkspaceProjection; section: string;
  assistant: ReturnType<typeof useSchoolAssistantContext>; preparationNavigation?: ReactNode; onNavigate: (path: string) => void;
}>) {
  const subjectExams = data.subject_exams ?? [];
  const workspaceName = data.exam_session_name || data.exam_name;
  return <header className={`exam-context-header${preparationNavigation ? " has-preparation" : ""}`}>
    <div className="exam-context-leading"><div className="exam-context-title"><h1 title={workspaceName}>{workspaceName}</h1>{subjectExams.length > 1 ? <label className="exam-workspace-subject"><span>当前学科</span><Select aria-label="切换考试学科" value={data.exam_id} options={subjectExams.map((exam) => ({ label: `${examSubjectLabel(exam.subject)} · ${exam.total_score} 分`, value: exam.exam_id }))} onChange={(examId) => onNavigate(`/exams/${encodeURIComponent(examId)}/${section}`)} /></label> : <span>{data.subject_summary.label} · {data.subject_summary.total_score} 分</span>}</div></div>
    {preparationNavigation}
    <div className="exam-context-status">
      <Button size="small" onClick={() => onNavigate(managementExamRoute({ kind: data.exam_session_id ? "session" : "standalone", id: data.exam_session_id || data.exam_id }))}>整场总览</Button>
      {assistant ? <Button className="exam-assistant-trigger" type="text" size="small" icon={<MessageCircle size={15} />} onClick={() => assistant.open({ exam_id: data.exam_id, exam_name: `${workspaceName} · ${data.subject_summary.label}`, revision: data.revision, origin_module: "exam_workspace", origin_section: section })}>问助手</Button> : null}
    </div>
  </header>;
}

export function ExamWorkspaceLayout({ data, section, moduleContent, onNavigate, canReadReadiness = false }: Readonly<{ data: ExamWorkspaceProjection; section: string; moduleContent?: ReactNode; onNavigate: (path: string) => void; canReadReadiness?: boolean }>) {
  const currentSectionName = sectionNames[section] ?? preparationSteps.find((step) => step.route === section)?.label ?? "考试工作区";
  const inPreparation = preparationSections.has(section);
  const showPreparationNavigation = inPreparation || section === "capture" || section === "processing";
  // 准备和答卷导入共用同一配置检查；切换步骤只更新选中项，避免完成标识先变灰再变绿。
  const readinessScopeKey = showPreparationNavigation && canReadReadiness ? JSON.stringify([data.exam_id, data.revision]) : "";
  // updated_at 是投影读取时间；用它触发保存后的核对，同版本后台重读期间保留上次结果。
  const readinessRequestKey = readinessScopeKey ? JSON.stringify([readinessScopeKey, data.updated_at]) : "";
  const [readinessResult, setReadinessResult] = useState<ReadinessResult>();
  const currentReadinessResult = readinessResult?.scopeKey === readinessScopeKey ? readinessResult : undefined;
  useEffect(() => {
    if (!readinessRequestKey) return;
    let active = true;
    // 切换学科或配置刷新后重新核对；旧请求返回不能给新的准备配置标绿。
    void getExamReadiness(data.exam_id).then(({ readiness }) => {
      if (active) setReadinessResult({ key: readinessRequestKey, scopeKey: readinessScopeKey, readiness, failed: false });
    }).catch(() => {
      if (active) setReadinessResult({ key: readinessRequestKey, scopeKey: readinessScopeKey, failed: true });
    });
    return () => { active = false; };
  }, [data.exam_id, readinessRequestKey, readinessScopeKey]);
  const assistant = useSchoolAssistantContext();
  const content = <ExamWorkspaceModule data={data} section={section} name={currentSectionName} inPreparation={inPreparation} moduleContent={moduleContent} />;
  const preparationLinks = preparationNavigationSteps(currentReadinessResult?.readiness);
  // 导航只挂载在页头一次；切换模块继续复用同一组按钮和准备检查结果。
  const preparationNavigation = showPreparationNavigation ? <PreparationNavigation links={preparationLinks} section={section} examId={data.exam_id} failed={Boolean(currentReadinessResult?.failed)} canRead={canReadReadiness} onNavigate={onNavigate} /> : null;
  return <div className={`exam-workspace-canvas${inPreparation ? " is-preparation" : section === "capture" ? " is-capture" : ""}`}><WorkspaceLayout
    header={<ExamWorkspaceHeader data={data} section={section} assistant={assistant} preparationNavigation={preparationNavigation} onNavigate={onNavigate} />}
    // 考试列表内只导航准备与答卷导入；阅卷和成绩使用左侧各自的业务入口。
    stageRail={null}
  >
    {content}
  </WorkspaceLayout></div>;
}
