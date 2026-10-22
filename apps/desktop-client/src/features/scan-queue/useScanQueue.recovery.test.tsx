// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SyncQueueItem } from "../../types";
import type { CaptureScanContext } from "../../api/exams";
import { useScanQueue } from "./useScanQueue";

const mocks = vi.hoisted(() => ({
  hasDurableDesktopStore: vi.fn(() => true),
  listDurableScanQueue: vi.fn(), persistDurableScanQueueItem: vi.fn(),
  archiveDurableScanQueueItems: vi.fn(), spoolScanAsset: vi.fn(),
  removeUnstartedScanQueueItem: vi.fn(),
  readOfflineDraftEnvelopes: vi.fn(() => []),
  listOfflineDraftEnvelopes: vi.fn(), readPersistedScanQueue: vi.fn(() => [] as SyncQueueItem[]),
  persistScanQueue: vi.fn(), inspectScanFile: vi.fn(),
  previewUrlForFile: vi.fn(() => "blob:preview"),
  isTauriRuntime: vi.fn(() => false), inspectScannerSample: vi.fn(), getCaptureScanContext: vi.fn()
}));
vi.mock("../../lib/durableStore", () => ({
  hasDurableDesktopStore: mocks.hasDurableDesktopStore,
  listDurableScanQueue: mocks.listDurableScanQueue,
  persistDurableScanQueueItem: mocks.persistDurableScanQueueItem,
  archiveDurableScanQueueItems: mocks.archiveDurableScanQueueItems,
  spoolScanAsset: mocks.spoolScanAsset,
  removeUnstartedScanQueueItem: mocks.removeUnstartedScanQueueItem
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
vi.mock("../../lib/localRuntime", () => ({ isTauriRuntime: mocks.isTauriRuntime }));
vi.mock("../../lib/scannerProfile", () => ({ inspectScannerSample: mocks.inspectScannerSample }));
vi.mock("../../api/exams", () => ({ getCaptureScanContext: mocks.getCaptureScanContext }));

function scanContext(): CaptureScanContext {
  return { batch: { id: "batch-a", exam_id: "exam-a", name: "上午扫描", created_at: "2026-10-10T01:00:00Z", status: "uploading", source_type: "scan", file_count: 0, page_count: 0 },
    exam: { id: "exam-a", name: "Exam A", subject: "english" }, template_status: "ready",
    template: { id: "locked-template", name: "英语答题卡", version_no: 3, page_count: 1, content_hash: "hash", binding_revision: 1, layout: { pages: [] } } };
}

function item(patch: Partial<SyncQueueItem> = {}): SyncQueueItem {
  return {
    id: "scan-1", localAssetId: "asset-1", title: "scan.pdf",
    kind: "scan_upload", status: "succeeded", progress: 100,
    detail: "", updatedAt: "", fileName: "scan.pdf", fileSize: 4,
    ...patch
  } as SyncQueueItem;
}

describe("scan queue account isolation and recovery", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useScanQueue>;
  const diagnostic = vi.fn();
  const logEvent = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    vi.clearAllMocks();
    // 恢复中止时可能没消费单次返回值；每例清空 mock 队列，不能把旧 deferred Promise 带进下一例。
    for (const mock of Object.values(mocks)) mock.mockReset();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.hasDurableDesktopStore.mockReturnValue(true);
    mocks.listDurableScanQueue.mockResolvedValue([]);
    mocks.listOfflineDraftEnvelopes.mockResolvedValue([]);
    mocks.persistDurableScanQueueItem.mockResolvedValue(undefined);
    mocks.archiveDurableScanQueueItems.mockResolvedValue(undefined);
    mocks.removeUnstartedScanQueueItem.mockResolvedValue(undefined);
    mocks.inspectScanFile.mockResolvedValue([]);
    mocks.inspectScannerSample.mockResolvedValue([]);
    mocks.isTauriRuntime.mockReturnValue(false);
    mocks.readPersistedScanQueue.mockReturnValue([]);
    mocks.readOfflineDraftEnvelopes.mockReturnValue([]);
    mocks.previewUrlForFile.mockReturnValue("blob:preview");
    mocks.getCaptureScanContext.mockResolvedValue(scanContext());
    URL.revokeObjectURL = vi.fn();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount(scope = "test-server|test-tenant|test-user", withClient = false, context: CaptureScanContext | null = scanContext(), overrides: Partial<Parameters<typeof useScanQueue>[0]> = {}) {
    const client = {} as never;
    function Probe({ durableScopeKey }: { durableScopeKey: string }) {
      current = useScanQueue({
        durableScopeKey,
        exams: [{ id: "exam-a", name: "Exam A" } as never],
        selectedExamId: "exam-a", captureBatchId: "batch-a",
        scanSubmissionId: "submission-a", scanStartPage: 1,
        captureBatches: [scanContext().batch], scanContext: context,
        client: withClient ? client : undefined, token: withClient ? "token" : null,
        fileImportTemplate: { templatePreset: "locked-template", paperSize: "A4", dpi: 300, duplex: false },
        setDiagnosticError: diagnostic, logEvent,
        ...overrides
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

  it("does not discard a newly spooled file when durable restoration finishes late", async () => {
    let finishRestore!: (items: SyncQueueItem[]) => void;
    mocks.listDurableScanQueue.mockImplementationOnce(() => new Promise((resolve) => { finishRestore = resolve; }));
    mocks.spoolScanAsset.mockResolvedValueOnce(item({ id: "new", status: "pending" }));
    await mount();
    // 恢复期间其它已启动的本机操作仍可能回填；同页重读不能丢掉这些当前项。
    await act(async () => { current.updateQueue(() => [item({ id: "new", status: "pending" })]); });
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

  it("stops A's selection when its durable spool finishes after switching to B", async () => {
    let finishSpool!: (entry: SyncQueueItem) => void;
    mocks.spoolScanAsset.mockImplementationOnce(() => new Promise((resolve) => { finishSpool = resolve; }));
    mocks.listDurableScanQueue.mockResolvedValueOnce([item({ status: "pending", examId: "exam-a", captureBatchId: "batch-a" })]);
    const switchScope = await mount("server|tenant|A");
    const input = document.createElement("input");
    const click = vi.spyOn(input, "click").mockImplementation(() => undefined);
    current.fileInputRef.current = input;
    await act(async () => { current.chooseReplacement("scan-1"); });
    expect(click).toHaveBeenCalledOnce();
    let selection!: Promise<void>;
    await act(async () => { selection = current.handleFileSelection([new File(["scan"], "a.pdf")] as unknown as FileList); });
    expect(mocks.spoolScanAsset).toHaveBeenCalledOnce();
    await switchScope("server|tenant|B");
    await act(async () => { finishSpool(item({ id: "a-item", status: "pending" })); await selection; });
    expect(current.queue).toEqual([]);
    expect(current.fileBufferRef.current.size).toBe(0);
    expect(current.importResults).toEqual([]);
    expect(current.fileImportError).toBeNull();
    expect(mocks.removeUnstartedScanQueueItem).not.toHaveBeenCalled();
    expect(logEvent).not.toHaveBeenCalledWith("info", "scan files queued", expect.anything());
  });

  it("keeps restoration failure visible and blocks duplicate import until a successful reread", async () => {
    mocks.listDurableScanQueue.mockRejectedValueOnce(new Error("磁盘读取失败"));
    await mount();
    expect(current.queueRestoreStatus).toBe("error"); expect(current.queueRestoreError).toContain("磁盘读取失败");
    const file = new File(["scan"], "scan.pdf");
    await act(async () => { await current.handleFileSelection([file] as unknown as FileList); });
    expect(mocks.spoolScanAsset).not.toHaveBeenCalled(); expect(current.fileImportError).toContain("原队列尚未核实");
    await act(async () => { current.updateQueue(() => [item({ id: "current", status: "pending", detail: "当前状态" })]); });
    mocks.listDurableScanQueue.mockResolvedValueOnce([item({ id: "current", status: "failed", detail: "旧快照" }), item({ id: "restored", status: "pending" })]);
    await act(async () => { await current.restoreQueue(); });
    expect(current.queueRestoreStatus).toBe("ready"); expect(current.queueRestoreError).toBeNull();
    expect(current.queue.map((entry) => entry.id)).toEqual(["current", "restored"]); expect(current.queue[0].detail).toBe("当前状态");
  });

  it("does not report a loading or locked native queue as an empty usable queue", async () => {
    mocks.listDurableScanQueue.mockReturnValueOnce(new Promise(() => undefined));
    const switchScope = await mount();
    expect(current.queueRestoreStatus).toBe("loading");
    await switchScope(""); expect(current.queueRestoreStatus).toBe("locked");
  });

  it("refreshes old queue names from its original authorized batch and persists them without changing upload ownership", async () => {
    mocks.listDurableScanQueue.mockResolvedValueOnce([item({ examId: "exam-a", captureBatchId: "batch-a", status: "pending" })]);
    await mount(undefined, true);
    expect(mocks.getCaptureScanContext).toHaveBeenCalledWith(expect.anything(), "batch-a");
    expect(current.queue[0]).toMatchObject({ examId: "exam-a", captureBatchId: "batch-a", examName: "Exam A", captureBatchName: "上午扫描", captureBatchCreatedAt: "2026-10-10T01:00:00Z" });
    await current.durablePersistenceRef.current;
    expect(mocks.persistDurableScanQueueItem).toHaveBeenCalledWith(expect.objectContaining({ captureBatchName: "上午扫描", captureBatchCreatedAt: "2026-10-10T01:00:00Z" }), "test-server|test-tenant|test-user");
    const mismatch = scanContext(); mismatch.batch.exam_id = "other-exam";
    mocks.getCaptureScanContext.mockResolvedValueOnce(mismatch);
    await act(async () => { await current.refreshQueueContext(); });
    expect(current.queueContextError).toContain("归属不一致"); expect(current.queue[0].examId).toBe("exam-a"); expect(current.queue[0].captureBatchName).toBe("上午扫描");
  });

  it("keeps older queue names after a read error and allows same-page metadata retry", async () => {
    mocks.listDurableScanQueue.mockResolvedValueOnce([item({ examId: "exam-a", captureBatchId: "batch-a", examName: "上次考试名称", captureBatchName: "上次批次名称" })]);
    mocks.getCaptureScanContext.mockRejectedValueOnce(new Error("名称读取失败"));
    await mount(undefined, true);
    expect(current.queueContextError).toContain("名称读取失败"); expect(current.queue[0].captureBatchName).toBe("上次批次名称");
    await act(async () => { await current.refreshQueueContext(); });
    expect(current.queueContextError).toBeNull(); expect(current.queue[0].captureBatchName).toBe("上午扫描");
  });
});
