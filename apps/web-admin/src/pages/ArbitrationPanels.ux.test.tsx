// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { Question } from "../api/papers";
import { ArbitrationRubricTab } from "./ArbitrationPanels";

vi.mock("antd", () => ({
  Alert: ({ message }: { message: ReactNode }) => <div>{message}</div>,
  Collapse: () => null, Descriptions: () => null, Tabs: () => null,
  Empty: Object.assign(({ description }: { description: string }) => <p>{description}</p>, { PRESENTED_IMAGE_SIMPLE: "simple" }),
  List: Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (value: unknown) => ReactNode }) => <ul>{dataSource.map((value, index) => <li key={index}>{renderItem(value)}</li>)}</ul>, { Item: ({ children }: { children: ReactNode }) => <div>{children}</div> }),
  Tag: ({ children }: { children: ReactNode }) => <span>{children}</span>,
}));
vi.mock("../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: ReactNode }) => <p>{children}</p> }));
vi.mock("../components/PageState", () => ({ EmptyState: ({ title, description }: { title: string; description: string }) => <div>{title}{description}</div>, ErrorState: () => null, LoadingState: () => null }));

describe("仲裁冻结评分材料", () => {
  it("展示任务参考答案、等价答案、误差、解析、扣分和示例", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const question = {
      id: "question", exam_id: "exam", score: 10, stem: "求结果", options: ["候选甲"],
      answer_key: { standard_answer: "12", equivalent_answers: ["十二"], tolerance: { value: "0.1" } },
      solution: { raw_text: "先列式", steps: [{ description: "再计算" }] },
      rubric: { id: "rubric", version: "frozen-v1", status: "approved", max_score: 10, points: [{ id: "p1", description: "计算正确", score: 10, required: true }], deductions: [{ description: "单位错误", score: -1 }], examples: ["完整作答示例"] },
    } as unknown as Question;
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => root.render(<ArbitrationRubricTab question={question} rubricPoints={question.rubric!.points} />));
    for (const text of ["满分 10", "frozen-v1", "求结果", "12", "十二", "0.1", "先列式", "再计算", "计算正确", "单位错误", "完整作答示例"]) expect(host.textContent).toContain(text);
    await act(async () => root.render(<ArbitrationRubricTab question={{ ...question, rubric: { ...question.rubric!, status: "missing" } }} rubricPoints={[]} />));
    expect(host.textContent).toContain("暂不能提交仲裁");
    expect(host.textContent).toContain("本场考试负责人");
    await act(async () => root.unmount());
  });
});
