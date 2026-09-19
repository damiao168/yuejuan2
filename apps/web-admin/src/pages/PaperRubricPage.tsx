import { useMemo } from "react";
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
  Switch,
  type TableColumnsType
} from "antd";
import { CheckCircle2, LockKeyhole, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { getPaperImportUserMessage } from "../api/client";
import {
  validatePaperConfig,
  type RubricEvidenceRequirement,
  type RubricPoint
} from "../api/papers";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { AssessmentProfileEditor } from "../components/features/assessment/AssessmentProfileEditor";
import { questionTypeOptions } from "../constants/examCatalog";
import { PaperImportWorkspace } from "../features/paper-import/PaperImportWorkspace";
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
  onExamChanged,
  onNavigate
}: {
  canManage: boolean;
  canManageAssessment: boolean;
  canReadQuestionBank?:boolean;
  canImportQuestionBank?:boolean;
  questionBankScope?:string;
  initialExamId?: string;
  onExamChanged?: () => void;
  onNavigate?: (path: string) => void;
}) {
  const { message } = App.useApp();
	const config = usePaperConfigData(initialExamId);
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
  const questionWorkflow = useQuestionEditor({
    selectedExam, selectedQuestion, papers, questionCount: questions.length,
    editorMode, setEditorMode, setSelectedQuestionId, loadConfig,
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
    savingRubric, rubricStatus, setRubricStatus, rubricPoints, deductionsJson,
    setDeductionsJson, examplesJson, setExamplesJson, pointTotal: rubricTotal,
    scoreMismatch, updatePoint, addEvidenceRequirement, updateEvidenceRequirement,
    removeEvidenceRequirement, removePoint, addPoint, saveRubric
  } = rubricWorkflow;
  const selectedLocked = selectedQuestion?.rubric?.status === "locked";
  const questionDisabled = !canManage || selectedLocked;
  const showTolerance = toleranceQuestionTypes.includes(watchedQuestionType ?? "");
  const showFormulaEvidence = formulaEvidenceEnabled(selectedExam?.subject, selectedQuestion?.question_type);
  const objectiveRuleType = selectedQuestion && ["single_choice", "true_false", "multiple_choice", "fill_blank", "numeric"].includes(selectedQuestion.question_type) ? selectedQuestion.question_type : "";
  const scoringRuleWorkflow = useScoringRules({ selectedQuestion, objectiveRuleType, onChanged: onExamChanged });
  const {
    scoringRuleConfig, savingScoringRule, publishedScoringRule,
    setRuleConfig, saveScoringRule
  } = scoringRuleWorkflow;

  const rubricColumns: TableColumnsType<RubricPoint> = [
    {
      title: "采分点",
      dataIndex: "description",
      render: (_, point, index) => (
        <Input
          value={point.description}
          disabled={questionDisabled}
          placeholder="采分点描述"
          onChange={(event) => updatePoint(index, { description: event.target.value })}
        />
      )
    },
    {
      title: "分值",
      dataIndex: "score",
      width: 120,
      render: (_, point, index) => (
        <InputNumber
          value={point.score}
          disabled={questionDisabled}
          min={0}
          precision={1}
          className="full-width-control"
          onChange={(value) => updatePoint(index, { score: Number(value ?? 0) })}
        />
      )
    },
    {
      title: "必需",
      dataIndex: "required",
      width: 90,
      render: (_, point, index) => (
        <Switch checked={point.required} disabled={questionDisabled} onChange={(checked) => updatePoint(index, { required: checked })} />
      )
    },
    ...(showFormulaEvidence
      ? [
          {
            title: "识别证据（仅供教师参考）",
            width: 380,
            render: (_: unknown, point: RubricPoint, index: number) => {
              const requirements = point.evidence_requirements ?? [];
              return (
                <Space direction="vertical" size={6} className="full-width-control">
                  {requirements.map((requirement, requirementIndex) => (
                    <Space key={`${requirement.type}-${requirementIndex}`} wrap size={6}>
                      <Select
                        value={requirement.type}
                        options={formulaEvidenceOptions}
                        disabled={questionDisabled}
                        className="rubric-evidence-type-select"
                        onChange={(type: RubricEvidenceRequirement["type"]) =>
                          updateEvidenceRequirement(index, requirementIndex, {
                            type,
                            target: type === "concept" || type === "unit" || type === "domain" ? requirement.target ?? "" : undefined,
                            minimum: undefined,
                            children: undefined
                          })
                        }
                      />
                      {requirement.type === "concept" || requirement.type === "unit" || requirement.type === "domain" ? (
                        <Input
                          value={requirement.target}
                          disabled={questionDisabled}
                          placeholder={requirement.type === "concept" ? "例如：配方法" : requirement.type === "unit" ? "例如：m/s" : "例如：x ≥ 0"}
                          className="rubric-evidence-target-input"
                          onChange={(event) => updateEvidenceRequirement(index, requirementIndex, { target: event.target.value })}
                        />
                      ) : null}
                      <Button
                        type="text"
                        danger
                        size="small"
                        aria-label="删除识别证据"
                        disabled={questionDisabled}
                        icon={<Trash2 size={14} />}
                        onClick={() => removeEvidenceRequirement(index, requirementIndex)}
                      />
                    </Space>
                  ))}
                  <Button
                    type="link"
                    size="small"
                    icon={<Plus size={14} />}
                    disabled={questionDisabled}
                    onClick={() => addEvidenceRequirement(index)}
                  >
                    添加识别证据
                  </Button>
                </Space>
              );
            }
          } satisfies TableColumnsType<RubricPoint>[number]
        ]
      : []),
    {
      title: "操作",
      width: 90,
      render: (_, __, index) => (
        <Button danger size="small" icon={<Trash2 size={14} />} disabled={questionDisabled || rubricPoints.length <= 1} onClick={() => removePoint(index)} />
      )
    }
  ];

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

	const latestPaperImport = paperImports[0];
  const editorVisible = !loading && !error && Boolean(selectedExam) && !configLoading && !configError;

  return (
    <div className="page-stack">
      {!editorVisible ? <Form form={form} component={false} /> : null}
      <section className="page-heading">
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
      </section>

      <section className="workspace-section filter-panel">
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
      </section>

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
          <PaperImportWorkspace
            canManage={canManage}
            latestPaperImport={latestPaperImport}
            workflow={importWorkflow}
            setShowQuestionEditor={setShowQuestionEditor}
            initialExamId={initialExamId}
            onNavigate={onNavigate}
          />

          {validation ? (
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

          <section id="paper-question-summary" className="workspace-section paper-question-summary">
            <div>
              <strong>{questions.length}</strong>
              <span>道题目</span>
              <small>{questions.filter((question) => question.answer_key).length} 道已配置标准答案</small>
            </div>
            <Button onClick={() => setShowQuestionEditor((current) => !current)}>
              {showQuestionEditor ? "收起逐题校对" : questions.length ? "逐题校对" : "手动补充题目"}
            </Button>
          </section>

          {showQuestionEditor ? <section className="paper-workbench">
            <aside className="workspace-section question-list-panel">
              <div className="section-head">
                <div>
                  <h2>题目列表</h2>
                  <p>{questions.length} 道题</p>
                </div>
                <Button
                  size="small"
                  icon={<Plus size={14} />}
                  disabled={!canManage}
                  onClick={() => {
                    setEditorMode("create");
                    setSelectedQuestionId(null);
                  }}
                >
                  新建
                </Button>
              </div>
              <List
                className="question-list"
                dataSource={questions}
                locale={{ emptyText: <EmptyState title="暂无题目" description="先新建题目配置。" /> }}
                renderItem={(question) => (
                  <List.Item
                    className={selectedQuestionId === question.id ? "question-list-item active" : "question-list-item"}
                    onClick={() => {
                      setEditorMode("edit");
                      setSelectedQuestionId(question.id);
                    }}
                  >
                    <div>
                      <strong>{question.question_no}</strong>
                      <span>{labelFrom(questionTypeOptions, question.question_type)}</span>
                    </div>
                    <div>
                      <span>{question.score} 分</span>
                      <span title={question.rubric?.status}>
                        <StatusTag tone={rubricTone(question.rubric?.status)}>{rubricStatusLabel(question.rubric?.status)}</StatusTag>
                      </span>
                    </div>
                  </List.Item>
                )}
              />
            </aside>

            <section className="workspace-section question-editor">
              <div className="section-head">
                <div>
                  <h2>{editorMode === "edit" ? "题目配置" : "新建题目"}</h2>
                  <p>{selectedLocked ? "评分细则已锁定，题目和细则不可编辑。" : "保存题目修改会生成新的答案版本。"}</p>
                </div>
                <Space>
                  {selectedLocked ? <StatusTag tone="success">已锁定</StatusTag> : null}
                  {editorMode === "edit" ? (
                    <Button danger icon={<Trash2 size={16} />} disabled={questionDisabled} onClick={confirmDeleteQuestion}>
                      删除
                    </Button>
                  ) : null}
                  <Button type="primary" icon={<Save size={16} />} disabled={questionDisabled} loading={savingQuestion} onClick={() => void saveQuestion()}>
                    保存题目
                  </Button>
                </Space>
              </div>

              <Form form={form} layout="vertical" disabled={questionDisabled}>
                <div className="form-grid">
                  <Form.Item label="关联试卷版本" name="exam_paper_id">
                    <Select
                      allowClear
                      options={papers.map((paper) => ({ label: `v${paper.version_no} ${paper.file.original_name || "未命名文件"}`, value: paper.id }))}
                      placeholder="可不关联"
                    />
                  </Form.Item>
                  <Form.Item label="题号" name="question_no" rules={[{ required: true, message: "请输入题号" }]}>
                    <Input placeholder="Q1" />
                  </Form.Item>
                  <Form.Item label="题型" name="question_type" rules={[{ required: true, message: "请选择题型" }]}>
                    <Select options={questionTypeOptions} />
                  </Form.Item>
                  <Form.Item label="分值" name="score" rules={[{ required: true, message: "请输入分值" }]}>
                    <InputNumber min={0.5} precision={1} className="full-width-control" />
                  </Form.Item>
                  <Form.Item label="排序" name="sort_order" rules={[{ required: true, message: "请输入排序" }]}>
                    <InputNumber min={1} precision={0} className="full-width-control" />
                  </Form.Item>
                  <Form.Item label="知识点" name="knowledge_points">
                    <Select mode="tags" placeholder="输入后回车" />
                  </Form.Item>
                </div>
                <Form.Item label="题干" name="stem">
                  <Input.TextArea rows={3} />
                </Form.Item>
                <details className="paper-advanced-settings"><summary>高级题目设置</summary><Form.Item label="答题区域位置" required>
                  <div className="form-grid">
                    <Form.Item label="所在页码" name="answer_area_page" rules={[{ required: true, message: "请输入所在页码" }]}>
                      <InputNumber min={1} precision={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="左边距" name="answer_area_x" rules={[{ required: true, message: "请输入左边距" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="上边距" name="answer_area_y" rules={[{ required: true, message: "请输入上边距" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="宽度" name="answer_area_w" rules={[{ required: true, message: "请输入宽度" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                    <Form.Item label="高度" name="answer_area_h" rules={[{ required: true, message: "请输入高度" }]}>
                      <InputNumber min={0} className="full-width-control" />
                    </Form.Item>
                  </div>
                  <p className="muted">也可以在「答题卡模板」中拖拽框选，更直观。</p>
                </Form.Item></details>
                <Form.Item label="标准答案" name="standard_answer" rules={[{ required: true, message: "请输入标准答案" }]}>
                  <Input.TextArea rows={3} />
                </Form.Item>
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

              <details className="paper-advanced-settings"><summary>高级评分配置</summary><AssessmentProfileEditor
                examId={selectedExam.id}
                examSubject={selectedExam.subject}
                examStatus={selectedExam.status}
                question={selectedQuestion}
                canManage={canManageAssessment}
                onChanged={onExamChanged}
              /></details>

              {selectedQuestion && objectiveRuleType ? <details className="paper-advanced-settings"><summary>客观题评分规则</summary><section className="scoring-rule-editor">
                <div className="section-head">
                  <div>
                    <h2>客观题评分规则</h2>
                    <p>规则发布后不可修改；再次调整会创建新版本。</p>
                  </div>
                  <Space>
                    {publishedScoringRule ? <StatusTag tone="success">{`已发布 v${publishedScoringRule.version}`}</StatusTag> : <StatusTag tone="warning">未发布</StatusTag>}
                    <Button icon={<Save size={16} />} disabled={!canManage} loading={savingScoringRule} onClick={() => void saveScoringRule(false)}>保存草稿</Button>
                    <Button type="primary" icon={<LockKeyhole size={16} />} disabled={!canManage} loading={savingScoringRule} onClick={() => void saveScoringRule(true)}>发布规则</Button>
                  </Space>
                </div>
                {objectiveRuleType === "multiple_choice" ? <div className="scoring-rule-grid">
                  <label><span>允许少选得分</span><Switch checked={Boolean(scoringRuleConfig.allow_partial)} onChange={(value) => setRuleConfig("allow_partial", value)} /></label>
                  <label><span>每个正确选项分值</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.score_per_correct_option ?? 0)} onChange={(value) => setRuleConfig("score_per_correct_option", Number(value ?? 0))} /></label>
                  <label><span>每个错误选项扣分</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.wrong_option_penalty ?? selectedQuestion.score)} onChange={(value) => setRuleConfig("wrong_option_penalty", Number(value ?? 0))} /></label>
                  <label><span>最低得分</span><InputNumber min={0} max={selectedQuestion.score} precision={2} value={Number(scoringRuleConfig.minimum_score ?? 0)} onChange={(value) => setRuleConfig("minimum_score", Number(value ?? 0))} /></label>
                </div> : null}
                {objectiveRuleType === "fill_blank" ? <div className="scoring-rule-grid">
                  <label><span>忽略大小写</span><Switch checked={Boolean(scoringRuleConfig.ignore_case)} onChange={(value) => setRuleConfig("ignore_case", value)} /></label>
                  <label><span>忽略空格</span><Switch checked={Boolean(scoringRuleConfig.ignore_spaces)} onChange={(value) => setRuleConfig("ignore_spaces", value)} /></label>
                  <label><span>忽略标点</span><Switch checked={Boolean(scoringRuleConfig.ignore_punctuation)} onChange={(value) => setRuleConfig("ignore_punctuation", value)} /></label>
                </div> : null}
                {objectiveRuleType === "numeric" ? <div className="scoring-rule-grid">
                  <label><span>绝对误差</span><InputNumber min={0} precision={6} value={Number(scoringRuleConfig.absolute ?? 0)} onChange={(value) => setRuleConfig("absolute", Number(value ?? 0))} /></label>
                  <label><span>相对误差</span><InputNumber min={0} max={1} step={0.001} precision={6} value={Number(scoringRuleConfig.relative ?? 0)} onChange={(value) => setRuleConfig("relative", Number(value ?? 0))} /></label>
                  <label><span>单位必须填写</span><Switch checked={Boolean(scoringRuleConfig.unit_required)} onChange={(value) => setRuleConfig("unit_required", value)} /></label>
                </div> : null}
                {["single_choice", "true_false"].includes(objectiveRuleType) ? <Alert type="info" showIcon message="使用标准答案精确判定" description="空白、多涂、擦除或识别把握不足的答卷不会自动判零分，将转入人工确认。" /> : null}
              </section></details> : null}

              <div className="rubric-editor">
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
                    <Button type="primary" icon={rubricStatus === "locked" ? <LockKeyhole size={16} /> : <Save size={16} />} disabled={questionDisabled || !selectedQuestion || scoreMismatch} loading={savingRubric} onClick={() => void saveRubric()}>
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
                <ResponsiveTable<RubricPoint> rowKey="id" dataSource={rubricPoints} columns={rubricColumns} pagination={false} size="small" />
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
              </div>
            </section>
          </section> : null}
        </>
      )}
    </div>
  );
}
