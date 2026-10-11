// @vitest-environment jsdom
import type { ComponentProps } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ScannerPreflightResult, ScannerProfile } from "../../lib/scannerProfile";
import type { SyncQueueItem } from "../../types";
import { ScanWorkspace } from "./ScanWorkspace";

const runtime = vi.hoisted(() => ({ isTauriRuntime: vi.fn(() => true) }));
vi.mock("../../lib/localRuntime", () => ({ isTauriRuntime: runtime.isTauriRuntime }));

type WorkspaceProps = ComponentProps<typeof ScanWorkspace>;

const profile: ScannerProfile = {
  id: "profile-1", name: "扫描站", deviceFingerprint: "device-1", dpi: 300,
  duplex: true, colorMode: "grayscale", paperSize: "A4", autoRotate: true,
  compression: "tiff", templatePreset: "locked-template", createdAt: "", updatedAt: ""
};

function preflight(readyToScan = true): ScannerPreflightResult {
  return {
    readyToScan, profile, acquisitionStatus: "requires_device_validation",
    checks: [
      { key: "driver", label: "驱动检查", status: "passed", blocking: true, detail: "驱动可用" },
      { key: "template", label: "模板检查", status: "failed", blocking: true, detail: "模板不一致" },
      { key: "network", label: "网络检查", status: "warning", blocking: false, detail: "离线采集" }
    ]
  };
}

function queueItem(status: SyncQueueItem["status"]): SyncQueueItem {
  return { id: status, title: "样张", kind: "scan_upload", status, progress: 0, detail: "", updatedAt: "", examId: "exam-1", captureBatchId: "batch-1" };
}

function workspaceProps(): WorkspaceProps {
  const queue = [queueItem("pending"), queueItem("failed")];
  return {
    token: "token", isOnline: true,
    capture: {
      exams: [{ id: "exam-1", name: "考试一" } as never], selectedExamId: "exam-1", setSelectedExamId: vi.fn(), examError: null,
      isLoadingExams: false, scanSubmissionId: "submission-1", setScanSubmissionId: vi.fn(),
      captureBatchId: "batch-1", setCaptureBatchId: vi.fn(), captureBatches: [{ id: "batch-1", exam_id: "exam-1", status: "uploading" } as never],
      setCaptureBatches: vi.fn(), isLoadingCaptureBatches: false, scanStartPage: 1,
      setScanStartPage: vi.fn(), handleLoadExams: vi.fn(), handleLoadCaptureBatches: vi.fn(), handleOpenQueueContext: vi.fn(),
      processingDetail: null, linkedSubmissionIds: ["submission-1"], processingError: null, loadingProcessing: false, handleRefreshProcessing: vi.fn(),
      scanContextError: null, loadingScanContext: false, handleRefreshScanContext: vi.fn(),
      scanContext: { batch: { id: "batch-1", exam_id: "exam-1", name: "上午批次", created_at: "2026-10-10T01:00:00Z", status: "uploading", source_type: "scan", file_count: 0, page_count: 0 }, exam: { id: "exam-1", name: "考试一", subject: "english" }, template_status: "ready",
        template: { id: "locked-template", name: "英语答题卡", version_no: 3, page_count: 1, content_hash: "hash", binding_revision: 1, layout: { pages: [{ page_no: 1, width: 1000, height: 1400, registration_marks: [], identity_regions: [], question_regions: [] }] } } }
    },
    scanner: {
      scannerProfiles: [profile], selectedScannerProfileId: profile.id, setSelectedScannerProfileId: vi.fn(),
      scannerPreflight: preflight(), setScannerPreflight: vi.fn(), scannerIntegration: null,
      scannerPreflightError: null, isCheckingScannerPreflight: false,
      expectedPaperSize: "A4", setExpectedPaperSize: vi.fn(), expectedTemplatePreset: "locked-template",
      setExpectedTemplatePreset: vi.fn(), expectedDuplex: true, setExpectedDuplex: vi.fn(),
      expectedDpi: 300, setExpectedDpi: vi.fn(), isScannerProfileModalOpen: false,
      setIsScannerProfileModalOpen: vi.fn(), scannerDevices: [], isLoadingScannerDevices: false,
      isSavingScannerProfile: false, scannerProfileName: "扫描站", setScannerProfileName: vi.fn(),
      scannerDeviceFingerprint: "device-1", setScannerDeviceFingerprint: vi.fn(),
      handleLoadScannerProfiles: vi.fn(), handleOpenScannerProfileSetup: vi.fn(),
      handleSaveScannerProfile: vi.fn(), handleScannerPreflight: vi.fn()
    },
    scanQueue: {
      fileImportError: null,
      importResults: [], removeQueueItem: vi.fn(), reinspectQueueItem: vi.fn(), chooseReplacement: vi.fn(),
      queue, offlineDraftCount: 0, setOfflineDraftCount: vi.fn(), fileInputRef: { current: null },
      fileBufferRef: { current: new Map() }, uploadInFlightRef: { current: new Set() },
      queueRef: { current: queue }, durablePersistenceRef: { current: Promise.resolve() },
      updateQueue: vi.fn(), handleFileSelection: vi.fn(), clearSucceededQueueItems: vi.fn(),
      queueRestoreStatus: "ready", queueRestoreError: null, restoreQueue: vi.fn(), queueContextError: null, loadingQueueContext: false, refreshQueueContext: vi.fn()
    },
    uploadSync: { uploadQueueItem: vi.fn(), uploadQueueItems: vi.fn() },
    quality: { qualityResult: null, qualityError: null, isCheckingQuality: false, canRunQualityCheck: true, handleRunQualityCheck: vi.fn(),
      imageQuality: null, imageQualityError: null, isReadingImageQuality: false, handleRefreshImageQuality: vi.fn() }
  };
}

