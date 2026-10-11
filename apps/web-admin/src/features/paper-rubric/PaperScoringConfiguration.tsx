import { Alert, Button, Input, InputNumber, Select, Space, Switch } from "antd";
import { Plus, Trash2 } from "lucide-react";
import type { RubricEvidenceRequirement, RubricPoint } from "../../api/papers";
import { StatusTag } from "../../components/StatusTag";
import { createScoringExample, StructuredListEditor } from "../../components/StructuredTeacherFields";
import { AssessmentProfileEditor } from "../../components/features/assessment/AssessmentProfileEditor";
import { assessmentScoringGuidance } from "../../components/features/assessment/assessmentScoringDefaults";
import { evidenceHasTarget, evidenceTargetPlaceholder, evidenceTypePatch, fixedScoringDescription, hasStandardAnswer, standardAnswerText } from "./paperQuestionPresentation";
import { rubricStatusOptions } from "./paperPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";

type ViewProps = Readonly<{ context: PaperRubricViewContext }>;
const formulaEvidenceOptions: { label: string; value: RubricEvidenceRequirement["type"] }[] = [
  { label: "关键等价变形成立", value: "valid_transformation" },
  { label: "最终结果经核验", value: "final_result" },
  { label: "出现指定概念", value: "concept" },
  { label: "单位正确", value: "unit" },
  { label: "定义域／取值条件", value: "domain" }
];

export function PaperScoringConfiguration({ context }: ViewProps) {
  const { selectedExam, selectedQuestion, options, access } = context;
  if (!selectedExam) return null;
  // 主界面未读取已保存的评分模式，只展示不随模式改变的题型限制；与高级配置共用说明，避免折叠后遗漏人工要求。
  const scoringGuidance = assessmentScoringGuidance(selectedQuestion?.question_type);
  return <>
    {selectedQuestion && selectedQuestion.score <= 0 && <Alert showIcon type="warning" message="请先核对并保存小题分值，再配置评分规则" />}
    {scoringGuidance && <Alert showIcon {...scoringGuidance} />}
    <section className="paper-scoring-method"><h3>本题评分方式（开考前必填）</h3><p>选择谁来判分并保存。考试准备页可一次补齐未配置题目；高级证据设置保留在配置窗口中。</p>
      <AssessmentProfileEditor examId={selectedExam.id} examSubject={selectedExam.subject} examStatus={selectedExam.status} examGradingMode={selectedExam.grading_mode}
        question={selectedQuestion} canManage={options.canManageAssessment} onChanged={options.onExamChanged} />
    </section>
    <PaperFixedScoringEditor context={context} />
    {!access.editorUsesFixedScoring && <PaperRubricEditor context={context} />}
  </>;
}

function PaperFixedScoringEditor({ context }: ViewProps) {
  const { selectedQuestion, access, scoringRuleWorkflow } = context;
  if (!selectedQuestion || !access.objectiveRuleType) {
    if (access.editorUsesFixedScoring) return <Alert type="info" showIcon message="先保存题目与答案" description="保存后可核对并发布固定判分规则。" />;
    return null;
  }
  const answer = standardAnswerText(selectedQuestion);
  const hasAnswer = hasStandardAnswer(selectedQuestion);
  return <section className="scoring-rule-editor">
    <div className="section-head"><div><h2>固定判分规则</h2><p>标准答案：{hasAnswer ? answer : "待补充"} · 满分 {selectedQuestion.score} 分</p></div>
      <Space>{scoringRuleWorkflow.publishedScoringRule ? <StatusTag tone="success">{`已发布 v${scoringRuleWorkflow.publishedScoringRule.version}`}</StatusTag> : <StatusTag tone="warning">未发布</StatusTag>}</Space>
    </div>
    {hasAnswer ? <Alert type="info" showIcon message="全对得满分；错误得 0 分" description={fixedScoringDescription(access.objectiveRuleType)} />
      : <Alert type="warning" showIcon message="尚无标准答案，无法判分" description="请先在「题目与参考解答」中核对答案并保存。" />}
    <PaperFixedScoringControls context={context} />
    {(access.objectiveRuleType === "single_choice" || access.objectiveRuleType === "true_false") && <Alert type="info" showIcon
      message="使用标准答案精确判定" description="空白、多涂、擦除或识别把握不足的答卷不会自动判零分，将转入人工确认。" />}
  </section>;
}

