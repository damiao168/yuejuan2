import { Alert, Button, Drawer, Form, Select, Space } from "antd";
import { CheckCircle2, RefreshCw } from "lucide-react";
import { getPaperImportUserMessage } from "../../api/client";
import { EmptyState, ErrorState, LoadingState } from "../../components/PageState";
import { PaperImportWorkspace } from "../paper-import/PaperImportWorkspace";
import { PaperStructurePanel } from "../paper-import/PaperStructurePanel";
import { PaperMaterialsConfirmation } from "../paper-import/PaperMaterialsConfirmation";
import { HistoryQuestionImporter } from "../question-bank/HistoryQuestionImporter";
import { PaperQuestionIndex } from "./PaperQuestionIndex";
import { PaperQuestionDetail } from "./PaperQuestionDetail";
import { PaperGroupedQuestionScores } from "./PaperGroupedQuestionScores";
import { PaperQuestionConfirmation } from "./PaperQuestionConfirmation";
import { PaperQuestionIssues } from "./PaperQuestionIssues";
import { questionEditorToggleLabel, questionNeedsAnswer, questionNeedsRubric } from "./paperQuestionPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";
import { PaperRubricJobsButton, PaperRubricJobsProvider } from "./PaperRubricJobs";

type ViewProps = Readonly<{ context: PaperRubricViewContext }>;

function paperContentReady({ config, selectedExam }: PaperRubricViewContext) {
  return !config.loading && !config.error && Boolean(selectedExam) && !config.configLoading && !config.configError;
}

export function PaperRubricView({ context }: ViewProps) {
  return <PaperRubricJobsProvider context={context}><div className="page-stack">
    {!paperContentReady(context) && <Form form={context.questionWorkflow.form} component={false} />}
    <PaperStandaloneHeader context={context} />
    <PaperRubricContent context={context} />
    <PaperSourceDrawer context={context} />
  </div></PaperRubricJobsProvider>;
}

function PaperStandaloneHeader({ context }: ViewProps) {
  const { options, config, selectedExam, actions } = context;
  if (options.mode === "embedded") return null;
  return <>
    <section className="page-heading">
      <div><Space><h1>考试资料与评分配置</h1><PaperBankActions context={context} /></Space>
        <p>手里有什么考试资料就直接添加；系统自动识别题目、答案与解析，并提示需要核对的缺失或冲突。</p></div>
      <Space wrap>
        <Button icon={<RefreshCw size={16} />} onClick={() => void config.loadExams()} loading={config.loading}>刷新</Button>
        <Button icon={<CheckCircle2 size={16} />} disabled={!selectedExam} onClick={actions.runValidation}>完整性检查</Button>
      </Space>
    </section>
    <section className="workspace-section filter-panel"><div className="paper-topline">
      <Select className="exam-picker" placeholder="选择考试" value={config.selectedExamId || undefined}
        options={config.exams.map((exam) => ({ label: exam.name, value: exam.id }))}
        onChange={config.setSelectedExamId} loading={config.loading} />
      {selectedExam && <span className="muted">当前考试总分：{selectedExam.total_score}</span>}
    </div></section>
  </>;
}

function PaperBankActions({ context }: ViewProps) {
  const { options, selectedExam, config, actions } = context;
  if (!selectedExam) return null;
  const editableExam = selectedExam.status === "draft" || selectedExam.status === "configured";
  if (!editableExam && options.canImportQuestionBank && config.questions.length) {
    const openBank = options.onNavigate ? (path?: string) => options.onNavigate?.(path ?? "/question-bank") : undefined;
    return <HistoryQuestionImporter key={`${options.questionBankScope}:${selectedExam.id}`} scopeKey={options.questionBankScope}
      examId={selectedExam.id} questions={config.questions} onImported={actions.onHistoryImported} onOpenBank={openBank} />;
  }
  return null;
}

