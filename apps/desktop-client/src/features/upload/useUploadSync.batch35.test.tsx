// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CaptureUploadProgress, CaptureUploadSource } from "../../api/captureUploads";
import type { SyncQueueItem } from "../../types";
import { useUploadSync } from "./useUploadSync";

const api = vi.hoisted(() => ({
  resume: vi.fn(), hasDurableStore: vi.fn(), persist: vi.fn(), restore: vi.fn()
}));
vi.mock("../../api/captureUploads", () => ({ resumeCaptureUpload: api.resume }));
vi.mock("../../lib/durableStore", () => ({
  hasDurableDesktopStore: api.hasDurableStore, persistDurableScanQueueItem: api.persist,
  loadDurableSpoolFile: api.restore
}));

const completed = { status: "completed", remote_upload_id: "remote-1", confirmed_offset: 100, capture_file_id: "capture-1" };
const progress: CaptureUploadProgress = {
  status: "uploading", remoteUploadId: "remote-1", confirmedOffset: 40, totalBytes: 100
};

function item(patch: Partial<SyncQueueItem> = {}): SyncQueueItem {
  return {
    id: "scan-1", title: "scan.pdf", kind: "scan_upload", status: "pending", progress: 0,
    detail: "", updatedAt: "", examId: "exam-1", captureBatchId: "batch-1",
    idempotencyKey: "stable-key", localAssetId: "local-1", ...patch
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((finish, fail) => { resolve = finish; reject = fail; });
  return { promise, resolve, reject };
}

describe("upload preparation and recovery boundaries", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useUploadSync>;
  let queueRef: { current: SyncQueueItem[] };
  let fileBufferRef: { current: Map<string, File | CaptureUploadSource> };
  let inFlightRef: { current: Set<string> };
  let persistenceRef: { current: Promise<void> };
  const logEvent = vi.fn();
  const setIsOnline = vi.fn();
  const updateQueue = vi.fn((update: (queue: SyncQueueItem[]) => SyncQueueItem[]) => {
    queueRef.current = update(queueRef.current);
  });

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    api.resume.mockReset().mockResolvedValue(completed);
    api.hasDurableStore.mockReset().mockReturnValue(false);
    api.persist.mockReset().mockResolvedValue(undefined);
    api.restore.mockReset();
    logEvent.mockReset().mockResolvedValue(undefined);
    setIsOnline.mockReset();
    updateQueue.mockClear();
    queueRef = { current: [item(), item({ id: "other", title: "other.pdf" })] };
    fileBufferRef = { current: new Map([["scan-1", new File(["scan"], "scan.pdf")]]) };
    inFlightRef = { current: new Set() };
    persistenceRef = { current: Promise.resolve() };
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
    vi.restoreAllMocks();
  });

  async function mount(token: string | null = "token", online = true, scope = "scope-A") {
    function Probe(props: { token: string | null; online: boolean; scope: string }) {
      current = useUploadSync({
        client: {} as never, token: props.token, durableScopeKey: props.scope,
        isOnline: props.online, setIsOnline, queueRef, fileBufferRef,
        uploadInFlightRef: inFlightRef, durablePersistenceRef: persistenceRef, updateQueue, logEvent
      });
      return null;
    }
    const render = async (nextToken: string | null, nextOnline: boolean, nextScope: string) => {
      await act(async () => { root.render(<Probe token={nextToken} online={nextOnline} scope={nextScope} />); });
    };
    await render(token, online, scope);
    return render;
  }

  it.each(["missing", "other_kind", "succeeded", "uploading", "in_flight"] as const)("leaves an ineligible %s queue entry untouched", async (reason) => {
    if (reason === "other_kind") queueRef.current[0] = item({ kind: "offline_grade" });
    if (reason === "succeeded" || reason === "uploading") queueRef.current[0] = item({ status: reason });
    if (reason === "in_flight") inFlightRef.current.add("scan-1");
    const before = [...queueRef.current];
    await mount();
    await act(async () => { await current.uploadQueueItem(reason === "missing" ? "missing" : "scan-1"); });
    expect(queueRef.current).toEqual(before);
    expect(updateQueue).not.toHaveBeenCalled();
    expect(api.resume).not.toHaveBeenCalled();
    expect(api.restore).not.toHaveBeenCalled();
  });

  it.each([
    [null, false, false, "failed", "未登录，无法通过后端鉴权上传"],
    ["token", false, false, "pending", "当前离线，等待联网后继续上传"],
    ["token", true, false, "failed", "缺少考试、采集批次或幂等键，不能启动可恢复上传"],
    ["token", true, true, "failed", "本地质量检查未通过，未上传"]
  ] as const)("keeps prerequisite priority for token %s, online %s and context %s", async (token, online, hasContext, status, detail) => {
    queueRef.current[0] = item({
      examId: hasContext ? "exam-1" : undefined,
      qualityChecks: [{ key: "blur", label: "清晰度", status: "failed", detail: "请重扫" }]
    });
    const other = queueRef.current[1];
    const readFile = vi.spyOn(fileBufferRef.current, "get");
    await mount(token, online);
    await act(async () => { await current.uploadQueueItem("scan-1"); });
    expect(queueRef.current[0]).toMatchObject({ status, detail });
    expect(queueRef.current[1]).toBe(other);
    expect(readFile).not.toHaveBeenCalled();
    expect(api.resume).not.toHaveBeenCalled();
    expect(api.restore).not.toHaveBeenCalled();
  });

  it("rejects an empty durable scope before reading the queue", async () => {
    api.hasDurableStore.mockReturnValue(true);
    const lookup = vi.spyOn(queueRef.current, "find");
    await mount("token", true, "");
    await act(async () => { await current.uploadQueueItem("scan-1"); });
    expect(lookup).not.toHaveBeenCalled();
    expect(updateQueue).not.toHaveBeenCalled();
    expect(api.resume).not.toHaveBeenCalled();
  });

  it("starts a cached-file upload before yielding and clears in-flight state on completion", async () => {
    await mount();
    let operation!: Promise<void>;
    await act(async () => {
      operation = current.uploadQueueItem("scan-1");
      expect(api.resume).toHaveBeenCalledOnce();
      expect(queueRef.current[0].status).toBe("uploading");
      expect(inFlightRef.current.has("scan-1")).toBe(true);
      await operation;
    });
    expect(queueRef.current[0].status).toBe("succeeded");
    expect(inFlightRef.current.has("scan-1")).toBe(false);
    expect(fileBufferRef.current.has("scan-1")).toBe(false);
  });

  it("uses the latest token and online state after rerendering", async () => {
    const render = await mount(null, true);
    await act(async () => { await current.uploadQueueItem("scan-1"); });
    expect(queueRef.current[0].detail).toBe("未登录，无法通过后端鉴权上传");
    await render("token", false, "scope-A");
    await act(async () => { await current.uploadQueueItem("scan-1"); });
    expect(queueRef.current[0].detail).toBe("当前离线，等待联网后继续上传");
    await render("token", true, "scope-A");
    await act(async () => { await current.uploadQueueItem("scan-1"); });
    expect(api.resume).toHaveBeenCalledOnce();
    expect(queueRef.current[0].status).toBe("succeeded");
  });

  it.each(["resolved", "rejected"] as const)("ignores a late %s original-file recovery after switching accounts", async (result) => {
    api.hasDurableStore.mockReturnValue(true);
    const restored = deferred<File>();
    api.restore.mockReturnValueOnce(restored.promise);
    fileBufferRef.current.clear();
    const render = await mount();
    let operation!: Promise<void>;
    await act(async () => { operation = current.uploadQueueItem("scan-1"); });
    const next = item({ id: "scan-B", examId: "exam-B" });
    queueRef.current = [next];
    const nextFile = new File(["B"], "B.pdf");
    fileBufferRef.current.set("scan-B", nextFile);
    await render("token", true, "scope-B");
    await act(async () => {
      if (result === "resolved") restored.resolve(new File(["A"], "A.pdf"));
      else restored.reject(new Error("旧账号原件无法恢复"));
      await operation;
    });
    expect(queueRef.current).toEqual([next]);
    expect(fileBufferRef.current.get("scan-B")).toBe(nextFile);
    expect(fileBufferRef.current.has("scan-1")).toBe(false);
    expect(api.resume).not.toHaveBeenCalled();
    expect(logEvent).not.toHaveBeenCalled();
  });

  it("rejects a late progress callback and ignores completion after switching accounts", async () => {
    const remote = deferred<typeof completed>();
    let report!: (value: CaptureUploadProgress) => Promise<void>;
    let signal!: AbortSignal;
    api.resume.mockImplementationOnce((_client, _input, onProgress, abortSignal) => {
      report = onProgress;
      signal = abortSignal;
      return remote.promise;
    });
    const render = await mount();
    let operation!: Promise<void>;
    await act(async () => { operation = current.uploadQueueItem("scan-1"); });
    const next = item({ id: "scan-B", examId: "exam-B" });
    queueRef.current = [next];
    await render("token", true, "scope-B");
    expect(signal.aborted).toBe(true);
    await expect(report(progress)).rejects.toThrow("登录账号已切换，上传已停止");
    await act(async () => { remote.resolve(completed); await operation; });
    expect(queueRef.current).toEqual([next]);
    expect(api.persist).not.toHaveBeenCalled();
    expect(logEvent).not.toHaveBeenCalled();
    expect(inFlightRef.current.has("scan-1")).toBe(false);
  });

  it("ignores a late upload error after switching accounts and still clears in-flight state", async () => {
    const remote = deferred<typeof completed>();
    api.resume.mockReturnValueOnce(remote.promise);
    const render = await mount();
    let operation!: Promise<void>;
    await act(async () => { operation = current.uploadQueueItem("scan-1"); });
    const next = item({ id: "scan-B", examId: "exam-B" });
    queueRef.current = [next];
    await render("token", true, "scope-B");
    await act(async () => { remote.reject(new Error("旧账号上传失败")); await operation; });
    expect(queueRef.current).toEqual([next]);
    expect(logEvent).not.toHaveBeenCalled();
    expect(inFlightRef.current.has("scan-1")).toBe(false);
  });

  // 两道延迟分别代表此前队列写入和本次进度落盘；模拟上传端等待回调后才发送下一块。
  it("waits for earlier queue writes and current progress persistence before the next block", async () => {
    api.hasDurableStore.mockReturnValue(true);
    const earlier = deferred<void>();
    const persisted = deferred<void>();
    const nextBlock = vi.fn();
    persistenceRef.current = earlier.promise;
    api.persist.mockReturnValueOnce(persisted.promise);
    api.resume.mockImplementationOnce(async (_client, _input, report) => {
      await report(progress);
      nextBlock();
      return completed;
    });
    await mount();
    let operation!: Promise<void>;
    await act(async () => { operation = current.uploadQueueItem("scan-1"); });
    expect(api.persist).not.toHaveBeenCalled();
    expect(nextBlock).not.toHaveBeenCalled();
    await act(async () => { earlier.resolve(); });
    expect(api.persist).toHaveBeenCalledWith(expect.objectContaining({ id: "scan-1", confirmedOffset: 40, status: "uploading" }), "scope-A");
    expect(nextBlock).not.toHaveBeenCalled();
    await act(async () => { persisted.resolve(); await operation; });
    expect(nextBlock).toHaveBeenCalledOnce();
    expect(queueRef.current[0].status).toBe("succeeded");
    expect(inFlightRef.current.has("scan-1")).toBe(false);
  });

  it("stops the next block when progress persistence fails and keeps the original file for retry", async () => {
    api.hasDurableStore.mockReturnValue(true);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    api.persist.mockRejectedValueOnce(new Error("进度落盘失败"));
    const nextBlock = vi.fn();
    api.resume.mockImplementationOnce(async (_client, _input, report) => {
      await report(progress);
      nextBlock();
      return completed;
    });
    await mount();
    await act(async () => { await current.uploadQueueItem("scan-1"); });
    expect(nextBlock).not.toHaveBeenCalled();
    expect(queueRef.current[0]).toMatchObject({ status: "failed", detail: "进度落盘失败", confirmedOffset: 40 });
    expect(fileBufferRef.current.has("scan-1")).toBe(true);
    expect(inFlightRef.current.has("scan-1")).toBe(false);
    await persistenceRef.current;
  });
});
