// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CaptureCollectionEntry, CaptureCollectionSummary } from "../../api/captureCollection";
import { CaptureCollectionPanel } from "./CaptureCollectionPanel";

const mocks = vi.hoisted(() => ({ complete: vi.fn(), exclude: vi.fn(), retry: vi.fn(), cancel: vi.fn(), attendance: vi.fn(), confirm: vi.fn(), success: vi.fn(), error: vi.fn(), info: vi.fn() }));
vi.mock("../../api/captureCollection", () => ({ completeCaptureCollection: mocks.complete, excludeCaptureFile: mocks.exclude }));
vi.mock("../../api/capture", () => ({ processCaptureBatch: mocks.retry, cancelCaptureBatch: mocks.cancel }));
vi.mock("../../api/scores", () => ({ setExamAttendance: mocks.attendance }));
vi.mock("../../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("../../components/ResponsiveTable", () => ({ ResponsiveTable: ({ columns, dataSource, locale }: {
  columns: { key: string; render?: (value: undefined, entry: CaptureCollectionEntry) => ReactNode }[];
  dataSource: CaptureCollectionEntry[]; locale: { emptyText: ReactNode };
}) => <div data-student-table>{dataSource.length ? dataSource.map(entry => <div key={entry.student_id} data-student={entry.student_id}>{columns.map(column => <span key={column.key}>{column.render?.(undefined, entry)}</span>)}</div>) : locale.emptyText}</div> }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error, info: mocks.info }, modal: { confirm: mocks.confirm } }) },
  Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: () => void; disabled?: boolean }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Alert: ({ message }: { message: ReactNode }) => <aside>{message}</aside>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Empty: Object.assign(({ description }: { description: ReactNode }) => <div>{description}</div>, { PRESENTED_IMAGE_SIMPLE: null }),
  Segmented: ({ options, onChange }: { options: { value: string; label: string }[]; onChange: (value: string) => void }) => <div>{options.map(option => <button key={option.value} onClick={() => onChange(option.value)}>{option.label}</button>)}</div>,
  Input: { TextArea: (props: object) => <textarea {...props} /> },
  Modal: ({ open, title, children, onOk, okText, okButtonProps }: { open: boolean; title: ReactNode; children: ReactNode; onOk: () => void; okText: string; okButtonProps: { disabled: boolean } }) => open ? <div role="dialog">{title}{children}<button disabled={okButtonProps.disabled} onClick={onOk}>{okText}</button></div> : null,
}));

export function collectionSummary(overrides: Partial<CaptureCollectionSummary> = {}): CaptureCollectionSummary {
  return {
    exam_id: "exam-a", exam_status: "collecting", revision: 12, closed: false, pages_per_student: 2,
    roster_count: 4, required_count: 3, ready_count: 1, absent_count: 1, pending_count: 2,
    can_complete: false, blockers: ["仍有 1 名学生未收卷", "仍有 1 份答卷需要处理"],
    entries: [
      { student_id: "missing", student_no: "001", student_name: "张未收", class_name: "一班", status: "missing", code: "missing_submission", expected_pages: 2, actual_pages: 0 },
      { student_id: "processing", student_no: "002", student_name: "李处理中", class_name: "一班", status: "processing", code: "processing", batch_id: "batch-a", submission_id: "sub-processing", expected_pages: 2, actual_pages: 2 },
      { student_id: "ready", student_no: "003", student_name: "王已导入", class_name: "一班", status: "ready", code: "ready", batch_id: "batch-a", submission_id: "sub-ready", expected_pages: 2, actual_pages: 2 },
      { student_id: "absent", student_no: "004", student_name: "赵缺考", class_name: "一班", status: "absent", code: "absent", expected_pages: 2, actual_pages: 0, reason: "病假缺考" },
    ], issues: [], ...overrides,
  };
}