function renderWorkspace(props: WorkspaceProps) {
  const container = document.createElement("div");
  container.innerHTML = renderToStaticMarkup(<ScanWorkspace {...props} />);
  return container;
}

function button(container: HTMLElement, label: string) {
  const result = Array.from(container.querySelectorAll("button")).find((entry) => entry.textContent === label);
  expect(result).toBeDefined();
  return result!;
}

describe("scan workspace quality presentation", () => {
  beforeEach(() => { runtime.isTauriRuntime.mockReturnValue(true); });
  afterEach(() => { vi.clearAllMocks(); });

  it("keeps passed, failed and warning preflight colors with their original labels", () => {
    const container = renderWorkspace(workspaceProps());
    const tags = Array.from(container.querySelectorAll(".quality-checks .ant-tag"));
    expect(tags.map((tag) => tag.textContent)).toEqual(["驱动检查", "模板检查", "网络检查"]);
    expect(tags.map((tag) => tag.className)).toEqual([
      expect.stringContaining("ant-tag-success"),
      expect.stringContaining("ant-tag-error"),
      expect.stringContaining("ant-tag-warning")
    ]);
  });

  it("shows the original empty state before the server quality check", () => {
    const container = renderWorkspace(workspaceProps());
    expect(container.textContent).toContain("尚未检查页数完整性");
    expect(container.querySelector(".quality-result")).toBeNull();
  });

  it("keeps the valid tag and no-issues message", () => {
    const props = workspaceProps();
    props.quality.qualityResult = { valid: true, issues: [] };
    const container = renderWorkspace(props);
    expect(container.querySelector(".quality-result .ant-tag-success")?.textContent).toBe("页数完整");
    expect(container.querySelector(".quality-result p")?.textContent).toContain("图像质量请以下方服务端结论为准");
    expect(container.textContent).toContain("图像检查结果尚未确认"); expect(container.textContent).not.toContain("图像质量已通过");
  });

  it("keeps issue order and filters unreadable server messages as plain text", () => {
    const props = workspaceProps();
    props.quality.qualityResult = {
      valid: false,
      issues: [
        { code: "quality", message: "internal quality failure" },
        { code: "quality", message: "请确认答题卡边框完整。" }
      ]
    };
    const container = renderWorkspace(props);
    expect(container.querySelector(".quality-result .ant-tag-warning")?.textContent).toBe("页数需核对");
    expect(Array.from(container.querySelectorAll(".quality-result p"), (entry) => entry.textContent))
      .toEqual(["页数完整性未通过，请核对缺页和页码", "请确认答题卡边框完整。"]);
  });

  it.each([
    [true, true, true, false, false, false],
    [true, true, false, false, false, false],
    [true, false, true, false, true, true],
    [false, true, true, false, true, true]
  ] as const)("keeps button gates for login %s, online %s and preflight %s", (loggedIn, online, ready, fileDisabled, uploadDisabled, qualityDisabled) => {
    const props = { ...workspaceProps(), token: loggedIn ? "token" : null, isOnline: online };
    props.scanner.scannerPreflight = preflight(ready);
    const container = renderWorkspace(props);
    expect(button(container, "批量选择 PDF/图片（含 TIFF）").disabled).toBe(fileDisabled);
    expect(button(container, "上传当前批次 1 个等待项").disabled).toBe(uploadDisabled);
    expect(button(container, "重试当前批次 1 个失败项").disabled).toBe(uploadDisabled);
    expect(button(container, "检查页数完整性").disabled).toBe(qualityDisabled);
  });

  it("keeps browser file selection available without a desktop preflight", () => {
    runtime.isTauriRuntime.mockReturnValue(false);
    const props = workspaceProps();
    props.scanner.scannerPreflight = null;
    const container = renderWorkspace(props);
    expect(container.textContent).toContain("浏览器开发模式不提供扫描设备能力");
    expect(button(container, "批量选择 PDF/图片（含 TIFF）").disabled).toBe(false);
  });

  it("imports files without a connected scanner or successful preflight", () => {
    const props = workspaceProps(); props.scanner.scannerDevices = []; props.scanner.scannerProfiles = [];
    props.scanner.selectedScannerProfileId = ""; props.scanner.scannerPreflight = null;
    const container = renderWorkspace(props);
    expect(button(container, "批量选择 PDF/图片（含 TIFF）").disabled).toBe(false);
    expect(container.textContent).toContain("已有文件导入无需设备检查");
    expect(container.textContent).toContain("不代表原件已匹配模板");
  });

  it.each(["unchecked", "review", "failed", "passed"])("shows the real image-quality state %s independently of complete pages", (status) => {
    const props = workspaceProps(); props.quality.qualityResult = { valid: true, issues: [] };
    props.quality.imageQuality = { id: "submission-1", exam_id: "exam-1", quality_status: status, quality_issues: [], expected_page_count: 2, actual_page_count: 2 };
    const text = renderWorkspace(props).textContent!;
    expect(text).toContain("页数完整");
    expect(text.includes("图像质量已通过")).toBe(status === "passed");
    if (status !== "passed") expect(text).toMatch(/图像质量尚未通过检查|图像质量待人工核对|图像质量未通过/);
  });

  it.each(["completed", "cancelled"])("retains historical %s results with import and upload disabled", (status) => {
    const props = workspaceProps(); props.capture.captureBatches[0].status = status;
    const host = renderWorkspace(props);
    expect(host.textContent).toContain("当前批次仅供查看处理结果");
    expect(button(host, "批量选择 PDF/图片（含 TIFF）").disabled).toBe(true);
    expect(button(host, "上传当前批次 1 个等待项").disabled).toBe(true);
    expect(button(host, "重试当前批次 1 个失败项").disabled).toBe(true);
    expect(button(host, "检查页数完整性").disabled).toBe(true);
    expect(button(host, "刷新图像检查状态").disabled).toBe(false);
  });

  it.each(["missing_template", "missing_batch", "wrong_exam", "completed", "cancelled", "bad_dpi"])("blocks file import when context is %s", (failure) => {
    const props = workspaceProps();
    if (failure === "missing_template") props.capture.scanContext = null;
    if (failure === "bad_dpi") props.scanner.expectedDpi = 100;
    if (failure === "missing_batch") props.capture.captureBatches = [];
    if (failure === "wrong_exam") props.capture.captureBatches[0].exam_id = "other-exam";
    if (failure === "completed" || failure === "cancelled") props.capture.captureBatches[0].status = failure;
    expect(button(renderWorkspace(props), "批量选择 PDF/图片（含 TIFF）").disabled).toBe(true);
  });

  it("uses the authorized template name and version rather than asking the scanner to type an internal identifier", () => {
    const props = workspaceProps(); props.scanner.expectedTemplatePreset = "obsolete-local-preset";
    const host = renderWorkspace(props);
    expect(host.textContent).toContain("英语答题卡 · 第 3 版 · 1 页"); expect(host.textContent).toContain("无需填写内部编号");
    expect(host.textContent).not.toContain("本批答题卡模板标识"); expect(host.textContent).not.toContain("obsolete-local-preset");
    expect(button(host, "批量选择 PDF/图片（含 TIFF）").disabled).toBe(false);
  });

  it.each(["loading", "error"] as const)("shows the true local queue %s state and suppresses the misleading choose-files empty state", (status) => {
    const props = workspaceProps(); props.scanQueue.queue = []; props.scanQueue.queueRestoreStatus = status;
    if (status === "error") props.scanQueue.queueRestoreError = "磁盘读取失败";
    const host = renderWorkspace(props);
    expect(host.textContent).toContain(status === "error" ? "本机上传队列读取失败" : "正在读取本机上传队列");
    expect(host.textContent).not.toContain("尚未选择文件"); expect(button(host, "批量选择 PDF/图片（含 TIFF）").disabled).toBe(true);
    if (status === "error") expect(button(host, "重新读取本机队列").disabled).toBe(false);
  });

  it("恢复完成且队列为空时使用原有文件空态，并允许有效批次导入", () => {
    const props = workspaceProps(); props.scanQueue.queue = [];
    const host = renderWorkspace(props);
    expect(host.textContent).toContain("尚未选择文件");
    expect(host.textContent).not.toContain("原队列尚未核实");
    expect(host.textContent).not.toContain("正在读取本机队列");
    expect(button(host, "批量选择 PDF/图片（含 TIFF）").disabled).toBe(false);
  });
});
