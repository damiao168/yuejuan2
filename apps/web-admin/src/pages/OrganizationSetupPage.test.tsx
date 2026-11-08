// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OrganizationSetupPage } from "./OrganizationSetupPage";

const mocks = vi.hoisted(() => ({
  schools: vi.fn(), grades: vi.fn(), classes: vi.fn(), students: vi.fn(), years: vi.fn(), users: vi.fn(), roles: vi.fn(), exams: vi.fn(),
  queryClient: { invalidateQueries: vi.fn() }, createUser: vi.fn(), createGrade: vi.fn(), staffFormReset: vi.fn(),
  message: { success: vi.fn(), error: vi.fn() }, gradeValues: {} as Record<string, unknown>, staffValues: {} as Record<string, unknown>
}));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => mocks.queryClient }));
vi.mock("../features/onboarding/queries", () => ({ onboardingQueryKey: ["onboarding", "readiness"], useOnboardingReadiness: () => ({ data: undefined }) }));
vi.mock("../api/org", () => ({
  listSchools: mocks.schools, listGrades: mocks.grades, listClasses: mocks.classes, listStudents: mocks.students,
  listAcademicYears: mocks.years,
  createSchool: vi.fn(), createGrade: mocks.createGrade, createClass: vi.fn(), importStudentsCSV: vi.fn()
}));
vi.mock("../api/users", () => ({ listManagedUsers: mocks.users, listAssignableRoles: mocks.roles, createManagedUser: mocks.createUser }));
vi.mock("../api/exams", () => ({ listExams: mocks.exams, createExam: vi.fn() }));
vi.mock("../components/PageState", () => ({ LoadingState: ({ label }: { label: string }) => <div>{label}</div>, ErrorState: () => null }));
vi.mock("../components/ResponsiveTable", () => ({ ResponsiveTable: ({ dataSource }: { dataSource: unknown[] }) => <div data-testid="import-preview">{dataSource.length}</div> }));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => ({ message: mocks.message }) },
    Button: ({ children, onClick, disabled }: { children?: ReactNode; onClick?: () => void; disabled?: boolean }) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Form: Object.assign(({children,onFinish}: {children?:ReactNode;onFinish?:(values:Record<string,unknown>)=>void}) => <form onSubmit={(event)=>{event.preventDefault();onFinish?.(mocks.staffValues);}}>{children}</form>, { Item: passthrough, useForm: () => [{ resetFields: mocks.staffFormReset, setFieldsValue: vi.fn() }], useWatch: (name: string) => mocks.gradeValues[name] }),
    Input: Object.assign(() => null, { Password: () => null }), InputNumber: () => null, Select: () => null,
    Alert: ({ message, description }: { message?: ReactNode; description?: ReactNode }) => (
      <div><div>{message}</div>{description && <div>{description}</div>}</div>
    ),
    Result: ({ title, extra }: { title: ReactNode; extra?: ReactNode }) => <section><h2>{title}</h2>{extra}</section>,
    Space: passthrough, Tag: passthrough,
    Steps: ({ items, current, onChange }: { items: Array<{ title: string; status: string; disabled?: boolean }>; current: number; onChange: (index: number) => void }) => (
      <nav>{items.map((item, index) => <button key={item.title} type="button" data-status={item.status} data-current={index === current} disabled={item.disabled} onClick={() => onChange(index)}>{item.title}</button>)}</nav>
    ),
    Upload: ({ children, beforeUpload }: { children?: ReactNode; beforeUpload: (file: File) => boolean }) => <>{children}<input type="file" aria-label="学生 CSV" onChange={(event) => {
      const file = event.target.files?.[0];
      if (file) beforeUpload(file);
    }} /></>
  };
});

beforeEach(() => {
  vi.clearAllMocks();
  mocks.gradeValues = {};
  mocks.schools.mockResolvedValue({ schools: [{ id: "school", name: "示例学校" }] });
  mocks.years.mockResolvedValue({ academic_years: [{ school_id: "school", is_current: true, start_year: 2026 }] });
  mocks.grades.mockResolvedValue({ grades: [{ id: "grade", name: "高一", academic_year: "2026-2027" }] });
  mocks.classes.mockResolvedValue({ classes: [{ id: "class", school_id: "school", code: "G10-01", name: "一班" }] });
  mocks.students.mockResolvedValue({ students: [{ student_no: "existing" }] });
  mocks.users.mockResolvedValue({ users: [{ status: "active", roles: ["teacher"] }] });
  mocks.roles.mockResolvedValue({ roles: [] });
  mocks.exams.mockResolvedValue({ exams: [] });
});
afterEach(cleanup);

