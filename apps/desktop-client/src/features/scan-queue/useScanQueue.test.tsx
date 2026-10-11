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

describe("scan queue controller", () => {
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

  it("imports an existing PDF in native mode with no scanner and still runs file and template checks", async () => {
    mocks.isTauriRuntime.mockReturnValue(true);
    mocks.spoolScanAsset.mockResolvedValueOnce(item({ status: "pending" }));
    await mount();
    const file = new File(["%PDF-1.7"], "answers.pdf", { type: "application/pdf" });
    await act(async () => { await current.handleFileSelection([file] as unknown as FileList); });
    expect(mocks.inspectScanFile).toHaveBeenCalledWith(file);
    expect(mocks.inspectScannerSample).toHaveBeenCalledWith(file, expect.objectContaining({ templatePreset: "locked-template", paperSize: "A4", dpi: 300, deviceFingerprint: "" }));
    expect(mocks.spoolScanAsset).toHaveBeenCalledWith(expect.objectContaining({ examId: "exam-a", captureBatchId: "batch-a", qualityChecks: expect.arrayContaining([expect.objectContaining({ key: "template_preset", status: "not_configured" })]) }), "test-server|test-tenant|test-user");
    expect(current.fileImportError).toBeNull();
    expect(current.queue).toHaveLength(1);
  });

  it("reports both saved and failed files when durable import succeeds only partly", async () => {
    mocks.spoolScanAsset.mockResolvedValueOnce(item({ status: "pending" })).mockRejectedValueOnce(new Error("磁盘写入失败"));
    await mount();
    const files = [new File(["1"], "kept.pdf"), new File(["2"], "failed.pdf")];
    await act(async () => { await current.handleFileSelection(files as unknown as FileList); });
    expect(current.queue).toHaveLength(1);
    expect(current.importResults).toEqual([expect.objectContaining({ filename: "kept.pdf", saved: true }), expect.objectContaining({ filename: "failed.pdf", saved: false })]);
    expect(current.fileImportError).toContain("写入");
  });

  it("refuses to change or remove a queue item after its remote upload starts", async () => {
    mocks.listDurableScanQueue.mockResolvedValueOnce([item({ status: "failed", remoteUploadId: "remote-started" })]);
    await mount();
    await act(async () => { await current.removeQueueItem("scan-1", true); });
    expect(mocks.removeUnstartedScanQueueItem).not.toHaveBeenCalled(); expect(current.queue).toHaveLength(1);
    expect(current.fileImportError).toContain("已启动上传");
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
    expect(current.importResults).toEqual([]);
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

  it("continues with the second file after the first file inspection fails", async () => {
    mocks.inspectScanFile.mockRejectedValueOnce(new Error("first file invalid")).mockResolvedValueOnce([]);
    mocks.spoolScanAsset.mockResolvedValueOnce(item({ id: "second", status: "pending" }));
    await mount();
    const files = [new File(["1"], "bad.pdf"), new File(["2"], "good.pdf")];
    await act(async () => { await current.handleFileSelection(files as unknown as FileList); });
    expect(mocks.inspectScanFile.mock.calls.map(([file]) => file.name)).toEqual(["bad.pdf", "good.pdf"]);
    expect(mocks.spoolScanAsset).toHaveBeenCalledOnce();
    expect(mocks.spoolScanAsset).toHaveBeenCalledWith(expect.objectContaining({ file: files[1], pageNo: 2 }), "test-server|test-tenant|test-user");
    expect(current.queue.map((entry) => entry.id)).toEqual(["second"]);
    expect(current.importResults).toEqual([expect.objectContaining({ filename: "bad.pdf", saved: false }), expect.objectContaining({ filename: "good.pdf", saved: true })]);
    expect(current.fileImportError).toContain("部分文件检查失败");
  });

  it.each([
    { label: "missing exam", overrides: { exams: [] } },
    { label: "missing batch", overrides: { captureBatches: [] } },
    { label: "empty selected exam", overrides: { selectedExamId: "" } },
    { label: "other exam batch", overrides: { captureBatches: [{ ...scanContext().batch, exam_id: "other" }] } },
    { label: "completed batch", overrides: { captureBatches: [{ ...scanContext().batch, status: "completed" as const }] } },
    { label: "cancelled batch", overrides: { captureBatches: [{ ...scanContext().batch, status: "cancelled" as const }] } }
  ])("refuses import for $label before inspecting or preserving files", async ({ overrides }) => {
    await mount(undefined, false, scanContext(), overrides);
    await act(async () => { await current.handleFileSelection([new File(["scan"], "scan.pdf")] as unknown as FileList); });
    expect(current.fileImportError).toContain("真实考试");
    expect(mocks.inspectScanFile).not.toHaveBeenCalled();
    expect(mocks.spoolScanAsset).not.toHaveBeenCalled();
  });

  it.each([
    { label: "missing item", patch: {}, missing: true, inFlight: false },
    { label: "remote upload", patch: { remoteUploadId: "remote" }, missing: false, inFlight: false },
    { label: "remote asset", patch: { fileAssetId: "asset" }, missing: false, inFlight: false },
    { label: "uploading", patch: { status: "uploading" as const }, missing: false, inFlight: false },
    { label: "succeeded", patch: { status: "succeeded" as const }, missing: false, inFlight: false },
    { label: "in flight", patch: {}, missing: false, inFlight: true },
    { label: "unknown exam", patch: { examId: undefined }, missing: false, inFlight: false },
    { label: "other exam", patch: { examId: "other" }, missing: false, inFlight: false },
    { label: "other batch", patch: { captureBatchId: "other" }, missing: false, inFlight: false }
  ])("does not open replacement selection for $label", async ({ patch, missing, inFlight }) => {
    mocks.listDurableScanQueue.mockResolvedValueOnce(missing ? [] : [item({ status: "pending", examId: "exam-a", captureBatchId: "batch-a", ...patch })]);
    await mount();
    const input = document.createElement("input");
    const click = vi.spyOn(input, "click").mockImplementation(() => undefined);
    current.fileInputRef.current = input;
    if (inFlight) current.uploadInFlightRef.current.add("scan-1");
    await act(async () => { current.chooseReplacement("scan-1"); });
    expect(click).not.toHaveBeenCalled();
    expect(current.fileImportError).toContain("所属考试与批次");
  });

  it.each(["new", "failed", "same"] as const)("preserves replacement ordering for %s durable writes", async (outcome) => {
    mocks.listDurableScanQueue.mockResolvedValueOnce([item({ status: "pending", examId: "exam-a", captureBatchId: "batch-a" })]);
    if (outcome === "failed") mocks.spoolScanAsset.mockRejectedValueOnce(new Error("disk failed"));
    else mocks.spoolScanAsset.mockResolvedValueOnce(item({ id: outcome === "same" ? "scan-1" : "new", status: "pending" }));
    // 本机移出命令开始时，新原件必须已经进入当前队列；写入失败时不能移出旧件。
    if (outcome === "new") mocks.removeUnstartedScanQueueItem.mockImplementationOnce(async () => {
      expect(current.queueRef.current.map((entry) => entry.id)).toEqual(["new", "scan-1"]);
    });
    await mount();
    const input = document.createElement("input");
    const click = vi.spyOn(input, "click").mockImplementation(() => undefined);
    current.fileInputRef.current = input;
    await act(async () => { current.chooseReplacement("scan-1"); });
    expect(click).toHaveBeenCalledOnce();
    await act(async () => { await current.handleFileSelection([new File(["fixed"], "fixed.pdf")] as unknown as FileList); });
    if (outcome === "new") {
      expect(mocks.removeUnstartedScanQueueItem).toHaveBeenCalledWith("asset-1", "test-server|test-tenant|test-user");
      expect(current.queue.map((entry) => entry.id)).toEqual(["new"]);
    } else {
      expect(mocks.removeUnstartedScanQueueItem).not.toHaveBeenCalled();
      expect(current.queue.map((entry) => entry.id)).toEqual(["scan-1"]);
      expect(current.fileImportError).toContain(outcome === "same" ? "所选文件与已有原件相同" : "写入");
    }
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

  it.each(["missing", "invalid"] as const)("refuses new import while the current template is %s", async (status) => {
    const context = scanContext(); context.template_status = status; delete context.template;
    await mount(undefined, false, context);
    await act(async () => { await current.handleFileSelection([new File(["scan"], "scan.pdf")] as unknown as FileList); });
    expect(mocks.spoolScanAsset).not.toHaveBeenCalled(); expect(current.fileImportError).toContain("已核实的答题卡模板");
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
