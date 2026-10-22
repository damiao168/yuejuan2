// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UploadProps } from "antd";
import type { CaptureBatch, CaptureBatchDetail, CapturePage, MatchingQueue, ProcessingSummary } from "../api/capture";
import type { SessionUser } from "../auth/session";
import { CaptureBatchPage } from "./CaptureBatchPage";

const mocks = vi.hoisted(() => ({
  detail: undefined as CaptureBatchDetail | undefined, summaries: {} as Record<string, ProcessingSummary>,
  batches: undefined as CaptureBatch[] | undefined, detailError: undefined as string | undefined, detailLoading: false,
  matching: undefined as MatchingQueue | undefined, matchingLoading: false,
  loadBatches: vi.fn(), loadDetail: vi.fn(), loadMatching: vi.fn(), resetDetail: vi.fn(), setBatches: vi.fn(), setSelectedId: vi.fn(), setMatching: vi.fn(),
  upload: vi.fn(), confirm: vi.fn(), retry: vi.fn(), success: vi.fn(), error: vi.fn(), info: vi.fn(), matchingProps: vi.fn(),
  matchStudent: vi.fn(), markUnknown: vi.fn(), matchPage: vi.fn(), lifecycle: vi.fn(), split: vi.fn(), merge: vi.fn(),
  form: { validateFields: vi.fn(), resetFields: vi.fn() },
  targets: vi.fn(), getBatch: vi.fn(),
  uploading: false, uploadProps: undefined as UploadProps | undefined,
}));

vi.mock("../api/capture", () => ({ confirmRegistration: mocks.confirm, retryRegistration: mocks.retry, findCaptureTargets: mocks.targets, getCaptureBatch: mocks.getBatch }));
vi.mock("../api/client", () => ({
  ApiClientError: class extends Error {},
  getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback,
}));
vi.mock("../features/capture/useCaptureBatchList", () => ({ useCaptureBatchList: () => ({
  batches: mocks.batches ?? (mocks.detail ? [mocks.detail.batch] : []), selectedId: "batch-16", setBatches: mocks.setBatches, setSelectedId: mocks.setSelectedId,
  loading: false, loadingMoreBatches: false, hasMoreBatches: false, error: null, loadBatches: mocks.loadBatches, loadMoreBatches: mocks.loadBatches,
}) }));
vi.mock("../features/capture/useCaptureBatchDetail", () => ({ useCaptureBatchDetail: () => ({
  detail: mocks.detail, detailError: mocks.detailError, detailLoading: mocks.detailLoading, matching: mocks.matching, matchingLoading: mocks.matchingLoading, setMatching: mocks.setMatching,
  processingSummaries: mocks.summaries, loadDetail: mocks.loadDetail, loadMatching: mocks.loadMatching, resetDetail: mocks.resetDetail,
}) }));
vi.mock("../features/capture/useCaptureUpload", () => ({ useCaptureUpload: () => ({ uploading: mocks.uploading, uploadSources: mocks.upload, retryUpload: vi.fn(), discardUpload: vi.fn() }) }));
vi.mock("../features/capture/useCaptureMatchActions", () => ({ useCaptureMatchActions: () => ({
  matchStudent: mocks.matchStudent, markUnknown: mocks.markUnknown, matchPage: mocks.matchPage,
  changePageLifecycle: mocks.lifecycle, splitSubmission: mocks.split, mergeSubmissions: mocks.merge,
}) }));
vi.mock("../features/capture/captureColumns", async (importOriginal) => ({
  ...await importOriginal<typeof import("../features/capture/captureColumns")>(),
  captureFileColumns: () => [], capturePageColumns: () => [],
}));
vi.mock("../features/capture/ImageQualityWorkspace", () => ({ ImageQualityWorkspace: () => null }));
vi.mock("../components/RegistrationCorrectionWorkspace", () => ({ RegistrationCorrectionWorkspace: () => null }));
vi.mock("../features/capture/MatchingWorkspace", () => ({ MatchingWorkspace: (props: { queue: MatchingQueue; canManage: boolean }) => {
  mocks.matchingProps(props);
  return <div data-matching="true">匹配 {props.queue.batch_id}</div>;
} }));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <div data-loading="true">{label}</div>,
  ErrorState: ({ message, onRetry }: { message: string; onRetry?: () => void }) => <div data-error="true">{message}<button onClick={onRetry}>重试</button></div>,
}));
vi.mock("../components/ResponsiveTable", () => ({ ResponsiveTable: ({ pagination }: { pagination: false | { pageSize: number } }) => <div data-page-size={pagination === false ? "none" : pagination.pageSize} /> }));
vi.mock("antd", () => {
  const Wrap = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error, info: mocks.info }, modal: { confirm: vi.fn() } }) },
    Form: Object.assign(Wrap, { Item: Wrap, useForm: () => [mocks.form] }),
    Button: ({ children, onClick, disabled, loading }: { children?: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) => <button onClick={onClick} disabled={disabled} data-loading={String(Boolean(loading))}>{children}</button>,
    Empty: Object.assign(({ description }: { description: ReactNode }) => <div>{description}</div>, { PRESENTED_IMAGE_SIMPLE: null }),
    Input: Object.assign(Wrap, { TextArea: Wrap }),
    Select: ({ options = [], onChange, disabled, value, "aria-label": label }: { options?: { value: string | number; label: string }[]; onChange?: (value: string | number) => void; disabled?: boolean; value?: string | number; "aria-label"?: string }) => <select aria-label={label} value={value ?? ""} disabled={disabled} onChange={event => onChange?.(options.find(option => String(option.value) === event.target.value)?.value ?? "")}><option value="">选择</option>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
    Alert: ({ message, description, action }: { message: ReactNode; description?: ReactNode; action?: ReactNode }) => <aside>{message}{description}{action}</aside>, Space: Wrap,
    Upload: (props: UploadProps) => { mocks.uploadProps = props; return <div>{props.children}</div>; },
    Modal: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <div>{children}</div> : null,
    Progress: ({ percent }: { percent: number }) => <progress value={percent} max={100} />,
    Tabs: ({ items }: { items: { key: string; children: ReactNode }[] }) => <div>{items.map((item) => <section key={item.key} data-tab={item.key}>{item.children}</section>)}</div>,
  };
});

