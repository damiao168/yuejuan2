// @vitest-environment jsdom
import type { ChangeEvent, ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { FastConfirmDrawer } from "./FastConfirmDrawer";

const mocks = vi.hoisted(() => ({ load: vi.fn(), image: vi.fn(), submit: vi.fn(), tasksChanged: vi.fn(), scoringChanged: vi.fn() }));
vi.mock("../gradingTaskContext", () => ({ loadTaskContext: mocks.load }));
vi.mock("../../../../api/review", () => ({ downloadReviewWorkspaceImage: mocks.image, submitHumanGrade: mocks.submit, getReviewTaskContext: vi.fn() }));
vi.mock("antd", () => ({
  Drawer: ({ open, children }: Readonly<{ open: boolean; children: ReactNode }>) => open ? <div role="dialog">{children}</div> : null,
  Button: ({ children, onClick, disabled, loading }: Readonly<{ children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean }>) => <button onClick={onClick} disabled={disabled || loading}>{children}</button>,
  Checkbox: ({ children, checked, disabled, onChange }: Readonly<{ children: ReactNode; checked: boolean; disabled: boolean; onChange: (event: ChangeEvent<HTMLInputElement>) => void }>) => <label><input type="checkbox" checked={checked} disabled={disabled} onChange={onChange} />{children}</label>,
  Alert: ({ message }: Readonly<{ message: ReactNode }>) => <div role="status">{message}</div>,
  Empty: ({ description }: Readonly<{ description: ReactNode }>) => <div>{description}</div>,
  Spin: () => <span>加载中</span>
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function context(id: string): WorkbenchContext {
  return {
    task: { id, exam_id: "exam-A", assigned_to: "grader", status: "assigned", revision: 1, question_no: "1", anonymous_code: `匿名${id}` },
    reviewContext: { expected_revision: 1, question_snapshot: { scoring_policy_snapshot: { mode: "AI_FAST_CONFIRM", confidence_threshold: 0.9 }, risk_tier: "R2", subject_code: "chinese" }, draft: null },
    question: { score: 10, rubric: { points: [] } },
    aiGrades: [{ id: `ai-${id}`, suggested_score: 8, max_score: 10, confidence: 0.97, evidence: [{ type: "text", answer_text: "依据" }], risk_flags: [], mock: false, status: "succeeded" }],
    warnings: [], segmentImageUrl: `/image-${id}`
  } as unknown as WorkbenchContext;
}

function props(ids = ["A-1"]): Parameters<typeof FastConfirmDrawer>[0] {
  return { tasks: ids.map((id) => context(id).task), examId: "exam-A", currentUserId: "grader", onTasksChanged: mocks.tasksChanged, onScoringChanged: mocks.scoringChanged };
}

describe("快捷确认的候选展示和提交结果", () => {
  let client: QueryClient;
  let imageNumber = 0;

  beforeEach(() => {
    vi.resetAllMocks();
    setBusinessCommandScope("school", "grader");
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    mocks.load.mockImplementation((_client: QueryClient, _scope: unknown, id: string) => Promise.resolve(context(id)));
    mocks.image.mockResolvedValue({ blob: new Blob(["image"]), contentType: "image/png" });
    mocks.submit.mockResolvedValue({});
    mocks.tasksChanged.mockResolvedValue(undefined);
    mocks.scoringChanged.mockResolvedValue(undefined);
    imageNumber = 0;
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => `blob:preview-${++imageNumber}`), revokeObjectURL: vi.fn() }));
  });

  afterEach(() => { cleanup(); client.clear(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  function tree(value: Parameters<typeof FastConfirmDrawer>[0]) {
    return <QueryClientProvider client={client}><FastConfirmDrawer {...value} /></QueryClientProvider>;
  }

  async function open(waitForLoaded = true) {
    fireEvent.click(screen.getByRole("button", { name: "AI 建议分" }));
    if (waitForLoaded) await waitFor(() => expect(screen.queryByText("加载中")).toBeNull());
  }

  async function preview(index: number) {
    fireEvent.click(screen.getAllByRole("button", { name: "核对原图与证据" })[index]);
    await screen.findByRole("img", { name: "待核对的学生答题原图" });
  }

  async function inspect(index: number) {
    await preview(index);
    fireEvent.load(screen.getByRole("img", { name: "待核对的学生答题原图" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "已核对原图、证据并确认此分数" }));
  }

  it("候选加载时先显示加载状态，完成后再显示空队列提示", async () => {
    const pending = deferred<WorkbenchContext>();
    mocks.load.mockReturnValueOnce(pending.promise);
    render(tree(props()));
    await open(false);
    expect(screen.getByText("加载中")).toBeDefined();
    expect(screen.queryByText("本次检查的任务中，暂无可批量确认的 AI 建议分")).toBeNull();
    expect(screen.queryByRole("button", { name: "核对原图与证据" })).toBeNull();

    const ineligible = context("A-1");
    ineligible.aiGrades[0].risk_flags = ["风险"];
    await act(async () => pending.resolve(ineligible));
    expect(screen.queryByText("加载中")).toBeNull();
    expect(screen.getByText("本次检查的任务中，暂无可批量确认的 AI 建议分")).toBeDefined();
    expect(screen.queryByRole("button", { name: "提交已核对的 0 份" })).toBeNull();
  });

  it("提交前出现当前执行失败时停止采纳旧建议，保留完整工作台人工处理", async () => {
    render(tree(props()));
    await open();
    await inspect(0);
    const failed = context("A-1");
    failed.reviewContext.ai_failure = { failure_kind: "output_truncated" };
    // 首次核对成功之后服务端报告失败；二次读取不能沿用已勾选的旧成功建议。
    mocks.load.mockResolvedValueOnce(failed);
    fireEvent.click(screen.getByRole("button", { name: "提交已核对的 1 份" }));
    await screen.findByText("已确认 0 / 1 份；其余任务可能已变化，请刷新并重新核对。");
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(screen.getByText("1 · 匿名卷 匿名A-1")).toBeDefined();
  });
  it("保留重复证据、文本优先级和英文回退，不同文本移动时沿用对应节点", async () => {
    const errors = vi.spyOn(console, "error");
    const loaded = context("A-1");
    const repeated = { type: "类型备用", answer_text: "相同依据", rule: "规则备用" };
    const rule = { type: "类型备用", answer_text: "", rule: "规则依据" };
    const type = { type: "类型依据" };
    const english = { type: "text", answer_text: "answer", rule: "不应替代非空答案的规则" };
    const empty = { type: "text" };
    loaded.aiGrades[0].evidence = [repeated, rule, { ...repeated }, type, english, empty];
    mocks.load.mockResolvedValue(loaded);
    const value = props();
    const mounted = render(tree(value));
    await open();
    expect(screen.getByText("符合条件 1 份")).toBeDefined();
    await preview(0);
    const checkbox = screen.getByRole("checkbox", { name: "已核对原图、证据并确认此分数" }) as HTMLInputElement;
    expect(checkbox.disabled).toBe(true);
    fireEvent.load(screen.getByRole("img", { name: "待核对的学生答题原图" }));
    expect(checkbox.disabled).toBe(false);
    const before = Array.from(mounted.container.querySelectorAll(".fast-confirm-evidence li"));
    expect(before.map((item) => item.textContent)).toEqual(["相同依据", "规则依据", "相同依据", "类型依据", "请在完整工作台核对证据", "请在完整工作台核对证据"]);

    // 只重排已加载的测试证据以检查列表身份；这不模拟服务端证据的实时更新。
    loaded.aiGrades[0].evidence = [rule, repeated, { ...repeated }, type, english, empty];
    mounted.rerender(tree(value));
    const after = Array.from(mounted.container.querySelectorAll(".fast-confirm-evidence li"));
    expect(after.map((item) => item.textContent)).toEqual(["规则依据", "相同依据", "相同依据", "类型依据", "请在完整工作台核对证据", "请在完整工作台核对证据"]);
    expect(after[0]).toBe(before[1]);
    expect(after[1]).toBe(before[0]);
    expect(after[2]).toBe(before[2]);
    expect(after[3]).toBe(before[3]);
    expect(after[4]).toBe(before[4]);
    expect(after[5]).toBe(before[5]);
    expect(errors).not.toHaveBeenCalled();
  });

  it.each([
    { name: "全部成功", ids: ["A-1"], confirmed: ["A-1"], remaining: [], message: "已由教师确认 1 份；提交后进入质检，不会直接发布成绩。" },
    { name: "部分成功", ids: ["A-1", "A-2"], confirmed: ["A-1"], remaining: ["A-2"], message: "已确认 1 / 2 份；其余任务可能已变化，请刷新并重新核对。" },
    { name: "零成功", ids: ["A-1"], confirmed: [], remaining: ["A-1"], message: "已确认 0 / 1 份；其余任务可能已变化，请刷新并重新核对。" }
  ])("$name 时保留结果文案和数量，只移除成功任务", async ({ ids, confirmed, remaining, message }) => {
    mocks.submit.mockImplementation((id: string) => confirmed.includes(id) ? Promise.resolve({}) : Promise.reject(new Error("任务已变化")));
    const mounted = render(tree(props(ids)));
    await open();
    for (let index = 0; index < ids.length; index++) await inspect(index);
    fireEvent.click(screen.getByRole("button", { name: `提交已核对的 ${ids.length} 份` }));
    await screen.findByText(message);

    expect(screen.getByText(message)).toBeDefined();
    expect(mocks.submit.mock.calls.map((call) => call[0])).toEqual(ids);
    expect(mocks.tasksChanged).toHaveBeenCalledOnce();
    expect(mocks.scoringChanged).toHaveBeenCalledOnce();
    const visible = Array.from(mounted.container.querySelectorAll(".fast-confirm-item-row strong"));
    expect(visible.map((item) => item.textContent)).toEqual(remaining.map((id) => `1 · 匿名卷 匿名${id}`));
    expect(mounted.container.querySelector(".fast-confirm-preview")).toBeNull();
    expect(screen.queryByRole("button", { name: "提交已核对的 0 份" })?.getAttribute("disabled")).toBe(remaining.length ? "" : undefined);
  });
});
