// @vitest-environment jsdom
import { act, useCallback, useEffect, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewTask } from "../../../../api/review";
import { createInitialDraft } from "../gradingWorkbench.model";
import type { PrefetchedTaskBundle, WorkbenchContext } from "../gradingWorkbench.types";
import { useGradingReviewActions, type UseGradingReviewActionsOptions } from "./useGradingReviewActions";

const mocks = vi.hoisted(() => ({
  submitHumanGrade: vi.fn(), removeQueries: vi.fn(), removeFallback: vi.fn(),
  refreshAggregate: vi.fn(), loadSummary: vi.fn(), invalidateViewer: vi.fn(),
  resetShortcuts: vi.fn(), nominate: vi.fn(), contextLoads: vi.fn(), loadMore: vi.fn()
}));
vi.mock("antd", () => ({ Modal: { confirm: vi.fn() } }));
vi.mock("../../../../api/review", () => ({ submitHumanGrade: mocks.submitHumanGrade, returnReviewTask: vi.fn() }));
vi.mock("../../../../query/client", () => ({ appQueryClient: { removeQueries: mocks.removeQueries } }));
vi.mock("../../../../auth/reviewDraftFallback", () => ({
  removeReviewDraftFallback: mocks.removeFallback, loadReviewDraftFallback: vi.fn(), saveReviewDraftFallback: vi.fn()
}));
vi.mock("../gradingTaskContext", () => ({ loadTaskContext: vi.fn() }));

function task(id: string): ReviewTask {
  return { id, status: "in_progress", assigned_to: "teacher" } as ReviewTask;
}

