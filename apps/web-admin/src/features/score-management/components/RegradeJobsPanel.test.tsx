// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../api/client";
import type { RegradeJob } from "../../../api/scoreReleases";
import type { RegradeWorkflowModals } from "./RegradeWorkflowModals";
import { RegradeJobsPanel } from "./RegradeJobsPanel";

const mocks = vi.hoisted(() => ({
  jobs: vi.fn(), current: vi.fn(), releases: vi.fn(), grades: vi.fn(), approve: vi.fn(), createRelease: vi.fn(), users: vi.fn(),
  modals: vi.fn(), message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() }
}));
vi.mock("../../../api/scoreReleases", () => ({
  listRegradeJobs: mocks.jobs, getCurrentPublishedScoreRelease: mocks.current, listScoreReleases: mocks.releases, approveRegradeJob: mocks.approve,
  createRegradeScoreRelease: mocks.createRelease, createRegradeJob: vi.fn(), finalizeRegradeJob: vi.fn(),
  getRegradeJob: vi.fn(), pauseRegradeJob: vi.fn(), previewRegrade: vi.fn(), resumeRegradeJob: vi.fn(),
  reviewRegradeItem: vi.fn(), startRegradeJob: vi.fn()
}));
vi.mock("../../../api/scores", () => ({ listExamGrades: mocks.grades }));
vi.mock("../../../api/workflowActors", () => ({ listReviewActors: mocks.users }));
vi.mock("../../../auth/stepUpContext", () => ({ isStepUpCancelledError: () => false }));
// 弹窗另有测试；这里只核对面板交给弹窗的发布状态和工作流。
vi.mock("./RegradeWorkflowModals", () => ({
  RegradeWorkflowModals: (props: ComponentProps<typeof RegradeWorkflowModals>) => { mocks.modals(props); return null; }
}));
vi.mock("antd", () => {
  const List = Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (value: unknown) => ReactNode }) =>
    <ul>{dataSource.map((value, index) => <li key={String(index)}>{renderItem(value)}</li>)}</ul>, {
    Item: ({ actions, children, className, "aria-current": current }: { actions: ReactNode[]; children: ReactNode; className?: string; "aria-current"?: "true" }) =>
      <article className={className} aria-current={current}>{children}{actions}</article>
  });
  return {
    App: { useApp: () => ({ message: mocks.message }) },
    List,
    Alert: ({ message, action }: { message: ReactNode; action?: ReactNode }) => <div role="alert">{message}{action}</div>,
    Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) =>
      <button type="button" disabled={disabled} data-loading={loading ? "true" : undefined} onClick={onClick}>{children}</button>,
    Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
    Tag: ({ children }: { children: ReactNode }) => <span data-tag>{children}</span>
  };
});

