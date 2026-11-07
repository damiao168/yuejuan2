// @vitest-environment jsdom
import { App, ConfigProvider } from "antd";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../auth/session";
import { StudentManagementPage } from "./StudentManagementPage";

// 保留真实 AntD 表格、选择器和弹窗；低资源测试环境的布局与无障碍树构建需要独立于接口等待的时间预算。
vi.setConfig({ testTimeout: 60000 });

const api = vi.hoisted(() => ({ listStudents: vi.fn(), listGrades: vi.fn(), listClasses: vi.fn(), transferStudent: vi.fn(), updateStudent: vi.fn(), createStudent: vi.fn(), openStudentAccount: vi.fn(), deleteStudent: vi.fn() }));
vi.mock("../api/org", () => ({ ...api, importStudentsCSV: vi.fn(), updateStudentStatus: vi.fn() }));
vi.mock("../auth/stepUpContext", () => ({ useStepUp: () => ({ runWithStepUp: vi.fn() }), isStepUpCancelledError: () => false }));

const user: SessionUser = {
  id: "admin", username: "admin", displayName: "合成管理员", name: "合成管理员", role: "school_admin", roles: ["school_admin"], tenant: "tenant", school: "本校", currentExam: "", permissions: ["student:manage"], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] }
};
const grades = [
  { id: "grade-26-old", tenant_id: "tenant", school_id: "school", grade_cohort_id: "cohort-26", entry_year: 2026, name: "旧记录自定义名", level_no: 10, education_stage: "senior", academic_year: "2026-2027", status: "active" },
  { id: "grade-26", tenant_id: "tenant", school_id: "school", grade_cohort_id: "cohort-26", entry_year: 2026, name: "当前自定义名", level_no: 11, education_stage: "senior", academic_year: "2027-2028", status: "active" },
  { id: "grade-25", tenant_id: "tenant", school_id: "school", grade_cohort_id: "cohort-25", entry_year: 2025, name: "高二", level_no: 11, education_stage: "senior", academic_year: "2026-2027", status: "active" },
  { id: "foreign-grade", tenant_id: "tenant", school_id: "foreign", entry_year: 2026, name: "外校年级", level_no: 10, education_stage: "senior", academic_year: "2026-2027", status: "active" }
];
const classes = [
  { id: "class-a", school_id: "school", grade_id: "grade-26-old", class_no: 1, name: "高一（1）班", code: "A", status: "active" },
  { id: "class-a-new", school_id: "school", grade_id: "grade-26", class_no: 2, name: "实验班", code: "A2", status: "active" },
  { id: "class-b", school_id: "school", grade_id: "grade-25", class_no: 1, name: "高二（1）班", code: "B", status: "active" },
  { id: "class-foreign", school_id: "foreign", grade_id: "foreign-grade", name: "外校班级", code: "F", status: "active" }
];
const students = [
  { id: "alice", school_id: "school", class_id: "class-a", student_no: "001", name: "甲同学", status: "active" },
  { id: "david", school_id: "school", class_id: "class-a-new", student_no: "002", name: "乙同学", status: "active" },
  { id: "bob", school_id: "school", class_id: "class-b", student_no: "003", name: "丙同学", status: "inactive" },
  { id: "foreign", school_id: "foreign", class_id: "class-foreign", student_no: "004", name: "外校同学", status: "active" }
];

