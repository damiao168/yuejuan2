// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../../api/client";
import type { ReviewTask } from "../../../../api/review";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { useGradingQueueActions } from "./useGradingQueueActions";

const api = vi.hoisted(() => ({ assign: vi.fn(), batch: vi.fn(), claim: vi.fn(), release: vi.fn() }));
vi.mock("../../../../api/review", () => ({ assignReviewTask: api.assign, batchAssignReviewTasks: api.batch, claimNextReviewTask: api.claim, releaseReviewTask: api.release }));

type Options = Parameters<typeof useGradingQueueActions>[0];
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (cause: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
function task(id: string, examId = "exam-A", status = "assigned"): ReviewTask {
  return { id, exam_id: examId, question_id: `question-${id}`, revision: 3, status } as ReviewTask;
}

function fixture(examId = "exam-A", id = "A") {
  let current = true;
  const original = task(id, examId);
  const state = { tasks: [original], ctx: { task: original } as WorkbenchContext, selected: id, action: null as string | null };
  const options: Options = {
    isCurrentScope: () => current,
    canManageTasks: true, initialExamId: examId, ctx: state.ctx, filteredTasks: state.tasks,
    assignableTasks: state.tasks, assignmentTaskIds: [id], assignmentUserId: "grader", graderNames: { grader: "教师甲" },
    hasMoreTasks: false, pageError: null, selectedTaskId: id, ownsSelectedTask: true,
    pendingNextRef: { current: null }, suppressAutoSelectRef: { current: false },
    setTasks: vi.fn((next) => { state.tasks = typeof next === "function" ? next(state.tasks) : next; }),
    setCtx: vi.fn((next) => { state.ctx = (typeof next === "function" ? next(state.ctx) : next) as WorkbenchContext; }),
    setAssignmentTaskIds: vi.fn(), setSelectedTaskId: vi.fn((next) => { state.selected = typeof next === "function" ? next(state.selected) : next; }),
    setTaskFilter: vi.fn(), setActioning: vi.fn((value) => { state.action = value; }), setCalibrationQuestionId: vi.fn(),
    loadTasks: vi.fn(async () => undefined), loadMoreTasks: vi.fn(async () => []), refreshTaskAggregate: vi.fn(async () => undefined),
    runAction: vi.fn(async (key, action, successText) => {
      if (!current) return;
      options.setActioning(key);
      try { await action(); if (current) options.notify.success(successText); }
      finally { if (current) options.setActioning(null); }
    }),
    notify: { info: vi.fn(), warning: vi.fn(), success: vi.fn(), error: vi.fn() }
  };
  return { options, state, invalidate: () => { current = false; } };
}

describe("workbench queue action scope and request ownership (mock API)", () => {
  let root: Root;
  let actions: ReturnType<typeof useGradingQueueActions>;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    root = createRoot(document.createElement("div"));
    api.assign.mockResolvedValue({ task: task("A", "exam-A", "in_progress") });
    api.batch.mockResolvedValue({ tasks: [task("A", "exam-A", "in_progress"), task("B", "exam-A", "in_progress")] });
    api.claim.mockResolvedValue({ task: task("A", "exam-A", "in_progress") });
    api.release.mockResolvedValue({ task: task("A", "exam-A", "assigned") });
  });
  afterEach(async () => { await act(async () => root.unmount()); });
  async function render(options: Options) {
    // 改变范围时复用组件类型，保留旧请求和新请求共用的 hook ref。
    await act(async () => root.render(<StableProbe options={options} publish={(value) => { actions = value; }} />));
  }

  it("keeps the original assignment revision and filters unavailable selection IDs", async () => {
    const current = fixture();
    current.options.assignmentTaskIds = ["A", "missing"];
    await render(current.options);
    await actions.assignSelectedTasks();
    expect(api.assign).toHaveBeenCalledWith("A", "grader", 3);
    expect(current.state.ctx.task.status).toBe("in_progress");
    expect(current.options.loadTasks).toHaveBeenCalledTimes(1);
    expect(current.options.notify.success).toHaveBeenCalledWith("已将 1 份任务分配给 教师甲");
    expect(current.state.action).toBeNull();
  });

  it("uses the existing batch API for multiple selected tasks", async () => {
    const current = fixture();
    current.options.assignableTasks = [task("A"), task("B")];
    current.options.assignmentTaskIds = ["A", "B"];
    await render(current.options);
    await actions.assignSelectedTasks();
    expect(api.assign).not.toHaveBeenCalled();
    expect(api.batch).toHaveBeenCalledWith(current.options.assignableTasks, "grader");
    expect(current.options.notify.success).toHaveBeenCalledWith("已将 2 份任务分配给 教师甲");
  });

  it.each(["assignSelectedTasks", "claimTask", "releaseCurrentTask"] as const)("does not apply %s after a scope change", async (name) => {
    const response = deferred<{ task: ReviewTask }>();
    ({ assignSelectedTasks: api.assign, claimTask: api.claim, releaseCurrentTask: api.release })[name].mockReturnValue(response.promise);
    const old = fixture();
    await render(old.options);
    const pending = actions[name]();
    old.invalidate();
    const current = fixture("exam-B", "B");
    await render(current.options);
    response.resolve({ task: task("A", "exam-A", "in_progress") });
    await pending;
    expect(old.options.setTasks).not.toHaveBeenCalled();
    expect(old.options.setSelectedTaskId).not.toHaveBeenCalled();
    expect(old.options.setAssignmentTaskIds).not.toHaveBeenCalled();
    expect(old.options.loadTasks).not.toHaveBeenCalled();
    expect(old.options.refreshTaskAggregate).not.toHaveBeenCalled();
    expect(old.options.notify.success).not.toHaveBeenCalled();
    expect(old.options.setActioning).toHaveBeenCalledTimes(1);
    expect(current.state.selected).toBe("B");
    expect(current.state.tasks.map((item) => item.id)).toEqual(["B"]);
  });

  it("does not open an old qualification drawer after a scope change", async () => {
    const response = deferred<{ task: ReviewTask }>();
    api.claim.mockReturnValue(response.promise);
    const old = fixture();
    await render(old.options);
    const pending = actions.claimTask();
    old.invalidate();
    await render(fixture("exam-B", "B").options);
    response.reject(new ApiClientError(409, "grader_qualification_required", "qualification"));
    await pending;
    expect(old.options.setCalibrationQuestionId).not.toHaveBeenCalled();
    expect(old.options.notify.warning).not.toHaveBeenCalled();
    expect(old.options.notify.error).not.toHaveBeenCalled();
    expect(old.options.setActioning).toHaveBeenCalledTimes(1);
  });

  it("opens the current task's qualification drawer when required", async () => {
    api.claim.mockRejectedValue(new ApiClientError(409, "grader_qualification_required", "qualification"));
    const current = fixture();
    await render(current.options);
    await actions.claimTask();
    expect(current.options.setCalibrationQuestionId).toHaveBeenCalledWith("question-A");
    expect(current.options.notify.warning).toHaveBeenCalledWith("该题属于高风险阅卷，请先完成本题校准");
    expect(current.state.action).toBeNull();
  });

  it("deduplicates same-frame queue mutations and lets the new scope keep its own lock", async () => {
    const oldResponse = deferred<{ task: ReviewTask }>();
    const newResponse = deferred<{ task: ReviewTask }>();
    api.claim.mockReturnValueOnce(oldResponse.promise).mockReturnValueOnce(newResponse.promise);
    const old = fixture();
    await render(old.options);
    const oldPending = actions.claimTask();
    await actions.assignSelectedTasks();
    await actions.claimTask();
    expect(api.claim).toHaveBeenCalledTimes(1);
    expect(api.assign).not.toHaveBeenCalled();
    old.invalidate();
    const current = fixture("exam-B", "B");
    await render(current.options);
    const currentPending = actions.claimTask();
    oldResponse.resolve({ task: task("A") });
    await oldPending;
    expect(current.state.action).toBe("claim-task");
    await actions.claimTask();
    expect(api.claim).toHaveBeenCalledTimes(2);
    newResponse.resolve({ task: task("B", "exam-B", "in_progress") });
    await currentPending;
    expect(current.state.selected).toBe("B");
    expect(current.state.action).toBeNull();
  });

  it("deduplicates same-frame release and keeps explicit empty selection after success", async () => {
    const response = deferred<{ task: ReviewTask }>();
    api.release.mockReturnValue(response.promise);
    const current = fixture();
    await render(current.options);
    const pending = actions.releaseCurrentTask();
    await actions.releaseCurrentTask();
    expect(api.release).toHaveBeenCalledTimes(1);
    response.resolve({ task: task("A") });
    await pending;
    expect(current.options.suppressAutoSelectRef.current).toBe(true);
    expect(current.state.selected).toBe("");
  });

  it("does not clear an unrelated busy action when runAction refuses release", async () => {
    const current = fixture();
    current.state.action = "evidence";
    current.options.runAction = vi.fn(async () => undefined);
    await render(current.options);
    await actions.releaseCurrentTask();
    expect(api.release).not.toHaveBeenCalled();
    expect(current.options.setActioning).not.toHaveBeenCalled();
    expect(current.state.action).toBe("evidence");
  });

  it("drops old success and busy cleanup when assignment refresh finishes in a new scope", async () => {
    const refresh = deferred<void>();
    const old = fixture();
    old.options.loadTasks = vi.fn(() => refresh.promise);
    await render(old.options);
    const pending = actions.assignSelectedTasks();
    await Promise.resolve();
    expect(old.options.loadTasks).toHaveBeenCalledTimes(1);
    old.invalidate();
    await render(fixture("exam-B", "B").options);
    refresh.resolve();
    await pending;
    expect(old.options.notify.success).not.toHaveBeenCalled();
    expect(old.options.setActioning).toHaveBeenCalledTimes(1);
  });

  it("keeps successful grader batches when a later assignment batch fails", async () => {
    const current = fixture();
    current.options.assignmentTaskIds = ["A", "B"];
    api.batch.mockReset().mockResolvedValueOnce({ tasks: [{ ...task("A"), assigned_to: "g1", revision: 4 }] }).mockRejectedValueOnce(new Error("qualification changed"));
    await render(current.options);
    const shouldClose = await actions.assignPlannedTasks([{ graderId: "g1", tasks: [task("A")] }, { graderId: "g2", tasks: [task("B")] }]);
    expect(shouldClose).toBe(true);
    expect(api.batch).toHaveBeenCalledWith([expect.objectContaining({ id: "A", revision: 3 })], "g1");
    expect(current.state.tasks[0].assigned_to).toBe("g1");
    const remaining = (current.options.setAssignmentTaskIds as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(remaining(["A", "B"])).toEqual(["B"]);
    expect(current.options.notify.error).toHaveBeenCalledWith(expect.stringContaining("已成功分配 1 份"));
    expect(current.options.notify.success).not.toHaveBeenCalled();
    expect(current.options.loadTasks).toHaveBeenCalledOnce();
  });

  it("stops a multi-grader plan before another batch after its scope changes", async () => {
    const pending = deferred<{ tasks: ReviewTask[] }>();
    api.batch.mockReset().mockReturnValueOnce(pending.promise);
    const current = fixture(); await render(current.options);
    const action = actions.assignPlannedTasks([{ graderId: "g1", tasks: [task("A")] }, { graderId: "g2", tasks: [task("B")] }]);
    current.invalidate(); pending.resolve({ tasks: [task("A")] }); await action;
    expect(api.batch).toHaveBeenCalledOnce();
    expect(current.options.setTasks).not.toHaveBeenCalled();
    expect(current.options.loadTasks).not.toHaveBeenCalled();
  });

  it("keeps pagination continuation and ignores stale navigation handlers", async () => {
    const current = fixture();
    current.options.hasMoreTasks = true;
    await render(current.options);
    const previous = actions;
    await actions.goNext();
    expect(current.options.pendingNextRef.current).toBe("A");
    expect(current.options.loadMoreTasks).toHaveBeenCalledTimes(1);
    current.invalidate();
    await previous.goNext();
    await previous.claimTask();
    await previous.assignSelectedTasks();
    await previous.releaseCurrentTask();
    expect(current.options.loadMoreTasks).toHaveBeenCalledTimes(1);
    expect(api.claim).not.toHaveBeenCalled();
    expect(api.assign).not.toHaveBeenCalled();
    expect(api.release).not.toHaveBeenCalled();
  });
});

function StableProbe({ options, publish }: Readonly<{ options: Options; publish: (actions: ReturnType<typeof useGradingQueueActions>) => void }>) {
  publish(useGradingQueueActions(options));
  return null;
}
