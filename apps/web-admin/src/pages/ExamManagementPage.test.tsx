// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TableColumnsType, TableProps } from "antd";
import type { ManagementExam } from "../api/exams";
import type { SessionUser } from "../auth/session";
import { ExamManagementPage } from "./ExamManagementPage";

const mocks = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), schools: vi.fn(), grades: vi.fn(), classes: vi.fn(), warning: vi.fn(), error: vi.fn(), details: vi.fn(), edit: vi.fn() }));
vi.mock("../api/exams", async (original) => ({ ...await original<typeof import("../api/exams")>(), listAllManagementExams: mocks.list, getManagementExam: mocks.get }));
vi.mock("../api/org", () => ({ listSchools: mocks.schools, listGrades: mocks.grades, listClasses: mocks.classes }));
vi.mock("../features/exams/management/ManagementExamDetails", () => ({ ManagementExamDetails: (props: { exam: ManagementExam }) => { mocks.details(props); return <div data-details>{props.exam.exams.map((subject) => subject.id).join(",")}</div>; } }));
vi.mock("../features/exams/management/ManagementExamEditDrawer", () => ({ ManagementExamEditDrawer: (props: unknown) => { mocks.edit(props); return null; } }));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("antd", () => {
  const pass = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => ({ message: { warning: mocks.warning, error: mocks.error } }) },
    Button: ({ children, onClick, disabled, loading, "aria-label": label }: { children?: ReactNode; onClick?: React.MouseEventHandler<HTMLButtonElement>; disabled?: boolean; loading?: boolean; "aria-label"?: string }) => <button disabled={disabled || loading} onClick={onClick} aria-label={label}>{children}</button>,
    Input: ({ placeholder, value, onChange }: { placeholder?: string; value: string; onChange: (event: unknown) => void }) => <input placeholder={placeholder} value={value} onChange={onChange} />,
    Select: ({ options, value, onChange, "aria-label": label }: { options: { label: string; value: string }[]; value?: string; onChange: (value: string) => void; "aria-label"?: string }) => <select aria-label={label} value={value ?? ""} onChange={(event) => onChange(event.target.value)}><option value="">全部</option>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
    Drawer: ({ children, open, onClose }: { children: ReactNode; open: boolean; onClose: () => void }) => open ? <div role="dialog"><button onClick={onClose}>关闭详情</button>{children}</div> : null,
    Table: ({ columns, dataSource, onRow }: { columns: TableColumnsType<ManagementExam>; dataSource: ManagementExam[]; onRow?: TableProps<ManagementExam>["onRow"] }) => <div><div>{columns.map((column, index) => <span role="columnheader" key={index}>{typeof column.title === "function" ? null : column.title}</span>)}</div>{dataSource.map((exam, index) => <div {...onRow?.(exam, index)} data-exam-id={exam.id} key={exam.id}>{columns.map((column, columnIndex) => "render" in column && column.render ? <div {...column.onCell?.(exam, index)} key={columnIndex}>{column.render("dataIndex" in column && column.dataIndex === "name" ? exam.name : undefined, exam, index) as ReactNode}</div> : null)}</div>)}</div>,
    Space: pass, Tooltip: pass, Spin: () => null, Empty: () => null, Alert: ({ message }: { message: ReactNode }) => <p role="alert">{message}</p>
  };
});

function exam(): ManagementExam {
  return { id: "session-1", kind: "session", school_id: "school-1", grade_id: "grade-1", name: "期中考试", exam_type: "midterm_exam", revision: 3, created_by: "user-1", class_ids: ["class-1"], default_class_ids: ["class-1"], inherited_subject_ids: ["math", "physics"],
    exams: ["math", "physics"].map((subject) => ({ id: subject, subject, tenant_id: "tenant-1", school_id: "school-1", name: subject, exam_type: "midterm_exam", total_score: 150, status: "draft", grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "manual_publish", created_by: "user-1", class_ids: ["class-1"], revision: 2 })),
    progress: { status: "configured", label: "准备中 · 0/2 科准备完成", total_count: 2, ready_count: 0, published_count: 0, counts: { draft: 2 } }, scope_complete: true, can_manage: true, can_edit: true, can_archive: true, can_restore: false };
}

