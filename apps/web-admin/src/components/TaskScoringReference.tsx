import { Alert } from "antd";
import type { Question } from "../api/papers";
import { MathMarkdown } from "./MathMarkdown";
import { referenceText } from "../features/grading/workbench/components/FrozenQuestionReference";

type TaskQuestion = Partial<Omit<Question, "solution" | "rubric">> & { solution?: unknown };
function record(value: unknown): Record<string, unknown> { return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {}; }

// 专属上下文提供本题冻结材料；不查询考试题库，也不展示原分或标准卷参考分。
// 历史快照的规则项形状不同，按原字段完整呈现，不能只挑 description 丢掉条件和例外。
export function TaskScoringReference({ question, rubric }: Readonly<{ question?: TaskQuestion; rubric?: unknown }>) {
  const rules = record(rubric);
  return <section aria-label="本任务题目与完整评分依据">
    {question ? <><h3>第 {question.question_no} 题 · 满分 {question.score}</h3>
      <MathMarkdown>{question.stem || "冻结材料未提供题干"}</MathMarkdown>
      {(question.options ?? []).map((option, index) => <MathMarkdown key={`${index}:${option}`}>{option}</MathMarkdown>)}
      <h4>参考答案与等价规则</h4><MathMarkdown>{question.answer_key ? referenceText(question.answer_key) : "冻结材料未提供参考答案"}</MathMarkdown>
      <h4>题目解析与步骤</h4><MathMarkdown>{question.solution ? referenceText(question.solution) : "冻结材料未提供题目解析"}</MathMarkdown>
    </> : <Alert type="warning" message="本任务未提供题目材料，请联系本场考试负责人核对。" showIcon />}
    <h3>冻结细则 · {typeof rules.version === "string" ? rules.version : "未记录版本"}</h3>
    {[{ title: "评分点", values: rules.points }, { title: "扣分与例外规则", values: rules.deductions }, { title: "评分样例", values: rules.examples }].map(section => <div key={section.title}><h4>{section.title}</h4>{Array.isArray(section.values) && section.values.length ? <MathMarkdown>{referenceText(section.values)}</MathMarkdown> : <p className="muted">冻结材料未提供{section.title}</p>}</div>)}
    {Object.entries(rules).filter(([key]) => !["id", "question_id", "version", "max_score", "points", "deductions", "examples", "created_at"].includes(key)).map(([key, value]) => <MathMarkdown key={key}>{referenceText({ [key]: value })}</MathMarkdown>)}
  </section>;
}
