// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SyncQueueItem } from "../../types";
import { useScanQueue } from "./useScanQueue";

const mocks = vi.hoisted(() => ({
  hasDurableDesktopStore: vi.fn(() => true),
  listDurableScanQueue: vi.fn(), persistDurableScanQueueItem: vi.fn(),
  archiveDurableScanQueueItems: vi.fn(), spoolScanAsset: vi.fn(),
  readOfflineDraftEnvelopes: vi.fn(() => []),
  listOfflineDraftEnvelopes: vi.fn(), readPersistedScanQueue: vi.fn(() => [] as SyncQueueItem[]),
  persistScanQueue: vi.fn(), inspectScanFile: vi.fn(),
  previewUrlForFile: vi.fn(() => "blob:preview")
}));
vi.mock("../../lib/durableStore", () => ({
  hasDurableDesktopStore: mocks.hasDurableDesktopStore,
  listDurableScanQueue: mocks.listDurableScanQueue,
  persistDurableScanQueueItem: mocks.persistDurableScanQueueItem,
  archiveDurableScanQueueItems: mocks.archiveDurableScanQueueItems,
  spoolScanAsset: mocks.spoolScanAsset
}));
vi.mock("../../lib/offlineStore", () => ({
  readOfflineDraftEnvelopes: mocks.readOfflineDraftEnvelopes,
  listOfflineDraftEnvelopes: mocks.listOfflineDraftEnvelopes
}));
vi.mock("../../lib/scanFiles", () => ({
  inferContentType: vi.fn(() => "application/pdf"),
  inspectScanFile: mocks.inspectScanFile,
  persistScanQueue: mocks.persistScanQueue,
  previewUrlForFile: mocks.previewUrlForFile,
  readPersistedScanQueue: mocks.readPersistedScanQueue
}));
vi.mock("../../lib/localRuntime", () => ({ isTauriRuntime: () => false }));
vi.mock("../../lib/scannerProfile", () => ({ inspectScannerSample: vi.fn() }));

function item(patch: Partial<SyncQueueItem> = {}): SyncQueueItem {
  return {
    id: "scan-1", localAssetId: "asset-1", title: "scan.pdf",
    kind: "scan_upload", status: "succeeded", progress: 100,
    detail: "", updatedAt: "", fileName: "scan.pdf", fileSize: 4,
    ...patch
  } as SyncQueueItem;
}

