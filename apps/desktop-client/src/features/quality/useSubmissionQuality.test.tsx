// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSubmissionQuality } from "./useSubmissionQuality";
import { useExamCaptureContext } from "../capture/useExamCaptureContext";
import type { SubmissionQualityResult } from "../../types";

const api = vi.hoisted(() => ({ getSubmissionImageQuality: vi.fn(), runSubmissionQualityCheck: vi.fn(), listExams: vi.fn(), listCaptureBatches: vi.fn(), getCaptureBatchDetail: vi.fn() }));
vi.mock("../../api/submissions", () => ({ getSubmissionImageQuality: api.getSubmissionImageQuality, runSubmissionQualityCheck: api.runSubmissionQualityCheck }));
vi.mock("../../api/exams", () => ({ listExams: api.listExams, listCaptureBatches: api.listCaptureBatches, getCaptureBatchDetail: api.getCaptureBatchDetail }));
function deferred<T>() {
  let resolve!: (value: T) => void; let reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

describe("submission quality ownership", () => {
  let root: Root; let container: HTMLDivElement;
  let current: ReturnType<typeof useSubmissionQuality>;
  let capture: ReturnType<typeof useExamCaptureContext>;
  const client = {} as never;
  const logEvent = vi.fn().mockResolvedValue(undefined);
  const defaults = { token: "token-a", durableScopeKey: "account-a", selectedExamId: "exam-a", captureBatchId: "batch-a",
    scanSubmissionId: "answer-a", linkedSubmissionIds: ["answer-a"] };
  function Probe(props: typeof defaults) {
    current = useSubmissionQuality({ client, logEvent, ...props });
    return <button disabled={!current.canRunQualityCheck || current.isCheckingQuality} onClick={current.handleRunQualityCheck}>检查</button>;
  }
  function IntegratedProbe() {
    capture = useExamCaptureContext({ client, token: "token-a", durableScopeKey: "account-a", workspace: "scan", logEvent });
    current = useSubmissionQuality({ client, logEvent, token: "token-a", durableScopeKey: "account-a", selectedExamId: capture.selectedExamId,
      captureBatchId: capture.captureBatchId, scanSubmissionId: capture.scanSubmissionId, linkedSubmissionIds: capture.linkedSubmissionIds });
    return <button disabled={!current.canRunQualityCheck || current.isCheckingQuality} onClick={current.handleRunQualityCheck}>检查</button>;
  }
  beforeEach(() => {
    vi.resetAllMocks(); logEvent.mockResolvedValue(undefined);
    api.getSubmissionImageQuality.mockImplementation((_client, id) => Promise.resolve({ submission: { id, exam_id: "exam-a", quality_status: "unchecked", quality_issues: [] } }));
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div"); root = createRoot(container);
  });
  afterEach(async () => { await act(async () => root.unmount()); });
  async function mount(props: Partial<typeof defaults> = {}) { await act(async () => root.render(<Probe {...defaults} {...props} />)); }

  it("checks the current associated answer and publishes its result", async () => {
    api.runSubmissionQualityCheck.mockResolvedValueOnce({ result: { valid: true, issues: [] } });
    await mount(); await act(async () => { await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).toHaveBeenCalledWith(client, "answer-a");
    expect(current.qualityResult).toEqual({ valid: true, issues: [] }); expect(current.qualityError).toBeNull();
  });
  it("shows a current request failure and allows retry", async () => {
    api.runSubmissionQualityCheck.mockRejectedValueOnce(new Error("unavailable"));
    await mount(); await act(async () => { await current.handleRunQualityCheck(); });
    expect(current.qualityResult).toBeNull(); expect(current.qualityError).toBeTruthy(); expect(current.isCheckingQuality).toBe(false);
    api.runSubmissionQualityCheck.mockResolvedValueOnce({ result: { valid: true, issues: [] } });
    await act(async () => { await current.handleRunQualityCheck(); }); expect(current.qualityError).toBeNull();
  });
  it.each([{ scanSubmissionId: "" }, { linkedSubmissionIds: [] }, { token: "" }, { selectedExamId: "" }, { captureBatchId: "" }])("blocks missing or unassociated context %j even when the handler is called directly", async (props) => {
    await mount(props); await act(async () => { await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).not.toHaveBeenCalled(); expect(current.canRunQualityCheck).toBe(false);
    expect(current.qualityError).toContain("服务端已关联");
  });
  const changes: Partial<typeof defaults>[] = [{ durableScopeKey: "account-b" }, { selectedExamId: "exam-b" }, { captureBatchId: "batch-b" },
    { scanSubmissionId: "answer-b", linkedSubmissionIds: ["answer-b"] }, { token: "token-b" }];
  it.each(changes)("discards old success and failure after context changes to %j", async (props) => {
    for (const fail of [false, true]) {
      await mount(); const old = deferred<{ result: SubmissionQualityResult }>(); api.runSubmissionQualityCheck.mockReturnValueOnce(old.promise);
      let pending!: Promise<void>; await act(async () => { pending = current.handleRunQualityCheck(); });
      await mount(props);
      expect(current.qualityResult).toBeNull(); expect(current.qualityError).toBeNull(); expect(current.isCheckingQuality).toBe(false);
      await act(async () => { if (fail) old.reject(new Error("old failure")); else old.resolve({ result: { valid: false, issues: [] } }); await pending; });
      expect(current.qualityResult).toBeNull(); expect(current.qualityError).toBeNull();
    }
  });
  it("clears a displayed result when only the selected answer changes", async () => {
    api.runSubmissionQualityCheck.mockResolvedValueOnce({ result: { valid: true, issues: [] } });
    await mount(); await act(async () => { await current.handleRunQualityCheck(); });
    await mount({ scanSubmissionId: "answer-b", linkedSubmissionIds: ["answer-b"] });
    expect(current.qualityResult).toBeNull(); expect(current.qualityError).toBeNull();
  });
  it("rejects A to B to A stale responses and prevents duplicate current POSTs", async () => {
    const old = deferred<{ result: SubmissionQualityResult }>(); api.runSubmissionQualityCheck.mockReturnValueOnce(old.promise);
    await mount(); let oldPending!: Promise<void>;
    await act(async () => { oldPending = current.handleRunQualityCheck(); await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).toHaveBeenCalledTimes(1);
    await mount({ scanSubmissionId: "answer-b", linkedSubmissionIds: ["answer-b"] }); await mount();
    const latest = deferred<{ result: SubmissionQualityResult }>(); api.runSubmissionQualityCheck.mockReturnValueOnce(latest.promise);
    let latestPending!: Promise<void>; await act(async () => { latestPending = current.handleRunQualityCheck(); });
    await act(async () => { old.resolve({ result: { valid: false, issues: [] } }); await oldPending; });
    expect(current.qualityResult).toBeNull(); expect(current.isCheckingQuality).toBe(true);
    await act(async () => { latest.resolve({ result: { valid: true, issues: [] } }); await latestPending; });
    expect(current.qualityResult?.valid).toBe(true); expect(current.isCheckingQuality).toBe(false);
  });
  it("switches the real capture controller from A to B without posting A under B", async () => {
    api.listCaptureBatches.mockResolvedValue({ batches: [{ id: "batch-a", exam_id: "exam-a", status: "uploading" }, { id: "batch-b", exam_id: "exam-a", status: "uploading" }] });
    api.getCaptureBatchDetail.mockImplementation((_client, batchId: string) => Promise.resolve({ batch: { id: batchId, exam_id: "exam-a" },
      pages: [{ submission_id: batchId === "batch-a" ? "answer-a" : "answer-b" }], files: [] }));
    api.runSubmissionQualityCheck.mockResolvedValue({ result: { valid: true, issues: [] } });
    await act(async () => root.render(<IntegratedProbe />)); await act(async () => capture.setSelectedExamId("exam-a"));
    await act(async () => { await capture.handleRefreshProcessing(); await current.handleRunQualityCheck(); });
    // detail读取后需要渲染把实际关联传入质量hook；再次点击才是当前合法答卷。
    await act(async () => { await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).toHaveBeenLastCalledWith(client, "answer-a");
    await act(async () => capture.setCaptureBatchId("batch-b"));
    expect((container.querySelector("button") as HTMLButtonElement).disabled).toBe(true); expect(current.qualityResult).toBeNull();
    await act(async () => { await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).toHaveBeenCalledTimes(1);
    await act(async () => { await capture.handleRefreshProcessing(); });
    await act(async () => { await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).toHaveBeenCalledTimes(2); expect(api.runSubmissionQualityCheck).toHaveBeenLastCalledWith(client, "answer-b");
  });

  it("does not convert a successful page-count check into image-quality approval", async () => {
    api.getSubmissionImageQuality.mockResolvedValue({ submission: { id: "answer-a", exam_id: "exam-a", quality_status: "failed", quality_issues: [{ code: "blur", message: "图片模糊" }] } });
    api.runSubmissionQualityCheck.mockResolvedValue({ result: { valid: true, issues: [] } });
    await mount(); await act(async () => { await current.handleRunQualityCheck(); });
    expect(current.qualityResult?.valid).toBe(true); expect(current.imageQuality?.quality_status).toBe("failed");
  });

  it("keeps image quality unknown on a read failure and offers a read-only retry", async () => {
    api.getSubmissionImageQuality.mockRejectedValueOnce(new Error("图像状态读取失败"));
    await mount(); expect(current.imageQuality).toBeNull(); expect(current.imageQualityError).toContain("读取失败");
    await act(async () => { await current.handleRefreshImageQuality(); });
    expect(current.imageQuality?.quality_status).toBe("unchecked"); expect(current.imageQualityError).toBeNull();
    expect(api.runSubmissionQualityCheck).not.toHaveBeenCalled();
  });

  it("discards a previous answer's late image approval and rejects mismatched exam results", async () => {
    const old = deferred<{ submission: { id: string; exam_id: string; quality_status: string } }>(); api.getSubmissionImageQuality.mockReturnValueOnce(old.promise);
    await mount(); await mount({ scanSubmissionId: "answer-b", linkedSubmissionIds: ["answer-b"] });
    await act(async () => { old.resolve({ submission: { id: "answer-a", exam_id: "exam-a", quality_status: "passed" } }); });
    expect(current.imageQuality?.id).toBe("answer-b"); expect(current.imageQuality?.quality_status).toBe("unchecked");
    api.getSubmissionImageQuality.mockResolvedValueOnce({ submission: { id: "answer-b", exam_id: "other-exam", quality_status: "passed" } });
    await act(async () => { await current.handleRefreshImageQuality(); });
    expect(current.imageQuality).toBeNull(); expect(current.imageQualityError).toContain("不一致");
  });
});