describe("学校初始化展示", () => {
  it("初始化也提前拦截重复届别，绕过按钮提交仍不调用创建接口", async () => {
    mocks.gradeValues = { school_id: "school", education_stage: "senior", entry_year: 2026 };
    mocks.staffValues = { ...mocks.gradeValues };
    mocks.grades.mockResolvedValue({ grades: [{ id: "grade", school_id: "school", education_stage: "senior", entry_year: 2026, level_no: 10, academic_year: "2026-2027", name: "高一" }] });
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: "创建第一场考试" });
    fireEvent.click(screen.getByRole("button", { name: "届别与年级" }));
    expect(await screen.findByText(/2026级高中已存在/)).toBeTruthy();
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "保存并继续" });
    expect(submit.disabled).toBe(true);
    fireEvent.submit(submit.closest("form")!);
    expect(mocks.createGrade).not.toHaveBeenCalled();
  });
  it("初始化从学校学年重算年级，不发送调用方强行填写的矛盾级数", async () => {
    mocks.gradeValues = { school_id: "school", education_stage: "senior", entry_year: 2025 };
    mocks.staffValues = { ...mocks.gradeValues, level_no: 12 };
    mocks.createGrade.mockResolvedValue({ grade: { id: "new-grade" } });
    mocks.grades.mockResolvedValue({ grades: [{ id: "grade", school_id: "school", education_stage: "senior", entry_year: 2026, level_no: 10, academic_year: "2026-2027", name: "高一" }] });
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: "创建第一场考试" });
    fireEvent.click(screen.getByRole("button", { name: "届别与年级" }));
    const submit = await screen.findByRole<HTMLButtonElement>("button", { name: "保存并继续" });
    expect(submit.disabled).toBe(false);
    fireEvent.submit(submit.closest("form")!);
    await waitFor(() => expect(mocks.createGrade).toHaveBeenCalledWith({ school_id: "school", education_stage: "senior", entry_year: 2025, level_no: 11, name: "高二" }));
  });
  it.each([
    { existingSchool: "school-a", csv: "S001,乙校学生,B", valid: 1 },
    { existingSchool: "school-b", csv: "S001,乙校学生,B", valid: 0 },
    { existingSchool: "", csv: "S001,甲校学生,A\nS001,乙校学生,B", valid: 2 },
    { existingSchool: "", csv: "S001,甲校学生,A\ns001,甲校重复,A", valid: 1 }
  ])("学生导入按学校及学号去重 $existingSchool/$valid", async ({ existingSchool, csv, valid }) => {
    mocks.classes.mockResolvedValue({ classes: [{ id: "class-a", school_id: "school-a", code: "A" }, { id: "class-b", school_id: "school-b", code: "B" }] });
    mocks.students.mockResolvedValue({ students: existingSchool ? [{ school_id: existingSchool, student_no: "S001" }] : [] });
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: existingSchool ? "创建第一场考试" : "导入学生" });
    if (existingSchool) fireEvent.click(screen.getByRole("button", { name: "学生" }));
    const file = new File([], "students.csv", { type: "text/csv" });
    Object.defineProperty(file, "text", { value: () => Promise.resolve(`student_no,name,class_code\n${csv}`) });
    fireEvent.change(screen.getByLabelText("学生 CSV"), { target: { files: [file] } });
    const button = await screen.findByRole("button", { name: `导入 ${valid} 行` });
    expect(button.hasAttribute("disabled")).toBe(valid === 0);
  });
  it("直接创建教师并提交初始密码，刷新失败仍保留回执且清除密码", async () => {
    mocks.users.mockResolvedValue({ users: [] });
    mocks.staffValues = {
      display_name: "王老师", username: "wang_teacher", phone: "13800000000", school_id: "school",
      teaching_subjects: ["mathematics"], password: "SyntheticTeacher123!", password_confirm: "SyntheticTeacher123!",
    };
    mocks.createUser.mockResolvedValue({ user: { id: "teacher", display_name: "王老师", username: "wang_teacher", status: "active", must_change_password: true } });
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: "人员账号" });
    mocks.schools.mockRejectedValueOnce(new Error("refresh failed"));
    const form = screen.getByRole("button", { name: "创建教师账号" }).closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    await screen.findByText("王老师：教师账号已创建");
    expect(mocks.createUser).toHaveBeenCalledExactlyOnceWith({ ...mocks.staffValues, employee_no: undefined, role_codes: ["teacher", "grader", "arbitrator"] });
    expect(mocks.createUser.mock.calls[0][0]).not.toHaveProperty("class_ids");
    expect(mocks.staffFormReset).toHaveBeenCalledOnce();
    expect(screen.getByText(/登录账号 wang_teacher/)).toBeTruthy();
    expect(screen.queryByText(/SyntheticTeacher123/)).toBeNull();
    expect(screen.getByRole("button", { name: "人员账号" }).getAttribute("data-current")).toBe("true");
  });
  it.each([
    { examCount: 0, heading: "创建第一场考试", current: "第一场考试" },
    { examCount: 1, heading: "学校已经可以使用", current: "完成" }
  ])("基础数据完整且已有 $examCount 场考试时首次定位对应步骤", async ({ examCount, heading, current }) => {
    mocks.exams.mockResolvedValue({ exams: Array.from({ length: examCount }, (_, index) => ({ id: `exam-${index}` })) });
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: heading });
    expect(screen.getByRole("button", { name: current }).getAttribute("data-current")).toBe("true");
  });

  it("创建失败时保留表单并提示错误，人员步骤可重试", async () => {
    mocks.users.mockResolvedValue({ users: [] });
    mocks.staffValues = { display_name: "王老师", username: "wang_teacher", phone: "13800000000", school_id: "school" };
    mocks.createUser.mockRejectedValueOnce(new Error("request failed"));
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: "人员账号" });
    fireEvent.submit(screen.getByRole("button", { name: "创建教师账号" }).closest("form")!);
    await waitFor(() => expect(mocks.message.error).toHaveBeenCalled());
    expect(mocks.staffFormReset).not.toHaveBeenCalled();
    expect(screen.queryByText("王老师：教师账号已创建")).toBeNull();
    expect(screen.getByRole("button", { name: "人员账号" }).getAttribute("data-current")).toBe("true");
  });

  it("首次未完成学生步骤显示process，已完成项finish，后续未完成项wait并保留禁用", async () => {
    mocks.students.mockResolvedValue({ students: [] });
    mocks.users.mockResolvedValue({ users: [] });
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: "导入学生" });
    expect(screen.getByRole("button", { name: "机构信息" }).getAttribute("data-status")).toBe("finish");
    expect(screen.getByRole("button", { name: "学生" }).getAttribute("data-status")).toBe("process");
    const staff = screen.getByRole("button", { name: "人员账号" });
    expect(staff.getAttribute("data-status")).toBe("wait");
    expect(staff.hasAttribute("disabled")).toBe(true);
    expect(screen.queryByText("已导入 1 名学生，可继续或追加导入。")).toBeNull();
  });

  it("学生步骤先显示已有名单提示，选择CSV后展示预览并保留继续入口", async () => {
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: "创建第一场考试" });
    fireEvent.click(screen.getByRole("button", { name: "学生" }));
    expect(screen.getByText("已导入 1 名学生，可继续或追加导入。")).toBeTruthy();
    expect(screen.getByRole("button", { name: "学生" }).getAttribute("data-status")).toBe("finish");
    const file = new File(["student_no,name,class_code\nnew,新学生,G10-01"], "students.csv", { type: "text/csv" });
    // jsdom 的 File 缺少 text()；通过上传入口提供同一份 CSV，不直接修改组件状态。
    Object.defineProperty(file, "text", { value: () => Promise.resolve("student_no,name,class_code\nnew,新学生,G10-01") });
    fireEvent.change(screen.getByLabelText("学生 CSV"), { target: { files: [file] } });
    await screen.findByTestId("import-preview");
    expect(screen.queryByText("已导入 1 名学生，可继续或追加导入。")).toBeNull();
    expect(screen.getByRole("button", { name: "导入 1 行" }).hasAttribute("disabled")).toBe(false);
    expect(screen.getByRole("button", { name: "继续" })).toBeTruthy();
  });
});
