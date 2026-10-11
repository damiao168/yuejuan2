// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useExamCaptureContext } from "./useExamCaptureContext";

const api = vi.hoisted(() => ({ listCaptureBatches: vi.fn(), listExams: vi.fn(), getCaptureBatchDetail: vi.fn(), getCaptureScanContext: vi.fn() }));
vi.mock("../../api/exams", () => ({
  listCaptureBatches: api.listCaptureBatches, listExams: api.listExams, getCaptureBatchDetail: api.getCaptureBatchDetail, getCaptureScanContext: api.getCaptureScanContext
}));
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("exam capture context controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useExamCaptureContext>;
  beforeEach(() => {
    vi.clearAllMocks();
    api.getCaptureScanContext.mockImplementation((_client, id: string) => Promise.resolve({ batch: { id, exam_id: "exam-a" }, exam: { id: "exam-a", name: "英语月考" }, template_status: "missing" }));
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount() {
    // 同一次挂载保持依赖引用稳定，避免夹具每次渲染重新触发批次加载 effect。
    const client = {} as never;
    const logEvent = vi.fn().mockResolvedValue(undefined);
    function Probe() {
      current = useExamCaptureContext({
        client, token: "token", durableScopeKey: "test-session", workspace: "scan", logEvent
      });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("ignores a previous exam's late batch response", async () => {
    const old = deferred<{ batches: Array<{ id: string; exam_id: string; status: string }> }>();
    api.listCaptureBatches.mockImplementation((_client, examId: string) =>
      examId === "exam-a" ? old.promise : Promise.resolve({
        batches: [{ id: "batch-b", exam_id: "exam-b", status: "open" }]
      }));
    await mount();
    await act(async () => { current.setSelectedExamId("exam-a"); });
    await act(async () => { current.setSelectedExamId("exam-b"); });
    expect(current.captureBatchId).toBe("batch-b");
    await act(async () => {
      old.resolve({ batches: [{ id: "batch-a", exam_id: "exam-a", status: "open" }] });
    });
    expect(current.captureBatchId).toBe("batch-b");
    expect(current.captureBatches.map((batch) => batch.id)).toEqual(["batch-b"]);
  });

  it("invalidates an old batch request when exam selection is cleared", async () => {
    const old = deferred<{ batches: Array<{ id: string; status: string }> }>();
    api.listCaptureBatches.mockReturnValueOnce(old.promise);
    await mount();
    await act(async () => { current.setSelectedExamId("exam-a"); });
    await act(async () => { current.setSelectedExamId(""); });
    await act(async () => { old.resolve({ batches: [{ id: "batch-a", status: "open" }] }); });
    expect(current.captureBatches).toEqual([]);
    expect(current.captureBatchId).toBe("");
  });

  it("selects a checkable answer only from the real batch page association", async () => {
    api.listCaptureBatches.mockResolvedValue({ batches: [{ id: "batch-a", exam_id: "exam-a", status: "uploading" }] });
    api.getCaptureBatchDetail.mockResolvedValue({ batch: { id: "batch-a", exam_id: "exam-a", status: "processing" }, pages: [{ id: "page-1", submission_id: "real-answer", status: "ready" }], files: [] });
    await mount(); await act(async () => current.setSelectedExamId("exam-a"));
    await act(async () => { await current.handleRefreshProcessing(); });
    expect(current.scanSubmissionId).toBe("real-answer");
    expect(api.getCaptureBatchDetail).toHaveBeenCalledWith(expect.anything(), "batch-a");
    api.getCaptureBatchDetail.mockResolvedValue({ batch: { id: "batch-a", exam_id: "wrong-exam" }, pages: [{ submission_id: "wrong-answer" }], files: [] });
    await act(async () => { await current.handleRefreshProcessing(); });
    expect(current.scanSubmissionId).toBe(""); expect(current.processingDetail).toBeNull(); expect(current.processingError).toContain("不一致");
  });

  it("clears the previous answer immediately when another batch or exam is selected", async () => {
    api.listCaptureBatches.mockResolvedValue({ batches: [{ id: "batch-a", exam_id: "exam-a", status: "uploading" }, { id: "batch-b", exam_id: "exam-a", status: "uploading" }] });
    api.getCaptureBatchDetail.mockResolvedValue({ batch: { id: "batch-a", exam_id: "exam-a" }, pages: [{ submission_id: "answer-a" }], files: [] });
    await mount(); await act(async () => current.setSelectedExamId("exam-a"));
    await act(async () => { await current.handleRefreshProcessing(); });
    expect(current.linkedSubmissionIds).toEqual(["answer-a"]);
    await act(async () => current.setCaptureBatchId("batch-b"));
    expect(current.scanSubmissionId).toBe(""); expect(current.linkedSubmissionIds).toEqual([]); expect(current.processingDetail).toBeNull();
    await act(async () => current.setSelectedExamId("exam-b"));
    expect(current.scanSubmissionId).toBe(""); expect(current.linkedSubmissionIds).toEqual([]);
  });

  it("rejects a late batch detail after A to B to A even when the ids match again", async () => {
    api.listCaptureBatches.mockResolvedValue({ batches: [{ id: "batch-a", exam_id: "exam-a", status: "uploading" }, { id: "batch-b", exam_id: "exam-a", status: "uploading" }] });
    const old = deferred<{ batch: { id: string; exam_id: string }; pages: { submission_id: string }[]; files: [] }>(); api.getCaptureBatchDetail.mockReturnValueOnce(old.promise);
    await mount(); await act(async () => current.setSelectedExamId("exam-a"));
    let pending!: Promise<void>; await act(async () => { pending = current.handleRefreshProcessing(); });
    await act(async () => current.setCaptureBatchId("batch-b"));
    await act(async () => current.setCaptureBatchId("batch-a"));
    await act(async () => { old.resolve({ batch: { id: "batch-a", exam_id: "exam-a" }, pages: [{ submission_id: "stale-answer" }], files: [] }); await pending; });
    expect(current.scanSubmissionId).toBe(""); expect(current.processingDetail).toBeNull(); expect(current.processingError).toBeNull();
  });

  it("opens the queued exam's completed batch even when it is absent from the first list page", async () => {
    api.listExams.mockResolvedValue({ exams: [{ id: "exam-b", name: "物理月考" }] });
    api.listCaptureBatches.mockImplementation((_client, examId) => Promise.resolve({ batches: [{ id: `active-${examId}`, exam_id: examId, status: "uploading" }] }));
    api.getCaptureBatchDetail.mockResolvedValue({ batch: { id: "completed-batch", exam_id: "exam-b", name: "教务处旧批次", status: "completed" }, pages: [{ submission_id: "answer-b" }], files: [] });
    await mount(); await act(async () => current.setSelectedExamId("exam-a"));
    await act(async () => current.handleOpenQueueContext("exam-b", "completed-batch"));
    expect(current.selectedExamId).toBe("exam-b"); expect(current.captureBatchId).toBe("completed-batch");
    expect(api.getCaptureBatchDetail).toHaveBeenCalledWith(expect.anything(), "completed-batch");
    expect(current.processingDetail?.batch.name).toBe("教务处旧批次"); expect(current.linkedSubmissionIds).toEqual(["answer-b"]);
    expect(current.captureBatches.find((batch) => batch.id === "completed-batch")?.status).toBe("completed");
    await act(async () => { await current.handleLoadCaptureBatches(); });
    expect(current.captureBatchId).toBe("completed-batch"); expect(current.captureBatches.some((batch) => batch.id === "completed-batch")).toBe(true);
  });

  it("does not replace an unreadable queued batch with an unrelated active batch", async () => {
    api.listExams.mockResolvedValue({ exams: [] });
    api.listCaptureBatches.mockResolvedValue({ batches: [{ id: "other", exam_id: "exam-b", status: "uploading" }] });
    api.getCaptureBatchDetail.mockRejectedValue(new Error("无法读取原批次"));
    await mount(); await act(async () => current.handleOpenQueueContext("exam-b", "missing-batch"));
    expect(current.captureBatchId).toBe("missing-batch"); expect(current.processingDetail).toBeNull();
    expect(current.linkedSubmissionIds).toEqual([]); expect(current.processingError).toContain("无法读取原批次");
  });

  it("reads only the selected batch's template and ignores a late result from another batch", async () => {
    api.listCaptureBatches.mockResolvedValue({ batches: [{ id: "batch-a", exam_id: "exam-a", status: "uploading" }, { id: "batch-b", exam_id: "exam-a", status: "uploading" }] });
    const old = deferred<unknown>(); api.getCaptureScanContext.mockReturnValueOnce(old.promise);
    await mount(); await act(async () => current.setSelectedExamId("exam-a"));
    expect(current.loadingScanContext).toBe(true);
    await act(async () => current.setCaptureBatchId("batch-b"));
    expect(current.scanContext?.batch.id).toBe("batch-b");
    await act(async () => old.resolve({ batch: { id: "batch-a", exam_id: "exam-a" }, exam: { id: "exam-a" }, template_status: "ready", template: { name: "过期模板" } }));
    expect(current.scanContext?.batch.id).toBe("batch-b"); expect(current.scanContext?.template).toBeUndefined();
  });

  it("shows template read failure and same-page retry without keeping an unverified previous template", async () => {
    api.listCaptureBatches.mockResolvedValue({ batches: [{ id: "batch-a", exam_id: "exam-a", status: "uploading" }] });
    api.getCaptureScanContext.mockRejectedValueOnce(new Error("模板读取失败"));
    await mount(); await act(async () => current.setSelectedExamId("exam-a"));
    expect(current.scanContext).toBeNull(); expect(current.scanContextError).toContain("模板读取失败"); expect(current.loadingScanContext).toBe(false);
    await act(async () => { await current.handleRefreshScanContext(); });
    expect(current.scanContext?.batch.id).toBe("batch-a"); expect(current.scanContextError).toBeNull();
    api.getCaptureScanContext.mockResolvedValueOnce({ batch: { id: "batch-a", exam_id: "another-exam" }, exam: { id: "another-exam" }, template_status: "ready" });
    await act(async () => { await current.handleRefreshScanContext(); });
    expect(current.scanContext).toBeNull(); expect(current.scanContextError).toContain("不一致");
  });
});