function PaperFixedScoringControls({ context }: ViewProps) {
  const { scoringRuleConfig: config, setRuleConfig } = context.scoringRuleWorkflow;
  const score = context.selectedQuestion?.score;
  switch (context.access.objectiveRuleType) {
    case "multiple_choice": return <div className="scoring-rule-grid">
      <label><span>允许少选得分</span><Switch checked={Boolean(config.allow_partial)} onChange={(value) => setRuleConfig("allow_partial", value)} /></label>
      <label><span>每个正确选项分值</span><InputNumber min={0} max={score} precision={2} value={Number(config.score_per_correct_option ?? 0)} onChange={(value) => setRuleConfig("score_per_correct_option", Number(value ?? 0))} /></label>
      <label><span>每个错误选项扣分</span><InputNumber min={0} max={score} precision={2} value={Number(config.wrong_option_penalty ?? score)} onChange={(value) => setRuleConfig("wrong_option_penalty", Number(value ?? 0))} /></label>
      <label><span>最低得分</span><InputNumber min={0} max={score} precision={2} value={Number(config.minimum_score ?? 0)} onChange={(value) => setRuleConfig("minimum_score", Number(value ?? 0))} /></label>
    </div>;
    case "fill_blank": return <div className="scoring-rule-grid">
      <label><span>忽略大小写</span><Switch checked={Boolean(config.ignore_case)} onChange={(value) => setRuleConfig("ignore_case", value)} /></label>
      <label><span>忽略空格</span><Switch checked={Boolean(config.ignore_spaces)} onChange={(value) => setRuleConfig("ignore_spaces", value)} /></label>
      <label><span>忽略标点（数学区间慎用）</span><Switch checked={Boolean(config.ignore_punctuation)} onChange={(value) => setRuleConfig("ignore_punctuation", value)} /></label>
    </div>;
    case "numeric": return <div className="scoring-rule-grid">
      <label><span>绝对误差</span><InputNumber min={0} precision={6} value={Number(config.absolute ?? 0)} onChange={(value) => setRuleConfig("absolute", Number(value ?? 0))} /></label>
      <label><span>相对误差</span><InputNumber min={0} max={1} step={0.001} precision={6} value={Number(config.relative ?? 0)} onChange={(value) => setRuleConfig("relative", Number(value ?? 0))} /></label>
      <label><span>单位必须填写</span><Switch checked={Boolean(config.unit_required)} onChange={(value) => setRuleConfig("unit_required", value)} /></label>
    </div>;
    default: return null;
  }
}

function PaperRubricEditor({ context }: ViewProps) {
  const { selectedQuestion, access, rubricWorkflow: rubric } = context;
  return <div className="rubric-editor">
    <div className="section-head"><div><h2>评分细则</h2>
      <p>{selectedQuestion ? `采分点合计 ${rubric.pointTotal} / ${selectedQuestion.score} 分` : "先保存题目，再配置评分细则"}</p></div>
      <Space><span className="muted">提交为</span>
        <Select value={rubric.rubricStatus} options={rubricStatusOptions} disabled={access.questionDisabled} onChange={rubric.setRubricStatus} className="rubric-status-select" />
        <Button icon={<Plus size={16} />} disabled={access.questionDisabled} onClick={rubric.addPoint}>添加采分点</Button>
      </Space>
    </div>
    {rubric.scoreMismatch && <Alert type="error" showIcon message="评分细则分值不匹配" description="采分点总分必须等于题目分值，调整后才能保存。" />}
    {access.showFormulaEvidence && <Alert type="info" showIcon message="公式与步骤证据只作阅卷提示"
      description="这些证据用于核对步骤、概念和单位，证据本身不会自动给分。核对结果时请结合原始作答和评分细则。" />}
    <div className="paper-rubric-points">{rubric.rubricPoints.map((point, index) => <PaperRubricPoint key={`${point.id}-${index}`} context={context} point={point} index={index} />)}</div>
    <PaperAdvancedRubricData context={context} />
  </div>;
}

