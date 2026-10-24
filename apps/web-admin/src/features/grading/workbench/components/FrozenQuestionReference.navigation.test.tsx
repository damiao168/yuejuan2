// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { FrozenQuestionReference } from "./FrozenQuestionReference";

vi.mock("antd", () => ({
  Alert: ({ message }: { message: ReactNode }) => <p>{message}</p>,
  Tabs: ({ activeKey, defaultActiveKey, onChange, items }: {
    activeKey?: string; defaultActiveKey: string; onChange: (key: string) => void;
    items: { key: string; label: ReactNode; children: ReactNode }[];
  }) => <>{items.map((item) => <button key={item.key} onClick={() => onChange(item.key)}>{item.label}</button>)}{items.find((item) => item.key === (activeKey ?? defaultActiveKey))?.children}</>
}));

afterEach(cleanup);

describe("frozen reference tab navigation", () => {
  it("reuses the requested tab and renders formulas in both question and rubric content", () => {
    const context = {
      task: { id: "task", exam_name: "公式测试", subject: "mathematics", question_no: "1" },
      reviewContext: { subject_tool_hints: { subject_code: "mathematics" } },
      question: {
        id: "question", stem: "计算 $x^2$", answer_key: { standard_answer: "参考结果" },
        rubric: { status: "frozen", version: "v1", points: [{ id: "point", description: String.raw`正确计算 $\frac{1}{2}$`, score: 2 }] }
      }
    } as unknown as WorkbenchContext;
    const onTabChange = vi.fn();
    const view = render(<FrozenQuestionReference context={context} activeTab="question" onTabChange={onTabChange} />);
    expect(view.container.querySelector(".frozen-question-content .katex")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "完整评分依据" }));
    expect(onTabChange).toHaveBeenCalledWith("rules");
    view.rerender(<FrozenQuestionReference context={context} activeTab="rules" onTabChange={onTabChange} />);
    expect(view.container.querySelector(".frozen-question-content")).toBeNull();
    expect(view.container.querySelector(".frozen-reference-list .katex")).not.toBeNull();
    expect(view.container.querySelector(".katex-error")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "题目与参考解答" }));
    expect(onTabChange).toHaveBeenLastCalledWith("question");
  });
});
