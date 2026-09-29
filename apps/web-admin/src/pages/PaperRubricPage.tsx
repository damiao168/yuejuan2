import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  InputNumber,
  List,
  Select,
  Space,
  Switch
} from "antd";
import { CheckCircle2, LockKeyhole, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { getPaperImportUserMessage } from "../api/client";
import {
  validatePaperConfig,
  type RubricEvidenceRequirement
} from "../api/papers";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { AssessmentProfileEditor } from "../components/features/assessment/AssessmentProfileEditor";
import { questionTypeOptions } from "../constants/examCatalog";
import { PaperImportWorkspace } from "../features/paper-import/PaperImportWorkspace";
import { PaperImportReviewPanel } from "../features/paper-import/PaperImportReviewPanel";
import { usePaperConfigData } from "../features/paper-import/usePaperConfigData";
import { useScoringRules } from "../features/paper-import/useScoringRules";
import { usePaperImportWorkflow } from "../features/paper-import/usePaperImportWorkflow";
import { useQuestionEditor } from "../features/paper-rubric/hooks/useQuestionEditor";
import { useRubricEditor } from "../features/paper-rubric/hooks/useRubricEditor";
import {
  formatPaperError,
  formulaEvidenceEnabled,
  labelFrom,
  rubricStatusLabel,
  rubricStatusOptions,
  rubricTone
} from "../features/paper-rubric/paperPresentation";
import { BankQuestionPicker } from "../features/question-bank/BankQuestionPicker";
import { HistoryQuestionImporter } from "../features/question-bank/HistoryQuestionImporter";
import { MathMarkdown } from "../components/MathMarkdown";

const toleranceQuestionTypes = ["numeric", "formula", "calculation"];

const formulaEvidenceOptions: { label: string; value: RubricEvidenceRequirement["type"] }[] = [
  { label: "关键等价变形成立", value: "valid_transformation" },
  { label: "最终结果经核验", value: "final_result" },
  { label: "出现指定概念", value: "concept" },
  { label: "单位正确", value: "unit" },
  { label: "定义域／取值条件", value: "domain" }
];


export function PaperRubricPage({
  canManage,
  canManageAssessment,
  canReadQuestionBank=false,
  canImportQuestionBank=false,
  questionBankScope="",
  initialExamId = "",
  fixedExamId,
  mode = "standalone",
  initialView = "materials",
  onExamChanged,
  onNavigate
}: {
  canManage: boolean;
  canManageAssessment: boolean;
  canReadQuestionBank?:boolean;
  canImportQuestionBank?:boolean;
  questionBankScope?:string;
  initialExamId?: string;
  fixedExamId?: string;
  mode?: "standalone" | "embedded";
  initialView?: "materials" | "questions";
  onExamChanged?: () => void;
  onNavigate?: (path: string) => void;
}) {
  const { message, modal } = App.useApp();
  const [formDirty, setFormDirty] = useState(false);
  const [activeCandidateIndex, setActiveCandidateIndex] = useState<number | null>(null);
  const [detailTab, setDetailTab] = useState("content");
	const config = usePaperConfigData({ initialExamId, fixedExamId });
	const {
		exams, selectedExamId, setSelectedExamId, papers, paperImports, questions,
		selectedQuestionId, setSelectedQuestionId, editorMode, setEditorMode,
		loading, configLoading, error, configError, validation, setValidation,
		loadExams, loadConfig
	} = config;
  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const importWorkflow = usePaperImportWorkflow({
    canManage, selectedExam, papers, paperImports, loadConfig, onChanged: onExamChanged
  });
  const selectedQuestion = useMemo(
    () => (editorMode === "edit" ? questions.find((question) => question.id === selectedQuestionId) ?? null : null),
    [editorMode, questions, selectedQuestionId]
  );
  const latestPaperImport = paperImports[0];
  const pendingDrafts = latestPaperImport?.status !== "applied" ? importWorkflow.reviewDrafts : [];
  useEffect(() => {
    setActiveCandidateIndex(latestPaperImport?.status === "review_required" && latestPaperImport.questions.length ? 0 : null);
  }, [latestPaperImport?.id, latestPaperImport?.status]);
  useEffect(() => { setFormDirty(false); }, [selectedQuestion?.id]);
  const questionWorkflow = useQuestionEditor({
    selectedExam, selectedQuestion, papers, questionCount: questions.length,
    editorMode, setEditorMode, setSelectedQuestionId, preserveUnsaved: formDirty, loadConfig,
    onChanged: onExamChanged
  });
  const {
    form, savingQuestion, showQuestionEditor, setShowQuestionEditor,
    saveQuestion, confirmDeleteQuestion
  } = questionWorkflow;
  const watchedQuestionType = Form.useWatch("question_type", form);
  const rubricWorkflow = useRubricEditor({
    selectedQuestion, selectedExamId, loadConfig, onChanged: onExamChanged
  });
  const {
    savingRubric, rubricDirty, discardRubricDraft, rubricStatus, setRubricStatus, rubricPoints, deductionsJson,
    setDeductionsJson, examplesJson, setExamplesJson, pointTotal: rubricTotal,
    scoreMismatch, updatePoint, addEvidenceRequirement, updateEvidenceRequirement,
    removeEvidenceRequirement, removePoint, addPoint, saveRubric
  } = rubricWorkflow;
  const isFillBlank = (watchedQuestionType ?? selectedQuestion?.question_type) === "fill_blank";
  const selectedLocked = !isFillBlank && selectedQuestion?.rubric?.status === "locked";
  const questionDisabled = !canManage || selectedLocked;
  const showTolerance = toleranceQuestionTypes.includes(watchedQuestionType ?? "");
  const showFormulaEvidence = formulaEvidenceEnabled(selectedExam?.subject, selectedQuestion?.question_type);
  const objectiveRuleType = selectedQuestion && ["single_choice", "true_false", "multiple_choice", "fill_blank", "numeric"].includes(selectedQuestion.question_type) ? selectedQuestion.question_type : "";
  const editorUsesFixedScoring = ["single_choice", "true_false", "multiple_choice", "fill_blank", "numeric"].includes(watchedQuestionType ?? selectedQuestion?.question_type ?? "");
  const scoringRuleWorkflow = useScoringRules({ selectedQuestion, objectiveRuleType, onChanged: onExamChanged });
  const {
    scoringRuleConfig, scoringRuleDirty, discardScoringRuleDraft, savingScoringRule, publishedScoringRule,
    setRuleConfig, saveScoringRule
  } = scoringRuleWorkflow;
  const unsavedWork = formDirty || rubricDirty || scoringRuleDirty;

  function navigateAfterDirtyCheck(action: () => void) {
    if (!unsavedWork) { action(); return; }
    modal.confirm({
      title: "当前题目有未保存修改",
      content: "切换题目会放弃当前题目、评分细则或客观规则中尚未保存的修改。",
      okText: "放弃修改并切换",
      okButtonProps: { danger: true },
      cancelText: "继续编辑",
      onOk: () => { setFormDirty(false); discardRubricDraft(); discardScoringRuleDraft(); action(); }
    });
  }

  // 三个编辑区共享刷新后的题目数据；保存一处前先处理其他草稿，避免刷新覆盖未提交内容。
  function saveWithoutOverwritingOtherDrafts(section: "question" | "rubric" | "scoring", action: () => void) {
    const otherDirty = section === "question" ? rubricDirty || scoringRuleDirty
      : section === "rubric" ? formDirty || scoringRuleDirty : formDirty || rubricDirty;
    if (otherDirty) {
      message.warning("请先保存或放弃其他区域的修改；刷新题目数据会覆盖那些未保存内容。");
      return;
    }
    action();
  }

  async function runValidation() {
    if (!selectedExam) {
      return;
    }
    try {
      const result = await validatePaperConfig(selectedExam.id);
      setValidation(result.result);
      if (result.result.valid) {
        message.success("试卷配置检查通过");
      } else {
        message.warning("试卷配置存在问题，请查看下方详情");
      }
    } catch (currentError) {
      message.error(formatPaperError(currentError));
    }
  }

  const embedded = mode === "embedded";
  const editorVisible = !loading && !error && Boolean(selectedExam) && !configLoading && !configError;
  const showMaterials = initialView === "materials";
  const showQuestionConfiguration = initialView === "questions" || (!embedded && (questions.length > 0 || Boolean(latestPaperImport)));
  const questionEditorVisible = initialView === "questions" || showQuestionEditor;

  return (
    <div className="page-stack">
      {!editorVisible ? <Form form={form} component={false} /> : null}
      {!embedded ? <section className="page-heading">
        <div>
          <Space>
            <h1>考试资料与评分配置</h1>
            {canManage&&canReadQuestionBank&&selectedExam&&["draft","configured"].includes(selectedExam.status)?<BankQuestionPicker scopeKey={questionBankScope} examId={selectedExam.id} nextOrder={questions.length+1} onCopied={async()=>{await loadConfig(selectedExam.id);onExamChanged?.();}}/>:null}
			{canImportQuestionBank&&selectedExam&&!["draft","configured"].includes(selectedExam.status)&&questions.length?<HistoryQuestionImporter key={`${questionBankScope}:${selectedExam.id}`} scopeKey={questionBankScope} examId={selectedExam.id} questions={questions} onImported={async()=>{await loadConfig(selectedExam.id,{silent:true});}} onOpenBank={onNavigate?()=>onNavigate("/question-bank"):undefined}/>:null}
          </Space>
          <p>手里有什么考试资料就直接添加；系统自动识别题目、答案与解析，并提示需要核对的缺失或冲突。</p>
        </div>
        <Space wrap>
          <Button icon={<RefreshCw size={16} />} onClick={() => void loadExams()} loading={loading}>
            刷新
          </Button>
          <Button icon={<CheckCircle2 size={16} />} disabled={!selectedExam} onClick={() => void runValidation()}>
            完整性检查
          </Button>
        </Space>
      </section> : null}

      {!embedded ? <section className="workspace-section filter-panel">
        <div className="paper-topline">
          <Select
            className="exam-picker"
            placeholder="选择考试"
            value={selectedExamId || undefined}
            options={exams.map((exam) => ({ label: exam.name, value: exam.id }))}
            onChange={(value) => setSelectedExamId(value)}
            loading={loading}
          />
          {selectedExam ? <span className="muted">当前考试总分：{selectedExam.total_score}</span> : null}
        </div>
      </section> : null}

      {loading ? (
        <section className="workspace-section">
          <LoadingState label="正在读取考试列表" />
        </section>
      ) : error ? (
        <ErrorState message={error} onRetry={() => void loadExams()} />
      ) : !selectedExam ? (
        <section className="workspace-section">
          <EmptyState title="暂无考试" description="还没有可配置的考试。请先在「考试管理」中创建考试。" />
        </section>
      ) : configLoading ? (
        <section className="workspace-section">
          <LoadingState label="正在读取试卷与题目配置" />
        </section>
      ) : configError ? (
        <ErrorState message={configError} onRetry={() => void loadConfig(selectedExam.id)} />
      ) : (
        <>
          {embedded && initialView === "questions" && !papers.length && !paperImports.length ? <Alert
            type="info"
            showIcon
            message="尚未上传考试资料"
            description="推荐先上传试卷、答案或评分标准，系统可自动识别题目和分值；也可以继续手动添加题目。"
            action={onNavigate ? <Button size="small" onClick={() => onNavigate(`/exams/${encodeURIComponent(selectedExam.id)}/paper`)}>上传资料</Button> : undefined}
          /> : null}
          {showMaterials ? <PaperImportWorkspace
            canManage={canManage}
            latestPaperImport={latestPaperImport}
            existingQuestions={questions}
            workflow={importWorkflow}
            setShowQuestionEditor={setShowQuestionEditor}
            initialExamId={fixedExamId ?? initialExamId}
            onNavigate={onNavigate}
          /> : null}

          {showQuestionConfiguration && validation ? (
            <Alert
              type={validation.valid ? "success" : "warning"}
              showIcon
              message={validation.valid ? "试卷配置完整" : "试卷配置存在问题"}
              description={
                validation.valid ? (
                  "检查完成，未发现配置问题。"
                ) : (
                  <ul className="validation-issue-list">
                    {validation.issues.map((issue, index) => (
                      <li key={`${issue.code}-${index}`}>
                        {getPaperImportUserMessage(issue.message, "考试资料存在需要核对的内容")}
                      </li>
                    ))}
                  </ul>
                )
              }
            />
          ) : null}

          {showQuestionConfiguration ? <section id="paper-question-summary" className="workspace-section paper-question-summary">
            <div>
              <strong>{questions.length}</strong>
              <span>道题目</span>
               <small>{questions.filter((question) => String(question.answer_key?.standard_answer ?? "").trim()).length} 道已配置标准答案</small>
            </div>
            {initialView === "materials" ? <Button onClick={() => setShowQuestionEditor((current) => !current)}>
              {showQuestionEditor ? "收起逐题校对" : questions.length ? "逐题校对" : "手动补充题目"}
            </Button> : null}
          </section> : null}

           {showQuestionConfiguration && questionEditorVisible ? <section className="paper-workbench" id="paper-question-workbench">
            <aside className="workspace-section question-list-panel">
              <div className="section-head">
                <div>
                  <h2>题目列表</h2>
                   <p>{questions.length} 道正式题目{pendingDrafts.length ? ` · ${pendingDrafts.length} 道资料候选待核对` : ""}</p>
                </div>
                <Button
                  size="small"
                  icon={<Plus size={14} />}
                  disabled={!canManage}
                   onClick={() => navigateAfterDirtyCheck(() => {
                     setActiveCandidateIndex(null);
                     setEditorMode("create");
                     setSelectedQuestionId(null);
                     setDetailTab("content");
                   })}
                >
                  新建
                </Button>
              </div>
               {pendingDrafts.length ? <div className="paper-pending-questions"><strong>待应用的识别结果</strong>{pendingDrafts.map((draft, index) => <button key={`${draft.candidate_id ?? draft.question_no}-${index}`} type="button" className={`paper-pending-question${activeCandidateIndex === index ? " is-active" : ""}`} onClick={() => navigateAfterDirtyCheck(() => { setActiveCandidateIndex(index); setDetailTab("content"); })}><span>第 {draft.question_no || index + 1} 题</span><span>{draft.score > 0 ? `${draft.score} 分` : "分值待确认"}</span></button>)}</div> : null}
               <List
                className="question-list"
                dataSource={questions}
                locale={{ emptyText: <EmptyState title="暂无题目" description="先新建题目配置。" /> }}
                renderItem={(question) => (
                  <List.Item
                     className={activeCandidateIndex === null && selectedQuestionId === question.id ? "question-list-item active" : "question-list-item"}
                     onClick={() => navigateAfterDirtyCheck(() => {
                       setActiveCandidateIndex(null);
                       setEditorMode("edit");
                       setSelectedQuestionId(question.id);
                       setDetailTab("content");
                     })}
                  >
                    <div>
                      <strong>{question.question_no}</strong>
                      <span>{labelFrom(questionTypeOptions, question.question_type)}</span>
                    </div>
                    <div>
                      <span>{question.score} 分</span>
                       <span title={question.question_type === "fill_blank" ? "根据标准答案完整性显示状态" : question.rubric?.status}>
                         {question.question_type === "fill_blank"
                           ? String(question.answer_key?.standard_answer ?? "").trim()
                             ? <StatusTag tone="success">答案已配置</StatusTag>
                             : <StatusTag tone="warning">答案待补充</StatusTag>
                           : ["single_choice", "multiple_choice", "true_false", "numeric"].includes(question.question_type)
                             ? String(question.answer_key?.standard_answer ?? "").trim()
                               ? <StatusTag tone="success">固定判分</StatusTag>
                               : <StatusTag tone="warning">答案待补充</StatusTag>
                             : <StatusTag tone={rubricTone(question.rubric?.status)}>{rubricStatusLabel(question.rubric?.status)}</StatusTag>}
                      </span>
                    </div>
                  </List.Item>
                )}
              />
            </aside>

             {activeCandidateIndex !== null && latestPaperImport && pendingDrafts[activeCandidateIndex] ? <section className="workspace-section question-editor paper-candidate-editor">
               <div className="section-head paper-editor-sticky-head">
                 <div><span className="preparation-kicker">资料识别候选</span><h2>第 {pendingDrafts[activeCandidateIndex].question_no || activeCandidateIndex + 1} 题</h2><p>此内容尚未写入正式题目；核对分值、答案与解析后确认导入。</p></div>
                 {latestPaperImport.status === "review_required" ? <Space wrap><Button loading={importWorkflow.savingImportReview} onClick={() => void importWorkflow.saveImportReview(latestPaperImport)}>保存核对</Button><Button type="primary" loading={importWorkflow.parsing} disabled={importWorkflow.invalidReviewRubric || importWorkflow.invalidReviewScore} onClick={() => void importWorkflow.confirmPaperImport(latestPaperImport)}>确认导入</Button></Space> : null}
               </div>
               <PaperImportReviewPanel job={latestPaperImport} drafts={[pendingDrafts[activeCandidateIndex]]} existingQuestions={questions} showOverview={false} readOnly={latestPaperImport.status !== "review_required"} onChange={(_, field, patch) => importWorkflow.updateReviewDraft(activeCandidateIndex, field, patch)} onOpenSource={(ref) => void importWorkflow.openImportSource(ref.file_asset_id, ref.page_no)} />
             </section> : <section className="workspace-section question-editor">
               <div className="section-head paper-editor-sticky-head">
                 <div>
                   <span className="preparation-kicker">正式题目</span>
                   <h2>{editorMode === "edit" ? `第 ${selectedQuestion?.question_no ?? "—"} 题` : "新建题目"}</h2>
                   <p>{selectedLocked ? "评分细则已锁定。" : unsavedWork ? "有未保存的修改" : "题目、答案与评分配置"}</p>
                </div>
                <Space>
                  {selectedLocked ? <StatusTag tone="success">已锁定</StatusTag> : null}
                  {editorMode === "edit" ? (
                    <Button danger icon={<Trash2 size={16} />} disabled={questionDisabled} onClick={confirmDeleteQuestion}>
                      删除
                    </Button>
                  ) : null}
                   {detailTab === "content" ? <Button type="primary" icon={<Save size={16} />} disabled={questionDisabled} loading={savingQuestion} onClick={() => saveWithoutOverwritingOtherDrafts("question", () => { void saveQuestion().then((saved) => { if (saved) setFormDirty(false); }); })}>保存题目与答案</Button> : null}
                 </Space>
               </div>
               <div className="paper-detail-tabs" role="tablist" aria-label="题目详情">
                 <button type="button" role="tab" aria-selected={detailTab === "content"} className={detailTab === "content" ? "active" : ""} onClick={() => setDetailTab("content")}>题目与参考解答</button>
                 <button type="button" role="tab" aria-selected={detailTab === "scoring"} className={detailTab === "scoring" ? "active" : ""} onClick={() => setDetailTab("scoring")}>评分配置</button>
                 <button type="button" role="tab" aria-selected={detailTab === "source"} className={detailTab === "source" ? "active" : ""} onClick={() => setDetailTab("source")}>来源与答题区域</button>
               </div>
               <div style={{ display: detailTab === "content" ? undefined : "none" }}>
               <Form form={form} layout="vertical" disabled={questionDisabled} onValuesChange={() => setFormDirty(true)}>
                 <div className="form-grid">
                   <Form.Item label="题号" name="question_no" rules={[{ required: true, message: "请输入题号" }]}>
                    <Input placeholder="Q1" />
                  </Form.Item>
                  <Form.Item label="题型" name="question_type" rules={[{ required: true, message: "请选择题型" }]}>
                    <Select options={questionTypeOptions} />
                  </Form.Item>
                  <Form.Item label="分值" name="score" rules={[{ required: true, message: "请输入分值" }]}>
                    <InputNumber min={0.5} precision={1} className="full-width-control" />
                  </Form.Item>
                 </div>
                  <Form.Item label="题干" name="stem">
                    <Input.TextArea autoSize={{ minRows: 3, maxRows: 9 }} placeholder="上传资料识别后自动填充，或在此录入题干" />
                  </Form.Item>
                  {["single_choice", "multiple_choice"].includes(watchedQuestionType ?? "") ? <Form.List name="options">{(fields, { add, remove }) => <div className="paper-option-editor"><div className="section-head"><h3>选项</h3><Button size="small" icon={<Plus size={14} />} onClick={() => add("")}>添加选项</Button></div>{fields.map((field, index) => <div className="paper-option-row" key={field.key}><span>{String.fromCharCode(65 + index)}</span><Form.Item name={field.name} rules={[{ required: true, message: "请输入选项" }]}><Input placeholder={`选项 ${String.fromCharCode(65 + index)} 内容`} /></Form.Item><Button type="text" danger aria-label={`删除选项 ${String.fromCharCode(65 + index)}`} icon={<Trash2 size={14} />} onClick={() => remove(field.name)} /></div>)}</div>}</Form.List> : null}
                  <Form.Item label="标准答案" name="standard_answer" rules={[{ required: true, message: "请输入标准答案" }]}>
                   <Input.TextArea autoSize={{ minRows: 2, maxRows: 7 }} placeholder="上传答案资料识别后自动填充，或在此录入" />
                 </Form.Item>
                 {isFillBlank ? <Alert type={String(selectedQuestion?.answer_key?.standard_answer ?? "").trim() ? "info" : "warning"} showIcon message={String(selectedQuestion?.answer_key?.standard_answer ?? "").trim() ? "填空题按答案判分" : "标准答案尚未配置"} description="逐空答案、等价写法及分值应在评分配置中核对。数学区间的括号不可随意忽略。" /> : null}
                 {selectedQuestion?.solution?.raw_text ? <div className="paper-reference-solution"><h3>教师解析</h3><MathMarkdown>{selectedQuestion.solution.raw_text}</MathMarkdown></div> : <p className="paper-missing-content">暂无教师解析。若资料中已有详解，请先核对并应用识别结果。</p>}
                <details className="paper-advanced-settings"><summary>高级答案设置</summary><Form.Item label="等价答案" name="equivalent_answers">
                  <Select mode="tags" placeholder="输入后回车" />
                </Form.Item>
                {showTolerance ? (
                  <Form.Item label="答案误差范围（选填）">
                    <div className="form-grid">
                      <Form.Item label="允许绝对误差" name="tolerance_absolute">
                        <InputNumber min={0} className="full-width-control" />
                      </Form.Item>
                      <Form.Item label="允许相对误差（%）" name="tolerance_relative">
                        <InputNumber min={0} className="full-width-control" />
                      </Form.Item>
                    </div>
                  </Form.Item>
                ) : null}</details>
               </Form>
               </div>

               {detailTab === "source" ? <div className="paper-source-context"><h3>来源</h3><p>{selectedQuestion?.paper_import_id ? "本题由考试资料识别导入" : "本题暂无已关联的识别来源"}</p>{selectedQuestion?.paper_import_source_refs?.length ? <p>已记录 {selectedQuestion.paper_import_source_refs.length} 处题目来源；教师解析来源 {selectedQuestion.solution?.source_refs.length ?? 0} 处。</p> : null}<h3>答题区域</h3><p>答题卡上的作答区域应在模板中可视化框选，不在题目配置中填写坐标。</p>{onNavigate ? <Button onClick={() => navigateAfterDirtyCheck(() => onNavigate(`/exams/${encodeURIComponent(selectedExam.id)}/template`))}>打开答题卡模板</Button> : null}</div> : null}

               {detailTab === "scoring" ? <>

              <details className="paper-advanced-settings"><summary>高级评分配置</summary><AssessmentProfileEditor
                examId={selectedExam.id}
                examSubject={selectedExam.subject}
                examStatus={selectedExam.status}
                question={selectedQuestion}
                canManage={canManageAssessment}
                onChanged={onExamChanged}
              /></details>

              {selectedQuestion && objectiveRuleType ? <section className="scoring-rule-editor">
                <div className="section-head">
                  <div>
                    <h2>固定判分规则</h2>
                    <p>标准答案：{String(selectedQuestion.answer_key?.standard_answer ?? "").trim() || "待补充"} · 满分 {selectedQuestion.score} 分</p>
                  </div>
                  <Space>
                    {publishedScoringRule ? <StatusTag tone="success">{`已发布 v${publishedScoringRule.version}`}</StatusTag> : <StatusTag tone="warning">未发布</StatusTag>}
                    <Button icon={<Save size={16} />} disabled={!canManage} loading={savingScoringRule} onClick={() => saveWithoutOverwritingOtherDrafts("scoring", () => { void saveScoringRule(false); })}>保存草稿</Button>
                    <Button type="primary" icon={<LockKeyhole size={16} />} disabled={!canManage || !String(selectedQuestion.answer_key?.standard_answer ?? "").trim()} loading={savingScoringRule} onClick={() => saveWithoutOverwritingOtherDrafts("scoring", () => { void saveScoringRule(true); })}>发布规则</Button>
                  </Space>
                </div>
                {!String(selectedQuestion.answer_key?.standard_answer ?? "").trim() ? <Alert type="warning" showIcon message="尚无标准答案，无法判分" description="请先在「题目与参考解答」中核对答案并保存。" /> : <Alert type="info" showIcon message="全对得满分；错误得 0 分" description={objectiveRuleType === "multiple_choice" ? "多选题少选分与错选扣分以下方政策为准。" : objectiveRuleType === "fill_blank" ? "填空题还需核对每空答案、等价写法和分值。" : "空白或识别不确定的答卷应转人工确认。"} />}
                {objectiveRuleType === "multiple_choice" ? <div className="scoring-rule-grid">
                  <label><span>允许少选得分</span><Switch checked={Boolean(scoringRuleConfig.allow_partial)} onChange={(value) => setRuleConfig("allow_partial", value)} /></label>
                  <label><span>每个正确选项分值</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.score_per_correct_option ?? 0)} onChange={(value) => setRuleConfig("score_per_correct_option", Number(value ?? 0))} /></label>
                  <label><span>每个错误选项扣分</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.wrong_option_penalty ?? selectedQuestion.score)} onChange={(value) => setRuleConfig("wrong_option_penalty", Number(value ?? 0))} /></label>
                  <label><span>最低得分</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.minimum_score ?? 0)} onChange={(value) => setRuleConfig("minimum_score", Number(value ?? 0))} /></label>
                </div> : null}
                {objectiveRuleType === "fill_blank" ? <div className="scoring-rule-grid">
                  <label><span>忽略大小写</span><Switch checked={Boolean(scoringRuleConfig.ignore_case)} onChange={(value) => setRuleConfig("ignore_case", value)} /></label>
                  <label><span>忽略空格</span><Switch checked={Boolean(scoringRuleConfig.ignore_spaces)} onChange={(value) => setRuleConfig("ignore_spaces", value)} /></label>
                  <label><span>忽略标点（数学区间慎用）</span><Switch checked={Boolean(scoringRuleConfig.ignore_punctuation)} onChange={(value) => setRuleConfig("ignore_punctuation", value)} /></label>
                </div> : null}
                {objectiveRuleType === "numeric" ? <div className="scoring-rule-grid">
                  <label><span>绝对误差</span><InputNumber min={0} precision={6} value={Number(scoringRuleConfig.absolute ?? 0)} onChange={(value) => setRuleConfig("absolute", Number(value ?? 0))} /></label>
                  <label><span>相对误差</span><InputNumber min={0} max={1} step={0.001} precision={6} value={Number(scoringRuleConfig.relative ?? 0)} onChange={(value) => setRuleConfig("relative", Number(value ?? 0))} /></label>
                  <label><span>单位必须填写</span><Switch checked={Boolean(scoringRuleConfig.unit_required)} onChange={(value) => setRuleConfig("unit_required", value)} /></label>
                </div> : null}
                {["single_choice", "true_false"].includes(objectiveRuleType) ? <Alert type="info" showIcon message="使用标准答案精确判定" description="空白、多涂、擦除或识别把握不足的答卷不会自动判零分，将转入人工确认。" /> : null}
              </section> : editorUsesFixedScoring ? <Alert type="info" showIcon message="先保存题目与答案" description="保存后可核对并发布固定判分规则。" /> : null}

              {editorUsesFixedScoring ? null : <div className="rubric-editor">
                <div className="section-head">
                  <div>
                    <h2>评分细则</h2>
                    <p>
                      {selectedQuestion ? `采分点合计 ${rubricTotal} / ${selectedQuestion.score} 分` : "先保存题目，再配置评分细则"}
                    </p>
                  </div>
                  <Space>
                    <span className="muted">提交为</span>
                    <Select value={rubricStatus} options={rubricStatusOptions} disabled={questionDisabled} onChange={setRubricStatus} className="rubric-status-select" />
                    <Button icon={<Plus size={16} />} disabled={questionDisabled} onClick={addPoint}>
                      添加采分点
                    </Button>
                    <Button type="primary" icon={rubricStatus === "locked" ? <LockKeyhole size={16} /> : <Save size={16} />} disabled={questionDisabled || !selectedQuestion || scoreMismatch} loading={savingRubric} onClick={() => saveWithoutOverwritingOtherDrafts("rubric", () => { void saveRubric(); })}>
                      {rubricStatus === "locked" ? "锁定评分细则" : rubricStatus === "pending_review" ? "提交审批" : "保存评分细则"}
                    </Button>
                  </Space>
                </div>
                {scoreMismatch ? <Alert type="error" showIcon message="评分细则分值不匹配" description="采分点总分必须等于题目分值，调整后才能保存。" /> : null}
                {showFormulaEvidence ? (
                  <Alert
                    type="info"
                    showIcon
                    message="公式与步骤证据只作阅卷提示"
                    description="系统只会为数学、物理、化学的公式类题目生成这些证据；不会自动给分，最终分数仍由规则或教师确认。"
                  />
                ) : null}
                <div className="paper-rubric-points">
                  {rubricPoints.map((point, index) => (
                    <div className="paper-rubric-point" key={`${point.id}-${index}`}>
                      <div className="paper-rubric-point-heading">
                        <strong>采分点 {index + 1}</strong>
                        <Button danger size="small" aria-label={`删除采分点 ${index + 1}`} icon={<Trash2 size={14} />} disabled={questionDisabled || rubricPoints.length <= 1} onClick={() => removePoint(index)}>删除</Button>
                      </div>
                      <label className="paper-rubric-description">
                        <span>得分条件</span>
                        <Input.TextArea value={point.description} disabled={questionDisabled} autoSize={{ minRows: 2, maxRows: 5 }} placeholder="描述学生需要完成的数学步骤或结果" onChange={(event) => updatePoint(index, { description: event.target.value })} />
                      </label>
                      <div className="paper-rubric-point-fields">
                        <label><span>分值</span><InputNumber value={point.score} disabled={questionDisabled} min={0} precision={1} className="full-width-control" onChange={(value) => updatePoint(index, { score: Number(value ?? 0) })} /></label>
                        <label><span>必需</span><Switch checked={point.required} disabled={questionDisabled} onChange={(checked) => updatePoint(index, { required: checked })} /></label>
                      </div>
                      {showFormulaEvidence ? <details className="paper-rubric-evidence">
                        <summary>识别证据（仅供教师参考）{point.evidence_requirements?.length ? ` · ${point.evidence_requirements.length} 项` : ""}</summary>
                        {(point.evidence_requirements ?? []).map((requirement, requirementIndex) => (
                          <div className="paper-rubric-evidence-row" key={`${requirement.type}-${requirementIndex}`}>
                            <Select value={requirement.type} options={formulaEvidenceOptions} disabled={questionDisabled} className="rubric-evidence-type-select" onChange={(type: RubricEvidenceRequirement["type"]) => updateEvidenceRequirement(index, requirementIndex, { type, target: type === "concept" || type === "unit" || type === "domain" ? requirement.target ?? "" : undefined, minimum: undefined, children: undefined })} />
                            {requirement.type === "concept" || requirement.type === "unit" || requirement.type === "domain" ? <Input value={requirement.target} disabled={questionDisabled} placeholder={requirement.type === "concept" ? "例如：配方法" : requirement.type === "unit" ? "例如：m/s" : "例如：x ≥ 0"} className="rubric-evidence-target-input" onChange={(event) => updateEvidenceRequirement(index, requirementIndex, { target: event.target.value })} /> : null}
                            <Button type="text" danger size="small" aria-label="删除识别证据" disabled={questionDisabled} icon={<Trash2 size={14} />} onClick={() => removeEvidenceRequirement(index, requirementIndex)} />
                          </div>
                        ))}
                        <Button type="link" size="small" icon={<Plus size={14} />} disabled={questionDisabled} onClick={() => addEvidenceRequirement(index)}>添加识别证据</Button>
                      </details> : null}
                    </div>
                  ))}
                </div>
                <details className="paper-advanced-settings"><summary>高级评分数据</summary><div className="form-grid rubric-json-grid">
                  <label>
                    <span>扣分点（JSON 格式，选填）</span>
                    <Input.TextArea
                      value={deductionsJson}
                      disabled={questionDisabled}
                      rows={4}
                      spellCheck={false}
                      placeholder={'示例：[{"description": "缺少必要步骤", "score": -2}]'}
                      onChange={(event) => setDeductionsJson(event.target.value)}
                    />
                  </label>
                  <label>
                    <span>样例答案（JSON 格式，选填）</span>
                    <Input.TextArea
                      value={examplesJson}
                      disabled={questionDisabled}
                      rows={4}
                      spellCheck={false}
                      placeholder={'示例：["满分示例答案", "部分得分示例答案"]'}
                      onChange={(event) => setExamplesJson(event.target.value)}
                    />
                  </label>
                </div></details>
              </div>}
              </> : null}
            </section>}
          </section> : null}
        </>
      )}
    </div>
  );
}
