// @vitest-environment jsdom
import { act, useEffect, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AiGrade, ReviewTask } from "../../../../api/review";
import type { RubricPoint } from "../../../../api/papers";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import { createReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { createInitialDraft } from "../gradingWorkbench.model";
import type { ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";
import type { MathWorkbenchEvidence } from "../mathWorkbenchEvidence";
import { useGradingQueue } from "./useGradingQueue";
import { useGradingReviewActions, type UseGradingReviewActionsOptions } from "./useGradingReviewActions";

const api = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), graders: vi.fn(), refreshMath: vi.fn(), remember: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock("antd", () => ({ Modal: { confirm: vi.fn() } }));
vi.mock("../../../../api/review", () => ({ listReviewTasks: api.list, getReviewTask: api.get, submitHumanGrade: vi.fn(), returnReviewTask: vi.fn() }));
vi.mock("../../../../api/users", () => ({ listAllActiveGraders: api.graders }));

function task(overrides: Partial<ReviewTask> = {}): ReviewTask {
  return { id: "listed", exam_id: "exam", exam_name: "期中", question_id: "question", question_no: "1", subject: "math", status: "pending", source: "manual_sample", assigned_to: "teacher", ...overrides } as ReviewTask;
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function suggestion(overrides: Partial<AiGrade> = {}): AiGrade {
  return { suggested_score: 9, max_score: 10, rubric_version: "v1", matched_points: [{ code: "p1", label: "步骤", score: 9 }], missing_points: [],
    answer_segment_id: "segment", math_artifact_id: "artifact", math_artifact_version: 2, math_correction_revision: 0, math_scoring_version: "math-rubric-score-v1",
    status: "succeeded", delivery_mode: "teacher_suggestion", needs_human_review: true, mock: false, ...overrides } as AiGrade;
}
function evidence(): MathWorkbenchEvidence {
  return { segmentId: "segment", phase: "ready", message: "", understanding: { correction_revision: 0, artifact: { id: "artifact", version: 2, correction_revision: 0, exam_question_snapshot_id: "snapshot" } },
    score: { schema_version: "math-rubric-score-v1", scope: "teacher_suggestion_only", artifact_id: "artifact", artifact_version: 2,
      correction_revision: 0, verified_correction_revision: 0, exam_question_snapshot_id: "snapshot", rubric_version: "v1", suggested_score: 9, max_score: 10, unresolved_score: 0 } } as unknown as MathWorkbenchEvidence;
}

let root: Root;
beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  window.location.hash = "";
  setBusinessCommandScope("tenant", "teacher");
  api.list.mockResolvedValue({ tasks: [task()], has_more: false });
  api.get.mockResolvedValue({ task: task({ id: "deep" }) });
  api.graders.mockResolvedValue([]);
  api.refreshMath.mockReset().mockResolvedValue(evidence());
  root = createRoot(document.createElement("div"));
});
afterEach(async () => { await act(async () => { root.unmount(); }); });