const release = { id: "release-1", version: 1, status: "published" };
function job(overrides: Partial<RegradeJob>): RegradeJob {
  return {
    id: "job", tenant_id: "tenant", exam_id: "exam-1", question_id: "q1", source_release_id: "release-1",
    reason_code: "quality_incident", reason_text: "评分细则需更正", strategy: "human_recheck", selector: {},
    severity_delta: 0, status: "running", affected_count: 3, created_by: "admin", created_at: "", updated_at: "",
    ...overrides
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe("复核与异常页的题目级复评面板", () => {
  let host: HTMLDivElement;
  let root: Root;
  let scroll: ReturnType<typeof vi.fn>;
  let scrollDescriptor: PropertyDescriptor | undefined;

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    // jsdom 没有 scrollIntoView；替换后记录调用元素和次数。
    scrollDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollIntoView");
    scroll = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scroll });
    mocks.jobs.mockResolvedValue({ regrade_jobs: [] });
    mocks.current.mockResolvedValue({ release });
    mocks.releases.mockResolvedValue({ score_releases: [release] });
    mocks.grades.mockResolvedValue({ grades: [] });
    mocks.users.mockResolvedValue([]);
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    if (scrollDescriptor) Object.defineProperty(HTMLElement.prototype, "scrollIntoView", scrollDescriptor);
    else Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
  });

  async function render(props: Partial<ComponentProps<typeof RegradeJobsPanel>> = {}) {
    await act(async () => root.render(<RegradeJobsPanel examId="exam-1" {...props} />));
  }
  const buttons = (scope: ParentNode = host) => [...scope.querySelectorAll<HTMLButtonElement>("button")];
  const button = (label: string, scope: ParentNode = host) => buttons(scope).find(value => value.textContent === label);
  const rows = () => [...host.querySelectorAll("article")];
  const lastModalProps = () => mocks.modals.mock.calls[mocks.modals.mock.calls.length - 1]?.[0] as ComponentProps<typeof RegradeWorkflowModals>;

  it("首次正式发布前不显示新建复评，只说明原因，也不读取成绩", async () => {
    mocks.current.mockRejectedValue(new ApiClientError(404, "score_release_not_found", "not found"));
    await render();
    expect(host.querySelector("section#regrade-jobs")).not.toBeNull();
    expect(host.textContent).toContain("题目级复评由管理员发起");
    expect(host.textContent).toContain("学生申请的成绩复核见“申诉处理”");
    expect(host.textContent).toContain("本场成绩首次正式发布后，才能发起题目级复评。");
    expect(button("新建复评")).toBeUndefined();
    expect(mocks.grades).not.toHaveBeenCalled();
    expect(mocks.releases).not.toHaveBeenCalled();
    expect(lastModalProps().publishedReleaseExists).toBe(false);
  });

  it("当前发布版本读取失败不能当成未发布，提示错误并可重读", async () => {
    mocks.current.mockRejectedValueOnce(new ApiClientError(500, "internal", "boom"));
    await render();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("系统暂时无法完成操作，请稍后重试。");
    expect(host.textContent).not.toContain("才能发起题目级复评");
    await act(async () => button("重新读取")!.click());
    expect(mocks.current).toHaveBeenCalledTimes(2);
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(host.textContent).toContain("当前没有题目级复评任务。");
  });

  it.each([
    { grades: [{ id: "g1", items: [{ question_id: "q1", question_no: "1", max_score: 5 }] }], enabled: true },
    { grades: [], enabled: false }
  ])("已发布后新建复评按可选小题开放：enabled=$enabled", async ({ grades, enabled }) => {
    mocks.grades.mockResolvedValue({ grades });
    await render();
    expect(mocks.grades).toHaveBeenCalledWith("exam-1", { limit: 50 });
    expect(button("新建复评")?.disabled).toBe(!enabled);
    expect(lastModalProps().publishedReleaseExists).toBe(true);
    if (!enabled) return;
    await act(async () => button("新建复评")!.click());
    expect(lastModalProps().regradeWorkflow.regradeModalOpen).toBe(true);
    expect(lastModalProps().regradeWorkflow.regradeQuestionOptions).toEqual([{ value: "q1", label: "1 · 满分 5" }]);
  });

  it("各状态只显示对应的生命周期操作；提交期间锁住其他操作，成功后重读任务", async () => {
    mocks.jobs.mockResolvedValue({ regrade_jobs: [
      job({ id: "a", status: "awaiting_approval" }), job({ id: "b", status: "approved" }), job({ id: "c", status: "running" }),
      job({ id: "d", status: "paused" }), job({ id: "e", status: "diff_review" }), job({ id: "f", status: "cancelled" })
    ] });
    await render();
    expect(rows().map(row => buttons(row).map(value => value.textContent))).toEqual([
      ["批准"], ["启动并开放给阅卷员"], ["暂停"], ["恢复"], ["暂停", "完成复评", "复核候选"], []
    ]);
    expect(rows().map(row => row.querySelector("[data-tag]")?.textContent)).toEqual(["待批准", "已批准", "复评中", "已暂停", "差异复核", "已取消"]);
    const approving = deferred<unknown>();
    mocks.approve.mockReturnValue(approving.promise);
    await act(async () => button("批准")!.click());
    expect(mocks.approve).toHaveBeenCalledWith("a");
    expect(button("恢复")!.disabled).toBe(true);
    expect(button("刷新复评任务")!.disabled).toBe(true);
    await act(async () => { approving.resolve({}); await approving.promise; });
    expect(mocks.message.success).toHaveBeenCalledWith("已批准复评任务");
    expect(mocks.jobs).toHaveBeenCalledTimes(2);
    expect(button("恢复")!.disabled).toBe(false);
  });

  it("结果待发布的任务只跳到成绩发布页发布更正，本面板不生成结果版本", async () => {
    const navigate = vi.fn();
    mocks.jobs.mockResolvedValue({ regrade_jobs: [job({ id: "job/1", status: "ready_for_release" })] });
    await render({ examId: "exam 1", onNavigate: navigate });
    expect(rows()[0].querySelector("[data-tag]")?.textContent).toBe("结果待发布");
    await act(async () => button("去发布更正")!.click());
    expect(navigate).toHaveBeenCalledWith("/exams/exam%201/scores?regrade=job%2F1");
    expect(mocks.createRelease).not.toHaveBeenCalled();
    expect(button("继续结果版本")).toBeUndefined();
  });

  // 发布结果版本不改变任务状态，只能从版本列表的 regrade_job_id 判断更正是否已发布。
  it.each([
    { status: "published", label: "更正已发布", hasButton: false },
    { status: "draft", label: "结果待发布", hasButton: true }
  ])("结果版本为 $status 时显示 $label", async ({ status, label, hasButton }) => {
    mocks.jobs.mockResolvedValue({ regrade_jobs: [job({ id: "done", status: "ready_for_release" })] });
    mocks.releases.mockResolvedValue({ score_releases: [release, { id: "release-2", version: 2, status, regrade_job_id: "done" }] });
    await render({ onNavigate: vi.fn() });
    expect(rows()[0].querySelector("[data-tag]")?.textContent).toBe(label);
    expect(Boolean(button("去发布更正"))).toBe(hasButton);
  });

  it("深链接任务排在最前并高亮，只在首次读取后滚动一次", async () => {
    mocks.jobs.mockResolvedValue({ regrade_jobs: [job({ id: "first", reason_text: "第一项" }), job({ id: "linked", reason_text: "链接任务" })] });
    await render({ focusedRegradeId: "linked" });
    expect(rows()[0].textContent).toContain("链接任务");
    expect(rows()[0].className).toBe("score-regrade-focused");
    expect(rows()[0].getAttribute("aria-current")).toBe("true");
    expect(rows()[1].className).toBe("");
    expect(scroll).toHaveBeenCalledTimes(1);
    expect(scroll.mock.contexts[0]).toBe(host.querySelector("section#regrade-jobs"));
    await act(async () => button("刷新复评任务")!.click());
    expect(mocks.jobs).toHaveBeenCalledTimes(2);
    expect(scroll).toHaveBeenCalledTimes(1);
  });

  it.each([
    { focusedRegradeId: undefined, scrolls: 0 },
    { focusedRegradeId: "", scrolls: 1 }
  ])("未指定任务时不高亮任何一行，focused=$focusedRegradeId 滚动 $scrolls 次", async ({ focusedRegradeId, scrolls }) => {
    mocks.jobs.mockResolvedValue({ regrade_jobs: [job({ id: "only" })] });
    await render({ focusedRegradeId });
    expect(rows()[0].className).toBe("");
    expect(rows()[0].getAttribute("aria-current")).toBeNull();
    expect(scroll).toHaveBeenCalledTimes(scrolls);
  });

  it("重读失败保留已读任务，但在重新读到之前不能提交", async () => {
    mocks.jobs.mockResolvedValueOnce({ regrade_jobs: [job({ id: "a", status: "awaiting_approval" })] });
    await render();
    expect(button("批准")!.disabled).toBe(false);
    mocks.jobs.mockRejectedValueOnce(new ApiClientError(500, "internal", "boom"));
    await act(async () => button("刷新复评任务")!.click());
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("系统暂时无法完成操作，请稍后重试。");
    expect(rows()).toHaveLength(1);
    expect(button("批准")!.disabled).toBe(true);
  });

  it("换考试后旧考试的迟到回包不能写入面板", async () => {
    const stale = deferred<{ regrade_jobs: RegradeJob[] }>();
    mocks.jobs.mockReturnValueOnce(stale.promise).mockResolvedValueOnce({ regrade_jobs: [job({ id: "new", exam_id: "exam-2", reason_text: "新考试任务" })] });
    await render({ examId: "exam-1" });
    await render({ examId: "exam-2" });
    expect(host.textContent).toContain("新考试任务");
    await act(async () => { stale.resolve({ regrade_jobs: [job({ id: "old", reason_text: "旧考试任务" })] }); await stale.promise; });
    expect(host.textContent).toContain("新考试任务");
    expect(host.textContent).not.toContain("旧考试任务");
    expect(mocks.jobs.mock.calls).toEqual([["exam-1"], ["exam-2"]]);
  });
});
