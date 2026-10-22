// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../api/papers";
import { PaperRubricAssistant } from "./PaperRubricAssistant";

const modal = vi.hoisted(() => ({ confirm: vi.fn() }));
vi.mock("antd", async () => ({
  ...await vi.importActual<typeof import("antd")>("antd"),
  App: { useApp: () => ({ modal }) }
}));
vi.mock("./PaperRubricStepAllocator", () => ({
  PaperRubricStepAllocator: ({ onApply }: { onApply: (points: unknown[]) => void }) =>
    <button onClick={() => onApply([{ id: "new-point", description: "第一题步骤", score: 10, required: false }])}>采用测试划分</button>
}));

const question: Question = {
  id: "q-a", tenant_id: "tenant", exam_id: "exam", question_no: "1", question_type: "calculation", score: 10,
  options: [], knowledge_points: [], sort_order: 1, status: "draft"
};
const base = { question, disabled: false, contentDirty: false, hasExistingPoints: true, dirty: false, onDirtyChange: vi.fn() };
beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("评分草稿替换确认的题目边界", () => {
  // Modal 由应用统一挂载：换题卸载原助手后，原确认框仍可能迟到执行 onOk。
  it("原题助手卸载后，迟到确认不能把旧题分段写入新题草稿", async () => {
    const onApply = vi.fn();
    const view = render(<PaperRubricAssistant key="q-a" {...base} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "采用测试划分" }));
    const pending = modal.confirm.mock.calls[0][0];
    view.rerender(<PaperRubricAssistant key="q-b" {...base} question={{ ...question, id: "q-b", question_no: "2" }} onApply={onApply} />);
    await act(async () => { await pending.onOk(); });
    expect(onApply).not.toHaveBeenCalled();
  });

  it("当前题目的有效确认仍能采用原草稿", async () => {
    const onApply = vi.fn();
    render(<PaperRubricAssistant {...base} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "采用测试划分" }));
    await act(async () => { await modal.confirm.mock.calls[0][0].onOk(); });
    expect(onApply).toHaveBeenCalledWith([expect.objectContaining({ id: "new-point", score: 10 })]);
  });

  it("确认期间变为只读时拒绝采用", async () => {
    const onApply = vi.fn();
    const view = render(<PaperRubricAssistant {...base} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "采用测试划分" }));
    const pending = modal.confirm.mock.calls[0][0];
    view.rerender(<PaperRubricAssistant {...base} disabled onApply={onApply} />);
    await act(async () => { await pending.onOk(); });
    expect(onApply).not.toHaveBeenCalled();
  });
});
