// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { setBusinessCommandScope } from "../../../api/businessCommand";
import { createReviewTaskContextScope, reviewTaskContextKeys, type ReviewTaskContextScope } from "../../../query/reviewTaskContext";
import { ApiClientError } from "../../../api/client";
import { clearAllReviewDraftFallbacks, loadReviewDraftFallback, saveReviewDraftFallback } from "../../../auth/reviewDraftFallback";
import { createDraftSnapshot, createInitialDraft } from "./gradingWorkbench.model";
import type { PrefetchedTaskBundle, WorkbenchContext } from "./gradingWorkbench.types";
import { TaskDraftSaveQueue } from "./hooks/taskDraftSaveQueue";
import { commitWorkbenchDraft, readQueuedWorkbenchContext, restoreWorkbenchDraft, type WorkbenchDraftCommitOptions } from "./workbenchDraftLifecycle";

const mocks = vi.hoisted(() => ({ save: vi.fn(), load: vi.fn(), removeQueries: vi.fn() }));
vi.mock("../../../api/review", () => ({ saveReviewDraft: mocks.save }));
vi.mock("./gradingTaskContext", () => ({ loadTaskContext: mocks.load }));
let queryClient: QueryClient;
let queryScope: ReviewTaskContextScope;

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function context(owner = "teacher", renewable = true): WorkbenchContext {
  return {
    task: { id: "A", assigned_to: owner, status: "in_progress" },
    reviewContext: { claim: { can_renew: renewable }, draft: null },
    ocrText: "原识别", ocrResults: [], warnings: [], aiGrades: []
  } as unknown as WorkbenchContext;
}

function snapshot(score = 0) {
  return createDraftSnapshot({ ...createInitialDraft(null), score, rubricSelections: { positive: 2, zero: 0, negative: -1 }, comments: "当前评语" }, "segment", 1.25, 90, { x: 3, y: 4 }, false);
}

function commitFixture(): WorkbenchDraftCommitOptions {
  const snapshotValue = snapshot();
  const draftSaves = new TaskDraftSaveQueue();
  const draftKey = JSON.stringify(["teacher", "A"]);
  draftSaves.observeRevision(draftKey, 3);
  saveReviewDraftFallback("teacher", "A", snapshotValue);
  return {
    queryClient, queryScope,
    currentUserId: "teacher", taskId: "A", draftKey, generation: 1, snapshotValue,
    snapshot: JSON.stringify(snapshotValue), online: true, activeTask: { current: "A" },
    contextRequestRef: { current: 1 }, conflictedDrafts: { current: new Set<string>() },
    draftSaves: { current: draftSaves }, prefetchedTaskRef: { current: new Map() },
    lastSavedDraft: { current: "server" }, setDraftRevision: vi.fn(), setDraftSaveStatus: vi.fn()
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  clearAllReviewDraftFallbacks();
  setBusinessCommandScope("tenant", "teacher");
  queryScope = createReviewTaskContextScope("exam-A", false);
  queryClient = new QueryClient();
  vi.spyOn(queryClient, "removeQueries").mockImplementation(mocks.removeQueries);
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(true);
  mocks.save.mockResolvedValue({ draft: { revision: 4 } });
  mocks.load.mockResolvedValue(context());
});
afterEach(() => vi.restoreAllMocks());