// 加载与错误按原优先级互斥；配置未就绪时不挂载编辑区，避免旧考试内容继续可操作。
export function PaperRubricContent({ context }: ViewProps) {
  const { config, selectedExam } = context;
  if (config.loading) return <section className="workspace-section"><LoadingState label="正在读取考试列表" /></section>;
  if (config.error) return <ErrorState message={config.error} onRetry={() => void config.loadExams()} />;
  if (!selectedExam) return <section className="workspace-section"><EmptyState title="暂无考试" description="还没有可配置的考试。请先在「考试管理」中创建考试。" /></section>;
  if (config.configLoading) return <section className="workspace-section"><LoadingState label="正在读取试卷与题目配置" /></section>;
  if (config.configError) return <ErrorState message={config.configError} onRetry={() => void config.loadConfig(selectedExam.id)} />;
  return <PaperReadyContent context={context} />;
}

function PaperReadyContent({ context }: ViewProps) {
  const { options, config, latestPaperImport, questionWorkflow } = context;
  const showQuestions = options.initialView === "questions" || (options.mode !== "embedded" && (config.questions.length > 0 || Boolean(latestPaperImport)));
  const showEditor = options.initialView === "questions" || questionWorkflow.showQuestionEditor;
  return <>
    <PaperMaterialContent context={context} />
    {showQuestions && <><PaperQuestionTools context={context} /><PaperValidationNotice context={context} /></>}
    {showQuestions && showEditor && <section className="paper-workbench" id="paper-question-workbench">
      <PaperQuestionIndex context={context} /><PaperQuestionDetail context={context} />
    </section>}
  </>;
}

function PaperMaterialContent({ context }: ViewProps) {
  const { options, config, latestPaperImport, state, importWorkflow, actions, questionWorkflow, selectedExam } = context;
  const suggestUpload = options.mode === "embedded" && options.initialView === "questions" && !config.papers.length && !config.paperImports.length;
  const canConfirmMaterials = options.canManage && (selectedExam?.status === "draft" || selectedExam?.status === "configured");
  const materialOperationBusy = state.unsavedWork || state.saveBusy || state.saveRefreshPending || importWorkflow.reviewDirty
    || importWorkflow.parsing || importWorkflow.updatingImportSources || importWorkflow.savingImportReview || importWorkflow.fillingImportContent;
  const adoptedMaterials = latestPaperImport?.status === "applied" || Boolean(latestPaperImport && latestPaperImport.structure_confirmed_generation === latestPaperImport.generation);
  // 登记状态随资料标题展示；已有登记只恢复读取，不重复采用题目结构。
  const materialConfirmation = latestPaperImport && adoptedMaterials ? <PaperMaterialsConfirmation
    job={latestPaperImport} papers={config.papers} canManage={canConfirmMaterials} disabled={materialOperationBusy}
    disabledReason={state.saveRefreshPending ? "请先重新读取保存结果，再确认考试资料。" : state.unsavedWork || importWorkflow.reviewDirty ? "请先保存当前修改，再确认考试资料。" : undefined}
    onConfirmed={actions.onStructureAdopted} /> : undefined;
  return <>
    {suggestUpload && <Alert type="info" showIcon message="尚未上传考试资料"
      description="推荐先上传试卷、答案或评分标准，系统可自动识别题目和分值；也可以继续手动添加题目。"
      action={options.onNavigate && selectedExam ? <Button size="small" onClick={() => options.onNavigate?.(`/exams/${encodeURIComponent(selectedExam.id)}/paper`)}>上传资料</Button> : undefined} />}
    {latestPaperImport?.status === "review_required" && !adoptedMaterials && canConfirmMaterials && <PaperStructurePanel
      job={latestPaperImport} papers={config.papers} disabled={materialOperationBusy} onAdopted={actions.onStructureAdopted} />}
    {options.initialView === "materials" && <PaperImportWorkspace canManage={options.canManage} latestPaperImport={latestPaperImport}
      headerStatus={materialConfirmation}
      existingQuestions={config.questions} workflow={importWorkflow} setShowQuestionEditor={questionWorkflow.setShowQuestionEditor}
      initialExamId={options.fixedExamId ?? options.initialExamId} onNavigate={options.onNavigate} connectionError={config.importConnectionError} onRecheck={config.retryImportConnection} />}
  </>;
}

