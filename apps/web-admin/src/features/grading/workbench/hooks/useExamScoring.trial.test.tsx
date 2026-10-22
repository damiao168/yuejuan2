// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import type { ScoringSummary } from "../../../../api/review";
import type { SubjectiveGradingBatch } from "../../../../api/subjectiveGrading";
import { useExamScoring, type UseExamScoringOptions } from "./useExamScoring";

const mocks = vi.hoisted(() => ({
  summary: vi.fn(), readiness: vi.fn(), detail: vi.fn(), create: vi.fn(), enqueue: vi.fn(), list: vi.fn(), failedSegments: vi.fn(),
  cancel: vi.fn(), retry: vi.fn(), resume: vi.fn(), loadPlan: vi.fn(), submit: vi.fn(), changed: vi.fn(),
  message: { error: vi.fn(), warning: vi.fn(), success: vi.fn(), info: vi.fn() },
  progress: {
    setAIBatchIds: vi.fn(), aiBatches: [], setAIBatches: vi.fn(), aiProgress: null,
    aiIssue: null, setAIIssue: vi.fn(), aiBatchRun: { current: "" }, retryableAIBatches: []
  }
}));

vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message }) } }));
vi.mock("../../../../api/review", () => ({
  getScoringSummary: mocks.summary, getScoringReadiness: mocks.readiness, getExamAutomationResults: mocks.detail,
  cancelScoringRun: mocks.cancel, retryFailedScoringRun: mocks.retry, resumeScoringRun: mocks.resume
}));
vi.mock("../../../../api/subjectiveGrading", () => ({
  createSubjectiveGradingBatch: mocks.create, enqueueSubjectiveGradingBatch: mocks.enqueue,
  getScoringRunAIBatches: mocks.list, getFailedSubjectiveBatchSegments: mocks.failedSegments
}));
vi.mock("./useScoringAIBatchProgress", () => ({ useScoringAIBatchProgress: () => mocks.progress }));
vi.mock("./useScoringResults", () => ({ useScoringResults: () => ({}) }));
vi.mock("./scoringCommand", () => ({ submitScoringCommand: mocks.submit }));
vi.mock("../scoringPlan", () => ({ loadScoringPlan: mocks.loadPlan }));
vi.mock("../gradingWorkbench.model", () => ({ formatError: (error: unknown) => String(error) }));

function summary(status: NonNullable<ScoringSummary["run"]>["status"] = "processing", examId = "exam-1") {
  return { scoring_summary: {
    run: { id: `run-${examId}`, tenant_id: "tenant", exam_id: examId, status, idempotency_key: "scoring-command",
      started_by: "teacher", created_at: "", updated_at: "", total_count: 100, queued_count: 0,
      auto_confirmed_count: 0, human_confirmed_count: 0, review_count: 100, failed_count: 0 },
    questions: [{ question_id: "ai-question", question_no: "1", question_type: "essay", total: 100, queued: 0, confirmed: 0, review: 100, failed: 0 }]
  } satisfies ScoringSummary };
}

