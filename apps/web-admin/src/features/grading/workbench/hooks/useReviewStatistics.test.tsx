// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewStatistics } from "../../../../api/reviewStatistics";
import { useReviewStatistics } from "./useReviewStatistics";

const mocks = vi.hoisted(() => ({ read: vi.fn() }));
vi.mock("../../../../api/reviewStatistics", () => ({ getReviewStatistics: mocks.read }));
vi.mock("../../../../api/client", () => ({ getUserErrorMessage: (error: unknown) => (error as Error).message }));

const sample = (exam = "exam", count = 0): ReviewStatistics => ({
  exam_id: exam, scope: "personal", graded_count: count, full_score_count: 0,
  correct_rate: count ? 0 : null, average_score_rate: count ? 0 : null,
  questions: [], updated_at: "2026-10-28T00:00:00Z",
});
const scope = { examId: "exam", tenantId: "tenant", actorId: "teacher", personalScope: true, refreshRevision: 0 };
const flush = async () => { await act(async () => { await Promise.resolve(); }); };

describe("formal review statistics lifecycle", () => {
  let focused = true;
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    focused = true;
    vi.spyOn(document, "hasFocus").mockImplementation(() => focused);
    mocks.read.mockResolvedValue(sample());
  });
  afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });

  it("polls every ten seconds, pauses on blur, and immediately refreshes after confirmed submission", async () => {
    const { result, rerender } = renderHook(props => useReviewStatistics(props), { initialProps: scope });
    await flush();
    expect(result.current.statistics?.correct_rate).toBeNull();
    await act(async () => { vi.advanceTimersByTime(10_000); });
    expect(mocks.read).toHaveBeenCalledTimes(2);
    act(() => { focused = false; window.dispatchEvent(new Event("blur")); });
    await act(async () => { vi.advanceTimersByTime(30_000); });
    expect(mocks.read).toHaveBeenCalledTimes(2);
    expect(result.current.focused).toBe(false);
    act(() => { focused = true; window.dispatchEvent(new Event("focus")); });
    await flush();
    expect(mocks.read).toHaveBeenCalledTimes(3);
    rerender({ ...scope, refreshRevision: 1 });
    await flush();
    expect(mocks.read).toHaveBeenCalledTimes(4);
  });

  it("aborts old scopes and discards late results after switching exam or identity", async () => {
    const pending: Array<(value: ReviewStatistics) => void> = [];
    mocks.read.mockImplementation(() => new Promise(resolve => pending.push(resolve)));
    const { result, rerender } = renderHook(props => useReviewStatistics(props), { initialProps: scope });
    const oldSignal = mocks.read.mock.calls[0][3] as AbortSignal;
    rerender({ ...scope, examId: "next" });
    expect(oldSignal.aborted).toBe(true);
    await act(async () => { pending[0](sample("exam", 1)); });
    expect(result.current.statistics).toBeUndefined();
    await act(async () => { pending[1](sample("next", 2)); });
    expect(result.current.statistics?.graded_count).toBe(2);
    rerender({ ...scope, examId: "next", tenantId: "new-tenant", actorId: "new-teacher" });
    expect(result.current.statistics).toBeUndefined();
    expect(mocks.read).toHaveBeenLastCalledWith("next", "new-teacher", true, expect.any(AbortSignal));
  });

  it("keeps the last successful data on failure and retries without overlapping reads", async () => {
    const { result } = renderHook(() => useReviewStatistics(scope));
    await flush();
    mocks.read.mockRejectedValueOnce(new Error("读取失败"));
    await act(async () => { vi.advanceTimersByTime(10_000); });
    expect(result.current.error).toBe("读取失败");
    expect(result.current.statistics?.graded_count).toBe(0);
    let resolveRead!: (value: ReviewStatistics) => void;
    mocks.read.mockImplementationOnce(() => new Promise(resolve => { resolveRead = resolve; }));
    act(() => result.current.refresh());
    await act(async () => { vi.advanceTimersByTime(30_000); });
    expect(mocks.read).toHaveBeenCalledTimes(3);
    await act(async () => { resolveRead(sample("exam", 1)); });
    expect(result.current.error).toBeUndefined();
    expect(result.current.statistics?.correct_rate).toBe(0);
  });

  it("requires an exam and authenticated identity before requesting statistics", async () => {
    const { rerender } = renderHook(props => useReviewStatistics(props), { initialProps: { ...scope, examId: "" } });
    await flush();
    expect(mocks.read).not.toHaveBeenCalled();
    rerender({ ...scope, actorId: "" });
    await flush();
    expect(mocks.read).not.toHaveBeenCalled();
  });
});
