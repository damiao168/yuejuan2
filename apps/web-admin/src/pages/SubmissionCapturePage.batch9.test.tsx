// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { TableColumnsType, UploadProps } from "antd";
import type { ColumnType } from "antd/es/table";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../api/exams";
import type { Student } from "../api/org";
import type { OcrTask, SubmissionPage } from "../api/submissions";
import type { CapturePage, SubmissionView } from "../features/capture/captureQueries";
import type { capturePageColumns, captureSubmissionColumns } from "../features/capture/submissionColumns";
import { SubmissionCapturePage } from "./SubmissionCapturePage";

const mocks = vi.hoisted(() => ({
  exams: vi.fn(), capture: vi.fn(), next: vi.fn(), detail: vi.fn(),
  create: vi.fn(), upload: vi.fn(), addPage: vi.fn(), integrity: vi.fn(), quality: vi.fn(),
  download: vi.fn(), replaceUpload: vi.fn(), replace: vi.fn(), getSubmission: vi.fn(), ocrTasks: vi.fn(),
  uploadProps: vi.fn(), columns: vi.fn(), error: vi.fn(), success: vi.fn(), tableMount: vi.fn(),
}));

vi.mock("../api/exams", () => ({ listExams: mocks.exams }));
vi.mock("../api/files", () => ({
  downloadFileBlob: mocks.download, uploadFileWithProgress: mocks.upload, uploadFile: mocks.replaceUpload,
}));
vi.mock("../api/capture", () => ({ runImageQualityCheck: mocks.quality }));
vi.mock("../api/submissions", () => ({
  createSubmission: mocks.create, addSubmissionPage: mocks.addPage, runQualityCheck: mocks.integrity,
  getSubmission: mocks.getSubmission, listOcrTasks: mocks.ocrTasks, replaceSubmissionPage: mocks.replace,
}));
vi.mock("../api/client", () => ({
  ApiClientError: class extends Error {},
  getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback,
  getSafeUserText: (value: string, fallback: string) => value || fallback,
}));
vi.mock("../features/capture/captureQueries", () => ({
  loadCapturePage: mocks.capture, loadNextCapturePage: mocks.next, buildSubmissionView: mocks.detail,
}));
vi.mock("../router/query", () => ({ hashQueryParam: () => "" }));
vi.mock("../components/OcrWorkerAlert", () => ({ OcrWorkerAlert: () => null }));
vi.mock("../components/ProcessingOperationsPanel", () => ({ ProcessingOperationsPanel: () => null }));
vi.mock("../features/capture/submissionColumns", async () => {
  const React = await import("react");
  return {
    captureSubmissionColumns: (options: Parameters<typeof captureSubmissionColumns>[0]) => {
      mocks.columns(options);
      return [{ render: (_value: unknown, row: SubmissionView) => React.createElement("div", null,
        React.createElement("button", { onClick: () => void options.openPages(row) }, `页面 ${row.submission.id}`),
        React.createElement("button", { onClick: () => void options.openOcrDrawer(row) }, `OCR ${row.submission.id}`)) }];
    },
    capturePageColumns: (options: Parameters<typeof capturePageColumns>[0]) => [{
      render: (_value: unknown, page: SubmissionPage) => React.createElement("button", {
        onClick: () => void options.previewPage(page),
      }, `预览 ${page.id}`),
    }],
    captureOcrTaskColumns: () => [],
  };
});
vi.mock("../components/PageState", async () => {
  const React = await import("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("p", { "data-state": "loading" }, label),
    EmptyState: ({ title, description }: { title: string; description: string }) => React.createElement("div", { "data-state": "empty" }, title, description),
    ErrorState: ({ message, onRetry }: { message: string; onRetry?: () => void }) => React.createElement("div", { "data-state": "error" },
      message, onRetry ? React.createElement("button", { onClick: onRetry }, "重试") : null),
  };
});
vi.mock("@edugrade/ui", async () => {
  const React = await import("react");
  return {
    PageHeader: ({ title, actions }: { title: string; actions: ReactNode }) => React.createElement("header", null, title, actions),
    FilterBar: ({ children }: { children: ReactNode }) => React.createElement("div", null, children),
    DataTableShell: ({ title, description, children }: { title: string; description: string; children: ReactNode }) =>
      React.createElement("section", { "data-table-shell": true }, title, React.createElement("p", null, description), children),
  };
});
vi.mock("../components/ResponsiveTable", async () => {
  const React = await import("react");
  function ResponsiveTable<T extends object>({ columns, dataSource = [], locale, rowClassName }: {
    columns: TableColumnsType<T>; dataSource?: T[]; locale?: { emptyText?: ReactNode }; rowClassName?: (row: T) => string;
  }) {
    const [instance] = React.useState(() => { mocks.tableMount(); return mocks.tableMount.mock.calls.length; });
    const flatColumns = columns.flatMap((column) => "children" in column ? column.children : [column]);
    const cell = (column: ColumnType<T>, row: T, index: number) => {
      const value = typeof column.dataIndex === "string" ? (row as Record<string, unknown>)[column.dataIndex] : undefined;
      const rendered = column.render?.(value, row, index);
      if (rendered && typeof rendered === "object" && "children" in rendered && "props" in rendered) return rendered.children;
      return rendered as ReactNode;
    };
    return React.createElement("div", { "data-table-instance": instance },
      dataSource.length === 0 ? locale?.emptyText : dataSource.map((row, index) => React.createElement("div", {
        key: index, "data-row-id": (row as { id?: string; submission?: { id: string } }).id ?? (row as { submission?: { id: string } }).submission?.id,
        className: rowClassName?.(row),
      }, flatColumns.map((column, columnIndex) => React.createElement("span", { key: columnIndex }, cell(column, row, index))))));
  }
  return { ResponsiveTable };
});
vi.mock("antd", async () => {
  const React = await import("react");
  const Wrap = ({ children, className }: { children: ReactNode; className?: string }) => React.createElement("div", { className }, children);
  const Descriptions = Object.assign(Wrap, { Item: ({ label, children }: { label: string; children: ReactNode }) => React.createElement("div", null, label, children) });
  const List = Object.assign(({ dataSource, renderItem, className }: {
    dataSource: { id: string }[]; renderItem: (item: { id: string }) => ReactNode; className?: string;
  }) => React.createElement("div", { className }, dataSource.map((item) => React.createElement(React.Fragment, { key: item.id }, renderItem(item)))), { Item: Wrap });
  return {
    App: { useApp: () => ({ message: { error: mocks.error, success: mocks.success } }) },
    Alert: ({ message, description }: { message: string; description: string }) => React.createElement("div", null, message, description),
    Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) =>
      React.createElement("button", { onClick, disabled, "data-loading": String(Boolean(loading)) }, children),
    Drawer: ({ title, open, onClose, children }: { title: string; open: boolean; onClose: () => void; children: ReactNode }) =>
      open ? React.createElement("aside", { "data-drawer": title }, React.createElement("button", { onClick: onClose }, `关闭 ${title}`), children) : null,
    Input: ({ value, onChange, placeholder }: { value: string; onChange: React.ChangeEventHandler<HTMLInputElement>; placeholder: string }) =>
      React.createElement("input", { value, onChange, placeholder }),
    InputNumber: ({ value, onChange }: { value: number; onChange: (value: number) => void }) =>
      React.createElement("input", { type: "number", value, onChange: (event: React.ChangeEvent<HTMLInputElement>) => onChange(Number(event.target.value)) }),
    Select: ({ value, options, onChange, className }: { value?: string; options: { value: string; label: string }[]; onChange: (value: string) => void; className?: string }) =>
      React.createElement("select", { value: value ?? "", className, onChange: (event: React.ChangeEvent<HTMLSelectElement>) => onChange(event.target.value) },
        options.map((option) => React.createElement("option", { key: option.value, value: option.value }, option.label))),
    Upload: (props: UploadProps) => { mocks.uploadProps(props); return React.createElement("div", null, props.children); },
    Progress: ({ percent, status, className }: { percent: number; status?: string; className?: string }) => React.createElement("progress", { value: percent, max: 100, "data-status": status, className }),
    Descriptions, List, Space: Wrap, Tooltip: Wrap,
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function exam(id = "exam-a"): Exam {
  return {
    id, tenant_id: "tenant-a", school_id: "school-a", name: id, subject: "mathematics", exam_type: "test",
    total_score: 100, status: "collecting", grading_mode: "ai_assisted", appeal_enabled: false,
    publish_policy: "manual", created_by: "teacher-a", class_ids: [], revision: 1,
  };
}

function row(id = "submission-a"): SubmissionView {
  return {
    submission: {
      id, tenant_id: "tenant-a", exam_id: "exam-a", student_id: "student-a", candidate_no: "考号甲",
      source_type: "image_upload", status: "created", expected_page_count: 1, actual_page_count: 1,
      quality_status: "unchecked", quality_issues: [], collected_by: "teacher-a", revision: 1, created_at: "2026-10-04T00:00:00Z",
    },
    pages: [{ id: "page-a", tenant_id: "tenant-a", submission_id: id, file_asset_id: "file-a", page_no: 1,
      status: "uploaded", quality_issues: [], created_at: "2026-10-04T00:00:00Z" }],
    ocrTasks: [], ocrNextCursor: "", ocrHasMore: false, segments: [],
  };
}

function task(id = "task-a"): OcrTask {
  return {
    id, tenant_id: "tenant-a", submission_id: "submission-a", status: "completed", engine: "ocr", engine_version: "v1",
    min_confidence: 0.8, result_count: 1, requires_human_review: false, requested_by: "teacher-a", created_at: "2026-10-04T00:00:00Z",
    results: [{ id: "result-a", tenant_id: "tenant-a", ocr_task_id: id, submission_id: "submission-a", submission_page_id: "page-a",
      text: "识别文本", bbox: [0, 0, 10, 10], confidence: 0.956, ocr_engine: "ocr", ocr_version: "v1", created_at: "2026-10-04T00:00:00Z" }],
  };
}

const student: Student = { id: "student-a", tenant_id: "tenant-a", school_id: "school-a", class_id: "class-a", student_no: "0001", name: "学生甲", status: "active" };
function capture(rows = [row()]): CapturePage {
  return { rows, students: [student], nextCursor: "", hasMore: false };
}

describe("答题卡采集页面重构边界", () => {
  let root: Root;
  let host: HTMLDivElement;
  let props: Parameters<typeof SubmissionCapturePage>[0];
  let events: string[];

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    window.location.hash = "";
    props = { canManage: true, canReadStudentNames: true, initialExamId: "exam-a" };
    events = [];
    mocks.exams.mockResolvedValue({ exams: [exam(), exam("exam-b")] });
    mocks.capture.mockImplementation(async () => { events.push("refresh"); return capture(); });
    mocks.next.mockResolvedValue(capture([]));
    mocks.detail.mockResolvedValue(row());
    mocks.create.mockImplementation(async () => { events.push("create"); return { submission: row().submission }; });
    mocks.upload.mockImplementation(async (_file, _owner, progress: (percent: number) => void) => { events.push("upload"); progress(100); return { file: { id: "uploaded-file" } }; });
    mocks.addPage.mockImplementation(async () => { events.push("associate"); return {}; });
    mocks.integrity.mockImplementation(async () => { events.push("integrity"); return { result: { valid: true, issues: [] } }; });
    mocks.quality.mockImplementation(async () => { events.push("quality"); return {}; });
    mocks.ocrTasks.mockResolvedValue({ tasks: [], next_cursor: "", has_more: false });
    vi.stubGlobal("URL", Object.assign(class extends URL {}, { createObjectURL: vi.fn(() => "blob:preview"), revokeObjectURL: vi.fn() }));
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  async function renderPage(next: Partial<typeof props> = {}) {
    props = { ...props, ...next };
    await act(async () => { root.render(<SubmissionCapturePage {...props} />); });
  }

  function button(label: string) {
    const target = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label);
    expect(target).toBeDefined();
    return target!;
  }

  async function click(label: string) {
    await act(async () => { button(label).click(); });
  }

  async function upload(name = "考号甲.png") {
    const calls = mocks.uploadProps.mock.calls;
    const options = calls[calls.length - 1][0] as UploadProps;
    const onSuccess = vi.fn(() => { events.push("success"); });
    const onError = vi.fn();
    await act(async () => {
      options.customRequest!({ file: new File(["scan"], name, { type: "image/png" }), filename: "file", action: "", method: "POST", onSuccess, onError }, { defaultRequest: vi.fn() });
    });
    return { onSuccess, onError };
  }

  it("考试列表加载先于采集错误，成功后再显示采集失败", async () => {
    const pending = deferred<{ exams: Exam[] }>();
    mocks.exams.mockReturnValue(pending.promise);
    mocks.capture.mockRejectedValue(new Error("采集读取失败"));
    await renderPage();
    expect(host.querySelector('[data-state="loading"]')?.textContent).toBe("正在读取考试列表");
    expect(host.querySelector('[data-state="error"]')).toBeNull();
    await act(async () => { pending.resolve({ exams: [exam()] }); });
    expect(host.querySelector('[data-state="error"]')?.textContent).toContain("采集读取失败");
  });

  it("考试列表失败可重试，空考试列表不展示残留采集记录", async () => {
    mocks.exams.mockRejectedValueOnce(new Error("考试读取失败")).mockResolvedValueOnce({ exams: [] });
    await renderPage();
    expect(host.querySelector('[data-state="error"]')?.textContent).toContain("考试读取失败");
    await click("重试");
    expect(host.querySelector('[data-state="empty"]')?.textContent).toContain("暂无考试");
    expect(host.querySelector("[data-table-shell]")).toBeNull();
  });

  it("采集加载完成后保留过滤数、失败后缀和详情警告", async () => {
    const pending = deferred<CapturePage>();
    mocks.capture.mockReturnValue(pending.promise);
    await renderPage();
    expect(host.querySelector('[data-state="loading"]')?.textContent).toBe("正在读取答题卡记录");
    const failed = row("failed-row");
    failed.submission.quality_status = "failed";
    failed.detailError = "详情读取失败";
    await act(async () => { pending.resolve(capture([row(), failed])); });
    expect(host.querySelector("[data-table-shell] p")?.textContent).toBe("2 / 2 条记录，已完成 0 份；1 份处理失败，可直接重试");
    expect(host.querySelector('[data-row-id="failed-row"]')?.className).toBe("row-with-warning");
  });

  it("考试切换后迟到的列表结果不能覆盖新考试", async () => {
    const pending = deferred<CapturePage>();
    mocks.capture.mockImplementation((id: string) => id === "exam-a" ? pending.promise : Promise.resolve(capture([row("new-row")])));
    await renderPage();
    await renderPage({ initialExamId: "exam-b" });
    expect(host.querySelector('[data-row-id="new-row"]')).not.toBeNull();
    await act(async () => { pending.resolve(capture([row("old-row")])); });
    expect(host.querySelector('[data-row-id="old-row"]')).toBeNull();
    expect(host.querySelector('[data-row-id="new-row"]')).not.toBeNull();
  });

  it("上传按创建、上传、页面关联、完整性、图像质检、回调、刷新顺序执行", async () => {
    await renderPage();
    events.length = 0;
    const result = await upload();
    expect(events).toEqual(["create", "upload", "associate", "integrity", "quality", "success", "refresh"]);
    expect(mocks.create).toHaveBeenCalledWith("exam-a", { candidate_no: "考号甲", source_type: "image_upload", expected_page_count: 1 });
    expect(mocks.upload.mock.calls[0][1]).toEqual({ owner_type: "submission", owner_id: "submission-a", school_id: "school-a", exam_id: "exam-a", submission_id: "submission-a" });
    expect(mocks.addPage).toHaveBeenCalledWith("submission-a", "uploaded-file", 1);
    expect(result.onSuccess).toHaveBeenCalledOnce();
    expect(result.onError).not.toHaveBeenCalled();
    expect(host.querySelector('.upload-queue-item progress')?.getAttribute("data-status")).toBe("success");
  });

  it("完整性未通过仍算上传成功，但不提交图像质检", async () => {
    mocks.integrity.mockResolvedValue({ result: { valid: false, issues: [] } });
    await renderPage();
    const result = await upload();
    expect(mocks.quality).not.toHaveBeenCalled();
    expect(result.onSuccess).toHaveBeenCalledOnce();
    expect(host.querySelector(".upload-queue-item")?.textContent).toContain("已上传，答卷完整性需处理");
  });

  it.each(["create", "upload", "addPage"] as const)("%s 在页面关联成功前失败时报告上传失败", async (stage) => {
    mocks[stage].mockRejectedValue(new Error("关联前失败"));
    await renderPage();
    const result = await upload();
    expect(result.onSuccess).not.toHaveBeenCalled();
    expect(result.onError).toHaveBeenCalledOnce();
    expect(mocks.integrity).not.toHaveBeenCalled();
    expect(host.querySelector('.upload-queue-item progress')?.getAttribute("data-status")).toBe("exception");
  });

  it.each(["integrity", "quality"] as const)("%s 在页面关联后失败时保留已上传事实并刷新", async (stage) => {
    // 页面已经关联，后续检测失败不能引导用户再次提交同一文件。
    mocks[stage].mockRejectedValue(new Error("后续处理失败"));
    await renderPage();
    mocks.capture.mockClear();
    const result = await upload();
    expect(result.onSuccess).toHaveBeenCalledOnce();
    expect(result.onError).not.toHaveBeenCalled();
    expect(mocks.capture).toHaveBeenCalledOnce();
    expect(host.querySelector(".upload-queue-item")?.textContent).toContain("后续处理失败");
    expect(host.querySelector('.upload-queue-item progress')?.getAttribute("data-status")).toBe("success");
  });

  it("无考试时上传入口禁用且直接回调也不会创建记录", async () => {
    mocks.exams.mockResolvedValue({ exams: [] });
    await renderPage();
    expect(button("上传答题卡").disabled).toBe(true);
    const result = await upload();
    expect(result.onError).toHaveBeenCalledOnce();
    expect(mocks.create).not.toHaveBeenCalled();
    expect(host.querySelector(".upload-queue-item")?.textContent).toContain("请先选择考试");
  });

  it("只读权限重渲染保留搜索焦点、表格实例和上传队列", async () => {
    await renderPage();
    await upload();
    const input = host.querySelector<HTMLInputElement>('input[placeholder="搜索学生或准考证号"]')!;
    input.focus();
    const table = host.querySelector("[data-table-instance]");
    await renderPage({ canManage: false });
    expect(document.activeElement).toBe(input);
    expect(host.querySelector("[data-table-instance]")).toBe(table);
    expect(host.querySelectorAll(".upload-queue-item")).toHaveLength(1);
    expect(button("上传答题卡").disabled).toBe(true);
    await renderPage({ canReadStudentNames: false });
    expect(mocks.capture).toHaveBeenLastCalledWith("exam-a", false);
    await click("页面 submission-a");
    expect(host.querySelector('[data-drawer="答题卡页面"]')?.textContent).toContain("无姓名权限");
    expect(host.querySelector('[data-drawer="答题卡页面"]')?.textContent).not.toContain("学生甲");
  });

  it.each([
    { contentType: "image/png", label: "扫描图片", selector: ".page-preview img" },
    { contentType: "application/pdf", label: "PDF 文档", selector: ".page-preview iframe" },
    { contentType: "application/octet-stream", label: "其他文件", selector: ".page-preview" },
  ])("$contentType 预览保留内容分类及新窗口参数", async ({ contentType, label, selector }) => {
    mocks.download.mockResolvedValue({ blob: new Blob(["data"]), contentType });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await renderPage();
    await click("页面 submission-a");
    await click("预览 page-a");
    expect(host.querySelector(".page-preview p")?.textContent).toBe(label);
    expect(host.querySelector(selector)).not.toBeNull();
    if (contentType === "application/octet-stream") {
      expect(host.querySelector(".page-preview")?.textContent).toContain("该文件类型不支持内嵌预览");
      expect(host.querySelector(".page-preview img, .page-preview iframe")).toBeNull();
    }
    await click("新窗口打开");
    expect(open).toHaveBeenCalledWith("blob:preview", "_blank", "noopener,noreferrer");
  });

  it("关闭抽屉后迟到的下载不能创建或回填预览地址", async () => {
    const pending = deferred<{ blob: Blob; contentType: string }>();
    mocks.download.mockReturnValue(pending.promise);
    await renderPage();
    await click("页面 submission-a");
    await click("预览 page-a");
    await click("关闭 答题卡页面");
    await act(async () => { pending.resolve({ blob: new Blob(["data"]), contentType: "image/png" }); });
    expect(URL.createObjectURL).not.toHaveBeenCalled();
    expect(host.querySelector(".page-preview")).toBeNull();
  });

  it("OCR 加载后展示结果并按页面编号和百分比显示，不因普通重渲染丢失抽屉", async () => {
    const pending = deferred<SubmissionView>();
    mocks.detail.mockReturnValue(pending.promise);
    await renderPage();
    await click("OCR submission-a");
    expect(host.querySelector('[data-drawer="文字识别详情"]')?.textContent).toContain("正在读取识别详情");
    await act(async () => { pending.resolve({ ...row(), ocrTasks: [task()] }); });
    const result = host.querySelector(".ocr-result-block");
    expect(result?.textContent).toContain("第 1 页");
    expect(result?.textContent).toContain("96%");
    await renderPage({ canManage: false });
    expect(host.querySelector(".ocr-result-block")).toBe(result);
  });

  it("OCR 详情失败显示错误，重新打开空任务时显示空结果", async () => {
    mocks.detail.mockRejectedValueOnce(new Error("OCR 详情读取失败")).mockResolvedValueOnce(row());
    await renderPage();
    await click("OCR submission-a");
    expect(host.querySelector('[data-drawer="文字识别详情"] [data-state="error"]')?.textContent).toContain("OCR 详情读取失败");
    await click("关闭 文字识别详情");
    await click("OCR submission-a");
    expect(host.querySelector('[data-drawer="文字识别详情"]')?.textContent).toContain("暂无识别结果");
    expect(host.querySelector(".ocr-result-list")).toBeNull();
  });
});
