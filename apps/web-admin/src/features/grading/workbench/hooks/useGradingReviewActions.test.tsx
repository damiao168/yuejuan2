// @vitest-environment jsdom
import { act, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import { createReviewTaskContextScope, reviewTaskContextKeys } from "../../../../query/reviewTaskContext";
import type { AiGrade, ReviewTask } from "../../../../api/review";
import { createInitialDraft } from "../gradingWorkbench.model";
import type { PrefetchedTaskBundle, WorkbenchContext } from "../gradingWorkbench.types";
import type { DraftConflictResolution } from "../draftConflict";
import { useGradingReviewActions, type UseGradingReviewActionsOptions } from "./useGradingReviewActions";

const mocks = vi.hoisted(() => ({
  submitHumanGrade: vi.fn(), removeQueries: vi.fn(), removeFallback: vi.fn(),
  refreshAggregate: vi.fn(), loadSummary: vi.fn(), invalidateViewer: vi.fn(),
  resetShortcuts: vi.fn(), nominate: vi.fn(), contextLoads: vi.fn(), loadMore: vi.fn(),
  returnReviewTask: vi.fn(), confirm: vi.fn(), refreshCurrent: vi.fn(), mathRefresh: vi.fn(),
  rememberCurrent: vi.fn(), loadTaskContext: vi.fn(), runAction: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn()
}));
vi.mock("antd", () => ({ Modal: { confirm: mocks.confirm } }));
vi.mock("../../../../api/review", () => ({ submitHumanGrade: mocks.submitHumanGrade, returnReviewTask: mocks.returnReviewTask }));
vi.mock("../../../../auth/reviewDraftFallback", () => ({
  removeReviewDraftFallback: mocks.removeFallback, loadReviewDraftFallback: vi.fn(), saveReviewDraftFallback: vi.fn()
}));
vi.mock("../gradingTaskContext", () => ({ loadTaskContext: mocks.loadTaskContext }));

function task(id: string): ReviewTask {
  return { id, status: "in_progress", assigned_to: "teacher" } as ReviewTask;
}

function context(id: string, revision = 3): WorkbenchContext {
  return {
    task: task(id),
    reviewContext: { expected_revision: revision, draft: null } as WorkbenchContext["reviewContext"],
    pages: [], ocrTasks: [], ocrResults: [], aiGrades: [], warnings: [], ocrText: "", segmentImageUrl: ""
  };
}

describe("grading review submission lifecycle", () => {
  let root: Root;
  let queryClient: QueryClient;
  let finishSubmit: (value: unknown) => void;
  let current: {
    selectedTaskId: string; ctx: WorkbenchContext | null;
    draft: ReturnType<typeof createInitialDraft>; tasks: ReviewTask[];
    hydrated: boolean; saveStatus: string; draftConflict: DraftConflictResolution | null; resolvingConflict: boolean; mathRequesting: boolean;
  };
  let selectTask: (id: string) => void;
  let switchUser: (id: string) => void;
  let switchExam: (id: string) => void;
  let changeOriginalImagePermission: (allowed: boolean) => void;
  let submitGrade: (nominate?: boolean) => Promise<void>;
  let actions: ReturnType<typeof useGradingReviewActions>;
  let refs: Pick<UseGradingReviewActionsOptions, "contextRequestRef" | "lastSavedDraft" | "prefetchedTaskRef" | "pendingNextRef" | "suppressAutoSelectRef">;

  beforeEach(() => {
    vi.clearAllMocks();
    setBusinessCommandScope("tenant", "teacher");
    queryClient = new QueryClient();
    vi.spyOn(queryClient, "removeQueries").mockImplementation(mocks.removeQueries);
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.submitHumanGrade.mockImplementation(() => new Promise((resolve) => { finishSubmit = resolve; }));
    mocks.refreshAggregate.mockResolvedValue(undefined);
    mocks.loadSummary.mockResolvedValue(undefined);
    mocks.loadMore.mockResolvedValue([]);
    mocks.returnReviewTask.mockReset().mockResolvedValue(undefined);
    mocks.refreshCurrent.mockReset().mockResolvedValue(undefined);
    mocks.mathRefresh.mockReset().mockResolvedValue(null);
    mocks.loadTaskContext.mockReset();
    mocks.runAction.mockReset().mockImplementation(async (_key: string, action: () => Promise<void>) => { await action(); });
    root = createRoot(document.createElement("div"));
  });

  afterEach(async () => { await act(async () => { root.unmount(); }); });

  async function mount({ nextTask = task("B"), hasMoreTasks = false, subjectCode = "chinese", suggestedText = false }: { nextTask?: ReviewTask | null; hasMoreTasks?: boolean; subjectCode?: string; suggestedText?: boolean } = {}) {
    function Harness() {
      const [selectedTaskId, setSelectedTaskId] = useState("A");
      const [currentUserId, setUser] = useState("teacher");
      const [examId, setExamId] = useState("exam-A");
      const [allowOriginalImage, setAllowOriginalImage] = useState(false);
      const queryScope = useMemo(() => createReviewTaskContextScope(examId, allowOriginalImage), [examId, allowOriginalImage]);
      const activeScope = useRef(queryScope);
      activeScope.current = queryScope;
      const isCurrentScope = useCallback(() => activeScope.current === queryScope, [queryScope]);
      const [ctx, setCtx] = useState<WorkbenchContext | null>(null);
      const [draft, setDraft] = useState(createInitialDraft(null));
      const [tasks, setTasks] = useState([task("A"), task("B"), task("C")]);
      const [hydrated, setHydrated] = useState(false);
      const [saveStatus, setSaveStatus] = useState<"idle" | "saved">("idle");
      const [draftConflict, setDraftConflict] = useState<DraftConflictResolution | null>(null);
      const [resolvingConflict, setResolvingConflict] = useState(false);
      const [mathRequesting, setMathRequesting] = useState(false);
      const mathActionLock = useRef(false);
      const liveDraft = useRef(draft);
      liveDraft.current = draft;
      const conflictServerContext = useRef<WorkbenchContext | null>(null);
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
      // 与真实工作台中稳定的上下文加载器和选中 ID 的 effect 一致。
      // 再次选择相同的 B 值，无法修复被 A 响应清空的上下文。
      const loadContext = useCallback((id: string) => {
        mocks.contextLoads(id);
        contextRequestRef.current += 1;
        setCtx(id ? context(id) : null);
        setDraft({ ...createInitialDraft(null), score: 6, comments: `${id} feedback` });
        setHydrated(true);
        setSaveStatus("saved");
        lastSavedDraft.current = `${id} snapshot`;
      }, []);
      useEffect(() => { loadContext(selectedTaskId); }, [loadContext, selectedTaskId]);
      const options = {
        queryScope, isCurrentScope,
        ctx, setCtx, draft, setDraft, canSubmit: true, maxScore: 10, rubricPoints: [],
        canReturn: true, canEditDraft: true, canAdoptAiScore: true, canViewOriginalImage: allowOriginalImage,
        selectedGrade: { suggested_score: 9, answer_segment_id: "segment-A", math_artifact_id: "artifact-A", math_artifact_version: 2, math_correction_revision: 0,
          math_scoring_version: "math-rubric-score-v1", rubric_version: "rubric-v1", status: "succeeded", delivery_mode: "teacher_suggestion", needs_human_review: true, max_score: 10, mock: false,
          ...(suggestedText ? { student_feedback: "AI 学生反馈", teacher_note: "AI 内部备注" } : {}) } as AiGrade,
        subjectCode, mathActionLock, liveDraft, math: { refresh: mocks.mathRefresh, dirty: false }, setMathRequesting,
        draftConflict, setDraftConflict, setResolvingConflict, conflictServerContext,
        viewerMode: "segment", scale: 1, rotation: 0, offset: { x: 0, y: 0 }, autoFit: true,
        nextTask: nextTask ?? undefined, currentUserId, selectedTaskId, hasMoreTasks,
        canManageTasks: false, activeTask, activeUserId, contextRequestRef, lastSavedDraft,
        prefetchedTaskRef, pendingNextRef, suppressAutoSelectRef, setSelectedTaskId, setTasks,
        setDraftHydrated: setHydrated, setDraftSaveStatus: setSaveStatus,
        setGoldPaperCandidate: mocks.nominate, invalidateViewerContent: mocks.invalidateViewer,
        scoreShortcuts: { reset: mocks.resetShortcuts, rememberCurrent: mocks.rememberCurrent }, refreshTaskAggregate: mocks.refreshAggregate,
        loadMoreTasks: mocks.loadMore, examScoring: { loadSummary: mocks.loadSummary },
        refreshCurrent: mocks.refreshCurrent, runAction: mocks.runAction,
        notify: { error: mocks.error, info: mocks.info, warning: mocks.warning }
      } as unknown as UseGradingReviewActionsOptions;
      actions = useGradingReviewActions(options);
      submitGrade = actions.submitGrade;
      selectTask = setSelectedTaskId;
      switchUser = setUser;
      switchExam = setExamId;
      changeOriginalImagePermission = setAllowOriginalImage;
      refs = { contextRequestRef, lastSavedDraft, prefetchedTaskRef, pendingNextRef, suppressAutoSelectRef };
      current = { selectedTaskId, ctx, draft, tasks, hydrated, saveStatus, draftConflict, resolvingConflict, mathRequesting };
      return null;
    }
    await act(async () => { root.render(<QueryClientProvider client={queryClient}><Harness /></QueryClientProvider>); });
  }

  it("adopts score and feedback through separate actions", async () => {
    await mount({ suggestedText: true });
    await act(async () => { await actions.adoptAiScore(); });
    expect(current.draft.score).toBe(9);
    expect(current.draft.studentFeedback).toBe("");
    expect(current.draft.privateNote).toBe("");
    await act(async () => { actions.adoptAiFeedback(); });
    expect(current.draft.score).toBe(9);
    expect(current.draft.studentFeedback).toBe("AI 学生反馈");
    expect(current.draft.privateNote).toBe("AI 内部备注");
    expect(current.draft.comments).toBe("A feedback");
  });

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
      expect(mocks.removeQueries).toHaveBeenCalledWith({ queryKey: reviewTaskContextKeys.task(createReviewTaskContextScope("exam-A"), "A") });
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
    expect(mocks.removeFallback).not.toHaveBeenCalled();
    expect(mocks.removeQueries).not.toHaveBeenCalled();
    expect(refs.prefetchedTaskRef.current.has("A")).toBe(true);
    expect(mocks.refreshAggregate).not.toHaveBeenCalled();
    expect(mocks.loadSummary).not.toHaveBeenCalled();
    expect(mocks.nominate).not.toHaveBeenCalled();
  });

  it("keeps the submitted answer open and nominates without advancing", async () => {
    await mount();
    const pending = submitGrade(true);
    await act(async () => { finishSubmit({ human_grade: { id: "grade-A" } }); await pending; });
    expect(current.selectedTaskId).toBe("A");
    expect(current.ctx?.task.id).toBe("A");
    expect(current.ctx?.task.status).toBe("submitted");
    expect(mocks.invalidateViewer).not.toHaveBeenCalled();
    expect(mocks.resetShortcuts).not.toHaveBeenCalled();
    expect(mocks.nominate).toHaveBeenCalledWith(expect.objectContaining({ sourceGradeId: "grade-A", referenceScore: 6 }));
  });

  it("自动提交只传最终分，不再用隐藏的评分点或自动跳题", async () => {
    await mount();
    const pending = actions.submitGrade(false, true);
    await act(async () => { finishSubmit({ human_grade: { id: "grade-A", score: 6 } }); await pending; });
    expect(mocks.submitHumanGrade).toHaveBeenCalledWith("A", expect.objectContaining({ auto_submit: true, score: 6, rubric_selections: [] }));
    expect(current.selectedTaskId).toBe("A");
    expect(current.ctx?.task.status).toBe("submitted");
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

  it.each(["exam", "original-image permission"])("ignores a same-user submission after changing %s scope", async (change) => {
    await mount();
    const pending = submitGrade(true);
    await act(async () => {
      if (change === "exam") switchExam("exam-B");
      else changeOriginalImagePermission(true);
    });
    const before = current;
    await act(async () => { finishSubmit({ human_grade: { id: "grade-A" } }); await pending; });

    expect(current).toBe(before);
    expect(mocks.removeFallback).not.toHaveBeenCalled();
    expect(mocks.removeQueries).not.toHaveBeenCalled();
    expect(refs.prefetchedTaskRef.current.has("A")).toBe(true);
    expect(mocks.refreshAggregate).not.toHaveBeenCalled();
    expect(mocks.loadSummary).not.toHaveBeenCalled();
    expect(mocks.nominate).not.toHaveBeenCalled();
  });


});