function PaperValidationNotice({ context }: ViewProps) {
  const validation = context.config.validation;
  if (!validation) return null;
  let description: React.ReactNode = "检查完成，未发现配置问题。";
  if (!validation.valid) {
    description = <details className="paper-validation-details"><summary>查看问题清单</summary><ul className="validation-issue-list">
      {validation.issues.map((issue, index) => <li key={`${issue.code}-${index}`}>{getPaperImportUserMessage(issue.message, "考试资料存在需要核对的内容")}</li>)}
    </ul></details>;
  }
  return <Alert type={validation.valid ? "success" : "warning"} showIcon
    message={validation.valid ? "试卷配置完整" : `配置检查发现 ${validation.issues.length} 项问题`} description={description} />;
}

function PaperQuestionTools({ context }: ViewProps) {
  const { config, latestPaperImport, structureConfirmed, importWorkflow, options, state, questionWorkflow, selectedExam, actions, groupedScores } = context;
  function openIssue(code: string) {
    if (code === "material_structure" && selectedExam && options.onNavigate) {
      options.onNavigate(`/exams/${encodeURIComponent(selectedExam.id)}/paper`);
      return;
    }
    const target = config.questions.find(question => code === "answer_keys" ? questionNeedsAnswer(question)
      : code === "rubrics" ? questionNeedsRubric(question) : code === "question_scores" ? question.score <= 0 : true);
    if (target) actions.openQuestionIssue(target, code === "answer_keys" ? "answer" : code === "rubrics" ? "rubric" : "score");
  }
  const tools = <>
    <PaperRubricJobsButton />
    <PaperGroupedQuestionScores key={selectedExam?.id} context={context} />
    <PaperQuestionIssues key={`issues:${selectedExam?.id}`} context={context} />
    <PaperBankActions context={context} />
    {options.initialView === "materials" && <Button onClick={() => questionWorkflow.setShowQuestionEditor(current => !current)}>
      {questionEditorToggleLabel(questionWorkflow.showQuestionEditor, config.questions.length)}
    </Button>}
  </>;
  return <section id="paper-question-tools" className="paper-question-tools" aria-label="题目工具">
    {options.initialView === "questions" && selectedExam ? <PaperQuestionConfirmation compact
      examId={selectedExam.id}
      configurationKey={JSON.stringify([selectedExam.revision, selectedExam.total_score, config.questions, latestPaperImport?.id, latestPaperImport?.generation, structureConfirmed])}
      canManage={options.canManage && (selectedExam.status === "draft" || selectedExam.status === "configured")}
      disabled={state.unsavedWork || groupedScores.dirty || state.saveBusy || state.saveRefreshPending || importWorkflow.reviewDirty || importWorkflow.parsing || importWorkflow.fillingImportContent || importWorkflow.savingImportReview || importWorkflow.updatingImportSources}
      disabledReason={state.saveRefreshPending ? "请先重新读取保存结果，再检查题目配置。" : groupedScores.dirty ? "请先保存或恢复批量分值修改，再检查题目配置。" : state.unsavedWork || importWorkflow.reviewDirty ? "请先保存当前修改，再检查题目配置。" : "请等待保存和资料处理完成。"}
      actions={tools} onOpenCheck={check => openIssue(check.code)} onChecked={() => options.onExamChanged?.()} />
      : <div className="paper-step-toolbar">{tools}</div>}
  </section>;
}
function PaperSourceDrawer({ context }: ViewProps) {
  const { selectedQuestion, state, actions, options, selectedExam } = context;
  return <Drawer title={selectedQuestion ? `第 ${selectedQuestion.question_no} 题 · 来源与答题区域` : "来源与答题区域"}
    open={state.sourceOpen} onClose={() => actions.setSourceOpen(false)} width="min(420px, 100vw)">
    <div className="paper-source-context"><h3>题目来源</h3>
      <p>{selectedQuestion?.paper_import_id ? "本题由考试资料识别导入" : "本题暂无已关联的识别来源"}</p>
      {Boolean(selectedQuestion?.paper_import_source_refs?.length) && <p>已记录 {selectedQuestion?.paper_import_source_refs?.length} 处题目来源；教师解析来源 {selectedQuestion?.solution?.source_refs.length ?? 0} 处。</p>}
      <h3>答题区域</h3><p>答题卡上的作答区域应在模板中可视化框选，不在题目配置中填写坐标。</p>
      {options.onNavigate && selectedExam && <Button onClick={actions.openTemplate}>打开答题卡模板</Button>}
    </div>
  </Drawer>;
}