function batch(id: string, status: SubjectiveGradingBatch["status"] = "planned", overrides: Partial<SubjectiveGradingBatch> = {}): SubjectiveGradingBatch {
  return {
    id, tenant_id: "tenant", scoring_run_id: "run-exam-1", idempotency_key: id, status,
    segment_ids: [id], total_count: 1, queued_count: 0, processing_count: 0, succeeded_count: 0, failed_count: 0,
    created_by: "teacher", created_at: "", updated_at: "", ...overrides
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("评分工作台服务端接续与会话隔离（mock API）", () => {
  let root: Root;
  let scoring: ReturnType<typeof useExamScoring>;
  let options: UseExamScoringOptions;

  function Probe({ value }: { value: UseExamScoringOptions }) {
    scoring = useExamScoring(value);
    return null;
  }

  async function render(patch: Partial<UseExamScoringOptions> = {}) {
    options = { ...options, ...patch };
    await act(async () => { root.render(<Probe value={options} />); });
  }

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    setBusinessCommandScope("tenant", "teacher");
    localStorage.clear();
    mocks.progress.aiBatchRun.current = "";
    mocks.summary.mockImplementation(async (examId: string) => summary("processing", examId));
    mocks.readiness.mockResolvedValue({ scoring_readiness: { ready: true, checks: [] } });
    mocks.detail.mockResolvedValue({ items: [] });
    mocks.list.mockResolvedValue({ batches: [] });
    mocks.enqueue.mockResolvedValue({ enqueue_result: { accepted_count: 1, failed_count: 0 } });
    mocks.loadPlan.mockResolvedValue({ questions: [{ questionId: "ai-question", lane: "ai" }] });
    mocks.changed.mockResolvedValue(undefined);
    mocks.resume.mockResolvedValue({ scoring_run: summary().scoring_summary.run, requeued: 1, skipped: 0 });
    mocks.retry.mockResolvedValue({ scoring_run: summary().scoring_summary.run, requeued: 1, skipped: 0 });
    options = { initialExamId: "exam-1", currentUserId: "teacher", currentTenantId: "tenant", canGrade: true, onTasksChanged: mocks.changed };
    root = createRoot(document.createElement("div"));
  });

  afterEach(async () => { await act(async () => { root.unmount(); }); localStorage.clear(); });

  it.each(["queued", "processing", "needs_review", "failed"] as const)("mounting and refreshing an active %s run does not create or enqueue any AI batch", async (status) => {
    // 遗留浏览器批准不能在重载后授权整场入队；即使远端存在 planned 批次，挂载也只读取进度。
    localStorage.setItem("scoring-trial-gate:exam-1:run-exam-1", "approved");
    mocks.summary.mockResolvedValue(summary(status));
    mocks.list.mockResolvedValue({ batches: [batch("existing-full-run", "planned", { total_count: 100 })] });
    await render();
    expect(scoring.summary?.run?.status).toBe(status);
    await act(async () => { await scoring.loadSummary(); });
    expect(mocks.summary).toHaveBeenCalledTimes(2);
    expect(mocks.list).toHaveBeenCalledTimes(2);
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.enqueue).not.toHaveBeenCalled();
  });

  it("ordinary recovery asks the server to resume the plan without building browser-owned batches", async () => {
    mocks.list.mockResolvedValue({ batches: [
      batch("planned"),
      batch("partial", "processing", { total_count: 2, queued_count: 1 }),
      batch("fully-queued", "processing", { queued_count: 1 }),
      batch("completed", "completed", { succeeded_count: 1 }),
      batch("failed", "failed", { failed_count: 1 }),
      batch("cancelled", "cancelled")
    ] });
    await render();
    await act(async () => { await scoring.retryAI(); });
    expect(mocks.resume).toHaveBeenCalledExactlyOnceWith("run-exam-1");
    expect(mocks.enqueue).not.toHaveBeenCalled();
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.loadPlan).not.toHaveBeenCalled();
    expect(mocks.failedSegments).not.toHaveBeenCalled();
    expect(scoring.actioning).toBeNull();
  });

  it("can resume a persisted plan before the server creates its first AI batch", async () => {
    await render();
    await act(async () => { await scoring.retryAI(); });
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.enqueue).not.toHaveBeenCalled();
    expect(mocks.resume).toHaveBeenCalledExactlyOnceWith("run-exam-1");
    expect(scoring.actioning).toBeNull();
  });

  it.each(["teacher", "tenant", "exam", "session", "permission"] as const)("a recovery waiting for the run cannot mutate after the %s scope changes", async (changedScope) => {
    await render();
    const old = deferred<ReturnType<typeof summary>>();
    mocks.summary.mockReturnValueOnce(old.promise);
    let pending!: Promise<void>;
    await act(async () => { pending = scoring.retryAI(); });
    const patch: Partial<UseExamScoringOptions> = {};
    if (changedScope === "teacher") { setBusinessCommandScope("tenant", "another-teacher"); patch.currentUserId = "another-teacher"; }
    if (changedScope === "tenant") { setBusinessCommandScope("another-tenant", "teacher"); patch.currentTenantId = "another-tenant"; }
    if (changedScope === "exam") patch.initialExamId = "exam-2";
    if (changedScope === "session") { setBusinessCommandScope("", ""); setBusinessCommandScope("tenant", "teacher"); }
    if (changedScope === "permission") patch.canGrade = false;
    await render(patch);
    await act(async () => { old.resolve(summary()); await pending; });
    expect(mocks.resume).not.toHaveBeenCalled();
    expect(mocks.enqueue).not.toHaveBeenCalled();
    expect(mocks.create).not.toHaveBeenCalled();
    expect(scoring.actioning).toBeNull();
    if (changedScope !== "permission") expect(scoring.summary?.run?.exam_id).toBe(options.initialExamId);
  });

  it("an old recovery result cannot refresh the new account or create browser-owned batches", async () => {
    const failed = batch("failed", "failed", { failed_count: 1 });
    mocks.list.mockResolvedValue({ batches: [failed] });
    await render();
    const old = deferred<{ scoring_run: NonNullable<ScoringSummary["run"]>; requeued: number; skipped: number }>();
    mocks.retry.mockReturnValueOnce(old.promise);
    let pending!: Promise<void>;
    await act(async () => { pending = scoring.retryFailedAI(); });
    expect(mocks.retry).toHaveBeenCalledExactlyOnceWith("run-exam-1");
    setBusinessCommandScope("tenant", "another-teacher");
    await render({ currentUserId: "another-teacher" });
    const requests = mocks.summary.mock.calls.length;
    await act(async () => { old.resolve({ scoring_run: summary().scoring_summary.run, requeued: 1, skipped: 0 }); await pending; });
    expect(mocks.summary).toHaveBeenCalledTimes(requests);
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.enqueue).not.toHaveBeenCalled();
    expect(scoring.actioning).toBeNull();
  });

  it("checks the live login session before resuming a batch even before React renders the new account", async () => {
    await render();
    const old = deferred<ReturnType<typeof summary>>();
    mocks.summary.mockReturnValueOnce(old.promise);
    let pending!: Promise<void>;
    await act(async () => { pending = scoring.retryAI(); });
    // Cookie 会话先变化、界面尚未重绘；闭包中的身份字符串不能单独授予下一次写请求。
    setBusinessCommandScope("tenant", "another-teacher");
    await act(async () => { old.resolve(summary()); await pending; });
    expect(mocks.resume).not.toHaveBeenCalled();
    expect(mocks.enqueue).not.toHaveBeenCalled();
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it("概览读取真实结果用于待办分类，但不会自动进入结果视图", async () => {
    mocks.summary.mockResolvedValue(summary("needs_review"));
    const detail = { items: [{ answer_segment_id: "q14", state: "review", reason_code: "answer_observation_uncertain" }] };
    mocks.detail.mockResolvedValue(detail);
    await render();
    expect(scoring.runDetail).toEqual(detail);
    expect(scoring.detailOpen).toBe(false);
    expect(scoring.detailError).toBeNull();
  });

  it("较早的结果请求不能覆盖新结果，离开后迟到的请求也不能重新打开结果视图", async () => {
    mocks.summary.mockResolvedValue(summary("needs_review"));
    await render();
    const old = deferred<{ items: [] }>();
    const latest = deferred<{ items: [] }>();
    mocks.detail.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise);
    let first!: Promise<void>;
    let second!: Promise<void>;
    await act(async () => { first = scoring.showDetail(); });
    await act(async () => { second = scoring.showDetail(); });
    expect(scoring.detailOpen).toBe(true);
    act(() => { scoring.setDetailOpen(false); });
    const latestValue = { items: [] as [] };
    await act(async () => { latest.resolve(latestValue); await second; });
    expect(scoring.runDetail).toBe(latestValue);
    await act(async () => { old.resolve({ items: [] }); await first; });
    expect(scoring.runDetail).toBe(latestValue);
    expect(scoring.detailOpen).toBe(false);
    expect(scoring.actioning).toBeNull();
  });
});
