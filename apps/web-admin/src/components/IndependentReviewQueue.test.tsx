// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../api/businessCommand";
import { RegradeQueue } from "./RegradeQueue";
import { BackmarkQueue } from "./BackmarkQueue";

const mocks = vi.hoisted(() => ({ confirm: vi.fn(), warning: vi.fn(), error: vi.fn(), success: vi.fn(), guard: vi.fn(),
  regrade: { list: vi.fn(), claim: vi.fn(), context: vi.fn(), image: vi.fn(), submit: vi.fn() },
  backmark: { list: vi.fn(), claim: vi.fn(), context: vi.fn(), image: vi.fn(), submit: vi.fn() }
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: mocks, modal: mocks }) },
  Button: ({ children, onClick, disabled }: any) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Drawer: ({ open, children, onClose }: any) => open ? <div><button onClick={onClose}>关闭抽屉</button>{children}</div> : null,
  Alert: ({ message, description }: any) => <div>{message}{description}</div>, Empty: ({ description }: any) => <p>{description}</p>,
  Input: { TextArea: ({ value, onChange, disabled }: any) => <textarea disabled={disabled} value={value} onChange={onChange} /> },
  List: Object.assign(({ dataSource, renderItem }: any) => <div>{dataSource.map(renderItem)}</div>, { Item: Object.assign(({ children, actions }: any) => <div>{children}{actions}</div>, { Meta: ({ title }: any) => <p>{title}</p> }) }),
  Space: ({ children }: any) => <div>{children}</div>, Spin: ({ children }: any) => <div>{children}</div>, Tag: ({ children }: any) => <span>{children}</span>
}));
vi.mock("../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: mocks.guard }));
vi.mock("../features/grading/SharedScoreControl", () => ({ SharedScoreControl: ({ value, onChange, disabled }: any) => <><span>分数 {value.score ?? "空"}</span><button disabled={disabled} onClick={() => onChange({ score: 4, rubricSelections: { p: 4 } })}>给4分</button></> }));
vi.mock("../api/regrade", () => ({ listMyRegradeItems: mocks.regrade.list, claimRegradeItem: mocks.regrade.claim, getRegradeItemContext: mocks.regrade.context, downloadRegradeSegmentImage: mocks.regrade.image, recordRegradeCandidate: mocks.regrade.submit }));
vi.mock("../api/backmark", () => ({ listMyBackmarkItems: mocks.backmark.list, claimBackmarkItem: mocks.backmark.claim, getBackmarkItemContext: mocks.backmark.context, downloadBackmarkSegmentImage: mocks.backmark.image, submitBackmarkItem: mocks.backmark.submit }));

for (const kind of ["regrade", "backmark"] as const) describe(`${kind} independent score protection`, () => {
  let root: Root; let container: HTMLDivElement;
  const label = kind === "regrade" ? "重评" : "回标";
  const submitLabel = kind === "regrade" ? "提交重评意见" : "提交独立评分";
  const api = mocks[kind];
  beforeEach(() => {
    vi.clearAllMocks(); setBusinessCommandScope("tenant", "teacher"); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const item = { id: "item", max_score: 5, created_at: "2026-10-05T00:00:00Z", status: "assigned" };
    const context = { expected_revision: 2, frozen_rubric: { points: [{ id: "p", score: 5 }] }, question: { question_no: "1", question_type: "short_answer", score: 5, stem: "题干" }, answer: { raw_answer: "回答" } };
    api.list.mockResolvedValue(kind === "regrade" ? { regrade_items: [item] } : { backmark_items: [item], has_more: false });
    api.claim.mockResolvedValue(kind === "regrade" ? { regrade_item: item } : { backmark_item: item });
    api.context.mockResolvedValue(kind === "regrade" ? { regrade_context: context } : { backmark_context: context });
    api.image.mockRejectedValue(new Error("image unavailable")); api.submit.mockResolvedValue({});
    container = document.createElement("div"); document.body.appendChild(container); root = createRoot(container);
  });
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); });
  const click = async (text: string) => { const button = [...container.querySelectorAll("button")].find((item) => item.textContent === text); if (!button) throw new Error(`missing ${text}`); await act(async () => button.click()); };
  const open = async () => { await act(async () => root.render(kind === "regrade" ? <RegradeQueue canWork /> : <BackmarkQueue canWork />)); await click(`${label}任务`); await click(`开始${label}`); await click("给4分"); };
  it("asks before discarding scored work and retains it on cancellation", async () => {
    await open(); await click("返回队列");
    expect(mocks.confirm).toHaveBeenCalledOnce(); expect(container.textContent).toContain("分数 4");
    expect(mocks.guard).toHaveBeenLastCalledWith(expect.objectContaining({ dirty: true, busy: false }));
    await act(async () => { mocks.confirm.mock.calls[0][0].onOk(); });
    expect(container.textContent).not.toContain("分数 4"); expect(container.textContent).toContain(`开始${label}`);
  });
  it("blocks close and duplicate submit until failure, then preserves the score", async () => {
    await open(); let reject!: (error: Error) => void; api.submit.mockImplementation(() => new Promise((_resolve, fail) => { reject = fail; }));
    await click(submitLabel); await click("关闭抽屉"); await click(submitLabel);
    expect(api.submit).toHaveBeenCalledOnce(); expect(mocks.warning).toHaveBeenCalled(); expect(container.querySelector("textarea")?.disabled).toBe(true);
    await act(async () => { reject(new Error("failed")); });
    expect(container.textContent).toContain("分数 4"); expect(container.querySelector("textarea")?.disabled).toBe(false);
    expect(mocks.guard).toHaveBeenLastCalledWith(expect.objectContaining({ dirty: true, busy: false }));
  });
  it("drops a previous login's late submission result", async () => {
    await open(); let finish!: (value: unknown) => void; api.submit.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    await click(submitLabel); await act(async () => { setBusinessCommandScope("tenant", "another-teacher"); });
    await act(async () => { finish({}); });
    expect(container.textContent).not.toContain("分数 4"); expect(mocks.success).not.toHaveBeenCalled();
  });
});