describe("grading review submission lifecycle", () => {
  let root: Root;
  let finishSubmit: (value: unknown) => void;
  let current: {
    selectedTaskId: string; ctx: WorkbenchContext | null;
    draft: ReturnType<typeof createInitialDraft>; tasks: ReviewTask[];
    hydrated: boolean; saveStatus: string;
  };
  let selectTask: (id: string) => void;
  let switchUser: (id: string) => void;
  let submitGrade: (nominate?: boolean) => Promise<void>;
  let refs: Pick<UseGradingReviewActionsOptions, "contextRequestRef" | "lastSavedDraft" | "prefetchedTaskRef" | "pendingNextRef" | "suppressAutoSelectRef">;

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.submitHumanGrade.mockImplementation(() => new Promise((resolve) => { finishSubmit = resolve; }));
    mocks.refreshAggregate.mockResolvedValue(undefined);
    mocks.loadSummary.mockResolvedValue(undefined);
    mocks.loadMore.mockResolvedValue([]);
    root = createRoot(document.createElement("div"));
  });

  afterEach(async () => { await act(async () => { root.unmount(); }); });

  async function mount({ nextTask = task("B"), hasMoreTasks = false }: { nextTask?: ReviewTask | null; hasMoreTasks?: boolean } = {}) {
    function Harness() {
      const [selectedTaskId, setSelectedTaskId] = useState("A");
      const [currentUserId, setUser] = useState("teacher");
      const [ctx, setCtx] = useState<WorkbenchContext | null>(null);
      const [draft, setDraft] = useState(createInitialDraft(null));
      const [tasks, setTasks] = useState([task("A"), task("B"), task("C")]);
      const [hydrated, setHydrated] = useState(false);
      const [saveStatus, setSaveStatus] = useState<"idle" | "saved">("idle");
      const activeTask = useRef(selectedTaskId);
      const activeUserId = useRef(currentUserId);
      activeTask.current = selectedTaskId;
      activeUserId.current = currentUserId;
      const contextRequestRef = useRef(0);
      const lastSavedDraft = useRef("");
      const prefetchedTaskRef = useRef(new Map<string, Promise<PrefetchedTaskBundle>>([
        ["A", Promise.resolve({} as PrefetchedTaskBundle)], ["C", Promise.resolve({} as PrefetchedTaskBundle)]
      ]));
      const pendingNextRef = useRef<string | null>(null);
      const suppressAutoSelectRef = useRef(false);
      // Match the real workbench's stable context loader and selected-id effect.
      // A same-value B selection cannot repair a context cleared by A's response.
      const loadContext = useCallback((id: string) => {
        mocks.contextLoads(id);
        contextRequestRef.current += 1;
        setCtx(id ? { task: task(id), reviewContext: { expected_revision: 3 } } as WorkbenchContext : null);
        setDraft({ ...createInitialDraft(null), score: 6, comments: `${id} feedback` });
        setHydrated(true);
        setSaveStatus("saved");
        lastSavedDraft.current = `${id} snapshot`;
      }, []);
      useEffect(() => { loadContext(selectedTaskId); }, [loadContext, selectedTaskId]);
      const options = {
        ctx, setCtx, draft, setDraft, canSubmit: true, maxScore: 10, rubricPoints: [],
        nextTask: nextTask ?? undefined, currentUserId, selectedTaskId, hasMoreTasks,
        canManageTasks: false, activeTask, activeUserId, contextRequestRef, lastSavedDraft,
        prefetchedTaskRef, pendingNextRef, suppressAutoSelectRef, setSelectedTaskId, setTasks,
        setDraftHydrated: setHydrated, setDraftSaveStatus: setSaveStatus,
        setGoldPaperCandidate: mocks.nominate, invalidateViewerContent: mocks.invalidateViewer,
        scoreShortcuts: { reset: mocks.resetShortcuts }, refreshTaskAggregate: mocks.refreshAggregate,
        loadMoreTasks: mocks.loadMore, examScoring: { loadSummary: mocks.loadSummary },
        runAction: async (_key: string, action: () => Promise<void>) => { await action(); },
        notify: { error: vi.fn(), info: vi.fn() }
      } as unknown as UseGradingReviewActionsOptions;
      submitGrade = useGradingReviewActions(options).submitGrade;
      selectTask = setSelectedTaskId;
      switchUser = setUser;
      refs = { contextRequestRef, lastSavedDraft, prefetchedTaskRef, pendingNextRef, suppressAutoSelectRef };
      current = { selectedTaskId, ctx, draft, tasks, hydrated, saveStatus };
      return null;
    }
    await act(async () => { root.render(<Harness />); });
  }

  for (const destination of ["C", "B"]) {
    it(`preserves the loaded ${destination} editor when an earlier A submission finishes`, async () => {
      await mount();
      const pending = submitGrade(true);
      expect(mocks.submitHumanGrade).toHaveBeenCalledWith("A", expect.objectContaining({ score: 6, expected_revision: 3 }));
      await act(async () => { selectTask(destination); });
      const previousContext = current.ctx;
      const previousDraft = current.draft;
      const previousGeneration = refs.contextRequestRef.current;
      await act(async () => { finishSubmit({ human_grade: { id: "grade-A" } }); await pending; });
      expect(current.selectedTaskId).toBe(destination);
      expect(current.ctx).toBe(previousContext);
      expect(current.draft).toBe(previousDraft);
      expect(current.hydrated).toBe(true);
      expect(current.saveStatus).toBe("saved");
      expect(refs.contextRequestRef.current).toBe(previousGeneration);
      expect(refs.lastSavedDraft.current).toBe(`${destination} snapshot`);
      expect(mocks.invalidateViewer).not.toHaveBeenCalled();
      expect(mocks.resetShortcuts).not.toHaveBeenCalled();
      expect(mocks.nominate).not.toHaveBeenCalled();
      expect(current.tasks.find((item) => item.id === "A")?.status).toBe("submitted");
      expect(mocks.removeQueries).toHaveBeenCalledWith({ queryKey: ["review-task-context", "A"] });
      expect(mocks.removeFallback).toHaveBeenCalledWith("teacher", "A");
      expect(refs.prefetchedTaskRef.current.has("A")).toBe(false);
      expect(refs.prefetchedTaskRef.current.has("C")).toBe(true);
      expect(mocks.refreshAggregate).toHaveBeenCalledTimes(1);
      expect(mocks.loadSummary).toHaveBeenCalledTimes(1);
      await act(async () => { selectTask(destination); });
      expect(current.ctx).toBe(previousContext);
      expect(mocks.contextLoads.mock.calls.map(([id]) => id)).toEqual(["A", destination]);
    });
  }

  it("does not touch another user's cache, queue, editor, or nomination", async () => {
    await mount();
    const pending = submitGrade(true);
    await act(async () => { switchUser("another-teacher"); });
    const before = current;
    await act(async () => { finishSubmit({ human_grade: { id: "grade-A" } }); await pending; });
    expect(current).toBe(before);
    expect(mocks.removeFallback).toHaveBeenCalledWith("teacher", "A");
    expect(mocks.removeQueries).not.toHaveBeenCalled();
    expect(refs.prefetchedTaskRef.current.has("A")).toBe(true);
    expect(mocks.refreshAggregate).not.toHaveBeenCalled();
    expect(mocks.loadSummary).not.toHaveBeenCalled();
    expect(mocks.nominate).not.toHaveBeenCalled();
  });

  it("still advances and nominates when the submitted task remains active", async () => {
    await mount();
    const pending = submitGrade(true);
    await act(async () => { finishSubmit({ human_grade: { id: "grade-A" } }); await pending; });
    expect(current.selectedTaskId).toBe("B");
    expect(current.ctx?.task.id).toBe("B");
    expect(mocks.invalidateViewer).toHaveBeenCalledTimes(1);
    expect(mocks.resetShortcuts).toHaveBeenCalledTimes(1);
    expect(mocks.nominate).toHaveBeenCalledWith(expect.objectContaining({ sourceGradeId: "grade-A", referenceScore: 6 }));
  });

  it("does not queue a later page or suppress selection after the user leaves A", async () => {
    await mount({ nextTask: null, hasMoreTasks: true });
    const pending = submitGrade();
    await act(async () => { selectTask("C"); });
    await act(async () => { finishSubmit({ human_grade: { id: "grade-A" } }); await pending; });
    expect(mocks.loadMore).not.toHaveBeenCalled();
    expect(refs.pendingNextRef.current).toBeNull();
    expect(refs.suppressAutoSelectRef.current).toBe(false);
    expect(current.ctx?.task.id).toBe("C");
  });
});
