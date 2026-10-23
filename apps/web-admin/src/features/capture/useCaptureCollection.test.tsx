// @vitest-environment jsdom
import { act, renderHook, cleanup } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CaptureCollectionSummary } from "../../api/captureCollection";
import { useCaptureCollection } from "./useCaptureCollection";

const mocks = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../api/captureCollection", () => ({ getCaptureCollectionSummary: mocks.get }));
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));

function summary(examId: string, revision = 1): CaptureCollectionSummary {
  return { exam_id: examId, exam_status: "collecting", revision, closed: false, pages_per_student: 2,
    roster_count: 0, required_count: 0, ready_count: 0, absent_count: 0, pending_count: 0, can_complete: false, blockers: [], entries: [], issues: [] };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe("考试导入事实读取边界", () => {
  beforeEach(() => { vi.resetAllMocks(); });
  afterEach(cleanup);
  it("切换考试后迟到的旧核对结果不能回填新场次", async () => {
    const old = deferred<{ summary: CaptureCollectionSummary }>();
    mocks.get.mockReturnValueOnce(old.promise).mockResolvedValueOnce({ summary: summary("exam-b") });
    const view = renderHook(({ examId }) => useCaptureCollection(examId, ""), { initialProps: { examId: "exam-a" } });
    await act(async () => view.rerender({ examId: "exam-b" }));
    expect(view.result.current.summary?.exam_id).toBe("exam-b");
    await act(async () => old.resolve({ summary: summary("exam-a") }));
    expect(view.result.current.summary?.exam_id).toBe("exam-b");
  });

  it("结束响应先到时，旧轮询不能把已关闭事实覆盖为旧状态", async () => {
    const old = deferred<{ summary: CaptureCollectionSummary }>();
    mocks.get.mockReturnValue(old.promise);
    const view = renderHook(() => useCaptureCollection("exam-a", ""));
    const closed = { ...summary("exam-a", 2), closed: true, exam_status: "grading" };
    await act(async () => view.result.current.acceptSummary(closed));
    await act(async () => old.resolve({ summary: summary("exam-a") }));
    expect(view.result.current.summary).toEqual(closed);
    expect(view.result.current.loading).toBe(false);
  });

  it("刷新失败保留已知人数并明确标错，不能伪装成空名册", async () => {
    mocks.get.mockResolvedValueOnce({ summary: summary("exam-a", 3) }).mockRejectedValueOnce(new Error("核对服务暂不可用"));
    const view = renderHook(() => useCaptureCollection("exam-a", ""));
    await act(async () => {});
    await act(async () => view.result.current.load());
    expect(view.result.current.summary?.revision).toBe(3);
    expect(view.result.current.error).toBe("核对服务暂不可用");
    expect(view.result.current.loading).toBe(false);
  });
});
