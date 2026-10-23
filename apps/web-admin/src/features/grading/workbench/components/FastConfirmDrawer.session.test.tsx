// @vitest-environment jsdom
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { FastConfirmDrawer } from "./FastConfirmDrawer";

const mocks = vi.hoisted(() => ({ load: vi.fn(), image: vi.fn(), submit: vi.fn(), tasksChanged: vi.fn(), scoringChanged: vi.fn() }));
vi.mock("../gradingTaskContext", () => ({ loadTaskContext: mocks.load }));
vi.mock("../../../../api/review", () => ({ downloadReviewWorkspaceImage: mocks.image, submitHumanGrade: mocks.submit, getReviewTaskContext: vi.fn() }));
vi.mock("antd", () => ({
  Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <div role="dialog">{children}</div> : null,
  Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean }) => <button onClick={onClick} disabled={disabled || loading}>{children}</button>,
  Checkbox: ({ children, checked, disabled, onChange }: { children: ReactNode; checked: boolean; disabled: boolean; onChange: (event: React.ChangeEvent<HTMLInputElement>) => void }) => <label><input type="checkbox" checked={checked} disabled={disabled} onChange={onChange} />{children}</label>,
  Alert: ({ message }: { message: ReactNode }) => <div role="status">{message}</div>,
  Empty: ({ description }: { description: ReactNode }) => <div>{description}</div>, Spin: () => <span>加载中</span>
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function context(id: string): WorkbenchContext {
  return { task: { id, exam_id: id.startsWith("A") ? "exam-A" : "exam-B", assigned_to: "grader", status: "assigned", revision: 1, question_no: "1", anonymous_code: `匿名${id}` },
    reviewContext: { expected_revision: 1, question_snapshot: { scoring_policy_snapshot: { mode: "AI_FAST_CONFIRM", confidence_threshold: 0.9 }, risk_tier: "R2", subject_code: "chinese" }, draft: null },
    question: { score: 10, rubric: { points: [] } },
    aiGrades: [{ id: `ai-${id}`, suggested_score: 8, max_score: 10, confidence: 0.97, evidence: [{ type: "text", answer_text: "依据" }], risk_flags: [], mock: false, status: "succeeded" }],
    warnings: [], segmentImageUrl: `/image-${id}` } as unknown as WorkbenchContext;
}
function props(examId = "exam-A", ids = ["A-1", "A-2"]): Parameters<typeof FastConfirmDrawer>[0] {
  return { tasks: ids.map((id) => context(id).task), examId, currentUserId: "grader", onTasksChanged: mocks.tasksChanged, onScoringChanged: mocks.scoringChanged };
}

describe("快捷确认的页面作用域和提交代次", () => {
  let client: QueryClient;
  let imageNumber = 0;
  beforeEach(() => {
    vi.resetAllMocks(); setBusinessCommandScope("school", "grader");
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    mocks.load.mockImplementation((_client: QueryClient, _scope: unknown, id: string) => Promise.resolve(context(id)));
    mocks.image.mockResolvedValue({ blob: new Blob(["image"]), contentType: "image/png" });
    mocks.submit.mockResolvedValue({}); mocks.tasksChanged.mockResolvedValue(undefined); mocks.scoringChanged.mockResolvedValue(undefined);
    imageNumber = 0;
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => `blob:preview-${++imageNumber}`), revokeObjectURL: vi.fn() }));
  });
  afterEach(() => { cleanup(); client.clear(); vi.unstubAllGlobals(); });
  function tree(value: Parameters<typeof FastConfirmDrawer>[0]) {
    return <QueryClientProvider client={client}><FastConfirmDrawer {...value} /></QueryClientProvider>;
  }
  async function open(waitForLoaded = true) {
    fireEvent.click(screen.getByRole("button", { name: "AI 建议分" }));
    if (waitForLoaded) await waitFor(() => expect(screen.queryByText("加载中")).toBeNull());
  }
  it("入口移到评分侧栏后，切题换挂载点不会关闭已打开的抽屉或重载批次", async () => {
    const first = document.createElement("div"); const second = document.createElement("div");
    document.body.append(first, second);
    try {
      const mounted = render(tree({ ...props(), triggerTarget: first }));
      expect(mounted.container.textContent).toBe("");
      fireEvent.click(within(first).getByRole("button", { name: "AI 建议分" }));
      await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(2));
      const drawer = screen.getByRole("dialog");
      mounted.rerender(tree({ ...props(), triggerTarget: null }));
      expect(screen.getByRole("dialog")).toBe(drawer);
      expect(screen.queryByRole("button", { name: "AI 建议分" })).toBeNull();
      mounted.rerender(tree({ ...props(), triggerTarget: second }));
      expect(screen.getByRole("dialog")).toBe(drawer);
      expect(within(second).getByRole("button", { name: "AI 建议分" })).not.toBeNull();
      expect(mocks.load).toHaveBeenCalledTimes(2);
    } finally { first.remove(); second.remove(); }
  });
  async function inspect(index: number) {
    fireEvent.click(screen.getAllByRole("button", { name: "核对原图与证据" })[index]);
    await screen.findByRole("img", { name: "待核对的学生答题原图" });
    fireEvent.load(screen.getByRole("img", { name: "待核对的学生答题原图" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "已核对原图、证据并确认此分数" }));
  }
  async function submit(count: number, waitForSubmission = true) {
    const previousSubmissions = mocks.submit.mock.calls.length;
    const previousLoads = mocks.load.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: `提交已核对的 ${count} 份` }));
    // 延迟复核用例只等待读取开始；其他用例等待真正提交，避免依赖 act 顺便冲刷网络微任务。
    if (waitForSubmission) await waitFor(() => expect(mocks.submit).toHaveBeenCalledTimes(previousSubmissions + 1));
    else await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(previousLoads + 1));
  }

  it("切考试后旧候选加载结果不能覆盖新列表，使用当前Provider的QueryClient", async () => {
    const old = deferred<WorkbenchContext>(); mocks.load.mockReturnValueOnce(old.promise);
    const mounted = render(tree(props("exam-A", ["A-1"]))); await open(false);
    mounted.rerender(tree(props("exam-B", ["B-1"]))); await open();
    expect(screen.getByText("1 · 匿名卷 匿名B-1")).toBeDefined();
    await act(async () => old.resolve(context("A-1")));
    expect(screen.queryByText("1 · 匿名卷 匿名A-1")).toBeNull(); expect(screen.getByText("1 · 匿名卷 匿名B-1")).toBeDefined();
    expect(mocks.load.mock.calls.every((call) => call[0] === client)).toBe(true);
  });

  it("旧批次的已发送提交返回后不发下一份、不刷新，也不能解开新考试的提交", async () => {
    // A 的首份已发出，切到 B 后再发 B；A 迟到完成不能继续 A 的第二份或改动 B 的忙状态。
    const oldSubmit = deferred<unknown>(); const newSubmit = deferred<unknown>();
    mocks.submit.mockReturnValueOnce(oldSubmit.promise).mockReturnValueOnce(newSubmit.promise);
    const mounted = render(tree(props())); await open(); await inspect(0); await inspect(1); await submit(2);
    expect(mocks.submit.mock.calls.map((call) => call[0])).toEqual(["A-1"]);
    mounted.rerender(tree(props("exam-B", ["B-1"]))); await open(); await inspect(0); await submit(1);
    await act(async () => oldSubmit.resolve({}));
    expect(mocks.submit.mock.calls.map((call) => call[0])).toEqual(["A-1", "B-1"]);
    expect(mocks.tasksChanged).not.toHaveBeenCalled(); expect(mocks.scoringChanged).not.toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "提交已核对的 1 份" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => newSubmit.resolve({}));
    expect(mocks.tasksChanged).toHaveBeenCalledOnce(); expect(mocks.scoringChanged).toHaveBeenCalledOnce();
  });

  it("同帧双击提交只发送一个批次", async () => {
    const pending = deferred<unknown>(); mocks.submit.mockReturnValueOnce(pending.promise);
    render(tree(props("exam-A", ["A-1"]))); await open(); await inspect(0);
    const button = screen.getByRole("button", { name: "提交已核对的 1 份" });
    await act(async () => { fireEvent.click(button); fireEvent.click(button); });
    expect(mocks.submit).toHaveBeenCalledOnce();
    await act(async () => pending.resolve({})); expect(mocks.tasksChanged).toHaveBeenCalledOnce();
  });

  it("卸载后迟到的提交前复核结果不能再发送评分", async () => {
    const pending = deferred<WorkbenchContext>();
    const mounted = render(tree(props("exam-A", ["A-1"]))); await open(); await inspect(0);
    mocks.load.mockReturnValueOnce(pending.promise); await submit(1, false); mounted.unmount();
    await act(async () => pending.resolve(context("A-1")));
    expect(mocks.submit).not.toHaveBeenCalled(); expect(mocks.tasksChanged).not.toHaveBeenCalled(); expect(mocks.scoringChanged).not.toHaveBeenCalled();
  });
});