describe("batch8 queued context and restore helpers (new helpers only)", () => {
  it("waits for the same draft's save before invalidating and loading context", async () => {
    const pending = deferred<number>();
    const saves = new TaskDraftSaveQueue();
    const save = saves.enqueue("teacher:A", async () => pending.promise);
    const prefetched = new Map<string, Promise<PrefetchedTaskBundle>>([["A", Promise.resolve({ context: context() } as PrefetchedTaskBundle)]]);
    const result = readQueuedWorkbenchContext({ queryClient, queryScope, taskId: "A", draftKey: "teacher:A", requestId: 1, requestRef: { current: 1 }, saves, prefetched, discardConflictingDraft: false, canViewOriginalImage: false });
    expect(mocks.load).not.toHaveBeenCalled();
    expect(mocks.removeQueries).not.toHaveBeenCalled();
    pending.resolve(4);
    await save;
    await expect(result).resolves.toMatchObject({ task: { id: "A" } });
    expect(mocks.removeQueries).toHaveBeenCalledWith({ queryKey: reviewTaskContextKeys.task(queryScope, "A") });
    expect(mocks.load).toHaveBeenCalledWith(queryClient, queryScope, "A");
    expect(prefetched.has("A")).toBe(false);
  });

  it("does not invalidate or fetch after a newer request supersedes a queue wait", async () => {
    const pending = deferred<number>();
    const saves = new TaskDraftSaveQueue();
    const save = saves.enqueue("teacher:A", async () => pending.promise);
    const requestRef = { current: 1 };
    const result = readQueuedWorkbenchContext({ queryClient, queryScope, taskId: "A", draftKey: "teacher:A", requestId: 1, requestRef, saves, prefetched: new Map(), discardConflictingDraft: false, canViewOriginalImage: false });
    requestRef.current = 2;
    pending.resolve(4);
    await save;
    await expect(result).resolves.toBeNull();
    expect(mocks.removeQueries).not.toHaveBeenCalled();
    expect(mocks.load).not.toHaveBeenCalled();
  });

  it("consumes a prefetch once and rejects its late response", async () => {
    const prefetchedResponse = deferred<PrefetchedTaskBundle>();
    const prefetched = new Map([["A", prefetchedResponse.promise]]);
    const requestRef = { current: 1 };
    const result = readQueuedWorkbenchContext({ queryClient, queryScope, taskId: "A", draftKey: "teacher:A", requestId: 1, requestRef, saves: new TaskDraftSaveQueue(), prefetched, discardConflictingDraft: false, canViewOriginalImage: true });
    await Promise.resolve();
    await Promise.resolve();
    expect(prefetched.has("A")).toBe(false);
    requestRef.current = 2;
    prefetchedResponse.resolve({ context: context() } as PrefetchedTaskBundle);
    await expect(result).resolves.toBeNull();
    expect(mocks.load).not.toHaveBeenCalled();
  });

  it.each([["other", true], ["teacher", false]])("does not hydrate fallback for owner=%s renewable=%s", (owner, renewable) => {
    saveReviewDraftFallback("teacher", "A", snapshot(7));
    const restored = restoreWorkbenchDraft(context(owner, renewable), { taskId: "A", currentUserId: "teacher", canViewOriginalImage: false, isConflicted: false });
    expect(restored.mayPersistDraft).toBe(false);
    expect(restored.status).toBe("readonly");
    expect(restored.restored.draft.score).toBeNull();
    expect(loadReviewDraftFallback("teacher", "A")).not.toBeNull();
  });

  it("restores conflicted local edits and downgrades an unauthorized original viewer", () => {
    const local = snapshot(7);
    local.viewer.mode = "original";
    saveReviewDraftFallback("teacher", "A", local);
    const restored = restoreWorkbenchDraft(context(), { taskId: "A", currentUserId: "teacher", canViewOriginalImage: false, isConflicted: true });
    expect(restored.status).toBe("conflict");
    expect(restored.restored.draft).toMatchObject({ score: 7, comments: "当前评语" });
    expect(restored.restored.viewer).toEqual({ ...local.viewer, mode: "segment" });
    expect(restored.server.snapshot.draft.score).toBeNull();
  });
});

