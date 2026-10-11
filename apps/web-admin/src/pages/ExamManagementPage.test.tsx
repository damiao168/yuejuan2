// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TableColumnsType } from "antd";
import type { ManagementExam } from "../api/exams";
import type { SessionUser } from "../auth/session";
import { ExamManagementPage } from "./ExamManagementPage";

const mocks = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), schools: vi.fn(), grades: vi.fn(), classes: vi.fn(), warning: vi.fn(), error: vi.fn(), details: vi.fn(), edit: vi.fn(), archive: vi.fn() }));
vi.mock("../api/exams", async (original) => ({ ...await original<typeof import("../api/exams")>(), listAllManagementExams: mocks.list, getManagementExam: mocks.get }));
vi.mock("../api/org", () => ({ listSchools: mocks.schools, listGrades: mocks.grades, listClasses: mocks.classes }));
vi.mock("../features/exams/management/ManagementExamDetails", () => ({ ManagementExamDetails: (props: { exam: ManagementExam }) => { mocks.details(props); return <div data-details>{props.exam.exams.map((subject) => subject.id).join(",")}</div>; } }));
vi.mock("../features/exams/management/ManagementExamEditDrawer", () => ({ ManagementExamEditDrawer: (props: unknown) => { mocks.edit(props); return null; } }));
vi.mock("../features/exams/management/ManagementExamArchiveDialog", () => ({ ManagementExamArchiveDialog: (props: unknown) => { mocks.archive(props); return null; } }));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("antd", () => {
  const pass = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => ({ message: { warning: mocks.warning, error: mocks.error } }) },
    Button: ({ children, onClick, disabled, loading, "aria-label": label }: { children?: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean; "aria-label"?: string }) => <button disabled={disabled || loading} onClick={onClick} aria-label={label}>{children}</button>,
    Input: ({ placeholder, value, onChange }: { placeholder?: string; value: string; onChange: (event: unknown) => void }) => <input placeholder={placeholder} value={value} onChange={onChange} />,
    Select: ({ options, value, onChange, "aria-label": label }: { options: { label: string; value: string }[]; value?: string; onChange: (value: string) => void; "aria-label"?: string }) => <select aria-label={label} value={value ?? ""} onChange={(event) => onChange(event.target.value)}><option value="">全部</option>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
    Dropdown: ({ children, menu }: { children: ReactNode; menu: { items: { key: string; label: string; disabled?: boolean }[]; onClick: (item: { key: string }) => void } }) => <div>{children}<div role="menu">{menu.items.map((item) => <button key={item.key} role="menuitem" disabled={item.disabled} onClick={() => menu.onClick({ key: item.key })}>{item.label}</button>)}</div></div>,
    Drawer: ({ children, open, onClose }: { children: ReactNode; open: boolean; onClose: () => void }) => open ? <div role="dialog"><button onClick={onClose}>关闭详情</button>{children}</div> : null,
    Table: ({ columns, dataSource }: { columns: TableColumnsType<ManagementExam>; dataSource: ManagementExam[] }) => <div>{dataSource.map((exam, index) => <div data-exam-id={exam.id} key={exam.id}>{columns.map((column, columnIndex) => "render" in column && column.render ? <div key={columnIndex}>{column.render("dataIndex" in column && column.dataIndex === "name" ? exam.name : undefined, exam, index) as ReactNode}</div> : null)}</div>)}</div>,
    Space: pass, Tooltip: pass, Spin: () => null, Empty: () => null, Alert: () => null
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

async function mount(onOpenWorkspace = vi.fn()) {
  await act(async () => root.render(<ExamManagementPage mode="admin" canManage currentUser={user} onOpenWorkspace={onOpenWorkspace} onCreateExam={vi.fn()} />));
  return onOpenWorkspace;
}
async function click(text: string) {
  const button = [...container.querySelectorAll("button")].find((item) => item.textContent === text);
  if (!button) throw new Error(`Missing button ${text}`);
  await act(async () => button.click());
}

describe("考试列表整场操作", () => {
  it("移除单科菜单与手工状态推进，名称和工作区都打开包含全部学科的整场对象", async () => {
    const open = await mount();
    expect(container.textContent).toContain("查看考试详情");
    const tableRegion = container.querySelector("section[aria-label='考试列表表格']");
    expect(tableRegion).not.toBeNull();
    expect(tableRegion?.getAttribute("tabindex")).toBeNull();
    expect(container.textContent).not.toMatch(/开始配置|归档当前学科|编辑学科设置|查看学科详情/);
    await click("考试工作区");
    await click("期中考试");
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls[0][0]).toMatchObject({ kind: "session", id: "session-1" });
    expect(open.mock.calls[0][0].exams).toHaveLength(2);
  });

  it("详情读取最新整场数据，归档始终提交整场选择而非默认科目", async () => {
    const latest = exam();
    latest.exams.push({ ...latest.exams[0], id: "chinese", subject: "chinese" });
    mocks.get.mockResolvedValue({ exam: latest });
    await mount();
    await click("查看考试详情");
    expect(mocks.get).toHaveBeenCalledWith("session", "session-1");
    expect(container.querySelector("[data-details]")?.textContent).toBe("math,physics,chinese");
    await click("归档考试");
    expect(mocks.archive.mock.lastCall?.[0].selection).toMatchObject({ action: "archive", exam: { id: "session-1", kind: "session" } });
    expect(mocks.archive.mock.lastCall?.[0].selection.exam.exams).toHaveLength(2);
  });

  it("筛选只决定整场行是否出现，操作对象保持全部学科", async () => {
    const open = await mount();
    const select = container.querySelector<HTMLSelectElement>("select[aria-label='考试进度筛选']")!;
    await act(async () => { select.value = "configured"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    await click("考试工作区");
    expect(open.mock.lastCall?.[0].exams.map((subject: { id: string }) => subject.id)).toEqual(["math", "physics"]);
    expect(mocks.list).toHaveBeenCalledTimes(1);
  });

  it("完整范围与服务端写能力一起约束整场写菜单", async () => {
    mocks.list.mockResolvedValue({ exams: [{ ...exam(), scope_complete: false, can_manage: false }] });
    await mount();
    expect([...container.querySelectorAll("button")].find((item) => item.textContent === "归档考试")?.disabled).toBe(true);
    expect([...container.querySelectorAll("button")].find((item) => item.textContent === "编辑考试")?.disabled).toBe(true);
  });

  it("最新权限拒绝不会打开编辑抽屉", async () => {
    mocks.get.mockResolvedValue({ exam: { ...exam(), can_edit: false } });
    await mount();
    await click("编辑考试");
    expect(mocks.warning).toHaveBeenCalled();
    expect(mocks.edit.mock.lastCall?.[0].exam).toBeNull();
  });

  it("关闭详情后，迟到响应不会重新打开旧考试", async () => {
    let resolve!: (value: { exam: ManagementExam }) => void;
    mocks.get.mockReturnValue(new Promise<{ exam: ManagementExam }>((done) => { resolve = done; }));
    await mount();
    await click("查看考试详情");
    await click("关闭详情");
    await act(async () => resolve({ exam: exam() }));
    expect(container.querySelector("[role='dialog']")).toBeNull();
  });
});