function page(status = "processing"): CapturePage {
  return { id: "page-16", capture_batch_id: "batch-16", capture_file_id: "file-16", source_index: 1,
    submission_id: "submission-16", submission_page_id: "submission-page-16", sequence_no: 1, rotation_degrees: 0,
    decoded_file_asset_id: "decoded-16", status, revision: 3, page_identity: {}, match_candidates: [] };
}

function detail(status = "processing"): CaptureBatchDetail {
  return {
    batch: { id: "batch-16", tenant_id: "tenant-16", exam_id: "exam-16", name: "扫描批次", source_type: "web_upload", status: "needs_review",
      revision: 2, operator_id: "teacher-16", file_count: 1, page_count: 1, submission_count: 1, normal_count: 0, review_count: 1, failed_count: 0, created_at: "2026-10-05T00:00:00Z" },
    files: [], pages: [page(status)], processing_summaries: [],
  };
}

function summary(action = "confirm_registration"): ProcessingSummary {
  return { submission_id: "submission-16", total_pages: 1, ready_pages: 0, blocked_pages: 1, pending_pages: 0, can_complete: false,
    blockers: [{ page_id: "page-16", page_no: 1, registration_run_id: "run-16", stage: "registration", code: "needs_review", action }] };
}

function file(status = "uploaded"): CaptureBatchDetail["files"][number] {
  return { id: "file-16", capture_batch_id: "batch-16", file_asset_id: "asset-16", original_name: "答卷.pdf", content_type: "application/pdf",
    sha256: "hash-16", byte_size: 10, page_count: 1, status, created_at: "2026-10-05T00:00:00Z" };
}

