// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import { act, createElement, useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { useGradingReviewActions, type UseGradingReviewActionsOptions } from "../../apps/web-admin/src/features/grading/workbench/hooks/useGradingReviewActions";
import { createInitialDraft } from "../../apps/web-admin/src/features/grading/workbench/gradingWorkbench.model";

const mocks = vi.hoisted(() => ({ submitHumanGrade: vi.fn(), removeQueries: vi.fn() }));
vi.mock("antd", () => ({ Modal: { confirm: vi.fn() } }));
vi.mock("../../apps/web-admin/src/api/review", () => ({
  submitHumanGrade: mocks.submitHumanGrade, returnReviewTask: vi.fn()
}));
vi.mock("../../apps/web-admin/src/query/client", () => ({ appQueryClient: { removeQueries: mocks.removeQueries } }));
vi.mock("../../apps/web-admin/src/query/reviewTaskContext", () => ({ reviewTaskContextKeys: { detail: (id: string) => [id] } }));
vi.mock("../../apps/web-admin/src/auth/reviewDraftFallback", () => ({
  removeReviewDraftFallback: vi.fn(), loadReviewDraftFallback: vi.fn(), saveReviewDraftFallback: vi.fn()
}));
vi.mock("../../apps/web-admin/src/features/grading/workbench/gradingTaskContext", () => ({ loadTaskContext: vi.fn() }));