describe("batch8 draft commit helper (mock API, real queue and memory fallback)", () => {
  it("keeps a new session's cache, prefetch and fallback when an old save finishes", async () => {
    const response = deferred<{ draft: { revision: number } }>();
    mocks.save.mockReturnValue(response.promise);
    const options = commitFixture();
    const pending = commitWorkbenchDraft(options);
    await Promise.resolve();
    await Promise.resolve();
    expect(mocks.save).toHaveBeenCalledTimes(1);
    // 同一账号重新登录，不能仅靠 userId 相同就接受旧保存的副作用。
    setBusinessCommandScope("", "");
    setBusinessCommandScope("tenant", "teacher");
    const newScope = createReviewTaskContextScope("exam-A", false);
    const cache = { currentSession: true };
    queryClient.setQueryData(reviewTaskContextKeys.detail(newScope, "A"), cache);
    const newerPrefetch = Promise.resolve({ context: context() } as PrefetchedTaskBundle);
    options.prefetchedTaskRef.current.set("A", newerPrefetch);
    saveReviewDraftFallback("teacher", "A", snapshot(9));
    response.resolve({ draft: { revision: 4 } });
    await pending;
    expect(queryClient.getQueryData(reviewTaskContextKeys.detail(newScope, "A"))).toBe(cache);
    expect(mocks.removeQueries).not.toHaveBeenCalled();
    expect(options.prefetchedTaskRef.current.get("A")).toBe(newerPrefetch);
    expect(loadReviewDraftFallback<{ draft: { score: number } }>("teacher", "A")?.snapshot.draft.score).toBe(9);
    expect(options.setDraftRevision).not.toHaveBeenCalled();
    expect(options.setDraftSaveStatus).toHaveBeenLastCalledWith("saving");
  });

  it("does not start a queued save after its session ends", async () => {
    const prior = deferred<number>();
    const options = commitFixture();
    const waiting = options.draftSaves.current.enqueue(options.draftKey, () => prior.promise);
    const pending = commitWorkbenchDraft(options);
    setBusinessCommandScope("tenant", "other-teacher");
    prior.resolve(4);
    await waiting;
    await pending;
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.removeQueries).not.toHaveBeenCalled();
  });

  it("saves zero, positive rubric selections, viewer state and expected revision", async () => {
    const options = commitFixture();
    await commitWorkbenchDraft(options);
    expect(mocks.save).toHaveBeenCalledWith("A", {
      score: 0, rubric_selections: [{ point_id: "positive", score: 2 }], comments: "当前评语",
      private_note: "", student_feedback: "", viewer_state: options.snapshotValue.viewer,
      expected_revision: 3, client_updated_at: expect.any(String)
    });
    expect(loadReviewDraftFallback("teacher", "A")).toBeNull();
    expect(options.setDraftRevision).toHaveBeenCalledWith(4);
    expect(options.setDraftSaveStatus).toHaveBeenLastCalledWith("saved");
  });

  it("keeps a newer local edit and signals another pending save", async () => {
    const late = deferred<{ draft: { revision: number } }>();
    mocks.save.mockReturnValue(late.promise);
    const options = commitFixture();
    const commit = commitWorkbenchDraft(options);
    await Promise.resolve();
    saveReviewDraftFallback("teacher", "A", snapshot(8));
    late.resolve({ draft: { revision: 4 } });
    await commit;
    expect(loadReviewDraftFallback<{ draft: { score: number } }>("teacher", "A")?.snapshot.draft.score).toBe(8);
    expect(options.setDraftSaveStatus).toHaveBeenLastCalledWith("saving");
    expect(options.lastSavedDraft.current).toBe(options.snapshot);
  });

  it("does not send an intermediate snapshot superseded while queued", async () => {
    const prior = deferred<number>();
    const options = commitFixture();
    const pending = options.draftSaves.current.enqueue(options.draftKey, async () => prior.promise);
    const commit = commitWorkbenchDraft(options);
    saveReviewDraftFallback("teacher", "A", snapshot(9));
    prior.resolve(4);
    await pending;
    await commit;
    expect(mocks.save).not.toHaveBeenCalled();
    expect(options.setDraftRevision).not.toHaveBeenCalled();
  });

  it("flushes a leaving task without changing the newly active editor", async () => {
    const options = commitFixture();
    options.activeTask.current = "B";
    options.contextRequestRef.current = 2;
    saveReviewDraftFallback("teacher", "B", snapshot(5));
    await commitWorkbenchDraft(options, true);
    expect(mocks.save).toHaveBeenCalledTimes(1);
    expect(options.setDraftRevision).not.toHaveBeenCalled();
    expect(options.setDraftSaveStatus).not.toHaveBeenCalled();
    expect(options.lastSavedDraft.current).toBe("server");
    expect(loadReviewDraftFallback("teacher", "B")).not.toBeNull();
  });

  it("records a late 409 against A without changing B's save status", async () => {
    const options = commitFixture();
    options.activeTask.current = "B";
    mocks.save.mockRejectedValue(new ApiClientError(409, "draft_conflict", "conflict"));
    await commitWorkbenchDraft(options, true);
    expect(options.conflictedDrafts.current.has(options.draftKey)).toBe(true);
    expect(options.setDraftSaveStatus).not.toHaveBeenCalled();
    expect(loadReviewDraftFallback("teacher", "A")).not.toBeNull();
  });

  it.each([[true, "error"], [false, "offline"]] as const)("classifies a network failure with navigator.online=%s", async (online, status) => {
    const options = commitFixture();
    vi.spyOn(navigator, "onLine", "get").mockReturnValue(online);
    mocks.save.mockRejectedValue(new Error("network"));
    await commitWorkbenchDraft(options);
    expect(options.setDraftSaveStatus).toHaveBeenLastCalledWith(status);
  });

  it("does not enqueue while captured offline or already conflicted", async () => {
    const options = commitFixture();
    options.online = false;
    await commitWorkbenchDraft(options);
    expect(options.setDraftSaveStatus).toHaveBeenLastCalledWith("offline");
    options.online = true;
    options.conflictedDrafts.current.add(options.draftKey);
    await commitWorkbenchDraft(options);
    expect(mocks.save).not.toHaveBeenCalled();
  });
});