const user = { id: "user-1", name: "管理员", tenant: "tenant-1" } as SessionUser;
let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  vi.clearAllMocks();
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  window.location.hash = "#/admin/exams";
  mocks.list.mockResolvedValue({ exams: [exam()] });
  mocks.get.mockResolvedValue({ exam: exam() });
  mocks.schools.mockResolvedValue({ schools: [] });
  mocks.grades.mockResolvedValue({ grades: [] });
  mocks.classes.mockResolvedValue({ classes: [] });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); });

async function mount(onOpenWorkspace = vi.fn(), canManage = true) {
  await act(async () => root.render(<ExamManagementPage mode="admin" canManage={canManage} currentUser={user} onOpenWorkspace={onOpenWorkspace} onCreateExam={vi.fn()} />));
  return onOpenWorkspace;
}
async function click(text: string) {
  const button = [...container.querySelectorAll("button")].find((item) => item.textContent === text);
  if (!button) throw new Error(`Missing button ${text}`);
  await act(async () => button.click());
}

describe("考试列表整场操作", () => {
  it("考试准备深链定位已授权学科所属整场并展开参考班级编辑", async () => {
    window.location.hash = "#/admin/exams?edit_exam=math&target=scope";
    const open = await mount();
    expect(mocks.get).toHaveBeenCalledWith("session", "session-1");
    expect(mocks.edit.mock.lastCall?.[0]).toMatchObject({ exam: { id: "session-1" }, focusTarget: "scope" });
    expect(open).not.toHaveBeenCalled();
    const props = mocks.edit.mock.lastCall![0]; await act(async () => { props.onClose(); await props.onSaved(); });
    expect(mocks.edit.mock.lastCall?.[0].exam).toBeNull(); expect(mocks.get).toHaveBeenCalledOnce();
  });
  it("精确入口不接受未授权考试标识，最新状态拒绝编辑时交接管理员", async () => {
    window.location.hash = "#/admin/exams?edit_exam=unlisted"; await mount();
    expect(mocks.get).not.toHaveBeenCalled(); expect(container.textContent).toContain("当前授权范围内未找到该考试");
    window.location.hash = "#/admin/exams?edit_exam=math&target=scope";
    mocks.get.mockResolvedValue({ exam: { ...exam(), can_edit: false } }); await mount();
    expect(mocks.edit.mock.lastCall?.[0].exam).toBeNull(); expect(container.textContent).toContain("请联系学校考试管理员核对年级与参考班级");
    expect(mocks.details.mock.lastCall?.[0].exam.id).toBe("session-1");
  });
  it("点击整行或考试名称都打开完整整场对象，名称点击只跳转一次", async () => {
    const open = await mount();
    expect(container.textContent).not.toMatch(/查看考试详情|归档考试|恢复考试/);
    expect(container.textContent).not.toContain("考试类型");
    expect([...container.querySelectorAll("select option")].map(option => option.textContent)).not.toContain("正式考试");
    const tableRegion = container.querySelector("section[aria-label='考试列表表格']");
    expect(tableRegion).not.toBeNull();
    expect(tableRegion?.getAttribute("tabindex")).toBeNull();
    expect(container.textContent).not.toMatch(/开始配置|归档当前学科|编辑学科设置|查看学科详情/);
    expect(container.querySelector(".exam-name-link")?.tagName).toBe("BUTTON");
    expect([...container.querySelectorAll("button")].some(button => button.textContent === "考试工作区")).toBe(false);
    await act(async () => container.querySelector<HTMLElement>('[data-exam-id="session-1"]')!.click());
    await click("期中考试");
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls[0][0]).toMatchObject({ kind: "session", id: "session-1" });
    expect(open.mock.calls[0][0].exams).toHaveLength(2);
  });

  it("直接编辑读取最新整场数据，保持全部学科而非默认科目", async () => {
    const latest = exam();
    latest.exams.push({ ...latest.exams[0], id: "chinese", subject: "chinese" });
    mocks.get.mockResolvedValue({ exam: latest });
    await mount();
    await click("编辑");
    expect(mocks.get).toHaveBeenCalledWith("session", "session-1");
    expect(mocks.edit.mock.lastCall?.[0].exam).toMatchObject({ id: "session-1", kind: "session" });
    expect(mocks.edit.mock.lastCall?.[0].exam.exams.map((subject: { id: string }) => subject.id)).toEqual(["math", "physics", "chinese"]);
  });

  it("筛选只决定整场行是否出现，操作对象保持全部学科", async () => {
    const open = await mount();
    const select = container.querySelector<HTMLSelectElement>("select[aria-label='考试进度筛选']")!;
    await act(async () => { select.value = "active"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    await click("期中考试");
    expect(open.mock.lastCall?.[0].exams.map((subject: { id: string }) => subject.id)).toEqual(["math", "physics"]);
    expect(mocks.list).toHaveBeenLastCalledWith({ school_id: undefined, status: "active", archive: "active" });
    expect(mocks.list).toHaveBeenCalledTimes(2);
  });

  it("完整范围与服务端写能力一起约束直接编辑按钮", async () => {
    mocks.list.mockResolvedValue({ exams: [{ ...exam(), scope_complete: false, can_manage: false }] });
    await mount();
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="编辑期中考试"]')?.disabled).toBe(true);
    await click("编辑");
    expect(mocks.get).not.toHaveBeenCalled();
  });

  it("编辑独立成列且点击不会触发行跳转，不再提供更多菜单", async () => {
    const open = await mount();
    expect([...container.querySelectorAll('[role="columnheader"]')].map(item => item.textContent)).toContain("编辑");
    expect([...container.querySelectorAll('[role="columnheader"]')].map(item => item.textContent)).not.toContain("操作");
    expect(container.querySelector('[role="menu"]')).toBeNull();
    expect(container.querySelector('[aria-label="期中考试更多操作"]')).toBeNull();
    expect(container.textContent).not.toMatch(/查看考试详情|归档考试|恢复考试/);
    await click("编辑");
    expect(open).not.toHaveBeenCalled();
    expect(mocks.get).toHaveBeenCalledOnce();
    expect(mocks.edit.mock.lastCall?.[0].exam.id).toBe("session-1");
  });

  it("最新权限拒绝不会打开编辑抽屉", async () => {
    mocks.get.mockResolvedValue({ exam: { ...exam(), can_edit: false } });
    await mount();
    await click("编辑");
    expect(mocks.warning).toHaveBeenCalled();
    expect(mocks.edit.mock.lastCall?.[0].exam).toBeNull();
  });

  it("读取设置时禁用编辑按钮，避免重复读取和触发行跳转", async () => {
    let resolve!: (value: { exam: ManagementExam }) => void;
    mocks.get.mockReturnValue(new Promise<{ exam: ManagementExam }>((done) => { resolve = done; }));
    const open = await mount();
    await click("编辑");
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="编辑期中考试"]')?.disabled).toBe(true);
    await click("编辑");
    expect(mocks.get).toHaveBeenCalledOnce();
    await act(async () => resolve({ exam: exam() }));
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="编辑期中考试"]')?.disabled).toBe(false);
    expect(mocks.edit.mock.lastCall?.[0].exam.id).toBe("session-1");
    expect(open).not.toHaveBeenCalled();
  });

  it("无整场管理权限时不显示编辑列，考试名称仍可进入工作区", async () => {
    const open = await mount(vi.fn(), false);
    expect([...container.querySelectorAll('[role="columnheader"]')].map(item => item.textContent)).not.toContain("编辑");
    expect(container.querySelector('[aria-label="编辑期中考试"]')).toBeNull();
    await click("期中考试");
    expect(open).toHaveBeenCalledOnce();
    expect(mocks.get).not.toHaveBeenCalled();
  });

  it("进度筛选只保留三项且请求服务端大类，页面不再显示归档筛选", async () => {
    await mount();
    const select = container.querySelector<HTMLSelectElement>("select[aria-label='考试进度筛选']")!;
    expect([...select.options].filter(option => option.value || option.textContent !== "全部").map(option => option.textContent)).toEqual(["全部进度", "正在进行", "阅卷结束"]);
    expect(container.querySelector("select[aria-label='归档范围筛选']")).toBeNull();
    expect(container.textContent).toContain("正在进行");
    await act(async () => { select.value = "completed"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(mocks.list).toHaveBeenLastCalledWith({ school_id: undefined, status: "completed", archive: "active" });
    expect(container.querySelector('[data-exam-id="session-1"]')).toBeNull();
  });
});
