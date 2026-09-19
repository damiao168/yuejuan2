// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SyncQueueItem } from "../../types";
import { useUploadSync } from "./useUploadSync";

const mocks = vi.hoisted(() => ({
  resumeCaptureUpload: vi.fn(), hasDurableDesktopStore: vi.fn(() => false),
  persistDurableScanQueueItem: vi.fn(), loadDurableSpoolFile: vi.fn()
}));
vi.mock("../../api/captureUploads", () => ({ resumeCaptureUpload: mocks.resumeCaptureUpload }));
vi.mock("../../lib/durableStore", () => ({
  hasDurableDesktopStore: mocks.hasDurableDesktopStore,
  persistDurableScanQueueItem: mocks.persistDurableScanQueueItem,
  loadDurableSpoolFile: mocks.loadDurableSpoolFile
}));

function item(patch: Partial<SyncQueueItem> = {}): SyncQueueItem {
  return {
    id: "item-a", title: "scan.pdf", kind: "scan_upload", status: "pending",
    progress: 0, detail: "", updatedAt: "", examId: "exam-a",
    captureBatchId: "batch-a", idempotencyKey: "key-a", fileName: "scan.pdf",
    ...patch
  } as SyncQueueItem;
}

describe("upload sync controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useUploadSync>;
  let queueRef: { current: SyncQueueItem[] };
  let fileBufferRef: { current: Map<string, File> };
  let uploadInFlightRef: { current: Set<string> };
  let durablePersistenceRef: { current: Promise<void> };
  const setIsOnline = vi.fn();
  const logEvent = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.resumeCaptureUpload.mockResolvedValue({
      status: "completed", remote_upload_id: "remote-a",
      confirmed_offset: 100, capture_file_id: "capture-a"
    });
    queueRef = { current: [item()] };
    fileBufferRef = { current: new Map([["item-a", new File(["scan"], "scan.pdf")]]) };
    uploadInFlightRef = { current: new Set() };
    durablePersistenceRef = { current: Promise.resolve() };
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount(isOnline: boolean) {
    function Probe() {
      current = useUploadSync({
        client: {} as never, token: "token", isOnline, setIsOnline,
        queueRef, fileBufferRef, uploadInFlightRef, durablePersistenceRef,
        updateQueue: (update) => { queueRef.current = update(queueRef.current); },
        logEvent
      });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("pauses while offline and resumes when the browser reports online", async () => {
    await mount(false);
    await act(async () => { await current.uploadQueueItem("item-a"); });
    expect(mocks.resumeCaptureUpload).not.toHaveBeenCalled();
    expect(queueRef.current[0].status).toBe("pending");
    await act(async () => {
      window.dispatchEvent(new Event("online"));
      await vi.waitFor(() => expect(mocks.resumeCaptureUpload).toHaveBeenCalledTimes(1));
    });
    expect(queueRef.current[0].status).toBe("succeeded");
  });

  it("rejects an item without an idempotency key before contacting the server", async () => {
    queueRef.current = [item({ idempotencyKey: undefined })];
    await mount(true);
    await act(async () => { await current.uploadQueueItem("item-a"); });
    expect(mocks.resumeCaptureUpload).not.toHaveBeenCalled();
    expect(queueRef.current[0].status).toBe("failed");
  });

  it("recovers the encrypted local original before resuming a durable upload", async () => {
    const restored = new File(["scan"], "scan.pdf");
    mocks.hasDurableDesktopStore.mockReturnValueOnce(true);
    mocks.loadDurableSpoolFile.mockResolvedValueOnce(restored);
    queueRef.current = [item({ localAssetId: "local-asset" })];
    fileBufferRef.current.clear();
    await mount(true);
    await act(async () => { await current.uploadQueueItem("item-a"); });
    expect(mocks.loadDurableSpoolFile).toHaveBeenCalledWith("local-asset");
    expect(mocks.resumeCaptureUpload).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({
      file: restored, idempotency_key: "key-a"
    }), expect.any(Function));
  });

  it("resumes from the stored server upload id and guards a duplicate in-flight call", async () => {
    let finish!: (value: unknown) => void;
    mocks.resumeCaptureUpload.mockImplementationOnce((_client, options) => {
      expect(options.remoteUploadId).toBe("remote-old");
      expect(options.idempotency_key).toBe("key-a");
      return new Promise((resolve) => { finish = resolve; });
    });
    queueRef.current = [item({ remoteUploadId: "remote-old", confirmedOffset: 40 })];
    await mount(true);
    let first!: Promise<void>;
    await act(async () => {
      first = current.uploadQueueItem("item-a");
      await Promise.resolve();
    });
    await act(async () => { await current.uploadQueueItem("item-a"); });
    expect(mocks.resumeCaptureUpload).toHaveBeenCalledTimes(1);
    await act(async () => {
      finish({ status: "completed", remote_upload_id: "remote-old",
        confirmed_offset: 100, capture_file_id: "capture-a" });
      await first;
    });
    expect(queueRef.current[0].status).toBe("succeeded");
  });

  it("does not let an old upload completion overwrite a new exam queue item", async () => {
    let finish!: (value: unknown) => void;
    mocks.resumeCaptureUpload.mockImplementationOnce((_client, options) => {
      expect(options.exam).toBe("exam-a");
      expect(options.batch).toBe("batch-a");
      return new Promise((resolve) => { finish = resolve; });
    });
    await mount(true);
    let oldUpload!: Promise<void>;
    await act(async () => {
      oldUpload = current.uploadQueueItem("item-a");
      await Promise.resolve();
    });
    queueRef.current = [item({ id: "item-b", examId: "exam-b", captureBatchId: "batch-b" })];
    await act(async () => {
      finish({ status: "completed", remote_upload_id: "remote-a",
        confirmed_offset: 100, capture_file_id: "capture-a" });
      await oldUpload;
    });
    expect(queueRef.current).toHaveLength(1);
    expect(queueRef.current[0]).toMatchObject({ id: "item-b", examId: "exam-b", status: "pending" });
  });
});
