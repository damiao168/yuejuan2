// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import { ApiClientError } from "../../../../api/client";
import type { CreateScoringAITrialInput, ScoringTrial, ScoringTrialSample } from "../../../../api/scoringTrials";
import { trialSamplesFinished, trialScoreDifference } from "../trialScoring";
import { useScoringTrials } from "./useScoringTrials";

const api = vi.hoisted(() => ({ run: vi.fn(), snapshot: vi.fn(), list: vi.fn(), create: vi.fn(), enqueue: vi.fn(), decide: vi.fn() }));
vi.mock("../../../../api/review", () => ({ getScoringRun: api.run }));
vi.mock("../../../../api/assessment", () => ({ getQuestionAssessmentSnapshot: api.snapshot }));
vi.mock("../../../../api/scoringTrials", () => ({
  getScoringRunAITrials: api.list, createScoringAITrial: api.create, enqueueScoringAITrial: api.enqueue, decideScoringAITrial: api.decide
}));
vi.mock("../gradingWorkbench.model", () => ({ formatError: (error: unknown) => String(error) }));

const samples: ScoringTrialSample[] = Array.from({ length: 5 }, (_, index) => ({
  segment_id: `segment-${index + 1}`, human_score: 2, max_score: 5,
  roles: index === 0 ? ["representative", "boundary", "incomplete_evidence"] : ["representative"]
}));

function trial(overrides: Partial<ScoringTrial> = {}): ScoringTrial {
  return {
    id: "trial-1", run_id: "run-1", exam_id: "exam-1", question_id: "question-1", command_id: "command-1",
    status: "awaiting_enqueue", revision: 1, fingerprint: "frozen-fingerprint", sample_batch_id: "sample-batch-1",
    samples: samples.map(sample => ({ ...sample })), remaining_segment_ids: ["segment-6"], created_by: "teacher",
    created_at: "2026-10-19T00:00:00Z", updated_at: "2026-10-19T00:00:00Z", ...overrides
  };
}

