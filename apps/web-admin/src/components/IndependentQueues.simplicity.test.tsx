// @vitest-environment jsdom
import { act, Fragment, type ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BackmarkQueue } from "./BackmarkQueue";
import { RegradeQueue } from "./RegradeQueue";

const api = vi.hoisted(() => ({ list: vi.fn(), claim: vi.fn(), context: vi.fn(), submit: vi.fn(), image: vi.fn(), success: vi.fn(), error: vi.fn(), current: () => true }));
vi.mock("../api/backmark", () => ({ listMyBackmarkItems: api.list, claimBackmarkItem: api.claim, getBackmarkItemContext: api.context, submitBackmarkItem: api.submit, downloadBackmarkSegmentImage: api.image }));
vi.mock("../api/regrade", () => ({ listMyRegradeItems: api.list, claimRegradeItem: api.claim, getRegradeItemContext: api.context, recordRegradeCandidate: api.submit, downloadRegradeSegmentImage: api.image }));
vi.mock("./TaskScoringReference", () => ({ TaskScoringReference: () => <p>完整冻结评分依据</p> }));
vi.mock("./MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <p>{children}</p> }));
vi.mock("../features/grading/SharedScoreControl", () => ({ SharedScoreControl: ({ value, onChange }: { value: { score: number | null; rubricSelections: Record<string, number> }; onChange: (value: unknown) => void }) => <input aria-label="独立评分" type="number" value={value.score ?? ""} onChange={event => onChange({ ...value, score: Number(event.target.value) })} /> }));
vi.mock("../features/grading/workbench/hooks/useIndependentReviewGuard", async () => {
  const React = await import("react");
  return { useIndependentReviewGuard: () => ({ busyRef: React.useRef(false), isCurrent: api.current, leave: (action: () => void) => action() }) };
});
vi.mock("antd", () => {
  const context = { message: { success: api.success, error: api.error } };
  const pass = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  const List = Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (row: unknown) => ReactNode }) => <div>{dataSource.map((row, index) => <Fragment key={index}>{renderItem(row)}</Fragment>)}</div>, { Item: Object.assign(({ children, actions }: { children: ReactNode; actions: ReactNode[] }) => <article>{children}{actions}</article>, { Meta: ({ title, description }: { title: ReactNode; description: ReactNode }) => <div><strong>{title}</strong><div>{description}</div></div> }) });
  return { List, Space: pass, Spin: pass, Tag: pass,
    App: { useApp: () => context },
    Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <section>{children}</section> : null,
    Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
    Empty: ({ description }: { description: string }) => <p>{description}</p>, Alert: ({ message }: { message: string }) => <p>{message}</p>,
    Input: { TextArea: ({ value, onChange }: { value: string; onChange: React.ChangeEventHandler<HTMLTextAreaElement> }) => <textarea value={value} onChange={onChange} /> }
  };
});

const first = { id: "one", exam_name: "合成期末", subject: "math", question_no: "1", anonymous_code: "匿名甲", max_score: 10, revision: 1, status: "pending", created_at: "2026-10-28" };
const second = { ...first, id: "two", question_no: "2", anonymous_code: "匿名乙" };
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.clearAllMocks(); api.image.mockRejectedValue(new Error("no image")); api.submit.mockResolvedValue({});
  api.list.mockResolvedValue({ backmark_items: [first, second], regrade_items: [first, second], has_more: false });
  api.claim.mockImplementation(async id => ({ backmark_item: { ...first, id }, regrade_item: { ...first, id } }));
  const context = { expected_revision: 2, question: { question_no: "1", score: 10, question_type: "calculation" }, frozen_rubric: { points: [] }, answer: { raw_answer: "本次独立答案" } };
  api.context.mockResolvedValue({ backmark_context: context, regrade_context: context });
});
afterEach(cleanup);

describe.each([{ label: "回标", Component: BackmarkQueue, submit: "提交独立评分" }, { label: "重评", Component: RegradeQueue, submit: "提交重评意见" }])("$label 的任务摘要和连续处理", ({ label, Component, submit }) => {
  it("先按考试题号匿名码辨认和搜索，不展示原分/原评人", async () => {
    render(<Component canWork />); await act(async () => fireEvent.click(screen.getByRole("button", { name: `${label}任务` })));
    expect(screen.getByText("合成期末 · 第 2 题")).toBeTruthy();
    fireEvent.change(screen.getByLabelText(`搜索${label}任务`), { target: { value: "匿名乙" } });
    expect(screen.queryByText("合成期末 · 第 1 题")).toBeNull(); expect(screen.getByText("合成期末 · 第 2 题")).toBeTruthy();
    expect(document.body.textContent).not.toContain("原得分");
  });
  it("失败保留零分输入，成功后才提供继续下一份且不自动提交后续任务", async () => {
    render(<Component canWork />); await act(async () => fireEvent.click(screen.getByRole("button", { name: `${label}任务` })));
    await act(async () => fireEvent.click(screen.getAllByRole("button", { name: `开始${label}` })[0]));
    fireEvent.change(screen.getByLabelText("独立评分"), { target: { value: "0" } });
    api.submit.mockRejectedValueOnce(new Error("temporarily unavailable"));
    await act(async () => fireEvent.click(screen.getByRole("button", { name: submit })));
    expect((screen.getByLabelText("独立评分") as HTMLInputElement).value).toBe("0"); expect(screen.queryByRole("button", { name: "继续下一份" })).toBeNull();
    api.list.mockResolvedValue({ backmark_items: [second], regrade_items: [second], has_more: false });
    await act(async () => fireEvent.click(screen.getByRole("button", { name: submit })));
    expect(api.submit).toHaveBeenCalledTimes(2); expect(api.submit.mock.calls[1][1].score).toBe(0);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "继续下一份" })));
    expect(api.claim).toHaveBeenLastCalledWith("two"); expect(api.submit).toHaveBeenCalledTimes(2);
  });
});
