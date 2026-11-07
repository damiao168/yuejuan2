// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Grade, School, SchoolClass, Student } from "../../../api/org";
import type { SessionUser } from "../../../auth/session";
import { ClassManagementPage } from "./ClassManagementPage";

const mocks = vi.hoisted(() => ({ schools: [] as School[], grades: [] as Grade[], classes: [] as SchoolClass[], students: [] as Student[], years: vi.fn(), confirm: vi.fn(), success: vi.fn(), error: vi.fn(), updateGrade: vi.fn(), createGrade: vi.fn(), createClass: vi.fn(), updateClass: vi.fn(), deleteGrade: vi.fn(), deleteClass: vi.fn() }));
vi.mock("../../../api/org", () => ({
  listSchools: async () => ({ schools: mocks.schools }), listGrades: async () => ({ grades: mocks.grades }),
  listClasses: async () => ({ classes: mocks.classes }), listStudents: async () => ({ students: mocks.students, has_more: false }),
  listAcademicYears: mocks.years,
  createClass: mocks.createClass, updateClass: mocks.updateClass, createGrade: mocks.createGrade, updateGrade: mocks.updateGrade, deleteGrade: mocks.deleteGrade, deleteClass: mocks.deleteClass
}));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));
vi.mock("../../../components/ResponsiveTable", () => ({ ResponsiveTable: ({ columns, dataSource }: { columns: { key?: string; dataIndex?: keyof SchoolClass; render?: (value: unknown, row: SchoolClass) => ReactNode }[]; dataSource: SchoolClass[] }) => <div>{dataSource.map(row => <article key={row.id} data-testid={row.id}>{columns.map((column, index) => <span key={column.key ?? index}>{column.render ? column.render(undefined, row) : String(row[column.dataIndex!] ?? "")}</span>)}</article>)}</div> }));
vi.mock("antd", async importOriginal => ({
  ...await importOriginal<typeof import("antd")>(),
  App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error }, modal: { confirm: mocks.confirm } }) },
  Modal: ({ open, title, children, onOk, onCancel, footer }: { open?: boolean; title?: ReactNode; children?: ReactNode; onOk?: () => void; onCancel?: () => void; footer?: ReactNode }) => open ? <section role="dialog" aria-label={String(title)}>{children}{footer === null ? null : <button onClick={onOk}>保存</button>}<button onClick={onCancel}>取消</button></section> : null
}));

const user = { id: "admin", tenant: "tenant", username: "admin", displayName: "合成管理员", name: "合成管理员", role: "school_admin", school: "合成学校", currentExam: "", permissions: ["org:manage"], publicComputer: false, roles: ["school_admin"], organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-a"], gradeIds: [], classIds: [] } } as SessionUser;
const older: Grade = { id: "grade-older", tenant_id: "tenant", school_id: "school-a", name: "高一", level_no: 10, academic_year: "2026-2027", education_stage: "senior", grade_cohort_id: "cohort-2026", entry_year: 2026, status: "active" };
const newer: Grade = { ...older, id: "grade-newer", name: "高二", level_no: 11, academic_year: "2027-2028" };

beforeEach(() => {
  vi.resetAllMocks();
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn() })));
  mocks.schools = [{ id: "school-b", name: "其他学校", status: "active" }, { id: "school-a", name: "合成学校", status: "active" }] as School[];
  mocks.grades = [older, newer];
  mocks.years.mockResolvedValue({ academic_years: [{ school_id: "school-a", is_current: true, start_year: 2028 }] });
  mocks.classes = [{ id: "class-old", tenant_id: "tenant", school_id: "school-a", grade_id: older.id, name: "高一（1）班", code: "2026-01", status: "active" }, { id: "class-new", tenant_id: "tenant", school_id: "school-a", grade_id: newer.id, name: "实验班", code: "2026-02", status: "active" }];
  mocks.students = [{ id: "student-a", class_id: "class-old", status: "inactive" }] as Student[];
  mocks.updateGrade.mockImplementation(async (id: string, payload: Partial<Grade>) => { const grade = { ...newer, ...payload, name: payload.level_no === 12 ? "高三" : newer.name }; mocks.grades = [older, grade]; return { grade: { ...grade, id } }; });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