describe("scan queue controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useScanQueue>;
  const diagnostic = vi.fn();
  const logEvent = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.hasDurableDesktopStore.mockReturnValue(true);
    mocks.listDurableScanQueue.mockResolvedValue([]);
    mocks.listOfflineDraftEnvelopes.mockResolvedValue([]);
    mocks.persistDurableScanQueueItem.mockResolvedValue(undefined);
    mocks.archiveDurableScanQueueItems.mockResolvedValue(undefined);
    mocks.inspectScanFile.mockResolvedValue([]);
    URL.revokeObjectURL = vi.fn();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount(scope = "test-server|test-tenant|test-user") {
    function Probe({ durableScopeKey }: { durableScopeKey: string }) {
      current = useScanQueue({
        durableScopeKey,
        exams: [{ id: "exam-a", name: "Exam A" } as never],
        selectedExamId: "exam-a", captureBatchId: "batch-a",
        scanSubmissionId: "submission-a", scanStartPage: 1,
        scannerPreflight: null, setDiagnosticError: diagnostic, logEvent
      });
      return null;
    }
    await act(async () => { root.render(<Probe durableScopeKey={scope} />); });
    return async (nextScope: string) => {
      await act(async () => { root.render(<Probe durableScopeKey={nextScope} />); });
    };
  }

  it("restores the durable queue and archives only confirmed items", async () => {
    mocks.listDurableScanQueue.mockResolvedValueOnce([item(), item({ id: "pending", status: "pending" })]);
    await mount();
    expect(current.queue.map((entry) => entry.id)).toEqual(["scan-1", "pending"]);
    await act(async () => { await current.clearSucceededQueueItems(); });
    expect(mocks.archiveDurableScanQueueItems).toHaveBeenCalledWith(["asset-1"], "test-server|test-tenant|test-user");
    expect(current.queue.map((entry) => entry.id)).toEqual(["pending"]);
    await current.durablePersistenceRef.current;
    expect(mocks.persistDurableScanQueueItem).toHaveBeenCalledWith(expect.objectContaining({ id: "pending" }), "test-server|test-tenant|test-user");
  });

  it("keeps a failed-quality original in durable storage without uploading it", async () => {
    mocks.inspectScanFile.mockResolvedValueOnce([{ status: "failed", code: "blur" }]);
    mocks.spoolScanAsset.mockResolvedValueOnce(item({ status: "pending" }));
    await mount();
    const file = new File(["scan"], "scan.pdf", { type: "application/pdf" });
    await act(async () => { await current.handleFileSelection([file] as unknown as FileList); });
    expect(current.queue[0]).toMatchObject({
      id: "scan-1", status: "failed", localAssetId: "asset-1"
    });
    await current.durablePersistenceRef.current;
    expect(mocks.persistDurableScanQueueItem).toHaveBeenCalledWith(expect.objectContaining({ status: "failed" }), "test-server|test-tenant|test-user");
  });

  it("does not discard a newly spooled file when durable restoration finishes late", async () => {
    let finishRestore!: (items: SyncQueueItem[]) => void;
    mocks.listDurableScanQueue.mockImplementationOnce(() => new Promise((resolve) => { finishRestore = resolve; }));
    mocks.spoolScanAsset.mockResolvedValueOnce(item({ id: "new", status: "pending" }));
    await mount();
    const file = new File(["scan"], "scan.pdf", { type: "application/pdf" });
    await act(async () => { await current.handleFileSelection([file] as unknown as FileList); });
    expect(current.queue.map((entry) => entry.id)).toEqual(["new"]);
    await act(async () => { finishRestore([item({ id: "old", status: "pending" })]); });
    expect(current.queue.map((entry) => entry.id)).toEqual(["new", "old"]);
  });

  it("clears A's queue before showing B's records and ignores A's late restoration", async () => {
    let finishA!: (items: SyncQueueItem[]) => void;
    mocks.listDurableScanQueue.mockImplementationOnce(() => new Promise((resolve) => { finishA = resolve; }));
    mocks.listDurableScanQueue.mockResolvedValueOnce([item({ id: "b-item", localAssetId: "b-asset" })]);
    const switchScope = await mount("server|tenant|A");
    await switchScope("server|tenant|B");
    expect(current.queue.map((entry) => entry.id)).toEqual(["b-item"]);
    await act(async () => { finishA([item({ id: "a-item", localAssetId: "a-asset" })]); });
    expect(current.queue.map((entry) => entry.id)).toEqual(["b-item"]);
  });

  it("stops A's file selection if the account changes during inspection", async () => {
    let finishInspection!: (checks: []) => void;
    mocks.inspectScanFile.mockImplementationOnce(() => new Promise((resolve) => { finishInspection = resolve; }));
    const switchScope = await mount("server|tenant|A");
    const file = new File(["scan"], "scan.pdf", { type: "application/pdf" });
    let selection!: Promise<void>;
    await act(async () => { selection = current.handleFileSelection([file] as unknown as FileList); });
    await switchScope("server|tenant|B");
    await act(async () => { finishInspection([]); await selection; });
    expect(mocks.spoolScanAsset).not.toHaveBeenCalled();
    expect(current.queue).toEqual([]);
  });

  it("recovers a non-durable item after the user reselects its file", async () => {
    mocks.hasDurableDesktopStore.mockReturnValue(false);
    mocks.readPersistedScanQueue.mockReturnValueOnce([item({
      status: "failed", requiresReselect: true, localAssetId: undefined
    })]);
    await mount();
    const file = new File(["scan"], "scan.pdf", { type: "application/pdf" });
    await act(async () => { await current.handleFileSelection([file] as unknown as FileList); });
    expect(current.queue).toHaveLength(1);
    expect(current.queue[0]).toMatchObject({ id: "scan-1", status: "pending", requiresReselect: false });
    expect(mocks.spoolScanAsset).not.toHaveBeenCalled();
  });
});
