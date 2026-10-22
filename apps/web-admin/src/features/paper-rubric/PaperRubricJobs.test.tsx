// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question, RubricDraftJob, SuggestedRubricCandidate } from "../../api/papers";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";
import { PaperRubricJobsButton, PaperRubricJobsProvider, usePaperRubricJobs } from "./PaperRubricJobs";
import { PaperRubricJobBoundary, PaperRubricJobSuggestion } from "./PaperRubricJobSuggestion";
import { PaperRubricAssistant } from "./PaperRubricAssistant";
import { newRubricAssistantDraft } from "./rubricAssistantDraft";
import { rubricSolutionLines, rubricSolutionSources } from "./rubricSolutionLines";

const mocks = vi.hoisted(() => ({ create: vi.fn(), list: vi.fn(), read: vi.fn(), cancel: vi.fn(), confirm: vi.fn(), success: vi.fn() }));
vi.mock("../../api/papers", async original => ({ ...await original<typeof import("../../api/papers")>(),
  createQuestionRubricDraftJob: mocks.create, listQuestionRubricDraftJobs: mocks.list,
  getQuestionRubricDraftJob: mocks.read, cancelQuestionRubricDraftJob: mocks.cancel }));
vi.mock("antd", async original => {
  const source = await original<typeof import("antd")>();
  const app = { modal: { confirm: mocks.confirm }, message: { success: mocks.success } };
  return { ...source, App: { useApp: () => app } };
});
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <div>{children}</div> }));

const first: Question = { id: "q1", tenant_id: "tenant", exam_id: "exam", question_no: "16", question_type: "calculation", score: 6,
  options: [], knowledge_points: [], sort_order: 1, status: "draft", stem: "解方程",
  solution: { id: "solution", question_id: "q1", solution_version: "v1", raw_text: "", steps: [{ step_no: 1, content: "2x+1=5" }, { step_no: 2, content: "2x=4" }, { step_no: 3, content: "x=2" }], source_refs: [], verification_status: "human_confirmed" } };
const second: Question = { ...first, id: "q2", question_no: "17", stem: "求函数值" };

function candidate(question = first): SuggestedRubricCandidate {
  return { candidate_id: question.id, max_score: question.score, origin: "ai_suggestion_from_solution", status: "review_required", issues: [], points: [
    { id: "a", description: `移项与化简 ${question.id}`, suggested_score: 4, evidence_step_ids: ["step-1", "step-2"], review_note: "允许等价解法", source_refs: [] },
    { id: "b", description: `得出结果 ${question.id}`, suggested_score: 2, evidence_step_ids: ["step-3"], review_note: "", source_refs: [] }
  ] };
}
function job(question = first, status: RubricDraftJob["status"] = "succeeded"): RubricDraftJob {
  return { id: `job-${question.id}`, exam_id: question.exam_id, question_id: question.id, question_no: question.question_no, status,
    request_id: `request-${question.id}`, model_name: "synthetic-model", attempt: 1, created_at: "2026-10-16T01:00:00Z", updated_at: "2026-10-16T01:00:00Z",
    input: { expected_question: structuredClone(question), solution_segments: rubricSolutionLines(question).map(line => ({ spans: line.spans ?? [] })) },
    ...(status === "succeeded" ? { result: { suggested_rubric_candidates: [candidate(question)] } } : {}) };
}
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
function context(examId = "exam") {
  return { selectedExam: { id: examId, tenant_id: "tenant" }, options: { questionBankScope: "actor-1" }, config: { questions: [first, second] }, actions: { openQuestionIssue: vi.fn() } } as unknown as PaperRubricViewContext;
}
function Controls({ question }: { question: Question }) {
  const tasks = usePaperRubricJobs()!;
  return <><button onClick={() => void tasks.start(question, [])}>提交本题</button>
    <output aria-label="任务列表">{tasks.jobs.map(item => `${item.question_id}:${item.status}`).join(",")}</output><PaperRubricJobsButton /></>;
}
const pageContext = context();
function suggestionPage(question: Question, onApply: (points: unknown[]) => void, onDirty: (dirty: boolean) => void, boundary = false) {
  const draft = newRubricAssistantDraft(question);
  return <PaperRubricJobsProvider context={pageContext}>
    {boundary ? <PaperRubricJobBoundary key={question.id} question={question} lines={rubricSolutionLines(question)} sources={rubricSolutionSources(question)} disabled={false} onApply={onApply} onDirtyChange={onDirty} draft={draft} />
      : <PaperRubricJobSuggestion key={question.id} question={question} lines={rubricSolutionLines(question)} disabled={false} onApply={onApply} onDirtyChange={onDirty} draft={draft} />}
  </PaperRubricJobsProvider>;
}

