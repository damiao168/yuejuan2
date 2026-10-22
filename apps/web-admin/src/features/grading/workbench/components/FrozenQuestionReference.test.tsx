import { createElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { Question } from "../../../../api/papers";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { FrozenQuestionReference, QuestionReferenceContent, referenceText } from "./FrozenQuestionReference";

vi.mock("antd", () => ({ Alert: ({ message, description }: { message?: ReactNode; description?: ReactNode }) => <p>{message}{description}</p>, Tabs: ({ items }: { items: { key: string; label?: ReactNode; children?: ReactNode }[] }) => <>{items.map((item) => <section key={item.key}>{item.label}{item.children}</section>)}</> }));
vi.mock("../../../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children?: ReactNode }) => <p>{children}</p> }));
const question = { id: "q", options: ["选项一", "选项二"], stem: "冻结题干",
  answer_key: { answer_version: "答案v1", standard_answer: { value: 2 }, equivalent_answers: ["二"], tolerance: 0.01 },
  solution: { solution_version: "解答v1", raw_text: "解答全文", steps: [{ text: "独立步骤" }] },
  rubric: { version: "细则v1", status: "frozen", points: [{ id: "p", description: "评分依据", score: 2, required: true }], deductions: [{ condition: "单位错误", deduction: 1 }], examples: [{ answer: "答案样例", score: 2 }] }
} as unknown as Question;
describe("task frozen reference presentation", () => {
  it("shows stem, options, standard and equivalent answers, tolerance and full solution steps", () => {
    const html = renderToStaticMarkup(createElement(QuestionReferenceContent, { question }));
    for (const expected of ["冻结题干", "选项一", "答案v1", "内容：2", "二", "0.01", "解答全文", "独立步骤"]) expect(html).toContain(expected);
    expect(html).not.toContain("[object Object]");
  });
  it("keeps deductions and examples visible and explains a missing frozen rubric", () => {
    const context = { question, task: { exam_id: "exam", exam_name: "期中", subject: "math", question_no: "1" }, reviewContext: { subject_tool_hints: { subject_code: "mathematics" } } } as WorkbenchContext;
    const html = renderToStaticMarkup(createElement(FrozenQuestionReference, { context }));
    for (const expected of ["期中", "数学", "评分依据", "必需点", "单位错误", "扣分：1", "答案样例"]) expect(html).toContain(expected);
    expect(html).not.toContain("math ·");
    const missing = renderToStaticMarkup(createElement(FrozenQuestionReference, { context: { ...context, question: { ...question, rubric: { ...question.rubric!, status: "missing", version: "", points: [] } } } }));
    expect(missing).toContain("本任务的冻结评分细则缺失"); expect(missing).toContain("当前题库的细则不会替代");
  });
  it("does not turn absent reference materials into an incorrect-answer assertion", () => {
    const html = renderToStaticMarkup(createElement(QuestionReferenceContent, { question: { ...question, answer_key: undefined, solution: undefined } }));
    expect(html).toContain("不能据此推断学生答案错误");
    expect(referenceText({ id: "internal", score: 2, description: "可读依据" })).toBe("分值：2\n\n说明：可读依据");
  });
});