describe("repository review: late grading submit", () => {
  it("reproduces task A response replacing the current task C and clearing C draft", async () => {
    let resolveSubmit!: (value: unknown) => void;
    mocks.submitHumanGrade.mockReturnValueOnce(new Promise((resolve) => { resolveSubmit = resolve; }));
    const contextA = { task: { id: "A" }, reviewContext: { expected_revision: 3 } };
    const draftA = { ...createInitialDraft(null), score: 6, comments: "A feedback" };
    const state = { selectedTaskId: "A", ctx: contextA as unknown, draft: draftA };
    const options = {
      ctx: contextA, draft: draftA, canSubmit: true, maxScore: 10,
      rubricPoints: [], nextTask: { id: "B" }, currentUserId: "teacher", hasMoreTasks: false,
      canManageTasks: false, activeTask: { current: "A" }, activeUserId: { current: "teacher" },
      contextRequestRef: { current: 1 }, lastSavedDraft: { current: "A snapshot" },
      setCtx: (next: unknown) => { state.ctx = next; },
      setSelectedTaskId: (next: string) => { state.selectedTaskId = next; },
      setDraft: (next: typeof draftA) => { state.draft = next; },
      setTasks: vi.fn(), setDraftHydrated: vi.fn(), setDraftSaveStatus: vi.fn(),
      invalidateViewerContent: vi.fn(), scoreShortcuts: { reset: vi.fn() },
      refreshTaskAggregate: vi.fn().mockResolvedValue(undefined),
      examScoring: { loadSummary: vi.fn().mockResolvedValue(undefined) },
      runAction: async (_key: string, action: () => Promise<void>) => { await action(); },
      notify: { error: vi.fn(), info: vi.fn() }
    } as unknown as UseGradingReviewActionsOptions;
    const submission = useGradingReviewActions(options).submitGrade();
    expect(mocks.submitHumanGrade).toHaveBeenCalledWith("A", expect.objectContaining({ score: 6 }));

    // TaskRail allows selecting another task while actioning === "submit".
    state.selectedTaskId = "C";
    state.ctx = { task: { id: "C" } };
    state.draft = { ...createInitialDraft(null), score: 9, comments: "C unsent work" };
    options.activeTask.current = "C";
    options.contextRequestRef.current += 1;

    resolveSubmit({ human_grade: { id: "grade-A" } });
    await submission;

    // These assertions capture observed buggy behavior, not desired behavior.
    expect(state.selectedTaskId).toBe("B");
    expect(state.ctx).toBeNull();
    expect(state.draft.score).toBeNull();
    expect(state.draft.comments).toBe("");
    expect(options.activeTask.current).toBe("C");
    expect(options.contextRequestRef.current).toBe(3);
    console.log("REPRODUCED: late submit A changed selected task C -> B and reset C score 9 -> null / comments -> empty.");
  });

  it("keeps B selected but clears its loaded context when late A submission also chooses B", async () => {
    (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    let resolveSubmit!: (value: unknown) => void;
    mocks.submitHumanGrade.mockReturnValueOnce(new Promise((resolve) => { resolveSubmit = resolve; }));
    const contextLoads = vi.fn();
    let current!: { selectedTaskId: string; ctx: unknown; draft: ReturnType<typeof createInitialDraft> };
    let selectTask!: (taskId: string) => void;
    let submitGrade!: () => Promise<void>;
    function Harness() {
      const [selectedTaskId, setSelectedTaskId] = useState("A");
      const [ctx, setCtx] = useState<unknown>(null);
      const [draft, setDraft] = useState(createInitialDraft(null));
      const activeTask = useRef(selectedTaskId);
      activeTask.current = selectedTaskId;
      const contextRequestRef = useRef(0);
      const lastSavedDraft = useRef("");
      // Minimal React lifecycle harness: mirrors the stable loadContext callback
      // and [loadContext, selectedTaskId] effect at GradingWorkbench.tsx:341-343.
      // It intentionally does not mount the full workbench or fetch server data.
      const loadContext = useCallback((taskId: string) => {
        contextLoads(taskId);
        contextRequestRef.current += 1;
        setCtx({ task: { id: taskId }, reviewContext: { expected_revision: 3 } });
        setDraft({ ...createInitialDraft(null), score: 6, comments: `${taskId} feedback` });
      }, []);
      useEffect(() => { loadContext(selectedTaskId); }, [loadContext, selectedTaskId]);
      const options = {
        ctx, setCtx, draft, setDraft, canSubmit: true, maxScore: 10,
        rubricPoints: [], nextTask: { id: "B" }, currentUserId: "teacher", hasMoreTasks: false,
        canManageTasks: false, activeTask, activeUserId: { current: "teacher" },
        contextRequestRef, lastSavedDraft, setSelectedTaskId,
        setTasks: vi.fn(), setDraftHydrated: vi.fn(), setDraftSaveStatus: vi.fn(),
        invalidateViewerContent: vi.fn(), scoreShortcuts: { reset: vi.fn() },
        refreshTaskAggregate: vi.fn().mockResolvedValue(undefined),
        examScoring: { loadSummary: vi.fn().mockResolvedValue(undefined) },
        runAction: async (_key: string, action: () => Promise<void>) => { await action(); },
        notify: { error: vi.fn(), info: vi.fn() }
      } as unknown as UseGradingReviewActionsOptions;
      submitGrade = useGradingReviewActions(options).submitGrade;
      selectTask = setSelectedTaskId;
      current = { selectedTaskId, ctx, draft };
      return null;
    }
    const root = createRoot(document.createElement("div"));
    try {
      await act(async () => { root.render(createElement(Harness)); });
      const pendingSubmit = submitGrade();
      await act(async () => { selectTask("B"); });
      expect(current.ctx).toEqual(expect.objectContaining({ task: { id: "B" } }));
      await act(async () => {
        resolveSubmit({ human_grade: { id: "grade-A" } });
        await pendingSubmit;
      });
      expect(current.selectedTaskId).toBe("B");
      expect(current.ctx).toBeNull();
      expect(current.draft.score).toBeNull();
      expect(contextLoads.mock.calls.map(([id]) => id)).toEqual(["A", "B"]);
      await act(async () => { selectTask("B"); });
      expect(current.ctx).toBeNull();
      expect(contextLoads).toHaveBeenCalledTimes(2);
      console.log("REPRODUCED: late submit A clears loaded B; selection remains B, so the selected-task effect does not reload context, even when B is clicked again.");
    } finally {
      await act(async () => { root.unmount(); });
    }
  });
});
