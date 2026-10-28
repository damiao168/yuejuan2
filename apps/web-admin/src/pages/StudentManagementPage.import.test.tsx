// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StudentManagementPage } from "./StudentManagementPage";

type ModalState = { open: boolean; onOk: () => void; onCancel: () => void; keyboard?: boolean; closable?: boolean; maskClosable?: boolean; confirmLoading?: boolean; okButtonProps?: { disabled?: boolean }; cancelButtonProps?: { disabled?: boolean } };
const mocks = vi.hoisted(() => ({
  listStudents: vi.fn(), importCSV: vi.fn(), workbook: vi.fn(), error: vi.fn(), success: vi.fn(), warning: vi.fn(),
  modals: new Map<string, ModalState>(), beforeUpload: undefined as undefined | ((file: File) => Promise<boolean>), onRemove: undefined as undefined | (() => boolean),
  uploadDisabled: false
}));
const app = vi.hoisted(() => ({ message: { error: mocks.error, success: mocks.success, warning: mocks.warning }, modal: { confirm: vi.fn() } }));
vi.mock("../api/org", () => ({
  listStudents: mocks.listStudents, importStudentsCSV: mocks.importCSV,
  listGrades: async () => ({ grades: [] }), listClasses: async () => ({ classes: [
    { id: "class-a", school_id: "school-a", code: "C1", name: "一班", status: "active" },
    { id: "class-b", school_id: "school-a", code: "C2", name: "二班", status: "active" }
  ] }), createStudent: vi.fn(), deleteStudent: vi.fn(), updateStudentStatus: vi.fn(), updateStudent: vi.fn(), transferStudent: vi.fn()
}));
vi.mock("../features/members/students/studentImportWorkbook", () => ({ readStudentImportWorkbook: mocks.workbook }));
vi.mock("../auth/stepUpContext", () => ({ useStepUp: () => ({ runWithStepUp: vi.fn() }), isStepUpCancelledError: () => false }));
vi.mock("../components/ResponsiveTable", () => ({ ResponsiveTable: () => null }));
vi.mock("../components/StatusTag", () => ({ StatusTag: () => null }));
vi.mock("../components/PageState", () => ({ LoadingState: () => <p>名册加载中</p>, ErrorState: () => null, EmptyState: () => null }));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const Input = ({ value, onChange }: { value?: string; onChange?: React.ChangeEventHandler<HTMLInputElement> }) => <input value={value ?? ""} onChange={onChange} />;
  return {
    App: { useApp: () => app }, Space: Box, Dropdown: Box,
    Form: Object.assign(Box, { Item: Box, useForm: () => [{ resetFields: vi.fn(), setFieldsValue: vi.fn(), submit: vi.fn() }] }),
    Input: Object.assign(Input, { Password: Input }),
    Button: ({ children, disabled, loading, onClick }: { children?: ReactNode; disabled?: boolean; loading?: boolean; onClick?: () => void }) => <button type="button" disabled={disabled || loading} onClick={onClick}>{children}</button>,
    Select: ({ value, options = [], disabled, onChange, "aria-label": label }: { value?: string | number; options?: { value: string | number; label: string }[]; disabled?: boolean; onChange?: (value: string | number) => void; "aria-label"?: string }) => <select aria-label={label} value={value ?? ""} disabled={disabled} onChange={event => { if (!disabled) { const option = options.find(item => String(item.value) === event.target.value); onChange?.(option?.value ?? event.target.value); } }}><option value="" />{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
    Segmented: () => null,
    Modal: ({ title, children, okText, cancelText, ...state }: ModalState & { title: string; children: ReactNode; okText: string; cancelText: string }) => {
      mocks.modals.set(title, state);
      return state.open ? <section role="dialog" aria-label={title} tabIndex={-1} onKeyDown={event => { if (event.key === "Escape" && state.keyboard !== false) state.onCancel(); }}>
        {children}<button type="button" disabled={state.okButtonProps?.disabled || state.confirmLoading} onClick={state.onOk}>{okText}</button><button type="button" disabled={state.cancelButtonProps?.disabled} onClick={state.onCancel}>{cancelText}</button>
      </section> : null;
    },
    Upload: { Dragger: ({ children, disabled, beforeUpload, onRemove }: { children: ReactNode; disabled?: boolean; beforeUpload: (file: File) => Promise<boolean>; onRemove: () => boolean }) => {
      mocks.beforeUpload = beforeUpload; mocks.onRemove = onRemove; mocks.uploadDisabled = Boolean(disabled);
      return <div>{children}<input type="file" aria-label="选择导入名单" disabled={disabled} onChange={event => { if (!disabled && event.target.files?.[0]) void beforeUpload(event.target.files[0]); }} /><button type="button" disabled={disabled} onClick={() => onRemove()}>移除名单</button></div>;
    } }
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
const csv = (studentNo: string, name: string, classCode = "C1") => `学号,姓名,班级\r\n${studentNo},${name},${classCode}\r\n`;
function file(name: string, content: string | Promise<string>) {
  return Object.assign(new File(["synthetic"], name), { text: () => Promise.resolve(content) });
}
async function openImport() {
  render(<StudentManagementPage />);
  fireEvent.click(await screen.findByRole("button", { name: "导入学生" }));
  return screen.getByRole("dialog", { name: "批量导入学生" });
}
async function chooseFile(source: File) {
  await act(async () => { await mocks.beforeUpload!(source); });
}
beforeEach(() => {
  vi.resetAllMocks(); mocks.modals.clear(); mocks.beforeUpload = undefined; mocks.onRemove = undefined; mocks.uploadDisabled = false;
  mocks.listStudents.mockResolvedValue({ students: [{ id: "existing", name: "已在籍学生", student_no: "existing", class_id: "class-a", status: "active" }], has_more: false });
  mocks.importCSV.mockResolvedValue({ result: { created: 1, errors: [] } });
});
afterEach(cleanup);

describe("学生名单导入页面的文件与提交边界", () => {
  it("读取旧文件时换新文件，旧Promise晚到不能替换新名单预检和提交", async () => {
    const old = deferred<string>();
    await openImport();
    let oldRead!: Promise<boolean>;
    act(() => { oldRead = mocks.beforeUpload!(file("old.csv", old.promise)); });
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "开始导入" }).disabled).toBe(true);
    await chooseFile(file("new.csv", csv("002", "新名单")));
    await act(async () => { old.resolve(csv("001", "旧名单")); await oldRead; });
    expect(screen.getByText(/预检：1 行可提交，0 行有问题/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "开始导入" }));
    await waitFor(() => expect(mocks.importCSV).toHaveBeenCalledOnce());
    expect(mocks.importCSV.mock.calls[0][0]).toContain("002,新名单,school-a,class-a");
    expect(mocks.importCSV.mock.calls[0][0]).not.toContain("旧名单");
  });

  it.each(["关闭", "移除"])("%s未完成读取后再打开，原xlsx回包不能复活已取消文件", async action => {
    const pending = deferred<{ name: string; csv: string }[]>();
    let signal!: AbortSignal;
    mocks.workbook.mockImplementation((_file: File, owner: AbortSignal) => { signal = owner; return pending.promise; });
    await openImport();
    let read!: Promise<boolean>;
    act(() => { read = mocks.beforeUpload!(file("old.xlsx", "")); });
    if (action === "关闭") {
      fireEvent.click(screen.getByRole("button", { name: "取消" }));
      expect(screen.queryByRole("dialog")).toBeNull();
      fireEvent.click(screen.getByRole("button", { name: "导入学生" }));
    } else fireEvent.click(screen.getByRole("button", { name: "移除名单" }));
    expect(signal.aborted).toBe(true);
    await act(async () => { pending.resolve([{ name: "旧表", csv: csv("001", "不能复活") }]); await read; });
    expect(screen.queryByRole("combobox", { name: "学号列" })).toBeNull();
    expect(screen.queryByText(/预检：/)).toBeNull();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "开始导入" }).disabled).toBe(true);
    expect(mocks.importCSV).not.toHaveBeenCalled();
  });

  it("多工作表切换重新预检和识别中文列，只提交选定工作表且保留文本学号", async () => {
    mocks.workbook.mockResolvedValue([{ name: "一班名单", csv: csv("001", "甲同学") }, { name: "二班名单", csv: "姓名,班级,学号\r\n乙同学,C2,0002\r\n" }]);
    await openImport(); await chooseFile(file("students.xlsx", ""));
    const sheets = screen.getByRole<HTMLSelectElement>("combobox", { name: "导入工作表" });
    expect(sheets.value).toBe("0");
    fireEvent.change(sheets, { target: { value: "1" } });
    expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "学号列" }).value).toBe("学号");
    expect(screen.getByText(/预检：1 行可提交，0 行有问题/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "开始导入" }));
    await waitFor(() => expect(mocks.importCSV).toHaveBeenCalledOnce());
    expect(mocks.importCSV.mock.calls[0][0]).toContain("0002,乙同学,school-a,class-b");
    expect(mocks.importCSV.mock.calls[0][0]).not.toContain("甲同学");
  });

  it("POST期间同步锁拒绝重复提交和旧取消/换文件回调，控件冻结直到本次回执", async () => {
    const pending = deferred<{ result: { created: number; errors: { row: number; message: string }[] } }>();
    mocks.importCSV.mockReturnValue(pending.promise);
    mocks.workbook.mockResolvedValue([{ name: "一班", csv: csv("001", "原表甲") }, { name: "二班", csv: csv("002", "其他表乙", "C2") }]);
    await openImport(); await chooseFile(file("students.xlsx", ""));
    const before = mocks.modals.get("批量导入学生")!;
    const staleRead = mocks.beforeUpload!; const staleRemove = mocks.onRemove!;
    // 同一事件批次直接调用捕获的旧处理器，检验ref保护，不能只依赖disabled重绘。
    act(() => { before.onOk(); before.onOk(); before.onCancel(); staleRemove(); void staleRead(file("replacement.csv", csv("999", "不得覆盖"))); });
    expect(mocks.importCSV).toHaveBeenCalledOnce();
    expect(mocks.importCSV.mock.calls[0][0]).toContain("001,原表甲,school-a,class-a");
    const modal = screen.getByRole("dialog", { name: "批量导入学生" });
    const saving = mocks.modals.get("批量导入学生")!;
    expect(saving.keyboard).toBe(false); expect(saving.closable).toBe(false); expect(saving.maskClosable).toBe(false);
    expect(mocks.uploadDisabled).toBe(true);
    for (const name of ["导入工作表", "学号列", "姓名列", "班级代码列", "导入目标班级"]) expect(within(modal).getByRole<HTMLSelectElement>("combobox", { name }).disabled).toBe(true);
    fireEvent.keyDown(modal, { key: "Escape" }); fireEvent.click(within(modal).getByRole("button", { name: "取消" }));
    fireEvent.change(within(modal).getByRole("combobox", { name: "导入工作表" }), { target: { value: "1" } });
    fireEvent.change(within(modal).getByRole("combobox", { name: "学号列" }), { target: { value: "姓名" } });
    expect(within(modal).getByRole<HTMLSelectElement>("combobox", { name: "导入工作表" }).value).toBe("0");
    expect(within(modal).getByRole<HTMLSelectElement>("combobox", { name: "学号列" }).value).toBe("学号");
    await act(async () => pending.resolve({ result: { created: 1, errors: [{ row: 2, message: "本次原表待核对" }] } }));
    expect(screen.getByText("本次已创建 1 名；未导入 1 行")).toBeTruthy();
    expect(screen.getByText("第 2 行：本次原表待核对")).toBeTruthy();
    expect(mocks.importCSV).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    fireEvent.click(screen.getByRole("button", { name: "导入学生" }));
    expect(screen.queryByText("本次已创建 1 名；未导入 1 行")).toBeNull();
    expect(screen.queryByRole("combobox", { name: "导入工作表" })).toBeNull();
  });
});
