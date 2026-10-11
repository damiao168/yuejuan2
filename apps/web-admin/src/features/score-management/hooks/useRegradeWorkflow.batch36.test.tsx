// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RegradeSummary } from "../../../api/scoreReleases";
import { useRegradeWorkflow } from "./useRegradeWorkflow";

const mocks = vi.hoisted(() => ({
  approve: vi.fn(), create: vi.fn(), createRelease: vi.fn(), finalize: vi.fn(),
  get: vi.fn(), pause: vi.fn(), preview: vi.fn(), resume: vi.fn(), review: vi.fn(),
  start: vi.fn(), users: vi.fn(), error: vi.fn(), confirm: vi.fn()
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { error: mocks.error }, modal: { confirm: mocks.confirm } }) }
}));
vi.mock("../../../api/scoreReleases", () => ({
  approveRegradeJob: mocks.approve, createRegradeJob: mocks.create,
  createRegradeScoreRelease: mocks.createRelease, finalizeRegradeJob: mocks.finalize,
  getRegradeJob: mocks.get, pauseRegradeJob: mocks.pause, previewRegrade: mocks.preview,
  resumeRegradeJob: mocks.resume, reviewRegradeItem: mocks.review, startRegradeJob: mocks.start
}));
vi.mock("../../../api/workflowActors", () => ({ listReviewActors: mocks.users }));
vi.mock("../../../api/client", () => ({
  getUserErrorMessage: (cause: unknown, fallback: string) => cause instanceof Error ? cause.message : fallback
}));

const item: RegradeSummary["items"][number] = {
  id: "item-1", job_id: "job-1", submission_id: "submission-1", old_final_grade_id: "grade-1",
  old_score: 4, max_score: 10, candidate_score: 5, status: "awaiting_review",
  revision: 7, created_at: "", updated_at: ""
};
const summary: RegradeSummary = {
  job: {
    id: "job-1", tenant_id: "tenant-1", exam_id: "exam-1", question_id: "question-1",
    source_release_id: "release-1", reason_code: "quality_incident", reason_text: "recheck",
    strategy: "human_recheck", selector: {}, severity_delta: 0, status: "reviewing",
    affected_count: 1, created_by: "user-1", created_at: "", updated_at: ""
  },
  items: [item], events: [], diff_histogram: [], severe_changes: []
};
const runAction = vi.fn<(key: string, action: () => Promise<void>, successText: string) => Promise<void>>();
const setActioning = vi.fn<(key: string | null) => void>();

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("regrade review batch36 decision boundaries", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useRegradeWorkflow>;

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.review.mockResolvedValue({});
    mocks.get.mockResolvedValue({ regrade: summary });
    mocks.users.mockResolvedValue([]);
    runAction.mockImplementation(async (_key, action) => { await action(); });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  async function mount() {
    function Probe() {
      current = useRegradeWorkflow({
        canManage: true, selectedExamId: "exam-1", grades: [], runAction, setActioning
      });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
    await act(async () => { current.markEvidenceReviewed(item.id, item.revision); });
  }

  it.each([
    { decision: "accept", score: 5, text: "已接受重评候选" },
    { decision: "reject", score: 9, text: "已驳回重评候选" },
    { decision: "exception", score: 0, text: "已标记重评异常" }
  ] as const)("retains $decision text and omits an inapplicable reviewed score", async ({ decision, score, text }) => {
    await mount();
    await act(async () => { current.setRegradeReviewScores({ [item.id]: score }); });
    await act(async () => {
      current.decideRegradeItem(item, decision);
      await runAction.mock.results[0].value;
    });
    expect(runAction).toHaveBeenCalledWith("regrade-review-item-1", expect.any(Function), text);
    expect(mocks.review).toHaveBeenCalledWith(item.id, { decision, expected_revision: 7 });
    expect(mocks.get).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("没有核对当前修订的材料时，三种复核决定均不能提交", async () => {
    await mount();
    await act(async () => { current.markEvidenceReviewed(item.id, item.revision - 1); current.setRegradeReviewScores({ [item.id]: 5 }); });
    for (const decision of ["accept", "reject", "exception"] as const) await act(async () => current.decideRegradeItem(item, decision));
    expect(mocks.review).not.toHaveBeenCalled(); expect(runAction).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith("请先核对本条候选的答卷与冻结标准");
  });

  it.each([undefined, null])("rejects accepting a candidate with a %s final score", async (score) => {
    await mount();
    await act(async () => {
      current.setRegradeReviewScores(score === undefined ? {} : { [item.id]: score });
    });
    await act(async () => { current.decideRegradeItem(item, "accept"); });
    expect(mocks.error).toHaveBeenCalledWith("接受候选前请填写最终得分");
    expect(runAction).not.toHaveBeenCalled();
    expect(mocks.review).not.toHaveBeenCalled();
    expect(mocks.get).not.toHaveBeenCalled();
  });

  it.each([0, 7])("sends an accepted manual score of %s together with the item's revision", async (score) => {
    await mount();
    await act(async () => { current.setRegradeReviewScores({ [item.id]: score }); });
    await act(async () => {
      current.decideRegradeItem(item, "accept");
      await runAction.mock.results[0].value;
    });
    expect(mocks.review).toHaveBeenCalledWith(item.id, {
      decision: "accept", reviewed_score: score, expected_revision: 7
    });
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("waits for the review request before reloading the active job and then replaces review scores", async () => {
    await mount();
    await act(async () => { await current.openRegradeReview(summary.job); });
    await act(async () => { current.markEvidenceReviewed(item.id, item.revision); });
    expect(current.regradeReview).toBe(summary);
    expect(current.regradeReviewScores).toEqual({ [item.id]: 5 });
    mocks.get.mockClear();
    const reviewed = deferred<void>();
    const reloaded = deferred<{ regrade: RegradeSummary }>();
    const order: string[] = [];
    // 挂起复核和重读两个回包，分别检查请求先后以及新候选分数的写入时机。
    mocks.review.mockImplementation(() => { order.push("review"); return reviewed.promise; });
    mocks.get.mockImplementation(() => { order.push("reload"); return reloaded.promise; });
    let command!: Promise<void>;
    await act(async () => {
      current.decideRegradeItem(item, "accept");
      command = runAction.mock.results[0].value;
    });
    expect(order).toEqual(["review"]);
    expect(mocks.get).not.toHaveBeenCalled();
    await act(async () => { reviewed.resolve(undefined); });
    expect(order).toEqual(["review", "reload"]);
    expect(mocks.get).toHaveBeenCalledWith("job-1");
    expect(current.regradeReview).toBe(summary);
    const refreshed: RegradeSummary = {
      ...summary, items: [{ ...item, candidate_score: 8 }, { ...item, id: "item-2", candidate_score: null }]
    };
    await act(async () => { reloaded.resolve({ regrade: refreshed }); await command; });
    expect(current.regradeReview).toBe(refreshed);
    expect(current.regradeReviewScores).toEqual({ "item-1": 8, "item-2": null });
    expect(mocks.review).toHaveBeenCalledWith(item.id, { decision: "accept", expected_revision: 7 });
    expect(setActioning.mock.calls).toEqual([["regrade-load-review"], [null]]);
  });
});