describe("第30批队列标签与深链范围", () => {
  let queue: ReturnType<typeof useGradingQueue>;
  async function mount(options: Partial<Parameters<typeof useGradingQueue>[0]> = {}) {
    function Probe() {
      const currentQueue = useGradingQueue({ canManageTasks: true, canWork: true, currentUserId: "teacher", initialExamId: "", personalScope: false, onInfo: api.info, ...options });
      queue = currentQueue;
      const { loadTasks } = currentQueue;
      useEffect(() => { void loadTasks(); }, [loadTasks]);
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("考试名和题目前缀保持原回退文本，固定考试后省略前缀", async () => {
    api.list.mockResolvedValue({ tasks: [task(), task({ id: "fallback", exam_id: "fallback-exam", exam_name: "", subject: undefined, question_id: "q2", question_no: "2" })], has_more: false });
    await mount();
    expect(queue.examOptions.map((option) => option.label)).toEqual(["期中 · 数学", "考试 fallback · 学科未标注"]);
    expect(queue.questionOptions.map((option) => option.label)).toEqual(["期中 · 第 1 题", "fallback · 第 2 题"]);
    await act(async () => { queue.setExamFilter("exam"); });
    expect(queue.questionOptions.map((option) => option.label)).toEqual(["第 1 题"]);
  });

  it.each(["exam", "personal"] as const)("深链任务不属于%s范围时保留原拒绝结果", async (scope) => {
    window.location.hash = "#/grading?task=deep";
    api.get.mockResolvedValue({ task: scope === "exam" ? task({ id: "deep", exam_id: "another-exam" }) : task({ id: "deep", assigned_to: "another-teacher" }) });
    await mount(scope === "exam" ? { initialExamId: "exam" } : { personalScope: true });
    expect(api.get).toHaveBeenCalledWith("deep");
    expect(queue.tasks).toEqual([]);
    expect(queue.taskError).toContain("此任务不在当前考试或已转派");
  });

  it("深链补读等待时换筛选，旧范围任务不能触发错误或覆盖新队列", async () => {
    const old = deferred<{ task: ReviewTask }>();
    window.location.hash = "#/grading?task=deep";
    api.get.mockReturnValueOnce(old.promise);
    api.list.mockImplementation(async (filter) => ({ tasks: [task({ id: filter.subject ? "deep" : "listed" })], has_more: false }));
    await mount({ initialExamId: "exam" });
    expect(api.get).toHaveBeenCalledTimes(1);
    // 先让新筛选确认深链任务，再返回原请求的越界任务；代次 guard 必须早于范围断言。
    await act(async () => { queue.setSubjectFilter("math"); });
    await act(async () => { old.resolve({ task: task({ id: "deep", exam_id: "another-exam" }) }); });
    expect(queue.tasks.map((item) => item.id)).toEqual(["deep"]);
    expect(queue.taskError).toBeNull();
  });
});

describe("第30批AI填稿与数学核对顺序", () => {
  let actions: ReturnType<typeof useGradingReviewActions>;
  let current: { draft: ScoreDraft; requesting: boolean; locked: boolean };
  let editDraft: (patch: Partial<ScoreDraft>) => void;
  async function mount(mode: "none" | "matched" | "unmatched" = "none", subjectCode = "chinese") {
    const queryClient = new QueryClient();
    const queryScope = createReviewTaskContextScope("exam");
    const point = { id: "p1", description: "步骤", score: 10, required: true, evidence_requirements: [] } satisfies RubricPoint;
    const ctx = { task: task({ id: "A" }), question: { rubric: { version: "v1" } } } as unknown as WorkbenchContext;
    function Probe() {
      const [draft, setDraft] = useState<ScoreDraft>(() => ({ ...createInitialDraft(null), score: 6, reason: "教师原说明", rubricSelections: { old: 2 }, studentFeedback: "原反馈", privateNote: "原备注" }));
      const [requesting, setMathRequesting] = useState(false);
      const liveDraft = useRef(draft); liveDraft.current = draft;
      const mathActionLock = useRef(false);
      const activeTask = useRef("A");
      const activeUserId = useRef("teacher");
      actions = useGradingReviewActions({ queryScope, isCurrentScope: () => true, ctx, draft, setDraft, currentUserId: "teacher", selectedTaskId: "A", activeTask, activeUserId,
        canEditDraft: true, canAdoptAiScore: true, selectedGrade: suggestion({ rubric_version: mode === "unmatched" ? "v2" : "v1" }), subjectCode,
        rubricPoints: mode === "none" ? [] : [point], math: { refresh: api.refreshMath, dirty: false }, mathActionLock, liveDraft, setMathRequesting,
        scoreShortcuts: { rememberCurrent: api.remember }, notify: { info: api.info, warning: api.warning, error: vi.fn() } } as unknown as UseGradingReviewActionsOptions);
      current = { draft, requesting, locked: mathActionLock.current };
      editDraft = (patch) => setDraft((value) => ({ ...value, ...patch }));
      return null;
    }
    await act(async () => { root.render(<QueryClientProvider client={queryClient}><Probe /></QueryClientProvider>); });
  }

  it.each(["none", "matched", "unmatched"] as const)("%s评分点时保留原reason与selection覆盖边界，普通学科同步填稿", async (mode) => {
    await mount(mode);
    let pending!: Promise<void>;
    act(() => { pending = actions.adoptAiScore(); expect(api.remember).toHaveBeenCalledTimes(1); });
    await act(async () => { await pending; });
    expect(api.refreshMath).not.toHaveBeenCalled();
    expect(current.draft.score).toBe(9);
    expect(current.draft.studentFeedback).toBe("原反馈");
    expect(current.draft.privateNote).toBe("原备注");
    expect(current.draft.rubricSelections).toEqual(mode === "matched" ? { p1: 9 } : { old: 2 });
    if (mode === "matched") expect(current.draft.reason).toBe("");
    else if (mode === "none") expect(current.draft.reason).toBe("教师原说明");
    else expect(current.draft.reason).toContain("未自动匹配冻结评分点");
  });

  it("数学刷新等待时人工评分变化，旧建议不覆盖新稿并结束等待", async () => {
    const reply = deferred<MathWorkbenchEvidence>();
    api.refreshMath.mockReturnValueOnce(reply.promise);
    await mount("none", "mathematics");
    let pending!: Promise<void>;
    await act(async () => { pending = actions.adoptAiScore(); });
    expect(current.requesting).toBe(true);
    await act(async () => { editDraft({ score: 7 }); });
    await act(async () => { reply.resolve(evidence()); await pending; });
    expect(current.draft.score).toBe(7);
    expect(api.remember).not.toHaveBeenCalled();
    expect(api.warning).toHaveBeenCalledWith("评分草稿已变化，请再次确认后采纳。");
    expect(current.requesting).toBe(false);
    expect(current.locked).toBe(false);
  });

  it("数学版本和草稿均未变化时采用评分点，并结束等待", async () => {
    await mount("matched", "mathematics");
    await act(async () => { await actions.adoptAiScore(); });
    expect(api.refreshMath).toHaveBeenCalledTimes(1);
    expect(api.remember).toHaveBeenCalledTimes(1);
    expect(current.draft.score).toBe(9);
    expect(current.draft.rubricSelections).toEqual({ p1: 9 });
    expect(current.draft.reason).toBe("");
    expect(current.draft.studentFeedback).toBe("原反馈");
    expect(current.draft.privateNote).toBe("原备注");
    expect(api.warning).not.toHaveBeenCalled();
    expect(current.requesting).toBe(false);
    expect(current.locked).toBe(false);
  });

  it("数学核对同轮重入只发一次刷新，证据失效后解锁且不填分", async () => {
    const reply = deferred<MathWorkbenchEvidence>();
    api.refreshMath.mockReturnValueOnce(reply.promise);
    await mount("none", "mathematics");
    let pending!: Promise<void>; let repeated!: Promise<void>;
    // 第二次调用仍持有重绘前的回调；同步锁不能被单独提取成异步请求后才设置。
    await act(async () => { pending = actions.adoptAiScore(); repeated = actions.adoptAiScore(); await repeated; });
    expect(api.refreshMath).toHaveBeenCalledTimes(1);
    await act(async () => { reply.resolve({ ...evidence(), phase: "conflict" }); await pending; });
    expect(current.draft.score).toBe(6);
    expect(api.remember).not.toHaveBeenCalled();
    expect(api.warning).toHaveBeenCalledWith("数学证据或图片已更新，旧建议不可采纳。");
    expect(current.requesting).toBe(false);
    expect(current.locked).toBe(false);
  });
});