function PaperRubricPoint({ context, point, index }: ViewProps & Readonly<{ point: RubricPoint; index: number }>) {
  const { access, rubricWorkflow: rubric } = context;
  return <div className="paper-rubric-point">
    <div className="paper-rubric-point-heading"><strong>采分点 {index + 1}</strong>
      <Button danger size="small" aria-label={`删除采分点 ${index + 1}`} icon={<Trash2 size={14} />}
        disabled={access.questionDisabled || rubric.rubricPoints.length <= 1} onClick={() => rubric.removePoint(index)}>删除</Button></div>
    <label className="paper-rubric-description"><span>得分条件</span>
      <Input.TextArea value={point.description} disabled={access.questionDisabled} autoSize={{ minRows: 2, maxRows: 5 }}
        placeholder="描述学生需要完成的数学步骤或结果" onChange={(event) => rubric.updatePoint(index, { description: event.target.value })} />
    </label>
    <div className="paper-rubric-point-fields">
      <label><span>分值</span><InputNumber value={point.score} disabled={access.questionDisabled} min={0} precision={1}
        className="full-width-control" onChange={(value) => rubric.updatePoint(index, { score: Number(value ?? 0) })} /></label>
      <label><span>必需</span><Switch checked={point.required} disabled={access.questionDisabled} onChange={(checked) => rubric.updatePoint(index, { required: checked })} /></label>
    </div>
    {access.showFormulaEvidence && <PaperRubricEvidence context={context} point={point} index={index} />}
  </div>;
}

function PaperRubricEvidence({ context, point, index }: ViewProps & Readonly<{ point: RubricPoint; index: number }>) {
  const { access, rubricWorkflow: rubric } = context;
  return <details className="paper-rubric-evidence">
    <summary>识别证据（仅供教师参考）{point.evidence_requirements?.length ? ` · ${point.evidence_requirements.length} 项` : ""}</summary>
    {(point.evidence_requirements ?? []).map((requirement, requirementIndex) => <div className="paper-rubric-evidence-row" key={`${requirement.type}-${requirementIndex}`}>
      <Select value={requirement.type} options={formulaEvidenceOptions} disabled={access.questionDisabled} className="rubric-evidence-type-select"
        onChange={(type: RubricEvidenceRequirement["type"]) => rubric.updateEvidenceRequirement(index, requirementIndex, evidenceTypePatch(type, requirement))} />
      {evidenceHasTarget(requirement.type) && <Input value={requirement.target} disabled={access.questionDisabled} placeholder={evidenceTargetPlaceholder(requirement.type)}
        className="rubric-evidence-target-input" onChange={(event) => rubric.updateEvidenceRequirement(index, requirementIndex, { target: event.target.value })} />}
      <Button type="text" danger size="small" aria-label="删除识别证据" disabled={access.questionDisabled} icon={<Trash2 size={14} />} onClick={() => rubric.removeEvidenceRequirement(index, requirementIndex)} />
    </div>)}
    <Button type="link" size="small" icon={<Plus size={14} />} disabled={access.questionDisabled} onClick={() => rubric.addEvidenceRequirement(index)}>添加识别证据</Button>
  </details>;
}

function PaperAdvancedRubricData({ context }: ViewProps) {
  const { access, rubricWorkflow: rubric } = context;
  return <details className="paper-advanced-settings"><summary>扣分规则与评分示例（选填）</summary><div className="form-grid rubric-json-grid">
    <section><h3>扣分规则</h3><p>填写扣分条件和分值，例如缺少必要步骤扣 2 分，分值填 -2。</p><StructuredListEditor value={rubric.deductionsJson} disabled={access.questionDisabled} itemLabel="扣分规则" newItem={{description:"",score:-1}} onChange={rubric.setDeductionsJson} /></section>
    <section><h3>评分示例</h3><StructuredListEditor value={rubric.examplesJson} disabled={access.questionDisabled} itemLabel="评分示例" newItem={createScoringExample()} onChange={rubric.setExamplesJson} /></section>
  </div></details>;
}