function command(commandId = "command-1"): CreateScoringAITrialInput {
  return { command_id: commandId, question_id: "question-1", samples: samples.map(({ segment_id, human_score, roles }) => ({ segment_id, human_score, roles })) };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("服务端试改恢复与异步身份边界（mock API）", () => {
  let root: Root;
  let state: ReturnType<typeof useScoringTrials>;
  let options: { examId: string; runId: string; scopeKey: string };
  const commandKey = "scoring-trial-command:tenant:teacher:exam-1:run-1";

  function Probe({ value }: { value: typeof options }) {
    state = useScoringTrials(value.examId, value.runId, value.scopeKey);
    return null;
  }

  async function mount(patch: Partial<typeof options> = {}) {
    options = { ...options, ...patch };
    await act(async () => { root.render(<Probe value={options} />); });
  }

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    setBusinessCommandScope("tenant", "teacher");
    sessionStorage.clear();
    api.run.mockResolvedValue({ items: samples.map(sample => ({ answer_segment_id: sample.segment_id, question_id: "question-1", question_no: "1", question_type: "essay", state: "review" })) });
    api.snapshot.mockResolvedValue({ assessment_snapshot: { id: "snapshot-1", scoring_policy_snapshot: { mode: "AI_ASSIST" }, rubric_snapshot: { max_score: 5 } } });
    api.list.mockResolvedValue({ trials: [] });
    api.create.mockResolvedValue({ trial: trial() });
    api.enqueue.mockResolvedValue({ batch: { id: "sample-batch-1" }, tasks: [], enqueue_result: { accepted_count: 5, failed_count: 0 } });
    options = { examId: "exam-1", runId: "run-1", scopeKey: "scope-1" };
    root = createRoot(document.createElement("div"));
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    vi.restoreAllMocks();
    sessionStorage.clear();
  });

  it("keeps an unaccepted pending command as a new draft even when unrelated historical trials exist", async () => {
    const pending = command("pending-command");
    const accepted = trial({ command_id: pending.command_id });
    const historical = trial({ id: "historical", question_id: "other-question", command_id: "historical-command", status: "stopped" });
    sessionStorage.setItem(commandKey, JSON.stringify(pending));
    api.list.mockResolvedValueOnce({ trials: [historical] }).mockResolvedValue({ trials: [accepted, historical] });
    api.create.mockResolvedValue({ trial: accepted });
    await mount();
    expect(state.pending).toEqual(pending);
    expect(state.trial).toBeNull();
    expect(state.questionId).toBe(pending.question_id);
    expect(api.create).not.toHaveBeenCalled();
    expect(api.enqueue).not.toHaveBeenCalled();

    await act(async () => { await state.create({ question_id: "edited-question", samples: [] }); });
    expect(api.create).toHaveBeenCalledExactlyOnceWith("run-1", pending);
    expect(api.enqueue).toHaveBeenCalledExactlyOnceWith("run-1", accepted.id);
    expect(state.trial?.command_id).toBe(pending.command_id);
    expect(state.pending).toBeNull();
  });

  it("restores an uncertain command for the same actor after the session generation changes without automatically enqueueing", async () => {
    api.create.mockRejectedValueOnce(new Error("response outcome unknown"));
    await mount();
    await act(async () => { await state.create(command()); });
    const original = state.pending;
    expect(original).not.toBeNull();
    const persisted = sessionStorage.getItem(commandKey);
    // 模拟刷新或同账号重登后的新会话；异步归属换代，持久化命令仍必须落在同一个身份与考试运行下。
    setBusinessCommandScope("", "");
    setBusinessCommandScope("tenant", "teacher");
    await mount({ scopeKey: "same-actor-new-session" });
    expect(state.pending).toEqual(original);
    expect(state.questionId).toBe(original!.question_id);
    expect(state.trial).toBeNull();
    expect(sessionStorage.getItem(commandKey)).toBe(persisted);
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it.each(["actor", "tenant", "exam", "run"] as const)("does not restore another %s scope's pending trial command", async changedScope => {
    const original = command("previous-scope-command");
    sessionStorage.setItem(commandKey, JSON.stringify(original));
    if (changedScope === "actor") setBusinessCommandScope("tenant", "another-teacher");
    if (changedScope === "tenant") setBusinessCommandScope("another-tenant", "teacher");
    await mount({ scopeKey: `new-${changedScope}-scope`, ...(changedScope === "exam" ? { examId: "exam-2" } : {}), ...(changedScope === "run" ? { runId: "run-2" } : {}) });
    expect(state.pending).toBeNull();
    expect(state.trial).toBeNull();
    expect(JSON.parse(sessionStorage.getItem(commandKey)!)).toEqual(original);
    expect(api.create).not.toHaveBeenCalled();
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it("waits for every selected sample result even when the surrounding scoring run already has many scores", async () => {
    const completed = samples.map(sample => ({ ...sample, result: { status: "succeeded", suggested_score: 3, evidence: [], risk_flags: [] } }));
    const running = trial({ status: "running", samples: completed.map((sample, index) => index === 4 ? { ...sample, result: { status: "processing", evidence: [], risk_flags: [] } } : sample) });
    api.run.mockResolvedValue({ items: Array.from({ length: 100 }, (_, index) => ({ answer_segment_id: `segment-${index + 1}`, question_id: "question-1", question_type: "essay", state: "review", score: 5 })) });
    api.list.mockResolvedValueOnce({ trials: [running] }).mockResolvedValue({ trials: [trial({ status: "reviewing", samples: completed })] });
    await mount();
    expect(state.items).toHaveLength(100);
    expect(trialSamplesFinished(state.trial!.samples)).toBe(false);
    await act(async () => { await state.refresh(); });
    expect(trialSamplesFinished(state.trial!.samples)).toBe(true);
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it("retains an accepted trial after enqueue fails and resumes its original server-linked batch", async () => {
    const accepted = trial();
    const running = trial({ status: "running", revision: 2 });
    api.list.mockResolvedValueOnce({ trials: [] }).mockResolvedValue({ trials: [running] });
    api.enqueue.mockRejectedValueOnce(new Error("enqueue response lost"))
      .mockResolvedValueOnce({ batch: { id: accepted.sample_batch_id }, enqueue_result: { failed_count: 0 } });
    await mount();
    await act(async () => { await state.create(command()); });
    expect(state.trial?.id).toBe(accepted.id);
    expect(state.trial?.sample_batch_id).toBe(accepted.sample_batch_id);
    expect(state.pending).toBeNull();
    expect(state.error).toContain("enqueue response lost");
    expect(state.busy).toBe(false);
    await act(async () => { await state.resume(state.trial!); });
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(api.enqueue.mock.calls).toEqual([["run-1", accepted.id], ["run-1", accepted.id]]);
    expect(state.trial?.status).toBe("running");
  });

  it("does not enqueue a late create response after the login session changes before a rerender", async () => {
    const response = deferred<{ trial: ScoringTrial }>();
    api.create.mockReturnValueOnce(response.promise);
    await mount();
    let pending!: Promise<void>;
    await act(async () => { pending = state.create(command()); });
    const persisted = JSON.parse(sessionStorage.getItem(commandKey)!);
    setBusinessCommandScope("tenant", "another-teacher");
    await act(async () => { response.resolve({ trial: trial() }); await pending; });
    expect(api.enqueue).not.toHaveBeenCalled();
    expect(state.trial).toBeNull();
    expect(JSON.parse(sessionStorage.getItem(commandKey)!)).toEqual(persisted);
  });

  it.each(["teacher", "tenant", "exam", "session", "permission", "run"] as const)("does not save or enqueue a late create response after the %s scope changes", async changedScope => {
    const response = deferred<{ trial: ScoringTrial }>();
    api.create.mockReturnValueOnce(response.promise);
    await mount();
    let creating!: Promise<void>;
    await act(async () => { creating = state.create(command()); });
    const persisted = sessionStorage.getItem(commandKey);
    if (changedScope === "teacher") setBusinessCommandScope("tenant", "another-teacher");
    if (changedScope === "tenant") setBusinessCommandScope("another-tenant", "teacher");
    if (changedScope === "session") { setBusinessCommandScope("", ""); setBusinessCommandScope("tenant", "teacher"); }
    await mount({ scopeKey: `scope-${changedScope}`, ...(changedScope === "exam" ? { examId: "exam-2" } : {}), ...(changedScope === "run" ? { runId: "run-2" } : {}) });
    await act(async () => { response.resolve({ trial: trial() }); await creating; });
    expect(state.trial).toBeNull();
    expect(api.enqueue).not.toHaveBeenCalled();
    expect(sessionStorage.getItem(commandKey)).toBe(persisted);
  });

  it("does not enqueue a late expansion decision after a live session switch", async () => {
    const reviewing = trial({ status: "reviewing", revision: 5 });
    const response = deferred<{ trial: ScoringTrial }>();
    api.list.mockResolvedValue({ trials: [reviewing] });
    api.decide.mockReturnValueOnce(response.promise);
    await mount();
    let deciding!: Promise<void>;
    await act(async () => { deciding = state.decide(reviewing, "expand", "已核对"); });
    expect(api.decide).toHaveBeenCalledExactlyOnceWith("run-1", reviewing.id, { decision: "expand", reason: "已核对", expected_revision: 5 });
    setBusinessCommandScope("tenant", "another-teacher");
    await act(async () => { response.resolve({ trial: trial({ status: "expanded", revision: 6, expansion_batch_id: "expansion-batch" }) }); await deciding; });
    expect(state.trial?.status).toBe("reviewing");
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it("keeps the accepted expansion if enqueue fails and resumes its approved trial resource", async () => {
    const reviewing = trial({ status: "reviewing", revision: 5 });
    const expanded = trial({ status: "expanded", revision: 6, expansion_batch_id: "approved-batch" });
    api.list.mockResolvedValueOnce({ trials: [reviewing] }).mockResolvedValue({ trials: [expanded] });
    api.decide.mockResolvedValue({ trial: expanded });
    api.enqueue.mockRejectedValueOnce(new Error("expansion enqueue failed"))
      .mockResolvedValueOnce({ enqueue_result: { failed_count: 0 } });
    await mount();
    await act(async () => { await state.decide(reviewing, "expand", "已核对"); });
    expect(state.trial).toEqual(expanded);
    expect(state.error).toContain("expansion enqueue failed");
    await act(async () => { await state.resume(state.trial!); });
    expect(api.decide).toHaveBeenCalledTimes(1);
    expect(api.create).not.toHaveBeenCalled();
    expect(api.enqueue.mock.calls).toEqual([["run-1", reviewing.id], ["run-1", reviewing.id]]);
  });

  it("does not send create when the original command cannot be durably stored", async () => {
    await mount();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("storage quota exceeded"); });
    await act(async () => { await state.create(command()); });
    expect(api.create).not.toHaveBeenCalled();
    expect(api.enqueue).not.toHaveBeenCalled();
    expect(state.error).toContain("storage quota exceeded");
    expect(state.busy).toBe(false);
  });

  it("does not import a delayed initial trial list after the login session changes without a rerender", async () => {
    const response = deferred<{ trials: ScoringTrial[] }>();
    api.list.mockReturnValueOnce(response.promise);
    await mount();
    // 会话 Cookie 先变化，列表回执后到；旧列表不能触发重绘后重新激活旧试改入口。
    setBusinessCommandScope("tenant", "another-teacher");
    await act(async () => { response.resolve({ trials: [trial()] }); });
    expect(state.trial).toBeNull();
    expect(state.trials).toEqual([]);
    expect(state.items).toEqual([]);
    expect(api.snapshot).not.toHaveBeenCalled();
  });

  it("does not import delayed refresh results after a session switch before React receives new props", async () => {
    const original = trial({ status: "stopped" });
    api.list.mockResolvedValueOnce({ trials: [original] });
    await mount();
    const response = deferred<{ trials: ScoringTrial[] }>();
    api.list.mockReturnValueOnce(response.promise);
    let refreshing!: Promise<void>;
    await act(async () => { refreshing = state.refresh(); });
    setBusinessCommandScope("tenant", "another-teacher");
    await act(async () => { response.resolve({ trials: [trial({ status: "expanded" })] }); await refreshing; });
    expect(state.trial?.status).toBe("stopped");
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it("keeps the accepted trial and continues enqueue when local command cleanup throws", async () => {
    const accepted = trial();
    api.list.mockResolvedValueOnce({ trials: [] }).mockResolvedValue({ trials: [accepted] });
    await mount();
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("storage unavailable"); });
    await act(async () => { await state.create(command()); });
    expect(state.trial?.id).toBe(accepted.id);
    expect(state.pending).toBeNull();
    expect(state.error).toBe("");
    expect(api.enqueue).toHaveBeenCalledExactlyOnceWith("run-1", accepted.id);
  });

  it("restores the exact pending command from server history even if a different trial is listed first and cleanup fails", async () => {
    const pending = command("accepted-command");
    const accepted = trial({ id: "accepted-trial", command_id: pending.command_id, status: "reviewing" });
    sessionStorage.setItem(commandKey, JSON.stringify(pending));
    api.list.mockResolvedValue({ trials: [trial({ id: "other-trial", command_id: "other-command" }), accepted] });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("cleanup unavailable"); });
    await mount();
    expect(state.trial).toEqual(accepted);
    expect(state.pending).toBeNull();
    expect(state.error).toBe("");
    expect(api.create).not.toHaveBeenCalled();
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it.each([400, 422])("an explicit HTTP %s input rejection unlocks editing and does not enqueue", async status => {
    api.create.mockRejectedValueOnce(new ApiClientError(status, "trial_input_invalid", "人工基准不合法"));
    await mount();
    await act(async () => { await state.create(command()); });
    expect(state.pending).toBeNull();
    expect(sessionStorage.getItem(commandKey)).toBeNull();
    expect(state.trial).toBeNull();
    expect(state.error).toContain("人工基准不合法");
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it.each([409, 500, 503])("an uncertain HTTP %s response keeps the original command and frozen samples for retry", async status => {
    api.create.mockRejectedValueOnce(new ApiClientError(status, "trial_outcome_unknown", "结果待确认"));
    await mount();
    await act(async () => { await state.create(command()); });
    const pending = state.pending;
    expect(pending).not.toBeNull();
    expect(JSON.parse(sessionStorage.getItem(commandKey)!)).toEqual(pending);
    await act(async () => { await state.create({ question_id: "new-question", samples: [] }); });
    expect(api.create.mock.calls).toEqual([["run-1", pending], ["run-1", pending]]);
    expect(api.enqueue).toHaveBeenCalledExactlyOnceWith("run-1", "trial-1");
  });

  it("does not turn formal run scores or older suggestions into this trial's sample results", async () => {
    api.run.mockResolvedValue({ items: samples.map(sample => ({ answer_segment_id: sample.segment_id, question_id: "question-1", question_type: "essay", state: "review", score: 5, grade_source: "human_confirmed" })) });
    api.list.mockResolvedValue({ trials: [trial({ samples: samples.map(sample => ({ ...sample, result: undefined })) })] });
    await mount();
    expect(state.trial?.samples.every(sample => sample.human_score === 2 && sample.result === undefined)).toBe(true);
    expect(state.trial?.samples.map(trialScoreDifference)).toEqual([null, null, null, null, null]);
    expect(trialSamplesFinished(state.trial!.samples)).toBe(false);
    expect(api.create).not.toHaveBeenCalled();
    expect(api.enqueue).not.toHaveBeenCalled();
  });
});