describe("考试级答卷导入核对", () => {
  let root: Root;
  let host: HTMLDivElement;
  let props: Parameters<typeof CaptureCollectionPanel>[0];
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.confirm.mockReturnValue({ destroy: vi.fn() });
    mocks.complete.mockResolvedValue({ summary: collectionSummary({ closed: true, exam_status: "grading" }) });
    mocks.attendance.mockResolvedValue({}); mocks.exclude.mockResolvedValue({}); mocks.retry.mockResolvedValue({});
    props = { examId: "exam-a", actorKey: "tenant:teacher", summary: collectionSummary(), loading: false, canManage: true, localBusy: false,
      onRefresh: vi.fn().mockResolvedValue(undefined), onLocate: vi.fn(), onReplace: vi.fn().mockResolvedValue(undefined), onCompleted: vi.fn().mockResolvedValue(undefined), onMaterialChanged: vi.fn().mockResolvedValue(undefined) };
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(next: Partial<typeof props> = {}) { props = { ...props, ...next }; await act(async () => root.render(<CaptureCollectionPanel {...props} />)); }
  function button(label: string) {
    const result = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === label);
    expect(result, label).toBeDefined(); return result!;
  }
  async function click(label: string) { await act(async () => button(label).click()); }
  async function fillReason(value: string) { await act(async () => fireEvent.change(host.querySelector("textarea")!, { target: { value } })); }
  async function openFinish() { await click("结束本场导入"); return mocks.confirm.mock.lastCall![0] as { onOk: () => Promise<void> }; }

  it("默认只看待完成学生，成功与缺考可切全部查看，统计使用服务端人数", async () => {
    await render();
    expect(host.querySelector(".capture-collection-heading p")?.textContent).toBe("名册 4 人 · 缺考 1 人 · 每人 2 页");
    expect(host.querySelector(".capture-collection-stats")?.textContent).toBe("应收3成功导入1待完成2");
    expect([...host.querySelectorAll("[data-student]")].map(item => item.getAttribute("data-student"))).toEqual(["missing", "processing"]);
    expect(button("结束本场导入").disabled).toBe(true);
    expect(host.textContent).toContain("仍有 1 名学生未收卷");
    await click("全部学生");
    expect(host.querySelectorAll("[data-student]")).toHaveLength(4);
    expect(host.textContent).toContain("病假缺考");
  });

  it.each([{ loading: true }, { localBusy: true }, { canManage: false }, { error: "核对读取失败" }])("%j 期间保留结束入口但不能提交", async state => {
    await render({ summary: collectionSummary({ can_complete: true, blockers: [] }), ...state });
    expect(button("结束本场导入").disabled).toBe(true);
    await click("结束本场导入");
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("结束使用已核对修订号，成功响应交回页面再提示", async () => {
    await render({ summary: collectionSummary({ can_complete: true, blockers: [] }) });
    const confirmation = await openFinish();
    await act(async () => confirmation.onOk());
    expect(mocks.complete).toHaveBeenCalledWith("exam-a", { revision: 12, idempotency_key: expect.any(String) });
    expect(props.onCompleted).toHaveBeenCalledWith(expect.objectContaining({ exam_status: "grading", closed: true }));
    expect(mocks.success).toHaveBeenCalledWith("本场导入已结束，已进入阅卷阶段");
    expect(localStorage.length).toBe(0);
  });

  it("确认框打开后修订变化，不能拿旧核对结果结束导入", async () => {
    await render({ summary: collectionSummary({ can_complete: true, blockers: [] }) });
    const confirmation = await openFinish();
    await render({ summary: collectionSummary({ revision: 13, can_complete: true, blockers: [] }) });
    await act(async () => confirmation.onOk());
    expect(mocks.complete).not.toHaveBeenCalled();
    expect(props.onRefresh).toHaveBeenCalledOnce();
    expect(mocks.info).toHaveBeenCalledWith(expect.stringContaining("导入情况已变化"));
  });

  it("网络结果不确定后重试保留原命令键和修订号", async () => {
    mocks.complete.mockRejectedValueOnce(new Error("connection lost"));
    await render({ summary: collectionSummary({ can_complete: true, blockers: [] }) });
    let confirmation = await openFinish();
    await act(async () => confirmation.onOk());
    const original = mocks.complete.mock.calls[0][1];
    expect(props.onRefresh).toHaveBeenCalledOnce();
    await render({ summary: collectionSummary({ revision: 13, can_complete: true, blockers: [] }) });
    confirmation = await openFinish();
    await act(async () => confirmation.onOk());
    expect(mocks.complete.mock.calls[1][1]).toEqual(original);
    expect(localStorage.length).toBe(0);
  });

  it("缺卷标记缺考必须填写原因，保存后重新读取导入事实", async () => {
    await render(); await click("标记缺考");
    expect(button("确认并重新核对").disabled).toBe(true);
    await fillReason("  病假已核实  "); await click("确认并重新核对");
    expect(mocks.attendance).toHaveBeenCalledWith("exam-a", "missing", "absent", "病假已核实");
    expect(props.onRefresh).toHaveBeenCalledOnce();
    expect(mocks.attendance.mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(props.onRefresh).mock.invocationCallOrder[0]);
  });

  it("材料单列不混学生人数，重试与不计入均刷新核对", async () => {
    await render({ summary: collectionSummary({ issues: [{ id: "file-a", kind: "file", code: "file_failed", name: "错误扫描.pdf", batch_id: "batch-a", file_id: "file-a" }] }) });
    expect(host.querySelector(".capture-collection-stats")?.textContent).toBe("应收3成功导入1待完成2");
    expect(host.querySelector(".capture-collection-materials")?.textContent).toContain("1 项，不计入学生人数");
    await click("重试原文件"); expect(mocks.retry).toHaveBeenCalledWith("batch-a");
    await click("不计入"); await fillReason("已按完整答卷重新导入"); await click("确认并重新核对");
    expect(mocks.exclude).toHaveBeenCalledWith("file-a", "已按完整答卷重新导入");
    expect(props.onMaterialChanged).toHaveBeenCalledWith("batch-a");
  });

  it("点击学生问题保留明确批次和答卷，并定位质检面板", async () => {
    const entry = { ...collectionSummary().entries[1], status: "needs_attention" as const, code: "quality_failed" };
    await render({ summary: collectionSummary({ entries: [entry] }) });
    await click("查看处理");
    expect(props.onLocate).toHaveBeenCalledWith({ batchId: "batch-a", submissionId: "sub-processing", tab: "image-quality" });
  });

  it("上传替代前说明整个选择退出有效输入，并把原因交给上传工作区", async () => {
    const issue = { id: "file-a", kind: "file" as const, code: "file_failed", name: "错误扫描.pdf", batch_id: "batch-a", file_id: "file-a" };
    await render({ summary: collectionSummary({ issues: [issue] }) });
    await click("上传替代答卷");
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain("本次选择的全部文件及其答卷将退出有效输入");
    await fillReason("重新选择完整答卷");
    await click("继续选择完整答卷");
    expect(props.onReplace).toHaveBeenCalledWith(issue, "重新选择完整答卷");
    expect(mocks.exclude).not.toHaveBeenCalled();
  });

  it("部分登记的上传组可整组不计入，尚无文件的中断上传可取消批次", async () => {
    await render({ summary: collectionSummary({ issues: [
      { id: "group", kind: "upload", code: "upload_group_incomplete", name: "未齐的选择", batch_id: "batch-a", file_id: "first-file" },
      { id: "session", kind: "upload", code: "upload_incomplete", name: "中断上传", batch_id: "batch-b" },
    ] }) });
    await click("不计入"); await fillReason("重新按完整答卷上传"); await click("确认并重新核对");
    expect(mocks.exclude).toHaveBeenCalledWith("first-file", "重新按完整答卷上传");
    await click("取消该批次");
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain("该批次的全部材料和未完成上传不再计入");
    await fillReason("放弃尚未开始的上传"); await click("确认并重新核对");
    expect(mocks.cancel).toHaveBeenCalledWith("batch-b", "放弃尚未开始的上传");
  });
});
