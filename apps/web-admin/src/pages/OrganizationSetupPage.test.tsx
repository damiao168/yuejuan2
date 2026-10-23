// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OrganizationSetupPage } from "./OrganizationSetupPage";

const mocks = vi.hoisted(() => ({
  schools: vi.fn(), grades: vi.fn(), classes: vi.fn(), students: vi.fn(), users: vi.fn(), roles: vi.fn(), exams: vi.fn(),
  queryClient: { invalidateQueries: vi.fn() }, createUser: vi.fn(), staffValues: {} as Record<string, unknown>
}));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => mocks.queryClient }));
vi.mock("../features/onboarding/queries", () => ({ onboardingQueryKey: ["onboarding", "readiness"], useOnboardingReadiness: () => ({ data: undefined }) }));
vi.mock("../api/org", () => ({
  listSchools: mocks.schools, listGrades: mocks.grades, listClasses: mocks.classes, listStudents: mocks.students,
  createSchool: vi.fn(), createGrade: vi.fn(), createClass: vi.fn(), importStudentsCSV: vi.fn()
}));
vi.mock("../api/users", () => ({ listManagedUsers: mocks.users, listAssignableRoles: mocks.roles, createManagedUser: mocks.createUser }));
vi.mock("../api/exams", () => ({ listExams: mocks.exams, createExam: vi.fn() }));
vi.mock("../components/PageState", () => ({ LoadingState: ({ label }: { label: string }) => <div>{label}</div>, ErrorState: () => null }));
vi.mock("../components/ResponsiveTable", () => ({ ResponsiveTable: ({ dataSource }: { dataSource: unknown[] }) => <div data-testid="import-preview">{dataSource.length}</div> }));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => ({ message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }) },
    Button: ({ children, onClick, disabled }: { children?: ReactNode; onClick?: () => void; disabled?: boolean }) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Form: Object.assign(({children,onFinish}: {children?:ReactNode;onFinish?:(values:Record<string,unknown>)=>void}) => <form onSubmit={(event)=>{event.preventDefault();onFinish?.(mocks.staffValues);}}>{children}</form>, { Item: passthrough, useForm: () => [{ resetFields: vi.fn() }], useWatch: () => undefined }),
    Input: Object.assign(() => null, { Password: () => null }), InputNumber: () => null, Select: () => null,
    Alert: ({ message, description }: { message?: ReactNode; description?: ReactNode }) => <div>{message}{description}</div>,
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
  mocks.schools.mockResolvedValue({ schools: [{ id: "school", name: "示例学校" }] });
  mocks.grades.mockResolvedValue({ grades: [{ id: "grade", name: "高一", academic_year: "2026-2027" }] });
  mocks.classes.mockResolvedValue({ classes: [{ id: "class", school_id: "school", code: "G10-01", name: "一班" }] });
  mocks.students.mockResolvedValue({ students: [{ student_no: "existing" }] });
  mocks.users.mockResolvedValue({ users: [{ status: "active", roles: ["teacher"] }] });
  mocks.roles.mockResolvedValue({ roles: [] });
  mocks.exams.mockResolvedValue({ exams: [] });
});
afterEach(cleanup);

describe("学校初始化展示", () => {
  it("教师邀请不发送初始密码，刷新失败仍保留一次性激活链接与待激活事实",async()=>{
    mocks.users.mockResolvedValue({users:[]});
    mocks.roles.mockResolvedValue({roles:[{code:"teacher",name:"教师"}]});
    mocks.staffValues={display_name:"王老师",username:"wang_teacher",phone:"13800000000",school_id:"school"};
    mocks.createUser.mockResolvedValue({user:{id:"invited",display_name:"王老师",status:"invited"},activation:{path:"/activate?token=one-time",expires_at:"2026-10-09T00:00:00Z"}});
    render(<OrganizationSetupPage onNavigate={vi.fn()}/>);
    await screen.findByRole("heading",{name:"人员账号"});
    mocks.schools.mockRejectedValueOnce(new Error("refresh failed"));
    fireEvent.submit(screen.getByRole("button",{name:"创建激活邀请"}).closest("form")!);
    await screen.findByText("王老师：邀请已创建，等待激活");
    expect(mocks.createUser).toHaveBeenCalledWith({...mocks.staffValues,employee_no:undefined,role_codes:["grader","arbitrator"],class_ids:[]});
    expect(mocks.createUser.mock.calls[0][0]).not.toHaveProperty("password");
    expect(screen.getByRole("button",{name:"复制激活链接"})).toBeTruthy();
    expect(screen.getByRole("button",{name:"人员账号"}).getAttribute("data-current")).toBe("true");
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

  it("邀请响应缺少激活链接时保留待激活回执并提供核对提示", async () => {
    mocks.users.mockResolvedValue({ users: [] });
    mocks.staffValues = { display_name: "王老师", username: "wang_teacher", phone: "13800000000", school_id: "school" };
    mocks.createUser.mockResolvedValue({ user: { id: "invited", display_name: "王老师", status: "invited" } });
    render(<OrganizationSetupPage onNavigate={vi.fn()} />);
    await screen.findByRole("heading", { name: "人员账号" });
    fireEvent.submit(screen.getByRole("button", { name: "创建激活邀请" }).closest("form")!);
    await screen.findByText("王老师：邀请已创建，等待激活");
    expect(screen.getByText("本次响应未提供激活链接，请进入人员管理核对该账号。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "复制激活链接" })).toBeNull();
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