describe("采集批次页条件展示与对齐回读", () => {
  let root: Root;
  let host: HTMLDivElement;
  const user: SessionUser = { id: "teacher-16", username: "teacher-16", displayName: "教师", name: "教师", role: "school_admin", roles: ["school_admin"],
    tenant: "tenant-16", school: "school-16", currentExam: "exam-16", permissions: [], publicComputer: false,
    organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-16"], gradeIds: [], classIds: [] } };
  const props = { user, examId: "exam-16", canManage: true };

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.detail = detail(); mocks.summaries = {}; mocks.matching = undefined; mocks.matchingLoading = false;
    mocks.batches = undefined; mocks.detailError = undefined; mocks.detailLoading = false;
    mocks.uploading = false; mocks.uploadProps = undefined;
    mocks.loadDetail.mockResolvedValue(undefined); mocks.confirm.mockResolvedValue({}); mocks.retry.mockResolvedValue({});
    localStorage.clear();
    location.hash = "";
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); location.hash = ""; });

  async function renderPage(canManage = true) {
    await act(async () => root.render(<CaptureBatchPage {...props} canManage={canManage} />));
  }
  async function click(label: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label);
    expect(button).toBeDefined();
    await act(async () => button!.click());
  }
  async function choosePages(pages = 2) {
    const selector = host.querySelector<HTMLSelectElement>('select[aria-label="每名学生答题卡页数"]');
    await act(async () => { selector!.value = String(pages); selector!.dispatchEvent(new Event("change", { bubbles: true })); });
  }

  it("先显式选择学生页数，再将 antd 的整次选择只提交一次", async () => {
    await renderPage();
    const importButton = () => [...host.querySelectorAll("button")].find(button => button.textContent === "导入文件");
    expect(importButton()?.disabled).toBe(true);
    expect(mocks.uploadProps?.disabled).toBe(true);
    await choosePages();
    expect(importButton()?.disabled).toBe(false);
    const files = [new File(["front"], "front.png"), new File(["back"], "back.png")] as Parameters<NonNullable<UploadProps["beforeUpload"]>>[1];
    await act(async () => { for (const current of files) await mocks.uploadProps!.beforeUpload!(current, files); });
    expect(mocks.upload).toHaveBeenCalledExactlyOnceWith(files, 2);
    expect(host.textContent).toContain("不会跨两次选择凑页");
  });

  it("上传期间冻结页数、批次选择和处理操作", async () => {
    await renderPage(); await choosePages();
    mocks.uploading = true; await renderPage();
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="每名学生答题卡页数"]')?.disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>(".capture-batch-row")?.disabled).toBe(true);
    expect(mocks.uploadProps?.disabled).toBe(true);
  });

  it.each(["waiting_files", "incomplete_pages", "identity_conflict"])("分组 %s 阻止答卷进入后续页面处理", async status => {
    mocks.detail!.files = [{ ...file("completed"), page_count: 1, upload_group_id: "group-16", upload_group_file_count: 1, upload_group_file_index: 1, pages_per_student: 2 }];
    mocks.detail!.pages[0].page_identity = { grouping_status: status };
    await renderPage();
    const processButton = [...host.querySelectorAll("button")].find(button => button.textContent === "等待答卷完整分组");
    expect(processButton?.disabled).toBe(true);
    if (status === "incomplete_pages") expect(host.textContent).toContain("缺少 1 页");
    if (status === "identity_conflict") expect(host.textContent).toContain("学生身份冲突");
  });

  it.each([
    [false, "首次读取失败", true, "error"],
    [false, undefined, false, "loading"],
    [false, undefined, true, "loading"],
    [true, undefined, true, "loading"],
    [true, "后台刷新失败", true, "loading"],
    [true, "后台刷新失败", false, "warning"],
    [true, undefined, false, "ready"],
  ] as const)("详情存在=%s、错误=%s、读取=%s 保留 %s 展示优先级", async (hasDetail, error, loading, mode) => {
    const current = detail();
    // 列表存在不代表详情已读到，首次失败也必须进入工作区的错误分支。
    mocks.batches = [current.batch]; mocks.detail = hasDetail ? current : undefined;
    mocks.detailError = error; mocks.detailLoading = loading;
    await renderPage();
    const workspace = host.querySelector(".capture-batch-workspace")!;
    expect(workspace.querySelector('[data-error="true"]') !== null).toBe(mode === "error");
    expect(workspace.querySelector('[data-loading="true"]') !== null).toBe(mode === "loading");
    expect(workspace.querySelector(".capture-batch-header") !== null).toBe(mode === "ready" || mode === "warning");
    expect(workspace.querySelector('[data-tab="files"]') !== null).toBe(mode === "ready" || mode === "warning");
    if (mode === "warning") {
      expect(workspace.textContent).toContain("后台刷新失败");
      expect([...workspace.querySelectorAll("button")].find(button => button.textContent === "导入文件")?.disabled).toBe(true);
    }
  });

  it.each([false, true])("有旧详情=%s 时重试仍读取当前批次，不改为后台静默刷新", async (hasDetail) => {
    const current = detail(); mocks.batches = [current.batch]; mocks.detail = hasDetail ? current : undefined; mocks.detailError = "读取失败";
    await renderPage(); mocks.loadDetail.mockClear();
    await click(hasDetail ? "重试读取批次" : "重试");
    expect(mocks.loadDetail).toHaveBeenCalledTimes(1);
    expect(mocks.loadDetail).toHaveBeenCalledWith("batch-16");
  });

  it.each([
    ["ready", true, true, true, false],
    ["ready", false, false, true, false],
    ["completed", true, false, false, true],
    ["completed", false, false, false, false],
    ["cancelled", true, false, false, false],
    ["needs_review", true, true, false, false],
  ] as const)("批次 %s、管理权限 %s 保留写入与完成/重开边界", async (status, canManage, writable, showComplete, showReopen) => {
    mocks.detail!.batch.status = status; mocks.detail!.files = [file()];
    await renderPage(canManage);
    if (writable) await choosePages();
    const findButton = (label: string) => [...host.querySelectorAll("button")].find(button => button.textContent === label);
    expect(findButton("导入文件")?.disabled).toBe(!writable);
    expect(findButton("开始处理")?.disabled).toBe(!writable);
    expect(findButton("完成批次") !== undefined).toBe(showComplete);
    if (showComplete) expect(findButton("完成批次")?.disabled).toBe(!writable);
    expect(findButton("重开批次") !== undefined).toBe(showReopen);
  });

  it.each([["uploaded", "开始处理"], ["failed", "处理待办文件"]] as const)("%s 文件保留处理按钮文案 %s", async (status, label) => {
    mocks.detail!.files = [file(status)];
    await renderPage();
    expect([...host.querySelectorAll("button")].find(button => button.textContent === label)?.disabled).toBe(false);
  });

  it.each([
    ["ready", true, "已完成版面对齐与题目切分"],
    ["processing", true, "已完成 0 页 · 需人工处理 1 页 · 处理中 0 页"],
    ["processing", false, "等待版面对齐或人工确认"],
  ] as const)("%s 页面和汇总 %s 保留处理文字优先级", async (status, withSummary, label) => {
    mocks.detail = detail(status);
    if (withSummary) mocks.summaries = { "submission-16": summary() };
    await renderPage();
    expect(host.querySelector(".capture-processing-row span")?.textContent).toBe(`1 页 · ${label}`);
    expect(host.querySelector('[data-tab="files"] [data-page-size="20"]')).not.toBeNull();
  });

  it("匹配读取优先于旧队列，成功后保留原对象和回调，缺失时显示空状态", async () => {
    const queue: MatchingQueue = { batch_id: "batch-16", exam_id: "exam-16", submissions: [], candidates: [] };
    mocks.matching = queue; mocks.matchingLoading = true;
    await renderPage();
    expect(host.querySelector('[data-tab="matching"]')?.textContent).toBe("正在加载考试名册");
    expect(mocks.matchingProps).not.toHaveBeenCalled();
    mocks.matchingLoading = false;
    await renderPage(false);
    const matchingProps = mocks.matchingProps.mock.calls[0][0];
    expect(matchingProps.queue).toBe(queue);
    expect(matchingProps.canManage).toBe(false);
    expect(matchingProps.onConfirmStudent).toBe(mocks.matchStudent);
    expect(matchingProps.onConfirmPage).toBe(mocks.matchPage);
    mocks.matching = undefined;
    await renderPage();
    expect(host.querySelector('[data-tab="matching"]')?.textContent).toBe("暂无匹配数据");
  });

  it.each([
    ["confirm_registration", "确认第 1 页对齐", "版面对齐已确认"],
    ["retry_registration", "重试第 1 页对齐", "已重新排队对齐"],
  ] as const)("%s 成功后都回读当前批次再提示", async (action, label, successText) => {
    mocks.summaries = { "submission-16": summary(action) };
    await renderPage();
    mocks.loadDetail.mockClear();
    await click(label);
    if (action === "confirm_registration") {
      expect(mocks.confirm).toHaveBeenCalledWith("run-16", "人工核对版面对齐边界与题目区域正确");
      expect(mocks.retry).not.toHaveBeenCalled();
      expect(mocks.confirm.mock.invocationCallOrder[0]).toBeLessThan(mocks.loadDetail.mock.invocationCallOrder[0]);
    } else {
      expect(mocks.retry).toHaveBeenCalledWith("run-16");
      expect(mocks.confirm).not.toHaveBeenCalled();
      expect(mocks.retry.mock.invocationCallOrder[0]).toBeLessThan(mocks.loadDetail.mock.invocationCallOrder[0]);
    }
    expect(mocks.loadDetail).toHaveBeenCalledWith("batch-16", true);
    expect(mocks.success).toHaveBeenCalledWith(successText);
    expect(mocks.loadDetail.mock.invocationCallOrder[0]).toBeLessThan(mocks.success.mock.invocationCallOrder[0]);
  });

  it("对齐操作失败时保留原错误提示，不回读或显示成功", async () => {
    mocks.summaries = { "submission-16": summary() }; mocks.confirm.mockRejectedValue(new Error("对齐确认失败"));
    await renderPage(); mocks.loadDetail.mockClear();
    await click("确认第 1 页对齐");
    expect(mocks.error).toHaveBeenCalledWith("对齐确认失败");
    expect(mocks.loadDetail).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("无问题保持空状态，失败文件仍可跳回文件页并提示原修复流程", async () => {
    await renderPage();
    expect(host.querySelector('[data-tab="issues"]')?.textContent).toBe("当前没有需要人工处理的页面");
    mocks.detail!.batch.failed_count = 1;
    mocks.detail!.files = [{ id: "file-16", capture_batch_id: "batch-16", file_asset_id: "asset-16", original_name: "待修复.pdf", content_type: "application/pdf", sha256: "hash-16", byte_size: 10, page_count: 1, status: "failed", created_at: "2026-10-05T00:00:00Z" }];
    await renderPage();
    expect(host.querySelector(".capture-file-issue")?.textContent).toContain("待修复.pdf");
    await click("上传修复文件");
    expect(mocks.info).toHaveBeenCalledWith("请按学生页序一次选择完整答卷重新导入；新选择独立分组，系统会保留原失败记录");
  });

  it("名册定位读取分页外的真实批次，保留明确答卷对象", async () => {
    location.hash = "#/exams/exam-16/capture?submission=outside-submission&tab=image-quality";
    mocks.targets.mockResolvedValue({ targets: [{ batch_id: "outside-batch", submission_id: "outside-submission", candidate_no: "A012" }] });
    mocks.getBatch.mockResolvedValue({ ...detail(), batch: { ...detail().batch, id: "outside-batch", name: "后续扫描批次" } });
    mocks.matching = { batch_id: "batch-16", exam_id: "exam-16", submissions: [], candidates: [] };
    await renderPage();
    expect(mocks.targets).toHaveBeenCalledWith("exam-16", "outside-submission", "");
    expect(mocks.getBatch).toHaveBeenCalledWith("outside-batch");
    expect(mocks.setSelectedId).toHaveBeenCalledWith("outside-batch");
    expect(mocks.matchingProps).toHaveBeenLastCalledWith(expect.objectContaining({ initialSubmissionId: "outside-submission" }));
  });

  it("同一学生有两份采集答卷时先锁操作并明确选择，不能自动选第一份", async () => {
    location.hash = "#/exams/exam-16/capture?student=student-16";
    mocks.targets.mockResolvedValue({ targets: [{ batch_id: "batch-a", submission_id: "sub-a", candidate_no: "A012" }, { batch_id: "batch-b", submission_id: "sub-b", candidate_no: "A012" }] });
    mocks.getBatch.mockImplementation(async (id: string) => ({ ...detail(), batch: { ...detail().batch, id, name: id === "batch-a" ? "原扫描" : "补扫" } }));
    await renderPage();
    expect(mocks.setSelectedId).not.toHaveBeenCalledWith("batch-a");
    expect(host.textContent).toContain("定位到 2 份采集答卷");
    expect([...host.querySelectorAll("button")].find(button => button.textContent === "导入文件")?.disabled).toBe(true);
    const selector = host.querySelector<HTMLSelectElement>('select[aria-label="选择名册对应采集答卷"]');
    expect(selector?.textContent).toContain("补扫 · 考号 A012 · 答卷 2");
    await act(async () => { selector!.value = "batch-b:sub-b"; selector!.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(mocks.setSelectedId).toHaveBeenLastCalledWith("batch-b");
  });

  it("跨考试定位材料被拒绝并显示内联重试", async () => {
    location.hash = "#/exams/exam-16/capture?batch=foreign-batch";
    mocks.getBatch.mockResolvedValue({ ...detail(), batch: { ...detail().batch, exam_id: "foreign-exam" } });
    await renderPage();
    expect(host.textContent).toContain("答卷不属于当前考试");
    expect(mocks.setSelectedId).not.toHaveBeenCalledWith("foreign-batch");
    expect([...host.querySelectorAll("button")].find(button => button.textContent === "重试定位")).toBeDefined();
  });
});