beforeEach(() => {
  vi.resetAllMocks();
  mocks.list.mockResolvedValue({ jobs: [], concurrency: 5 });
  mocks.read.mockResolvedValue({ job: job() });
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }) });
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("AI 建议后台任务与题目归属", { timeout: 20_000 }, () => {
  it("第一题提交未完成时仍可换题并提交第二题，两题均保留任务", async () => {
    const pendingFirst = deferred<{ job: RubricDraftJob }>();
    const pendingSecond = deferred<{ job: RubricDraftJob }>();
    mocks.create.mockImplementation((question: Question) => question.id === first.id ? pendingFirst.promise : pendingSecond.promise);
    const view = render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} /></PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("提交本题"));
    view.rerender(<PaperRubricJobsProvider context={pageContext}><Controls question={second} /></PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("提交本题"));
    expect(mocks.create.mock.calls.map(call => call[0].id)).toEqual(["q1", "q2"]);
    // 两个短提交请求分别迟到，换题只改变展示，不向后台任务发送取消请求。
    await act(async () => { pendingFirst.resolve({ job: job(first, "running") }); pendingSecond.resolve({ job: job(second, "queued") }); });
    expect(screen.getByLabelText("任务列表").textContent).toContain("q1:running");
    expect(screen.getByLabelText("任务列表").textContent).toContain("q2:queued");
    expect(mocks.cancel).not.toHaveBeenCalled();
  });

  it("较早发出的列表响应不能抹掉已提交成功的新任务", async () => {
    const oldList = deferred<{ jobs: RubricDraftJob[]; concurrency: number }>();
    mocks.list.mockReturnValueOnce(oldList.promise);
    mocks.create.mockResolvedValue({ job: job(first, "queued") });
    render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} /></PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("提交本题"));
    await waitFor(() => expect(screen.getByLabelText("任务列表").textContent).toContain("q1:queued"));
    await act(async () => oldList.resolve({ jobs: [], concurrency: 5 }));
    expect(screen.getByLabelText("任务列表").textContent).toContain("q1:queued");
  });

  it("同题提交未完成时重复点击只提交一次", async () => {
    const submission = deferred<{ job: RubricDraftJob }>(); mocks.create.mockReturnValueOnce(submission.promise);
    render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} /></PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("提交本题")); fireEvent.click(screen.getByText("提交本题"));
    expect(mocks.create).toHaveBeenCalledOnce();
    await act(async () => submission.resolve({ job: job(first, "queued") }));
    expect(screen.getByLabelText("任务列表").textContent).toContain("q1:queued");
  });

  it("切换身份后丢弃旧身份的迟到列表与提交结果", async () => {
    const listing = deferred<{ jobs: RubricDraftJob[]; concurrency: number }>();
    const submission = deferred<{ job: RubricDraftJob }>();
    mocks.list.mockReturnValueOnce(listing.promise).mockResolvedValue({ jobs: [], concurrency: 5 });
    mocks.create.mockReturnValueOnce(submission.promise);
    const view = render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} /></PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("提交本题"));
    const changed = { ...pageContext, options: { ...pageContext.options, questionBankScope: "actor-2" } };
    view.rerender(<PaperRubricJobsProvider context={changed}><Controls question={first} /></PaperRubricJobsProvider>);
    await act(async () => { listing.resolve({ jobs: [job()], concurrency: 5 }); submission.resolve({ job: job(first, "running") }); });
    expect(screen.getByLabelText("任务列表").textContent).toBe("");
    expect(mocks.cancel).not.toHaveBeenCalled();
  });

  it("刷新后从列表恢复已完成建议，自动预览不产生未保存编辑", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const onApply = vi.fn(); const onDirty = vi.fn();
    const view = render(suggestionPage(first, onApply, onDirty));
    await screen.findByDisplayValue("移项与化简 q1");
    expect(onDirty).not.toHaveBeenCalledWith(true);
    view.unmount();
    render(suggestionPage(first, onApply, onDirty));
    await screen.findByDisplayValue("移项与化简 q1");
    expect(mocks.list).toHaveBeenCalledTimes(2);
    expect(mocks.read).toHaveBeenCalledTimes(2);
    expect(mocks.create).not.toHaveBeenCalled();
    expect(onApply).not.toHaveBeenCalled();
  });

  it("读取第一题结果期间换题，迟到结果不能进入第二题", async () => {
    const oldRead = deferred<{ job: RubricDraftJob }>();
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 }); mocks.read.mockReturnValueOnce(oldRead.promise);
    const onApply = vi.fn(); const onDirty = vi.fn();
    const view = render(suggestionPage(first, onApply, onDirty));
    await waitFor(() => expect(mocks.read).toHaveBeenCalledOnce());
    view.rerender(suggestionPage(second, onApply, onDirty));
    await act(async () => oldRead.resolve({ job: job() }));
    expect(screen.queryByDisplayValue("移项与化简 q1")).toBeNull();
    expect(screen.getByText("生成分值建议")).toBeDefined();
    expect(onApply).not.toHaveBeenCalled();
    expect(onDirty).not.toHaveBeenCalledWith(true);
  });

  it("采用前重新验证服务端任务，来源失效时保留预览并阻止采用", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const onApply = vi.fn(); render(suggestionPage(first, onApply, vi.fn()));
    await screen.findByDisplayValue("移项与化简 q1");
    mocks.read.mockResolvedValue({ job: { ...job(first, "stale"), error: "参考答案已更新" } });
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    await screen.findAllByText("参考答案已更新");
    expect(mocks.read).toHaveBeenCalledTimes(2);
    expect(onApply).not.toHaveBeenCalled();
    expect(screen.getByDisplayValue("移项与化简 q1")).toBeDefined();
  });

  it("教师编辑建议后，采用前验证保留教师改动且采用后仍由外部保存", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const onApply = vi.fn(); const onDirty = vi.fn(); render(suggestionPage(first, onApply, onDirty));
    await screen.findByDisplayValue("移项与化简 q1");
    fireEvent.change(screen.getByLabelText("建议采分点 1 得分条件"), { target: { value: "教师修改后的条件" } });
    expect(onDirty).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    await waitFor(() => expect(onApply).toHaveBeenCalledOnce());
    expect(onApply.mock.calls[0][0]).toEqual([expect.objectContaining({ description: "教师修改后的条件\n参考解答：\n2x+1=5\n2x=4", score: 4 }), expect.objectContaining({ score: 2 })]);
    expect(mocks.read).toHaveBeenCalledTimes(2);
  });

  it("替换确认窗口打开后任务失效，最终确认不能写入旧建议", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const onApply = vi.fn();
    render(<PaperRubricJobsProvider context={pageContext}><PaperRubricAssistant question={first} disabled={false} contentDirty={false}
      hasExistingPoints dirty={false} onApply={onApply} onDirtyChange={vi.fn()} /></PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("AI 建议分值划分"));
    await screen.findByDisplayValue("移项与化简 q1");
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    await waitFor(() => expect(mocks.confirm).toHaveBeenCalledOnce());
    // 窗口停留期间其他教师更新参考答案，最后的采用动作必须再次读取来源。
    mocks.read.mockResolvedValue({ job: { ...job(first, "stale"), error: "参考答案已更新" } });
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.read).toHaveBeenCalledTimes(3);
    expect(onApply).not.toHaveBeenCalled();
    expect(screen.getByDisplayValue("移项与化简 q1")).toBeDefined();
  });

  it("采用校验尚未返回时换题，迟到校验不能采用到新题", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const onApply = vi.fn(); const onDirty = vi.fn(); const view = render(suggestionPage(first, onApply, onDirty));
    await screen.findByDisplayValue("移项与化简 q1");
    const adoption = deferred<{ job: RubricDraftJob }>(); mocks.read.mockReturnValueOnce(adoption.promise);
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    view.rerender(suggestionPage(second, onApply, onDirty));
    await act(async () => adoption.resolve({ job: job() }));
    expect(onApply).not.toHaveBeenCalled();
  });

  it("服务端结果对应旧题目分值时不能进入建议预览", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const changed = { ...first, score: 8 }; const onApply = vi.fn();
    render(suggestionPage(changed, onApply, vi.fn()));
    await screen.findByText("本轮建议已失效，请重新生成");
    expect(screen.queryByDisplayValue("移项与化简 q1")).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });

  it("提交失败后重试沿用幂等请求标识", async () => {
    mocks.create.mockRejectedValueOnce(new Error("网络中断")).mockResolvedValueOnce({ job: job(first, "queued") });
    render(suggestionPage(first, vi.fn(), vi.fn()));
    fireEvent.click(screen.getByText("生成分值建议"));
    await screen.findByText("网络中断");
    fireEvent.click(screen.getByText("生成分值建议"));
    await screen.findByText("已加入队列");
    expect(mocks.create).toHaveBeenCalledTimes(2);
    expect(mocks.create.mock.calls[0][2]).toBe(mocks.create.mock.calls[1][2]);
  });

  it("任务抽屉显示失败原因并可取消另一项运行任务", async () => {
    const failed = { ...job(first, "failed"), error: "模型调用超时" };
    mocks.list.mockResolvedValue({ jobs: [failed, job(second, "running")], concurrency: 5 });
    mocks.cancel.mockResolvedValue({ job: job(second, "cancelled") });
    render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} /></PaperRubricJobsProvider>);
    await waitFor(() => expect(screen.getByLabelText("任务列表").textContent).toContain("q2:running"));
    fireEvent.click(screen.getByText("AI 任务 · 1"));
    await screen.findByText("模型调用超时");
    fireEvent.click(screen.getByText("取消生成"));
    await screen.findByText("已取消");
    expect(mocks.cancel).toHaveBeenCalledWith("exam", "job-q2");
    expect(screen.getByLabelText("任务列表").textContent).toContain("q2:cancelled");
  });

  it("分段建议采用前也要重新验证，不能采用已经失效的方案", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const onApply = vi.fn(); render(suggestionPage(first, onApply, vi.fn(), true));
    fireEvent.click(screen.getByText("让 AI 建议采分段（可选）"));
    await screen.findByText("查看分段建议");
    fireEvent.click(screen.getByText("查看分段建议"));
    await screen.findByText("采用分段建议，不采用建议分值");
    mocks.read.mockResolvedValue({ job: { ...job(first, "stale"), error: "参考答案已更新" } });
    fireEvent.click(screen.getByText("采用分段建议，不采用建议分值"));
    await waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(2));
    expect(onApply).not.toHaveBeenCalled();
  });

  it("新一轮任务已经开始时丢弃上一轮迟到的分段预览", async () => {
    const oldRead = deferred<{ job: RubricDraftJob }>();
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 }); mocks.read.mockReturnValueOnce(oldRead.promise);
    const next = { ...job(first, "queued"), id: "job-new-round", created_at: "2026-10-16T02:00:00Z" };
    mocks.create.mockResolvedValue({ job: next });
    const onApply = vi.fn(); const draft = newRubricAssistantDraft(first);
    render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} />
      <PaperRubricJobBoundary question={first} lines={rubricSolutionLines(first)} sources={rubricSolutionSources(first)} disabled={false} onApply={onApply} onDirtyChange={vi.fn()} draft={draft} />
    </PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("让 AI 建议采分段（可选）"));
    await screen.findByText("查看分段建议"); fireEvent.click(screen.getByText("查看分段建议"));
    await waitFor(() => expect(mocks.read).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByText("提交本题"));
    await waitFor(() => expect(screen.getByLabelText("任务列表").textContent).toContain("q1:queued"));
    await act(async () => oldRead.resolve({ job: job() }));
    expect(screen.queryByText("采用分段建议，不采用建议分值")).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });

  it("分段预览绑定生成轮次，不能借新一轮完成状态采用旧方案", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const next = { ...job(), id: "job-new-round", created_at: "2026-10-16T02:00:00Z" };
    mocks.create.mockResolvedValue({ job: next });
    const onApply = vi.fn(); const draft = newRubricAssistantDraft(first);
    render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} />
      <PaperRubricJobBoundary question={first} lines={rubricSolutionLines(first)} sources={rubricSolutionSources(first)} disabled={false} onApply={onApply} onDirtyChange={vi.fn()} draft={draft} />
    </PaperRubricJobsProvider>);
    fireEvent.click(screen.getByText("让 AI 建议采分段（可选）"));
    await screen.findByText("查看分段建议"); fireEvent.click(screen.getByText("查看分段建议"));
    const apply = await screen.findByRole("button", { name: "采用分段建议，不采用建议分值" }) as HTMLButtonElement;
    expect(apply.disabled).toBe(false);
    fireEvent.click(screen.getByText("提交本题"));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce());
    await waitFor(() => expect(apply.disabled).toBe(true));
    fireEvent.click(apply);
    expect(mocks.read).toHaveBeenCalledOnce();
    expect(onApply).not.toHaveBeenCalled();
  });

  it("旧轮读取结束不能提前解除新轮结果读取期间的禁用状态", async () => {
    const oldRead = deferred<{ job: RubricDraftJob }>(); const newRead = deferred<{ job: RubricDraftJob }>();
    const next = { ...job(), id: "job-new-round", created_at: "2026-10-16T02:00:00Z" };
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    mocks.read.mockReturnValueOnce(oldRead.promise).mockReturnValueOnce(newRead.promise); mocks.create.mockResolvedValue({ job: next });
    const draft = newRubricAssistantDraft(first);
    render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} />
      <PaperRubricJobSuggestion question={first} lines={rubricSolutionLines(first)} disabled={false} onApply={vi.fn()} onDirtyChange={vi.fn()} draft={draft} />
    </PaperRubricJobsProvider>);
    await waitFor(() => expect(mocks.read).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByText("提交本题"));
    await waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(2));
    // 两轮读取同时在途，先返回的旧轮只能丢弃结果，不能释放仍属于新轮的加载状态。
    await act(async () => oldRead.resolve({ job: job() }));
    expect((screen.getByRole("button", { name: "重新生成分值建议" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => newRead.resolve({ job: next }));
    await screen.findByDisplayValue("移项与化简 q1");
  });

  it("新一轮完成时保留教师旧预览，明确确认后可切换查看新轮建议", async () => {
    mocks.list.mockResolvedValue({ jobs: [job()], concurrency: 5 });
    const next = { ...job(), id: "job-new-round", created_at: "2026-10-16T02:00:00Z", result: { suggested_rubric_candidates: [{ ...candidate(), points: [
      { ...candidate().points[0], description: "第二轮的方法建议" }, candidate().points[1]
    ] }] } };
    mocks.create.mockResolvedValue({ job: next });
    const draft = newRubricAssistantDraft(first);
    render(<PaperRubricJobsProvider context={pageContext}><Controls question={first} />
      <PaperRubricJobSuggestion question={first} lines={rubricSolutionLines(first)} disabled={false} onApply={vi.fn()} onDirtyChange={vi.fn()} draft={draft} />
    </PaperRubricJobsProvider>);
    await screen.findByDisplayValue("移项与化简 q1");
    fireEvent.change(screen.getByLabelText("建议采分点 1 得分条件"), { target: { value: "教师正在核对旧轮建议" } });
    mocks.read.mockResolvedValue({ job: next }); fireEvent.click(screen.getByText("提交本题"));
    await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce());
    expect(screen.getByDisplayValue("教师正在核对旧轮建议")).toBeDefined();
    fireEvent.click(await screen.findByRole("button", { name: /查看.*建议/ }));
    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(screen.getByDisplayValue("教师正在核对旧轮建议")).toBeDefined();
    await act(async () => { await mocks.confirm.mock.calls[0][0].onOk(); });
    await screen.findByDisplayValue("第二轮的方法建议");
  });
});
