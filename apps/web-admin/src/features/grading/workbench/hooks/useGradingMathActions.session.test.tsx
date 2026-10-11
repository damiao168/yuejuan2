// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../../api/client";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import type { AiGrade } from "../../../../api/review";
import type { MathRubricScoreResponse, MathUnderstandingResponse } from "../../../../api/mathUnderstanding";
import { createReviewTaskContextScope, reviewTaskContextKeys } from "../../../../query/reviewTaskContext";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import type { MathWorkbenchEvidence } from "../mathWorkbenchEvidence";
import { useGradingMathActions } from "./useGradingMathActions";

const api = vi.hoisted(() => ({ create: vi.fn(), confirm: vi.fn() }));
vi.mock("../../../../api/review", () => ({ createSubjectiveAiGrade: api.create }));
vi.mock("antd", () => ({ Modal: { confirm: api.confirm } }));

type Options = Parameters<typeof useGradingMathActions>[0];
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function fixture(id = "A", activeTask = { current: id }, mathActionLock = { current: false }) {
  let current = true;
  const segmentId = `segment-${id}`;
  const understanding = { artifact: { id: `artifact-${id}`, version: 3, correction_revision: 2, answer_segment_id: segmentId, exam_question_snapshot_id: `snapshot-${id}` }, correction_revision: 0 } as MathUnderstandingResponse;
  const score = { schema_version: "math-rubric-score-v1", scope: "teacher_suggestion_only", artifact_id: `artifact-${id}`, artifact_version: 3, correction_revision: 0, verified_correction_revision: 2, exam_question_snapshot_id: `snapshot-${id}`, rubric_version: "rubric", suggested_score: 2, max_score: 2, unresolved_score: 0 } as MathRubricScoreResponse;
  const evidence: MathWorkbenchEvidence = { segmentId, phase: "ready", understanding, score, message: "" };
  const grade = { id: `grade-${id}`, answer_segment_id: segmentId, math_artifact_id: `artifact-${id}`, math_artifact_version: 3, math_correction_revision: 2, math_scoring_version: "math-rubric-score-v1", rubric_version: "rubric", delivery_mode: "teacher_suggestion", needs_human_review: true, status: "succeeded", mock: false, suggested_score: 2, max_score: 2 } as AiGrade;
  const state = { ctx: { task: { id, answer_segment_id: segmentId }, aiGrades: [{ ...grade, suggested_score: 0 }], reviewContext: { question_snapshot: { risk_tier: "R2", scoring_policy_snapshot: { mode: "AI_ASSIST" } } } } as unknown as WorkbenchContext, requesting: false, notice: "" };
  const math = { state: evidence, dirty: false, refresh: vi.fn(async () => evidence), setDirty: vi.fn() } as unknown as Options["math"];
  const options: Options = {
    isCurrentScope: () => current, queryScope: createReviewTaskContextScope(`exam-${id}`), ctx: state.ctx,
    canGrade: true, canEditDraft: true, viewerMode: "segment", math, activeTask, mathActionLock,
    setCtx: vi.fn((next) => { state.ctx = (typeof next === "function" ? next(state.ctx) : next) as WorkbenchContext; }),
    setMathRequesting: vi.fn((next) => { state.requesting = typeof next === "function" ? next(state.requesting) : next; }),
    setMathRequestNotice: vi.fn((next) => { state.notice = typeof next === "function" ? next(state.notice) : next; }),
    setMathSelection: vi.fn(), changeViewerMode: vi.fn(), notifyInfo: vi.fn()
  };
  return { options, state, math, grade, evidence, invalidate: () => { current = false; } };
}

