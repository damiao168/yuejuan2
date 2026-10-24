// @vitest-environment jsdom
import { act, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearAllReviewDraftFallbacks, loadReviewDraftFallback, saveReviewDraftFallback } from "../../../auth/reviewDraftFallback";
import { createDraftSnapshot, createInitialDraft } from "./gradingWorkbench.model";
import type { WorkbenchContext } from "./gradingWorkbench.types";
import type { ScoreEditor } from "./components/ScoreEditor";
import type { WorkbenchHeader } from "./components/WorkbenchHeader";
import type { RegionScoreEditor } from "./components/RegionScoreEditor";
import type { TaskDraftSaveQueue } from "./hooks/taskDraftSaveQueue";
import { GradingWorkbench, type GradingWorkbenchProps } from "./GradingWorkbench";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setBusinessCommandScope } from "../../../api/businessCommand";
import { createReviewTaskContextScope, reviewTaskContextKeys } from "../../../query/reviewTaskContext";

const mocks = vi.hoisted(() => ({
  loadContext: vi.fn(), save: vi.fn(), renew: vi.fn(), verify: vi.fn(), removeQueries: vi.fn(), invalidateQueries: vi.fn(),
  notify: { info: vi.fn(), warning: vi.fn(), error: vi.fn(), success: vi.fn() },
  loadTasks: vi.fn(async () => undefined), loadGraders: vi.fn(async () => undefined), resetQueue: vi.fn(),
  resetShortcuts: vi.fn(), prepareViewer: vi.fn(), restoreViewer: vi.fn(), invalidateViewer: vi.fn(),
  noop: vi.fn(), asyncNoop: vi.fn(async () => undefined),
  header: null as Record<string, unknown> | null, editor: null as Record<string, unknown> | null,
  region: null as Record<string, unknown> | null,
  evidence: null as Record<string, unknown> | null, review: null as Record<string, unknown> | null,
  selectTask: null as ((id: string) => void) | null,
  prefetchedTaskRef: { current: new Map() }, prefetchedPreviewRef: { current: new Map() },
  pendingNextRef: { current: null }, suppressAutoSelectRef: { current: false },
  offset: { x: 0, y: 0 }, viewportRef: { current: null }
}));

vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.notify }) }, Alert: () => null, Button: () => null, Space: () => null, Drawer: () => null }));
vi.mock("./components/ReviewBookmarks", () => ({ ReviewBookmarkControls: () => null, ReviewBookmarksDrawer: () => null }));
vi.mock("./hooks/usePersonalReviewBookmarks", () => ({ usePersonalReviewBookmarks: () => ({ state: {}, busy: false, error: "", uncertainTask: "" }) }));
vi.mock("../../../api/review", () => ({ renewReviewTask: mocks.renew, saveReviewDraft: mocks.save, verifyEvidence: mocks.verify }));
vi.mock("./gradingTaskContext", () => ({ loadTaskContext: mocks.loadContext }));
vi.mock("../../../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: mocks.noop }));
vi.mock("./hooks/useGradingQueue", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return { useGradingQueue: function Queue() {
    const [selectedTaskId, setSelectedTaskId] = React.useState("A");
    mocks.selectTask = setSelectedTaskId;
    return {
      selectedTaskId, setSelectedTaskId, tasks: [], setTasks: mocks.noop, taskAggregate: null,
      taskFilter: "all", setTaskFilter: mocks.noop, queueScope: "mine", setQueueScope: mocks.noop,
      keyword: "", setKeyword: mocks.noop, loadingTasks: false, loadingMoreTasks: false, hasMoreTasks: false,
      pageError: null, taskError: null, gradersError: null, assignmentUserId: "", setAssignmentUserId: mocks.noop,
      assignmentTaskIds: [], setAssignmentTaskIds: mocks.noop, filteredTasks: [], assignableTasks: [], graderOptions: [], graderNames: {},
      reviewerProgress: [], myProgress: { id: "teacher", name: "教师", total: 0, completed: 0, active: 0, percent: 0 }, remainingCount: 0,
      prefetchTasks: [], nextTask: undefined, pendingNextRef: mocks.pendingNextRef, suppressAutoSelectRef: mocks.suppressAutoSelectRef,
      loadTasks: mocks.loadTasks, loadMoreTasks: mocks.asyncNoop, refreshTaskAggregate: mocks.asyncNoop,
      loadGraders: mocks.loadGraders, reset: mocks.resetQueue
    };
  } };
});
vi.mock("./hooks/useGradingPrefetch", () => ({ useGradingPrefetch: () => ({ prefetchedTaskRef: mocks.prefetchedTaskRef, prefetchedPreviewRef: mocks.prefetchedPreviewRef }) }));
vi.mock("./hooks/useAnswerViewer", () => ({ useAnswerViewer: () => ({
  preview: null, previewLoading: false, mode: "segment", scale: 1, autoFit: true, imageSize: null, rotation: 0,
  offset: mocks.offset, dragging: false, viewportRef: mocks.viewportRef,
  invalidateContent: mocks.invalidateViewer, prepareContext: mocks.prepareViewer, restore: mocks.restoreViewer,
  fit: mocks.noop, changeMode: mocks.noop, zoom: mocks.noop, rotate: mocks.noop,
  onPointerDown: mocks.noop, onPointerMove: mocks.noop, onPointerUp: mocks.noop, onImageLoad: mocks.noop
}) }));
vi.mock("./hooks/useMathWorkbenchEvidence", () => ({ useMathWorkbenchEvidence: () => ({ state: { phase: "unavailable", understanding: null }, dirty: false, refresh: mocks.asyncNoop }) }));
vi.mock("./hooks/useExamScoring", () => ({ useExamScoring: () => ({ loadSummary: mocks.asyncNoop }) }));
vi.mock("./hooks/useGradingScoreShortcuts", () => ({ useGradingScoreShortcuts: () => ({ reset: mocks.resetShortcuts, canUndo: false, setScore: mocks.noop, toggleCriterion: mocks.noop, undo: mocks.noop }) }));
vi.mock("./hooks/useGradingQueueActions", () => ({ useGradingQueueActions: () => ({ assignSelectedTasks: mocks.asyncNoop, claimTask: mocks.asyncNoop, goNext: mocks.asyncNoop, releaseCurrentTask: mocks.asyncNoop }) }));
vi.mock("./hooks/useGradingReviewActions", () => ({ useGradingReviewActions: (options: Record<string, unknown>) => {
  mocks.review = options;
  return { submitGrade: mocks.asyncNoop, markDispute: mocks.asyncNoop, adoptAiScore: mocks.asyncNoop, openDraftConflict: mocks.asyncNoop, applyDraftConflict: mocks.noop, confirmReturnTask: mocks.noop };
} }));
vi.mock("./hooks/useGradingMathActions", () => ({ useGradingMathActions: () => ({ refreshMath: mocks.asyncNoop, selectMathEvidenceStep: mocks.noop, requestMathSuggestion: mocks.asyncNoop }) }));
vi.mock("./hooks/useGradingKeyboard", () => ({ useGradingKeyboard: mocks.noop }));
vi.mock("./components/ExamScoringPanel", () => ({ ExamScoringPanel: () => null }));
vi.mock("./components/WorkbenchHeader", () => ({ WorkbenchHeader: (props: Record<string, unknown>) => { mocks.header = props; return null; } }));
vi.mock("./components/TaskRail", () => ({ TaskRail: () => null }));
vi.mock("./components/WorkbenchOverlays", () => ({ WorkbenchOverlays: () => null }));
vi.mock("./components/AnswerEvidencePane", () => ({ AnswerEvidencePane: () => null }));
vi.mock("./components/EvidenceInspector", () => ({ EvidenceInspector: (props: Record<string, unknown>) => { mocks.evidence = props; return null; } }));
vi.mock("./components/ScoreEditor", () => ({ ScoreEditor: (props: Record<string, unknown>) => { mocks.editor = props; return null; } }));
vi.mock("./components/RegionScoreEditor", () => ({
  groupTaskRefs: (value: WorkbenchContext) => value.reviewContext.answer_artifact?.grading_group_tasks ?? [],
  RegionScoreEditor: (value: Record<string, unknown>) => { mocks.region = value; return null; }
}));
vi.mock("./components/FrozenQuestionReference", () => ({ FrozenQuestionReference: () => null }));
vi.mock("../../../components/PageState", () => ({ LoadingState: () => null, ErrorState: () => null, EmptyState: () => null }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

function context(id = "A", owner = "teacher", renewable = true, examId = "exam-A"): WorkbenchContext {
  return {
    task: { id, assigned_to: owner, status: "in_progress", exam_id: examId, answer_segment_id: `segment-${id}`, question_id: `question-${id}`, source: "manual_sample" },
    reviewContext: { claim: { can_renew: renewable }, draft: null, question_snapshot: { id: "snapshot", risk_tier: "R2", scoring_policy_snapshot: { mode: "AI_ASSIST" } }, subject_tool_hints: { subject_code: "history" } },
    question: { score: 10, rubric: { points: [{ id: "criterion", score: 2 }] } },
    ocrText: `${id} 答案`, ocrResults: [], warnings: [], aiGrades: []
  } as unknown as WorkbenchContext;
}

describe("batch8 workbench common original/current lifecycle (mock hooks/API)", () => {
  let root: Root;
  let queryClient: QueryClient;
  let props: GradingWorkbenchProps;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.spyOn(navigator, "onLine", "get").mockReturnValue(true);
    clearAllReviewDraftFallbacks();
    setBusinessCommandScope("tenant", "teacher");
    queryClient = new QueryClient();
    vi.spyOn(queryClient, "removeQueries").mockImplementation(mocks.removeQueries);
    vi.spyOn(queryClient, "invalidateQueries").mockImplementation(mocks.invalidateQueries);
    mocks.prefetchedTaskRef.current.clear(); mocks.prefetchedPreviewRef.current.clear();
    mocks.header = null; mocks.editor = null; mocks.region = null; mocks.review = null; mocks.evidence = null;
    mocks.loadContext.mockImplementation(async (_client: unknown, _scope: unknown, id: string) => context(id));
    mocks.save.mockResolvedValue({ draft: { revision: 1 } });
    mocks.renew.mockResolvedValue(undefined);
    root = createRoot(document.createElement("div"));
    props = { canWork: true, canManageTasks: false, canViewOriginalImage: false, canGrade: false, canVerifyEvidence: false, canReturn: true, currentUserId: "teacher", currentTenantId: "tenant", initialExamId: "exam-A" };
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  async function render(overrides: Partial<GradingWorkbenchProps> = {}) {
    if ((overrides.currentUserId && overrides.currentUserId !== props.currentUserId) || (overrides.currentTenantId && overrides.currentTenantId !== props.currentTenantId)) {
      setBusinessCommandScope(overrides.currentTenantId ?? props.currentTenantId, overrides.currentUserId ?? props.currentUserId);
    }
    props = { ...props, ...overrides };
    await act(async () => root.render(<QueryClientProvider client={queryClient}><GradingWorkbench {...props} /></QueryClientProvider>));
  }
  function editor() { return mocks.editor as unknown as ComponentProps<typeof ScoreEditor>; }
  function header() { return mocks.header as unknown as ComponentProps<typeof WorkbenchHeader>; }
  function evidence() { return mocks.evidence as unknown as { onVerifyEvidence: () => Promise<void> }; }
  async function select(id: string) { await act(async () => { mocks.selectTask!(id); }); }
  async function edit(score: number) {
    await act(async () => editor().setDraft((current) => ({ ...current, score, comments: "教师评语", rubricSelections: { criterion: 2, skipped: 0 } })));
  }

  it("整区逐行保存停用当前题自动保存，提交当前行不换题也不显示锚点未评分", async () => {
    const grouped = context();
    grouped.gradingGroupId = "fill"; grouped.gradingGroupImageUrl = "/shared";
    grouped.reviewContext.answer_artifact = { grading_group_tasks: ["A", "B", "C"].map(id => ({ id })) } as WorkbenchContext["reviewContext"]["answer_artifact"];
    mocks.loadContext.mockResolvedValue(grouped);
    await render({ canGrade: true });
    const region = () => mocks.region as unknown as ComponentProps<typeof RegionScoreEditor>;
    expect(mocks.editor).toBeNull();
    expect(header().scoreLabel).toBeUndefined();
    await act(async () => region().onStateChange(true, false));
    expect(header().draftSaveStatus).toBe("pending");
    expect(header().draftStatusText).toBe("本区有未保存评分");
    await act(async () => region().onStateChange(false, false));
    expect(header().draftSaveStatus).toBe("saved");
    await act(async () => region().setDraft(value => ({ ...value, score: 2 })));
    await act(async () => { vi.advanceTimersByTime(1500); });
    expect(mocks.save).not.toHaveBeenCalled();
    await act(async () => region().onSubmitted({ ...grouped.task, status: "submitted", revision: 2 }));
    expect(region().context.task.id).toBe("A");
    expect(region().draft.score).toBe(2);
    expect(mocks.loadContext).toHaveBeenCalledTimes(1);
  });

  it.each([
    [false, "teacher", "当前身份无重新生成数学建议的权限；请按冻结细则人工评分，需要重新生成时请联系管理员。"],
    [false, "other", "当前身份无重新生成数学建议的权限；请按冻结细则人工评分，需要重新生成时请联系管理员。"],
    [true, "other", "当前任务只读；请先领取属于你的可编辑任务，再重新生成建议。"],
    [true, "teacher", undefined]
  ] as const)("数学建议禁用原因保留身份优先级 canGrade=%s owner=%s", async (canGrade, owner, expected) => {
    mocks.loadContext.mockResolvedValue(context("A", owner));
    await render({ canGrade });
    expect(mocks.evidence?.mathDisabledReason).toBe(expected);
  });

  it("does not replace B's loaded editor with A's late context", async () => {
    const lateA = deferred<WorkbenchContext>();
    mocks.loadContext.mockImplementation((_client: unknown, _scope: unknown, id: string) => id === "A" ? lateA.promise : Promise.resolve(context(id)));
    await render();
    await select("B");
    const previous = editor().context;
    expect(previous.task.id).toBe("B");
    await act(async () => { lateA.resolve(context("A")); });
    expect(editor().context).toBe(previous);
    expect(header().loading).toBe(false);
    expect(editor().draft.answerText).toBe("B 答案");
  });

  it("waits for a queued save before an explicit same-task reload", async () => {
    await render();
    const lateSave = deferred<number>();
    const queue = (mocks.review!.draftSaves as { current: TaskDraftSaveQueue }).current;
    const key = JSON.stringify(["teacher", "A"]);
    const save = queue.enqueue(key, async () => lateSave.promise);
    let reload!: Promise<void>;
    await act(async () => { reload = header().onReloadConflict(); });
    expect(mocks.loadContext).toHaveBeenCalledTimes(1);
    expect(mocks.removeQueries).not.toHaveBeenCalled();
    await act(async () => { lateSave.resolve(4); await save; await reload; });
    expect(mocks.loadContext).toHaveBeenCalledTimes(2);
    expect(mocks.removeQueries).toHaveBeenCalledWith({ queryKey: reviewTaskContextKeys.task(createReviewTaskContextScope("exam-A", false), "A") });
    expect(queue.revision(key)).toBe(4);
    expect(editor().context.task.id).toBe("A");
  });

  it.each([[false, "segment"], [true, "original"]] as const)("restores fallback viewer with original-image permission=%s", async (allowed, mode) => {
    saveReviewDraftFallback("teacher", "A", createDraftSnapshot({ ...createInitialDraft(null), score: 7 }, "original", 1.5, 90, { x: 3, y: 4 }, false));
    await render({ canViewOriginalImage: allowed });
    expect(editor().draft.score).toBe(7);
    expect(header().draftSaveStatus).toBe("offline");
    expect(mocks.restoreViewer).toHaveBeenCalledWith({ mode, scale: 1.5, rotation: 90, offset: { x: 3, y: 4 }, fit: false });
  });

  it.each([["other", true], ["teacher", false]])("keeps owner=%s renewable=%s out of draft hydration/lease", async (owner, renewable) => {
    mocks.loadContext.mockResolvedValue(context("A", owner, renewable));
    await render();
    expect(header().draftSaveStatus).toBe("readonly");
    expect(mocks.renew).not.toHaveBeenCalled();
    await edit(0);
    await act(async () => { vi.advanceTimersByTime(1200); });
    expect(mocks.save).not.toHaveBeenCalled();
    expect(loadReviewDraftFallback("teacher", "A")).toBeNull();
  });

  it("debounces edits for 1200ms and persists an explicit zero rather than null", async () => {
    await render();
    expect(mocks.renew).toHaveBeenCalledWith("A");
    expect(header().scoreLabel).toBe("未评分");
    await edit(0);
    expect(editor().canEditDraft).toBe(true);
    expect(header().scoreLabel).toBe("0/10 分");
    await act(async () => { vi.advanceTimersByTime(1199); });
    expect(mocks.save).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(mocks.save).toHaveBeenCalledWith("A", expect.objectContaining({ score: 0, expected_revision: 0, rubric_selections: [{ point_id: "criterion", score: 2 }], comments: "教师评语" }));
    expect(header().draftSaveStatus).toBe("saved");
    expect(loadReviewDraftFallback("teacher", "A")).toBeNull();
  });

  it("flushes before the timer on a same-user task switch and shields B from A's late save", async () => {
    const lateSave = deferred<{ draft: { revision: number } }>();
    mocks.save.mockReturnValue(lateSave.promise);
    await render();
    await edit(6);
    await select("B");
    expect(mocks.save).toHaveBeenCalledWith("A", expect.objectContaining({ score: 6 }));
    const before = editor().context;
    const statusBefore = header().draftSaveStatus;
    await act(async () => { lateSave.resolve({ draft: { revision: 8 } }); });
    expect(editor().context).toBe(before);
    expect(editor().context.task.id).toBe("B");
    expect(header().draftSaveStatus).toBe(statusBefore);
    expect(editor().draft.score).toBeNull();
  });

  it("does not flush the old user's edited draft during an account change", async () => {
    await render();
    await edit(6);
    await render({ currentUserId: "another-teacher" });
    await act(async () => { vi.advanceTimersByTime(1200); });
    expect(mocks.save).not.toHaveBeenCalled();
    expect(editor().canEditDraft).toBe(false);
    expect(header().draftSaveStatus).toBe("readonly");
    expect(loadReviewDraftFallback("teacher", "A")).not.toBeNull();
  });

  it("refuses an out-of-exam context before draft/viewer hydration", async () => {
    mocks.loadContext.mockResolvedValue(context("A", "teacher", true, "other-exam"));
    await render();
    expect(mocks.restoreViewer).not.toHaveBeenCalled();
    expect(mocks.editor).toBeNull();
    expect(mocks.renew).not.toHaveBeenCalled();
  });

  it("reloads the queue when the same account binds a new session generation", async () => {
    await render();
    expect(mocks.loadTasks).toHaveBeenCalledTimes(1);
    setBusinessCommandScope("tenant", "teacher");
    await render();
    expect(mocks.resetQueue).toHaveBeenCalledTimes(2);
    expect(mocks.loadTasks).toHaveBeenCalledTimes(2);
  });

  it("reloads the queue after the image permission changes and clears its old scope", async () => {
    await render();
    await render({ canViewOriginalImage: true });
    expect(mocks.resetQueue).toHaveBeenCalledTimes(2);
    expect(mocks.loadTasks).toHaveBeenCalledTimes(2);
  });

  it("does not show A's late lease failure as B's draft error", async () => {
    const renewal = deferred<void>();
    mocks.renew.mockImplementation((id: string) => id === "A" ? renewal.promise : Promise.resolve());
    await render();
    await select("B");
    const status = header().draftSaveStatus;
    await act(async () => { renewal.reject(new Error("old lease expired")); });
    expect(editor().context.task.id).toBe("B");
    expect(header().draftSaveStatus).toBe(status);
  });

  it("does not reload A after a refresh invalidation finishes while B is selected", async () => {
    const invalidation = deferred<void>();
    mocks.invalidateQueries.mockReturnValueOnce(invalidation.promise);
    await render();
    let refresh: unknown;
    await act(async () => { refresh = header().onRefresh(); });
    await select("B");
    await act(async () => { invalidation.resolve(); await refresh; });
    expect(editor().context.task.id).toBe("B");
    expect(mocks.loadContext.mock.calls.map((call) => call[2])).toEqual(["A", "B"]);
    expect(mocks.loadTasks).toHaveBeenCalledTimes(1);
  });

  it.each([false, true])("keeps evidence verification scoped when switching task=%s", async (switchTask) => {
    const verification = deferred<{ job: { id: string } }>();
    mocks.verify.mockReturnValueOnce(verification.promise);
    mocks.loadContext.mockImplementation(async (_client: unknown, _scope: unknown, id: string) => ({
      ...context(id), aiGrades: [{ id: `grade-${id}`, created_at: "2026-10-04", suggested_score: 6 }]
    }));
    await render();
    let pending!: Promise<void>;
    await act(async () => { pending = evidence().onVerifyEvidence(); });
    expect(mocks.verify).toHaveBeenCalledWith("grade-A");
    if (switchTask) await select("B");
    await act(async () => { verification.resolve({ job: { id: "job-A" } }); await pending; });
    expect(editor().context.task.id).toBe(switchTask ? "B" : "A");
    expect(editor().context.evidenceJob?.id).toBe(switchTask ? undefined : "job-A");
  });
});
