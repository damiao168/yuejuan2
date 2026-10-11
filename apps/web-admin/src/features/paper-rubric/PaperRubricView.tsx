import { Alert, Button, Drawer, Form, Select, Space } from "antd";
import { CheckCircle2, RefreshCw } from "lucide-react";
import { getPaperImportUserMessage } from "../../api/client";
import { EmptyState, ErrorState, LoadingState } from "../../components/PageState";
import { PaperImportWorkspace } from "../paper-import/PaperImportWorkspace";
import { PaperStructurePanel } from "../paper-import/PaperStructurePanel";
import { BankQuestionPicker } from "../question-bank/BankQuestionPicker";
import { HistoryQuestionImporter } from "../question-bank/HistoryQuestionImporter";
import { PaperQuestionIndex } from "./PaperQuestionIndex";
import { PaperQuestionDetail } from "./PaperQuestionDetail";
import { questionEditorToggleLabel, questionSummary } from "./paperQuestionPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";

type ViewProps = Readonly<{ context: PaperRubricViewContext }>;

function paperContentReady({ config, selectedExam }: PaperRubricViewContext) {
  return !config.loading && !config.error && Boolean(selectedExam) && !config.configLoading && !config.configError;
}

export function PaperRubricView({ context }: ViewProps) {
  return <div className="page-stack">
    {!paperContentReady(context) && <Form form={context.questionWorkflow.form} component={false} />}
    <PaperStandaloneHeader context={context} />
    <PaperRubricContent context={context} />
    <PaperSourceDrawer context={context} />
  </div>;
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
  if (editableExam && options.canManage && options.canReadQuestionBank) {
    return <BankQuestionPicker key={`${options.questionBankScope}:${selectedExam.id}`} scopeKey={options.questionBankScope} examId={selectedExam.id} nextOrder={config.questions.length + 1} onCopied={actions.onBankCopied} />;
  }
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
    {showQuestions && <><PaperValidationNotice context={context} /><PaperQuestionSummary context={context} /></>}
    {showQuestions && showEditor && <section className="paper-workbench" id="paper-question-workbench">
      <PaperQuestionIndex context={context} /><PaperQuestionDetail context={context} />
    </section>}
  </>;
}

function PaperMaterialContent({ context }: ViewProps) {
  const { options, config, latestPaperImport, state, importWorkflow, actions, questionWorkflow, selectedExam } = context;
  const suggestUpload = options.mode === "embedded" && options.initialView === "questions" && !config.papers.length && !config.paperImports.length;
  return <>
    {suggestUpload && <Alert type="info" showIcon message="尚未上传考试资料"
      description="推荐先上传试卷、答案或评分标准，系统可自动识别题目和分值；也可以继续手动添加题目。"
      action={options.onNavigate && selectedExam ? <Button size="small" onClick={() => options.onNavigate?.(`/exams/${encodeURIComponent(selectedExam.id)}/paper`)}>上传资料</Button> : undefined} />}
    {options.initialView === "materials" && <PaperImportWorkspace canManage={options.canManage} latestPaperImport={latestPaperImport}
      existingQuestions={config.questions} workflow={importWorkflow} setShowQuestionEditor={questionWorkflow.setShowQuestionEditor}
      initialExamId={options.fixedExamId ?? options.initialExamId} onNavigate={options.onNavigate} connectionError={config.importConnectionError} onRecheck={config.retryImportConnection} />}
    {latestPaperImport?.status === "review_required" && options.canManage && <PaperStructurePanel job={latestPaperImport}
      disabled={state.unsavedWork || importWorkflow.reviewDirty} onAdopted={actions.onStructureAdopted} />}
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

function PaperQuestionSummary({ context }: ViewProps) {
  const { config, latestPaperImport, structureConfirmed, pendingDrafts, importWorkflow, options, state, questionWorkflow } = context;
  const summary = questionSummary(config.questions, latestPaperImport, structureConfirmed);
  return <section id="paper-question-summary" className="workspace-section paper-question-summary">
    <div className="paper-question-summary-title"><span className="preparation-kicker">题目配置</span><h2>小题与分值</h2>{options.mode === "embedded" ? <PaperBankActions context={context} /> : null}</div>
    <div className="paper-question-summary-facts" aria-label="题目配置概览">
      <span><strong>{config.questions.length}</strong> 道{structureConfirmed || !latestPaperImport ? "正式" : "已配置"}题目</span>
      <span>{summary.pendingScoreCount ? "已确认分值 " : ""}<strong>{summary.totalScore}</strong> 分</span>
      {summary.pendingScoreCount > 0 && <span className="needs-attention">分值待确认 <strong>{summary.pendingScoreCount}</strong></span>}
      <span className={summary.needsAnswerCount ? "needs-attention" : ""}>待补答案 <strong>{summary.needsAnswerCount}</strong></span>
      <span className={summary.needsRubricCount ? "needs-attention" : ""}>待锁定评分细则 <strong>{summary.needsRubricCount}</strong></span>
      {pendingDrafts.length > 0 && <span className="needs-attention">识别候选 <strong>{pendingDrafts.length}</strong></span>}
      {summary.unmatchedPlaceholderCount > 0 && <span className="needs-attention">待核对预设占位 <strong>{summary.unmatchedPlaceholderCount}</strong></span>}
    </div>
    {latestPaperImport?.status === "review_required" && structureConfirmed && config.questions.length > 0 && <Button
      loading={importWorkflow.fillingImportContent} disabled={!options.canManage || state.unsavedWork || importWorkflow.reviewDirty}
      onClick={() => void importWorkflow.fillRecognizedContent(latestPaperImport)}>自动填入识别内容</Button>}
    {options.initialView === "materials" && <Button onClick={() => questionWorkflow.setShowQuestionEditor((current) => !current)}>
      {questionEditorToggleLabel(questionWorkflow.showQuestionEditor, config.questions.length)}
    </Button>}
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
