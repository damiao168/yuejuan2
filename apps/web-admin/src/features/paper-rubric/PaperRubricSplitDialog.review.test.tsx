// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PaperRubricSplitDialog } from "./PaperRubricSplitDialog";
import { rubricSolutionLines, rubricSolutionSources } from "./rubricSolutionLines";
import type { Question } from "../../api/papers";

vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <div>{children}</div> }));
beforeEach(() => {
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }) });
});
afterEach(cleanup);

function setup(content: string) {
  const question = { solution: { steps: [{ content }] } } as unknown as Question;
  const props = { line: rubricSolutionLines(question)[0], sources: rubricSolutionSources(question), disabled: false, onClose: vi.fn(), onSplit: vi.fn() };
  return { ...render(<PaperRubricSplitDialog {...props} />), props };
}

function selectOffset(offset: number) {
  const input = screen.getByLabelText("原解答拆分光标") as HTMLTextAreaElement;
  input.setSelectionRange(offset, offset);
  fireEvent.select(input);
}

describe("拆分弹窗的无效光标恢复", () => {
  it("落在文字后的换行只能产生空白半段时保持弹窗并禁止确认", async () => {
    const { props } = setup("甲\n乙");
    // 第一单元含原文换行；几何边界有效不代表两半都可成为配分目标。
    selectOffset(1);
    await waitFor(() => expect((screen.getByRole("button", { name: "确认拆分" }) as HTMLButtonElement).disabled).toBe(true));
    expect(screen.getByLabelText("原解答拆分光标")).toBeDefined();
    expect(props.onSplit).not.toHaveBeenCalled();
  });

  it("拒绝emoji代理对中间，改到完整码点后可提交完整范围", async () => {
    const { props } = setup("甲😀乙");
    selectOffset(2);
    expect((screen.getByRole("button", { name: "确认拆分" }) as HTMLButtonElement).disabled).toBe(true);
    selectOffset(3);
    await waitFor(() => expect((screen.getByRole("button", { name: "确认拆分" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认拆分" }));
    expect(props.onSplit).toHaveBeenCalledWith([
      expect.objectContaining({ spans: [{ source_id: "step-1", start: 0, end: 3 }] }),
      expect.objectContaining({ spans: [{ source_id: "step-1", start: 3, end: 4 }] })
    ]);
  });

  it("已有合法预览变只读后不能确认拆分", async () => {
    const { props, rerender } = setup("甲😀乙");
    selectOffset(3);
    await waitFor(() => expect((screen.getByRole("button", { name: "确认拆分" }) as HTMLButtonElement).disabled).toBe(false));
    rerender(<PaperRubricSplitDialog {...props} disabled />);
    fireEvent.click(screen.getByRole("button", { name: "确认拆分" }));
    expect(props.onSplit).not.toHaveBeenCalled();
  });
});