describe("math workbench action scope and late lock ownership (mock API)", () => {
  let root: Root;
  let client: QueryClient;
  let actions: ReturnType<typeof useGradingMathActions>;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    setBusinessCommandScope("tenant", "teacher");
    client = new QueryClient();
    vi.spyOn(client, "invalidateQueries").mockResolvedValue(undefined);
    root = createRoot(document.createElement("div"));
  });
  afterEach(async () => { await act(async () => root.unmount()); client.clear(); });
  async function render(options: Options) {
    await act(async () => root.render(<QueryClientProvider client={client}><Probe options={options} publish={(value) => { actions = value; }} /></QueryClientProvider>));
  }

  it("does not let A's late finally unlock B's pending math request in the same login session", async () => {
    const first = deferred<MathWorkbenchEvidence>();
    const second = deferred<MathWorkbenchEvidence>();
    const activeTask = { current: "A" }, lock = { current: false };
    const old = fixture("A", activeTask, lock);
    vi.mocked(old.math.refresh).mockReturnValueOnce(first.promise);
    await render(old.options);
    const oldPending = actions.requestMathSuggestion();
    old.invalidate(); activeTask.current = "B"; lock.current = false;
    const current = fixture("B", activeTask, lock);
    vi.mocked(current.math.refresh).mockReturnValueOnce(second.promise);
    api.create.mockResolvedValue({ grade: current.grade });
    await render(current.options);
    const currentPending = actions.requestMathSuggestion();
    expect(lock.current).toBe(true);
    first.resolve(old.evidence);
    await oldPending;
    expect(lock.current).toBe(true);
    expect(current.state.requesting).toBe(true);
    expect(old.options.setMathRequesting).toHaveBeenCalledTimes(1);
    expect(api.create).not.toHaveBeenCalled();
    await actions.requestMathSuggestion();
    expect(current.math.refresh).toHaveBeenCalledTimes(1);
    second.resolve(current.evidence);
    await currentPending;
    expect(api.create).toHaveBeenCalledWith("segment-B");
    expect(lock.current).toBe(false);
    expect(current.state.requesting).toBe(false);
    expect(current.state.ctx.aiGrades).toEqual([current.grade]);
  });

  it("does not continue evidence refresh or write context after an old suggestion request returns", async () => {
    const response = deferred<{ grade: AiGrade }>();
    api.create.mockReturnValue(response.promise);
    const old = fixture();
    await render(old.options);
    const pending = actions.requestMathSuggestion();
    await Promise.resolve();
    expect(api.create).toHaveBeenCalledTimes(1);
    old.invalidate(); old.options.activeTask.current = "B";
    response.resolve({ grade: old.grade });
    await pending;
    expect(old.math.refresh).toHaveBeenCalledTimes(1);
    expect(old.options.setCtx).not.toHaveBeenCalled();
    expect(client.invalidateQueries).not.toHaveBeenCalled();
    expect(old.options.setMathRequesting).toHaveBeenCalledTimes(1);
  });

  it("discards a late post-generation evidence result after switching exam", async () => {
    const latest = deferred<MathWorkbenchEvidence>();
    const old = fixture();
    api.create.mockResolvedValue({ grade: old.grade });
    vi.mocked(old.math.refresh).mockResolvedValueOnce(old.evidence).mockReturnValueOnce(latest.promise);
    await render(old.options);
    const pending = actions.requestMathSuggestion();
    await Promise.resolve(); await Promise.resolve();
    expect(old.math.refresh).toHaveBeenCalledTimes(2);
    old.invalidate();
    latest.resolve(old.evidence);
    await pending;
    expect(old.options.setCtx).not.toHaveBeenCalled();
    expect(old.options.setMathRequestNotice).toHaveBeenCalledTimes(1);
    expect(client.invalidateQueries).not.toHaveBeenCalled();
  });

  it("keeps exact artifact/correction/scorer binding and replaces the same grade ID in the current context", async () => {
    const current = fixture();
    api.create.mockResolvedValue({ grade: current.grade });
    await render(current.options);
    await actions.requestMathSuggestion();
    expect(current.math.refresh).toHaveBeenCalledTimes(2);
    expect(current.state.ctx.aiGrades).toEqual([current.grade]);
    expect(current.state.notice).toBe("新版本数学建议已生成，仍需教师确认。");
    expect(client.invalidateQueries).toHaveBeenCalledWith({ queryKey: reviewTaskContextKeys.task(current.options.queryScope, "A") });
    expect(current.options.mathActionLock.current).toBe(false);
    expect(current.state.requesting).toBe(false);
  });

  it("keeps a generated but outdated grade non-adoptable when evidence version advances", async () => {
    const current = fixture();
    const latest: MathWorkbenchEvidence = { ...current.evidence, understanding: { ...current.evidence.understanding!, artifact: { ...current.evidence.understanding!.artifact, version: 4 } } };
    api.create.mockResolvedValue({ grade: current.grade });
    vi.mocked(current.math.refresh).mockResolvedValueOnce(current.evidence).mockResolvedValueOnce(latest);
    await render(current.options);
    await actions.requestMathSuggestion();
    expect(current.state.notice).toBe("未生成可采纳的当前数学 v2 建议，请继续人工阅卷。");
    expect(current.state.ctx.aiGrades).toEqual([current.grade]);
    expect(current.options.mathActionLock.current).toBe(false);
  });

  it("retains the current revision-conflict notice and refresh recovery", async () => {
    const current = fixture();
    api.create.mockRejectedValue(new ApiClientError(409, "math_revision_conflict", "conflict"));
    await render(current.options);
    await actions.requestMathSuggestion();
    expect(current.state.notice).toBe("数学证据已变化，本次建议未落库，请刷新后核对。");
    expect(current.math.refresh).toHaveBeenCalledTimes(2);
    expect(current.options.setCtx).not.toHaveBeenCalled();
    expect(current.options.mathActionLock.current).toBe(false);
  });

  it.each([
    { name: "human review before HTTP conflict", cause: new ApiClientError(409, "math_human_review_required", "human review"), expected: "仍有未决评分点或风险，已转人工确认；未生成成功建议。" },
    { name: "ordinary request failure", cause: new Error("service unavailable"), expected: "数学建议服务暂不可用或未开放，请继续人工阅卷。" }
  ])("keeps $name notice and evidence refresh recovery", async ({ cause, expected }) => {
    const current = fixture();
    api.create.mockRejectedValue(cause);
    await render(current.options);
    await actions.requestMathSuggestion();
    expect(current.state.notice).toBe(expected);
    expect(current.math.refresh).toHaveBeenCalledTimes(2);
    expect(current.options.setCtx).not.toHaveBeenCalled();
    expect(current.options.mathActionLock.current).toBe(false);
    expect(current.state.requesting).toBe(false);
  });

  it("does not generate a grade while the pre-generation evidence is pending", async () => {
    const current = fixture();
    vi.mocked(current.math.refresh).mockResolvedValue({ ...current.evidence, phase: "pending" });
    await render(current.options);
    await actions.requestMathSuggestion();
    expect(api.create).not.toHaveBeenCalled();
    expect(current.options.mathActionLock.current).toBe(false);
    expect(current.state.requesting).toBe(false);
  });

  it("ignores stale entry points and a stale correction-discard confirmation", async () => {
    const current = fixture();
    current.math.dirty = true;
    await render(current.options);
    const previous = actions;
    previous.refreshMath();
    expect(api.confirm).toHaveBeenCalledTimes(1);
    const confirmation = api.confirm.mock.calls[0][0] as { onOk: () => Promise<void> };
    current.invalidate();
    await confirmation.onOk();
    previous.refreshMath(); previous.selectMathEvidenceStep("old-step"); await previous.requestMathSuggestion();
    expect(current.math.setDirty).not.toHaveBeenCalled();
    expect(current.math.refresh).not.toHaveBeenCalled();
    expect(current.options.setMathSelection).not.toHaveBeenCalled();
    expect(api.create).not.toHaveBeenCalled();
    expect(api.confirm).toHaveBeenCalledTimes(1);
  });
});

function Probe({ options, publish }: Readonly<{ options: Options; publish: (actions: ReturnType<typeof useGradingMathActions>) => void }>) {
  publish(useGradingMathActions(options));
  return null;
}
