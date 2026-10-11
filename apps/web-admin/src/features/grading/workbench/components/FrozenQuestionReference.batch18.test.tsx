// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../../../api/papers";
import { QuestionReferenceContent } from "./FrozenQuestionReference";

vi.mock("antd", () => ({
  Alert: ({ message, description }: Readonly<{ message: ReactNode; description?: ReactNode }>) => <p>{message}{description}</p>,
  Tabs: ({ items }: Readonly<{ items: readonly { key: string; label: ReactNode; children: ReactNode }[] }>) => <>{items.map((item) => <section key={item.key}>{item.label}{item.children}</section>)}</>
}));
vi.mock("../../../../components/MathMarkdown", () => ({
  MathMarkdown: ({ children }: Readonly<{ children: ReactNode }>) => <p>{children}</p>
}));

const question = {
  id: "frozen-question", options: [], stem: "冻结题干",
  answer_key: { standard_answer: "参考答案", equivalent_answers: [], tolerance: null }
} as unknown as Question;

describe("frozen reference identity and tolerance boundaries", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    host = document.createElement("div");
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  async function render(nextQuestion: Question) {
    await act(async () => root.render(<QuestionReferenceContent question={nextQuestion} />));
  }

  it("keeps repeated options and preserves their DOM nodes when distinct content moves", async () => {
    const errors = vi.spyOn(console, "error");
    await render({ ...question, options: ["相同选项", "不同选项", "相同选项", ""] });
    const before = Array.from(host.querySelectorAll(".frozen-question-options li"));

    // 相同文本各保留一个位置；移动另一选项时不能把原节点改作不同内容。
    await render({ ...question, options: ["不同选项", "相同选项", "相同选项", ""] });
    const after = Array.from(host.querySelectorAll(".frozen-question-options li"));
    expect(after.map((item) => item.textContent)).toEqual(["不同选项", "相同选项", "相同选项", ""]);
    expect(after[0]).toBe(before[1]);
    expect(after[1]).toBe(before[0]);
    expect(after[2]).toBe(before[2]);
    expect(after[3]).toBe(before[3]);
    expect(errors).not.toHaveBeenCalled();
  });

  it("preserves duplicate structured answers and empty entries when references move", async () => {
    const errors = vi.spyOn(console, "error");
    const repeated = { id: "hidden-one", description: "同一依据", score: 2 };
    const sameText = { id: "hidden-two", description: "同一依据", score: 2 };
    const other = { description: "另一依据", score: 1 };
    const withAnswers = (values: unknown[]): Question => ({
      ...question, answer_key: { ...question.answer_key!, equivalent_answers: values }
    });
    await render(withAnswers([repeated, other, sameText, null]));
    const before = Array.from(host.querySelectorAll(".frozen-reference-list li"));
    await render(withAnswers([other, repeated, sameText, null]));
    const after = Array.from(host.querySelectorAll(".frozen-reference-list li"));
    expect(after).toHaveLength(4);
    expect(after[0]).toBe(before[1]);
    expect(after[1]).toBe(before[0]);
    expect(after[2]).toBe(before[2]);
    expect(after[3]).toBe(before[3]);
    expect(after[1].textContent).toBe("说明：同一依据\n\n分值：2");
    expect(after[2].textContent).toBe(after[1].textContent);
    expect(after[3].textContent).toBe("");
    expect(errors).not.toHaveBeenCalled();
  });

  it.each([[0, "0"], [false, "否"]] as const)("shows an explicitly stored tolerance of %s", async (tolerance, text) => {
    await render({ ...question, answer_key: { ...question.answer_key!, tolerance } });
    const heading = Array.from(host.querySelectorAll("h4")).find((item) => item.textContent === "参考答案允许误差");
    expect(heading?.nextElementSibling?.textContent).toBe(text);
  });

  it.each([null, undefined])("does not show a tolerance that was not stored: %s", async (tolerance) => {
    await render({ ...question, answer_key: { ...question.answer_key!, tolerance } });
    expect(host.textContent).not.toContain("参考答案允许误差");
    expect(host.textContent).toContain("参考答案");
  });
});
