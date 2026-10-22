import { Alert, Button, Form, Input, InputNumber, Select, type FormListFieldData, type FormListOperation } from "antd";
import { Plus, Trash2 } from "lucide-react";
import { MathMarkdown } from "../../components/MathMarkdown";
import { StructuredAnswerEditor } from "../../components/StructuredTeacherFields";
import { questionTypeOptions } from "../../constants/examCatalog";
import { hasStandardAnswer } from "./paperQuestionPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";

type ViewProps = Readonly<{ context: PaperRubricViewContext }>;

export function PaperQuestionForm({ context }: ViewProps) {
  const { questionWorkflow, access, selectedQuestion } = context;
  const watchedType = context.state.watchedQuestionType;
  const answerOptions = Form.useWatch("options", questionWorkflow.form) ?? selectedQuestion?.options ?? [];
  return <>
    <Form.Item label="题干" name="stem"><Input.TextArea autoSize={{ minRows: 3, maxRows: 9 }} placeholder="上传资料识别后自动填充，或在此录入题干" /></Form.Item>
    {(watchedType === "single_choice" || watchedType === "multiple_choice") && <Form.List name="options">{renderQuestionOptions}</Form.List>}
    <Form.Item className="paper-question-answer-target" label="标准答案" name="standard_answer" rules={[{ required: true, message: "请输入标准答案" }]}>
      <StructuredAnswerEditor questionType={watchedType} options={answerOptions} disabled={access.questionDisabled} />
    </Form.Item>
    {access.isFillBlank && <Alert type={hasStandardAnswer(selectedQuestion) ? "info" : "warning"} showIcon
      message={hasStandardAnswer(selectedQuestion) ? "填空题按答案判分" : "标准答案尚未配置"}
      description="逐空答案、等价写法及分值应在评分配置中核对。数学区间的括号不可随意忽略。" />}
    <PaperReferenceSolution context={context} />
    <details className="paper-advanced-settings"><summary>高级答案设置</summary>
      <Form.Item label="等价答案（选填）" name="equivalent_answers"><Select mode="tags" placeholder="添加允许的其他正确写法，回车确认" /></Form.Item>
      {access.showTolerance && <Form.Item label="答案误差范围（选填）"><div className="form-grid">
        <Form.Item label="允许绝对误差" name="tolerance_absolute"><InputNumber min={0} className="full-width-control" /></Form.Item>
        <Form.Item label="允许相对误差（%）" name="tolerance_relative"><InputNumber min={0} className="full-width-control" /></Form.Item>
      </div></Form.Item>}
    </details>
  </>;
}

export function PaperQuestionBasics() {
  return <div className="paper-question-basics paper-question-basics-compact">
    <Form.Item label="题号" name="question_no" rules={[{ required: true, message: "请输入题号" }]}><Input placeholder="Q1" /></Form.Item>
    <Form.Item label="题型" name="question_type" rules={[{ required: true, message: "请选择题型" }]}><Select options={questionTypeOptions} /></Form.Item>
    <Form.Item className="paper-question-score-target" label="分值" name="score" rules={[{ required: true, message: "请输入分值" }]}><InputNumber min={0.5} precision={1} className="full-width-control" /></Form.Item>
  </div>;
}

function renderQuestionOptions(fields: FormListFieldData[], { add, remove }: FormListOperation) {
  return <div className="paper-option-editor"><div className="section-head"><h3>选项</h3>
    <Button size="small" icon={<Plus size={14} />} onClick={() => add("")}>添加选项</Button></div>
    {fields.map((field, index) => {
      const label = String.fromCodePoint(65 + index);
      return <div className="paper-option-row" key={field.key}><span>{label}</span>
        <Form.Item name={field.name} rules={[{ required: true, message: "请输入选项" }]}><Input placeholder={`选项 ${label} 内容`} /></Form.Item>
        <Button type="text" danger aria-label={`删除选项 ${label}`} icon={<Trash2 size={14} />} onClick={() => remove(field.name)} />
      </div>;
    })}
  </div>;
}

function PaperReferenceSolution({ context }: ViewProps) {
  const rawText = context.selectedQuestion?.solution?.raw_text;
  if (!rawText) return <p className="paper-missing-content">暂无教师解析。若资料中已有详解，请先核对并应用识别结果。</p>;
  return <div className="paper-reference-solution"><h3>教师解析</h3><MathMarkdown>{rawText}</MathMarkdown></div>;
}
