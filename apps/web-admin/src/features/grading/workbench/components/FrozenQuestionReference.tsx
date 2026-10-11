import { Alert, Tabs } from "antd";
import { MathMarkdown } from "../../../../components/MathMarkdown";
import type { Question } from "../../../../api/papers";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { examSubjectLabel } from "../../../../constants/examStatus";
import "../teacher-product-ux.css";

const referenceLabels: Record<string, string> = {
  description: "说明", label: "说明", rule: "规则", reason: "依据", score: "分值", max_score: "满分",
  answer: "答案", answers: "答案", standard_answer: "参考答案", equivalent_answers: "等价答案",
  text: "内容", raw_text: "内容", solution: "解法", expected: "标准", condition: "条件", conditions: "条件",
  deduction: "扣分", points: "评分点", comment: "评语", title: "标题", note: "备注", examples: "样例",
  required: "必需点", trigger: "触发条件", penalty: "扣分", cap: "上限", max_deduction: "最多扣分", value: "内容",
  criterion: "判定依据", criteria: "判定依据", content: "内容", expression: "表达式", formula: "公式", explanation: "解释", result: "结果", steps: "步骤"
};

/** 资料可能是文本或结构化答案；逐字段保留可读内容，不能用 String(object) 隐掉答案。 */
export function referenceText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "string" || typeof value === "number") return String(value);
  if (typeof value === "boolean") return value ? "是" : "否";
  if (Array.isArray(value)) return value.map(referenceText).filter(Boolean).join("\n\n");
  if (typeof value === "object") return Object.entries(value).filter(([key]) => !["id", "point_id", "rubric_point_id", "source_refs"].includes(key))
    .map(([key, item]) => { const text = referenceText(item); return text ? `${referenceLabels[key] ?? key}：${text}` : ""; }).filter(Boolean).join("\n\n");
  return "";
}

function referenceEntries(values: readonly unknown[]) {
  // 参考项没有统一 id；按显示内容生成 key，相同内容用各自的出现次数区分。
  const occurrences = new Map<string, number>();
  return values.map((value) => {
    const text = referenceText(value);
    const occurrence = occurrences.get(text) ?? 0;
    occurrences.set(text, occurrence + 1);
    return { key: `${text}:${occurrence}`, text };
  });
}

function ReferenceList({ values, empty }: Readonly<{ values: readonly unknown[]; empty: string }>) {
  return values.length ? <ol className="frozen-reference-list">{referenceEntries(values).map((entry) => <li key={entry.key}><MathMarkdown>{entry.text}</MathMarkdown></li>)}</ol> : <p className="muted">{empty}</p>;
}

export function QuestionReferenceContent({ question }: Readonly<{ question: Question }>) {
  const answerKey = question.answer_key;
  const tolerance = answerKey?.tolerance;
  const toleranceContent = tolerance !== undefined && tolerance !== null
    ? <div><h4>参考答案允许误差</h4><MathMarkdown>{referenceText(tolerance)}</MathMarkdown></div>
    : null;
  const equivalentAnswers = answerKey?.equivalent_answers;
  const equivalentAnswersContent = equivalentAnswers?.length
    ? <><h4>允许的等价答案</h4><ReferenceList values={equivalentAnswers} empty="无" /></>
    : null;
  return <div className="frozen-question-content">
    {question.stem ? <MathMarkdown>{question.stem}</MathMarkdown> : <Alert type="warning" showIcon message="本题未保存题干，请联系考试管理员核对。" />}
    {question.options?.length ? <ol className="frozen-question-options">{referenceEntries(question.options).map((entry) => <li key={entry.key}><MathMarkdown>{entry.text}</MathMarkdown></li>)}</ol> : null}
    <h3>参考答案{answerKey?.answer_version ? ` · ${answerKey.answer_version}` : ""}</h3>
    {answerKey ? <><MathMarkdown>{referenceText(answerKey.standard_answer) || "未保存有效参考答案"}</MathMarkdown>
      {toleranceContent}{equivalentAnswersContent}</> : <p className="muted">本任务没有可用的冻结参考答案，不能据此推断学生答案错误。</p>}
    <h3>参考解答{question.solution?.solution_version ? ` · ${question.solution.solution_version}` : ""}</h3>
    {question.solution?.raw_text ? <MathMarkdown>{question.solution.raw_text}</MathMarkdown> : null}
    {question.solution?.steps?.length ? <><h4>解答步骤</h4><ReferenceList values={question.solution.steps} empty="无" /></> : null}
    {!question.solution?.raw_text && !question.solution?.steps?.length ? <p className="muted">本任务没有可用的参考解答，请按冻结评分细则独立判断。</p> : null}
  </div>;
}

export function FrozenQuestionReference({ context }: Readonly<{ context: WorkbenchContext }>) {
  const question = context.question;
  if (!question) return null;
  return <section className="frozen-question-reference" aria-label="本任务题目与完整评分依据">
    <strong>{context.task.exam_name || `考试 ${context.task.exam_id.slice(0, 8)}`} · {examSubjectLabel(context.task.subject || context.reviewContext.subject_tool_hints.subject_code || "")} · 第 {context.task.question_no} 题</strong>
    <Tabs size="small" items={[
      { key: "question", label: "题目与参考解答", children: <QuestionReferenceContent question={question} /> },
      { key: "rules", label: "完整评分依据", children: <div>{question.rubric?.status === "missing" ? <Alert type="warning" showIcon message="本任务的冻结评分细则缺失" description="请联系考试管理员核对历史评分依据。当前题库的细则不会替代本任务的冻结版本。" /> : <p>任务冻结细则：{question.rubric?.version || "未记录版本"}；此处只供核对，给分请在最终评分区操作。</p>}
        <h3>评分点</h3><ReferenceList values={question.rubric?.points ?? []} empty="本题没有评分点" />
        <h3>扣分与例外规则</h3><ReferenceList values={question.rubric?.deductions ?? []} empty="冻结细则未提供扣分或例外规则" />
        <h3>评分样例</h3><ReferenceList values={question.rubric?.examples ?? []} empty="冻结细则未提供评分样例" /></div> }
    ]} />
  </section>;
}