// 保留真实 AntD 表单与下拉；按字段标签和弹窗范围查找，避免 jsdom 反复计算整页可见性。
// 交互预算覆盖真实校验与状态刷新，断言等待仍使用 Testing Library 的短预算。
describe("固定届别班级管理", { timeout: 15_000 }, () => {
  it("本校固定、顶部仅届别筛选，无左栏学年；跨学年全部旧绑定班级仍显示", async () => {
    const { container } = render(<ClassManagementPage currentUser={user} />);
    await screen.findByText("2026级 · 高二", { selector: "h2" });
    expect(screen.queryByLabelText("学校", { selector: "input" })).toBeNull();
    expect(screen.queryByText("其他学校")).toBeNull();
    expect(container.querySelector("aside")).toBeNull();
    expect(screen.queryByText("学年")).toBeNull();
    expect(screen.getByLabelText("届别年级", { selector: "input" })).toBeDefined();
    const toolbar = container.querySelector(".class-cohort-toolbar")!;
    expect(within(toolbar as HTMLElement).queryByText("合成学校")).toBeNull();
    expect(toolbar.firstElementChild?.querySelector('input[aria-label="届别年级"]')).not.toBeNull();
    expect(screen.getByTestId("class-old").textContent).toContain("高二（1）班");
    expect(screen.getByTestId("class-old").textContent).toContain("1 人");
    expect(screen.getByTestId("class-new").textContent).toContain("实验班");
    expect(screen.getByText("2 个班级")).toBeDefined();
  });

  it("学校进入2028学年后可校正2026级为高三，只改最新代表且不发送届别归属", async () => {
    render(<ClassManagementPage currentUser={user} />);
    fireEvent.click(await screen.findByText("编辑年级"));
    const dialog = within(screen.getByLabelText("编辑年级", { selector: "section" }));
    expect(dialog.getByLabelText<HTMLInputElement>("入学届别").value).toBe("2026级");
    expect(dialog.queryByLabelText("年级名称")).toBeNull();
    fireEvent.mouseDown(dialog.getByLabelText("当前年级"));
    fireEvent.click(await screen.findByText("高三", { selector: ".ant-select-item-option-content" }));
    fireEvent.click(dialog.getByText("保存"));
    await waitFor(() => expect(mocks.updateGrade).toHaveBeenCalledExactlyOnceWith("grade-newer", { level_no: 12 }));
    await screen.findByText("2026级 · 高三", { selector: "h2" });
    expect(screen.getByTestId("class-old").textContent).toContain("高三（1）班");
  });

  it("编辑旧班级使用所属当前届别和短班名，不把历史高一名称带进高二表单", async () => {
    mocks.updateClass.mockImplementation(async (id: string, payload: Partial<SchoolClass>) => ({ class: { ...mocks.classes[0], ...payload, id } }));
    render(<ClassManagementPage currentUser={user} />);
    const row = await screen.findByTestId("class-old");
    fireEvent.click(within(row).getByText("编辑"));
    const dialog = within(screen.getByLabelText("编辑班级", { selector: "section" }));
    expect(dialog.getByLabelText<HTMLInputElement>("所属届别年级").value).toBe("2026级 · 高二");
    expect(dialog.getByLabelText<HTMLInputElement>("班号或自定义班名").value).toBe("1班");
    expect(dialog.getByLabelText<HTMLInputElement>("显示名称").value).toBe("2026级 · 高二 · 1班");
    fireEvent.click(dialog.getByText("保存"));
    await waitFor(() => expect(mocks.updateClass).toHaveBeenCalledExactlyOnceWith("class-old", { name: "1班", code: "2026-01" }));
  });

  it("新增班级与列表共用当前届别，输入班号预览完整名称而不改变原年级ID", async () => {
    mocks.createClass.mockResolvedValue({ class: { ...mocks.classes[0], id: "created" } });
    render(<ClassManagementPage currentUser={user} />);
    fireEvent.click(await screen.findByText("新增班级"));
    const dialog = within(screen.getByLabelText("新增班级", { selector: "section" }));
    expect(dialog.getByLabelText<HTMLInputElement>("所属届别年级").value).toBe("2026级 · 高二");
    fireEvent.change(dialog.getByLabelText("班号或自定义班名"), { target: { value: "2" } });
    fireEvent.change(dialog.getByLabelText("班级代码"), { target: { value: "2026-03" } });
    const preview = dialog.getByLabelText<HTMLInputElement>("显示名称");
    await waitFor(() => expect(preview.value).toBe("2026级 · 高二 · 2班"));
    fireEvent.click(dialog.getByText("新增班级"));
    await waitFor(() => expect(mocks.createClass).toHaveBeenCalledExactlyOnceWith({ school_id: "school-a", grade_id: "grade-newer", name: "2班", code: "2026-03" }));
  });

  it("删除非空班级仍提交后端核验，失败保留班级并显示具体原因", async () => {
    mocks.deleteClass.mockRejectedValue(new Error("班级下仍有学生，含停用学生，请先调整学生归属"));
    render(<ClassManagementPage currentUser={user} />);
    const row = await screen.findByTestId("class-old");
    const remove = within(row).getByText("删除").closest("button")!;
    expect((remove as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(remove);
    const confirmation = mocks.confirm.mock.lastCall![0] as { onOk: () => Promise<void> };
    await act(async () => { await expect(confirmation.onOk()).rejects.toThrow("班级下仍有学生"); });
    expect(mocks.deleteClass).toHaveBeenCalledExactlyOnceWith("class-old");
    expect((await screen.findByText("班级下仍有学生，含停用学生，请先调整学生归属")).closest('[role="alert"]')).not.toBeNull();
    expect(screen.getByTestId("class-old")).toBeDefined();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("删除届别调用最新代表ID，空届别成功后不让旧年度记录重新出现", async () => {
    mocks.classes = []; mocks.students = [];
    mocks.deleteGrade.mockImplementation(async () => { mocks.grades = []; });
    render(<ClassManagementPage currentUser={user} />);
    fireEvent.click(await screen.findByText("删除年级"));
    const confirmation = mocks.confirm.mock.lastCall![0] as { content: string; onOk: () => Promise<void> };
    expect(confirmation.content).toContain("整个届别");
    await act(async () => { await confirmation.onOk(); });
    expect(mocks.deleteGrade).toHaveBeenCalledExactlyOnceWith("grade-newer");
    await waitFor(() => expect(screen.queryByText("删除年级")).toBeNull());
    expect(screen.queryByText(/2026级/, { selector: "h2" })).toBeNull();
  });

  it("未确定本校范围时不拿学校列表首项充当本校，平台管理员保留学校选择", async () => {
    const view = render(<ClassManagementPage currentUser={{ ...user, organizationScope: { ...user.organizationScope, schoolIds: [] } }} />);
    await screen.findByText("当前账号尚未确定本校管理范围，请重新登录或联系管理员核对。");
    expect(screen.queryByText("新增年级")).toBeNull();
    view.rerender(<ClassManagementPage currentUser={{ ...user, roles: ["tenant_admin"], organizationScope: { ...user.organizationScope, tenantWide: true } }} />);
    await screen.findByLabelText("学校", { selector: "input" });
  });

  it("新建同届先提示重复，未来和已毕业届别不能提交，合法旧届自动算高二", async () => {
    mocks.years.mockResolvedValue({ academic_years: [{ school_id: "school-a", is_current: true, start_year: 2026 }] });
    mocks.grades = [older]; mocks.classes = []; mocks.students = [];
    mocks.createGrade.mockImplementation(async payload => ({ grade: { ...older, ...payload, id: "created", grade_cohort_id: "created-cohort" } }));
    render(<ClassManagementPage currentUser={user} />);
    fireEvent.click(await screen.findByText("新增年级"));
    const dialog = within(screen.getByLabelText("新增年级", { selector: "section" }));
    const submit = dialog.getByText("新增年级").closest("button")!;
    expect(dialog.getByText(/2026级高中已存在/)).toBeTruthy();
    expect(submit.disabled).toBe(true);
    const entry = dialog.getByLabelText("入学年份（届别）");
    fireEvent.change(entry, { target: { value: "2027" } });
    expect(await dialog.findByText(/尚未入学/)).toBeTruthy();
    expect(submit.disabled).toBe(true);
    fireEvent.change(entry, { target: { value: "2023" } });
    expect(await dialog.findByText(/超出当前学段的三年范围/)).toBeTruthy();
    fireEvent.change(entry, { target: { value: "2025" } });
    await waitFor(() => expect(dialog.getByLabelText<HTMLInputElement>("显示名称").value).toBe("2025级 · 高二"));
    expect(submit.disabled).toBe(false);
    expect(dialog.getByLabelText<HTMLInputElement>("当前年级（自动）").disabled).toBe(true);
    fireEvent.click(submit);
    await waitFor(() => expect(mocks.createGrade).toHaveBeenCalledWith({ school_id: "school-a", education_stage: "senior", entry_year: 2025, level_no: 11, name: "高二" }));
  });

  it("其他管理员抢先创建后刷新校验，保留输入并阻止继续重复提交", async () => {
    mocks.years.mockResolvedValue({ academic_years: [{ school_id: "school-a", is_current: true, start_year: 2026 }] });
    mocks.grades = [older]; mocks.classes = []; mocks.students = [];
    mocks.createGrade.mockImplementation(async () => {
      mocks.grades.push({ ...older, id: "concurrent", entry_year: 2025, level_no: 11, grade_cohort_id: "cohort-2025" });
      throw new Error("该入学届别已存在");
    });
    render(<ClassManagementPage currentUser={user} />);
    fireEvent.click(await screen.findByText("新增年级"));
    const dialog = within(screen.getByLabelText("新增年级", { selector: "section" }));
    const entry = dialog.getByLabelText<HTMLInputElement>("入学年份（届别）");
    fireEvent.change(entry, { target: { value: "2025" } });
    const submit = dialog.getByText("新增年级").closest("button")!;
    await waitFor(() => expect(submit.disabled).toBe(false));
    fireEvent.click(submit);
    await dialog.findByText(/2025级高中已存在/);
    expect(entry.value).toBe("2025");
    expect(submit.disabled).toBe(true);
    expect(mocks.createGrade).toHaveBeenCalledTimes(1);
  });
});