beforeEach(() => {
  vi.clearAllMocks();
  api.listStudents.mockResolvedValue({ students, has_more: false });
  api.listGrades.mockResolvedValue({ grades });
  api.listClasses.mockResolvedValue({ classes });
  api.transferStudent.mockResolvedValue({ student: { ...students[0], class_id: "class-a-new" } });
  vi.stubGlobal("matchMedia", vi.fn((query: string) => ({ matches: query.includes("min-width"), media: query, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })));
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const original = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation((element) => original(element));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function openPage() {
  render(<ConfigProvider theme={{ token: { motion: false } }}><App><StudentManagementPage currentUser={user} /></App></ConfigProvider>);
  await screen.findByText("甲同学");
}
async function selectOption(name: string, text: string) {
  const select = document.querySelector<HTMLInputElement>(`input[role="combobox"][aria-label="${name}"]`)!;
  expect(select).not.toBeNull();
  fireEvent.mouseDown(select.closest(".ant-select-selector") ?? select);
  fireEvent.click(await screen.findByText(text, { selector: ".ant-select-item-option-content" }));
}

describe("学生管理的届别与直接转班操作", () => {
  it("全部、在籍、已停用和唯一届别筛选始终在同一工具区，同届跨学年学生不漏掉", async () => {
    await openPage();
    const filters = screen.getByRole("region", { name: "学生筛选" });
    expect(within(filters).getAllByRole("radio").map((item) => item.closest("label")?.textContent)).toEqual(["全部", "在籍", "已停用"]);
    expect(within(filters).getByRole<HTMLInputElement>("radio", { name: "全部" }).checked).toBe(true);
    expect(screen.queryByText("学年")).toBeNull();
    expect(screen.queryByRole("button", { name: /更多筛选|收起筛选/ })).toBeNull();
    expect(screen.queryByText("学校")).toBeNull();
    expect(screen.queryByText("外校同学")).toBeNull();
    await selectOption("届别年级", "2026级 · 高二");
    expect(screen.getByText("甲同学")).toBeTruthy();
    expect(screen.getByText("乙同学")).toBeTruthy();
    expect(screen.queryByText("丙同学")).toBeNull();
    expect(within(filters).getAllByText("2026级 · 高二")).toHaveLength(1);
  });

  it("换届别清掉原班级条件，班级下拉只含新届别，切状态也不残留隐藏筛选", async () => {
    await openPage();
    await selectOption("届别年级", "2026级 · 高二");
    await selectOption("班级筛选", "实验班");
    expect(screen.queryByText("甲同学")).toBeNull();
    await selectOption("届别年级", "2025级 · 高二");
    expect(screen.getByText("丙同学")).toBeTruthy();
    expect(screen.queryByText("乙同学")).toBeNull();
    const classFilter = screen.getByRole("combobox", { name: "班级筛选" }).closest(".ant-select")!;
    expect(classFilter.textContent).toContain("全部班级");
    await selectOption("届别年级", "全部届别年级");
    fireEvent.click(screen.getByRole("radio", { name: "已停用" }));
    expect(screen.getByText("丙同学")).toBeTruthy();
    expect(screen.queryByText("甲同学")).toBeNull();
    fireEvent.click(screen.getByRole("radio", { name: "全部" }));
    expect(screen.getByText("甲同学")).toBeTruthy();
  });

  it("调整班级直接可见且走转班接口，状态与删除也直接显示，没有更多入口", async () => {
    await openPage();
    const row = screen.getByText("甲同学").closest("tr")!;
    fireEvent.click(within(row).getByRole("button", { name: "调整班级" }));
    const dialog = await screen.findByRole("dialog", { name: "调整班级 · 甲同学" });
    await selectOption("目标班级", "2026级 · 高二 · 实验班");
    fireEvent.click(within(dialog).getByRole("button", { name: "保 存" }));
    await waitFor(() => expect(api.transferStudent).toHaveBeenCalledWith("alice", "class-a-new"));
    expect(api.updateStudent).not.toHaveBeenCalled();
    expect(api.createStudent).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const inactiveRow = screen.getByText("丙同学").closest("tr")!;
    expect(within(inactiveRow).getByRole("button", { name: "恢复" })).toBeTruthy();
    expect(within(inactiveRow).getByRole("button", { name: "彻底删除" })).toBeTruthy();
    expect(within(row).getByRole("button", { name: "停用" })).toBeTruthy();
    expect(within(row).queryByRole("button", { name: "彻底删除" })).toBeNull();
    expect(screen.queryByRole("button", { name: /更多/ })).toBeNull();
  });

  it("刷新失效班级后清掉旧班级筛选，不把已隐藏的条件留在空表中", async () => {
    await openPage();
    await selectOption("班级筛选", "实验班");
    api.listClasses.mockResolvedValue({ classes: classes.filter((item) => item.id !== "class-a-new") });
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    await waitFor(() => expect(screen.getByText("甲同学")).toBeTruthy());
    const classFilter = screen.getByRole("combobox", { name: "班级筛选" }).closest(".ant-select")!;
    expect(classFilter.textContent).toContain("全部班级");
  });

  it("新增时必须设置并确认密码，保存后名册立即显示已开通账号", async () => {
    await openPage();
    fireEvent.click(screen.getByRole("button", { name: "新增学生" }));
    const dialog = await screen.findByRole("dialog", { name: "新增学生" });
    await selectOption("学生班级", "2026级 · 高二 · 实验班");
    fireEvent.change(within(dialog).getByLabelText("学号"), { target: { value: " 0042 " } });
    fireEvent.change(within(dialog).getByLabelText("姓名"), { target: { value: " 丁同学 " } });
    fireEvent.click(within(dialog).getByRole("button", { name: "保 存" }));
    await within(dialog).findByText("请输入初始密码", {}, { timeout: 10000 });
    expect(api.createStudent).not.toHaveBeenCalled();
    fireEvent.change(within(dialog).getByLabelText("初始密码"), { target: { value: "  secret123  " } });
    fireEvent.change(within(dialog).getByLabelText("确认密码"), { target: { value: "different" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "保 存" }));
    await within(dialog).findByText("两次输入的密码不一致", {}, { timeout: 10000 });
    expect(api.createStudent).not.toHaveBeenCalled();
    api.createStudent.mockResolvedValue({ student: { id: "new-student", tenant_id: "tenant", school_id: "school", class_id: "class-a-new", student_no: "0042", name: "丁同学", status: "active", account_status: "active", login_account: "0042", login_school_code: "SCH01", must_change_password: true } });
    fireEvent.change(within(dialog).getByLabelText("确认密码"), { target: { value: "  secret123  " } });
    fireEvent.click(within(dialog).getByRole("button", { name: "保 存" }));
    await waitFor(() => expect(api.createStudent).toHaveBeenCalledWith({ school_id: "school", class_id: "class-a-new", student_no: "0042", name: "丁同学", gender: undefined, password: "  secret123  ", password_confirm: "  secret123  " }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const row = screen.getByText("丁同学").closest("tr")!;
    expect(within(row).getByText("已启用")).toBeTruthy();
    expect(within(row).getByText("学校代码 SCH01 · 首次登录需改密")).toBeTruthy();
    expect(within(row).queryByRole("button", { name: "开通账号" })).toBeNull();
    expect(api.listStudents).toHaveBeenCalledTimes(1);
  });

  it("既有未开户学生可单独开通账号，停用学生先恢复，编辑不重新索要密码", async () => {
    await openPage();
    const inactiveRow = screen.getByText("丙同学").closest("tr")!;
    expect(within(inactiveRow).getByRole<HTMLButtonElement>("button", { name: "开通账号" }).disabled).toBe(true);
    const row = screen.getByText("甲同学").closest("tr")!;
    fireEvent.click(within(row).getByRole("button", { name: "开通账号" }));
    const dialog = await screen.findByRole("dialog", { name: "开通账号 · 甲同学" });
    expect(within(dialog).getByText(/登录账号：001/)).toBeTruthy();
    expect((within(dialog).getByLabelText("初始密码") as HTMLInputElement).value).toBe("");
    api.openStudentAccount.mockResolvedValue({ student: { ...students[0], account_status: "active", login_account: "001", login_school_code: "SCH01", must_change_password: true } });
    fireEvent.change(within(dialog).getByLabelText("初始密码"), { target: { value: "Student123!" } });
    fireEvent.change(within(dialog).getByLabelText("确认密码"), { target: { value: "Student123!" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "开通账号" }));
    await waitFor(() => expect(api.openStudentAccount).toHaveBeenCalledWith("alice", { password: "Student123!", password_confirm: "Student123!" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const updatedRow = screen.getByText("甲同学").closest("tr")!;
    expect(within(updatedRow).getByText("已启用")).toBeTruthy();
    const editButton = () => within(screen.getByText("甲同学").closest("tr")!).getByRole<HTMLButtonElement>("button", { name: "编辑" });
    await waitFor(() => expect(editButton().disabled).toBe(false));
    expect(editButton().isConnected).toBe(true);
    fireEvent.click(editButton());
    // rc-dialog 在测试环境复用 test-id；已关闭弹窗的标题会干扰名字查询，从可见标题定位新的弹窗。
    const edit = (await screen.findByText("编辑学生")).closest<HTMLElement>('[role="dialog"]')!;
    expect(screen.getAllByRole("dialog")).toContain(edit);
    expect(within(edit).queryByLabelText("初始密码")).toBeNull();
    expect(within(edit).getByText(/修改后旧学号失效/)).toBeTruthy();
  });
});
