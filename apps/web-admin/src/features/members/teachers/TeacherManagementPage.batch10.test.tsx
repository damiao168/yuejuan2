// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Grade, School, SchoolClass } from "../../../api/org";
import type { ManagedUser } from "../../../api/users";
import type { SessionUser } from "../../../auth/session";
import { TeacherManagementPage } from "./TeacherManagementPage";

const mocks = vi.hoisted(() => ({
  users: vi.fn(), roles: vi.fn(), schools: vi.fn(), grades: vi.fn(), classes: vi.fn(),
  create: vi.fn(), access: vi.fn(), reissue: vi.fn(), recovery: vi.fn(), status: vi.fn(), stepUp: vi.fn(),
  message: { success: vi.fn(), error: vi.fn() }, modal: { success: vi.fn(), confirm: vi.fn() }, clipboard: vi.fn(),
  forms: [] as Array<{ setFieldValue: (name: string, value: unknown) => void; getFieldValue: (name: string) => unknown }>,
}));
vi.mock("../../../api/users", () => ({
  listAllManagedUsers: async () => (await mocks.users({ limit: 200 })).users, listAssignableRoles: mocks.roles, createManagedUser: mocks.create, updateManagedUserAccess: mocks.access,
  reissueManagedUserActivation: mocks.reissue, createCredentialRecovery: mocks.recovery, updateManagedUserStatus: mocks.status,
}));
vi.mock("../../../api/org", () => ({ listSchools: mocks.schools, listGrades: mocks.grades, listClasses: mocks.classes }));
vi.mock("../../../api/client", () => ({
  getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback,
}));
vi.mock("../../../auth/stepUpContext", () => ({
  useStepUp: () => ({ runWithStepUp: mocks.stepUp }),
  isStepUpCancelledError: (error: unknown) => error instanceof Error && error.name === "StepUpCancelledError",
}));
vi.mock("../../../components/StatusTag", () => ({
  StatusTag: ({ tone, children }: { tone: string; children: string }) => <span data-tone={tone}>{children}</span>,
}));
vi.mock("../../../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <p>{label}</p>,
  ErrorState: ({ message, onRetry }: { message: string; onRetry: () => void }) => <div>{message}<button type="button" onClick={onRetry}>重试</button></div>,
}));
vi.mock("../../../components/ResponsiveTable", () => ({
  ResponsiveTable: ({ columns, dataSource }: {
    columns: Array<{ title: string; dataIndex?: keyof ManagedUser; key?: string; render?: (value: unknown, user: ManagedUser, index: number) => ReactNode }>;
    dataSource: ManagedUser[];
  }) => <table><thead><tr>{columns.map((column) => <th key={column.title}>{column.title}</th>)}</tr></thead><tbody>{dataSource.map((user, index) => <tr key={user.id} data-user-id={user.id}>{columns.map((column) => {
    const value = column.dataIndex ? user[column.dataIndex] : undefined;
    return <td key={column.title} data-column={column.title}>{column.render ? column.render(value, user, index) : String(value ?? "")}</td>;
  })}</tr>)}</tbody></table>,
}));
vi.mock("antd", async () => {
  const React = await import("react");
  type Store = {
    values: Record<string, unknown>;
    listeners: Set<() => void>;
    subscribe: (listener: () => void) => () => void;
    getFieldValue: (name: string) => unknown;
    setFieldValue: (name: string, value: unknown) => void;
    resetFields: () => void;
  };
  const FormContext = React.createContext<Store | null>(null);
  function createStore(): Store {
    const store: Store = {
      values: {}, listeners: new Set(),
      subscribe(listener) { store.listeners.add(listener); return () => { store.listeners.delete(listener); }; },
      getFieldValue(name) { return store.values[name]; },
      setFieldValue(name, value) { store.values[name] = value; for (const listener of store.listeners) listener(); },
      resetFields() { store.values = {}; for (const listener of store.listeners) listener(); },
    };
    mocks.forms.push(store);
    return store;
  }
  function FormItem({ name, label, hidden, children }: { name: string; label?: string; hidden?: boolean; children: React.ReactElement<{ mode?: string; value?: unknown; onChange?: (value: unknown) => void }> }) {
    const form = React.useContext(FormContext)!;
    const value = React.useSyncExternalStore(form.subscribe, () => form.getFieldValue(name));
    return <label hidden={hidden} data-field={name}>{label}{React.cloneElement(children, {
      value: value ?? (children.props.mode === "multiple" ? [] : ""),
      onChange: (next: unknown) => {
        if (typeof next === "object" && next !== null && "target" in next) {
          form.setFieldValue(name, (next.target as HTMLInputElement).value);
        } else form.setFieldValue(name, next);
      },
    })}</label>;
  }
  const Form = Object.assign(({ form, onFinish, children }: { form: Store; onFinish: (values: Record<string, unknown>) => void; children: ReactNode }) =>
    <FormContext.Provider value={form}><form onSubmit={(event) => { event.preventDefault(); onFinish({ ...form.values }); }}>{children}</form></FormContext.Provider>, {
    useForm: () => [React.useState(createStore)[0]],
    useWatch: (name: string, form: Store) => React.useSyncExternalStore(form.subscribe, () => form.getFieldValue(name)) as string | undefined,
    Item: FormItem,
  });
  const Input = Object.assign(({ value, onChange, readOnly, addonAfter, placeholder }: {
    value?: string; onChange?: React.ChangeEventHandler<HTMLInputElement>; readOnly?: boolean; addonAfter?: ReactNode; placeholder?: string;
  }) => <><input value={value ?? ""} onChange={onChange} readOnly={readOnly} placeholder={placeholder} />{addonAfter}</>, {
    Search: ({ value, onChange, placeholder }: { value: string; onChange: React.ChangeEventHandler<HTMLInputElement>; placeholder?: string }) => <input value={value} onChange={onChange} placeholder={placeholder} />,
  });
  return {
    App: { useApp: () => ({ message: mocks.message, modal: mocks.modal }) }, Form, Input,
    Button: ({ children, onClick, loading, disabled, htmlType, danger }: { children?: ReactNode; onClick?: () => void; loading?: boolean; disabled?: boolean; htmlType?: "button" | "submit" | "reset"; danger?: boolean }) =>
      <button type={htmlType ?? "button"} onClick={onClick} disabled={loading || disabled} data-loading={String(Boolean(loading))} data-danger={String(Boolean(danger))}>{children}</button>,
    // 菜单在此直接展开，只核对命令和禁用状态。
    Dropdown: ({ children, disabled, menu }: { children: ReactNode; disabled?: boolean; menu: { items: Array<{ key: string; label: ReactNode; danger?: boolean; onClick?: () => void }> } }) =>
      <div>{children}{menu.items.map((item) => <button key={item.key} type="button" disabled={disabled} data-danger={String(Boolean(item.danger))} onClick={item.onClick}>{item.label}</button>)}</div>,
    Space: Object.assign(({ children }: { children: ReactNode }) => <div>{children}</div>, { Compact: ({ children }: { children: ReactNode }) => <div>{children}</div> }),
    Tabs: ({ items, activeKey, onChange }: { items: Array<{ key: string; label: string }>; activeKey: string; onChange: (key: string) => void }) =>
      <div role="tablist">{items.map((item) => <button key={item.key} type="button" role="tab" aria-selected={item.key === activeKey} onClick={() => onChange(item.key)}>{item.label}</button>)}</div>,
    Alert: ({ message, description, type }: { message: string; description: string; type: string }) => <aside data-alert-type={type}><strong>{message}</strong><p>{description}</p></aside>,
    Modal: ({ open, title, children, onCancel }: { open: boolean; title: string; children: ReactNode; onCancel: () => void }) =>
      open ? <div role="dialog"><h2>{title}</h2><button type="button" onClick={onCancel}>取消</button>{children}</div> : null,
    Select: ({ options, mode, value, onChange }: { options: Array<{ value: string; label: string }>; mode?: string; value?: string | string[]; onChange?: (value: string | string[]) => void }) =>
      <select multiple={mode === "multiple"} value={value} onChange={(event) => onChange?.(mode === "multiple" ? [...event.currentTarget.selectedOptions].map((option) => option.value) : event.currentTarget.value)}>
        {mode !== "multiple" ? <option value="" /> : null}{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>,
  };
});

const currentUser: SessionUser = {
  id: "self", username: "manager", displayName: "管理员", name: "管理员", role: "tenant_admin", roles: ["tenant_admin"],
  tenant: "tenant-a", school: "学校甲", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: true, schoolIds: [], gradeIds: [], classIds: [] },
};
const schools: School[] = ["甲", "乙"].map((name, index) => ({ id: `school-${index + 1}`, tenant_id: "tenant-a", name: `学校${name}`, code: name, status: "active" }));
const grades: Grade[] = [{ id: "grade-1", tenant_id: "tenant-a", school_id: "school-1", name: "七年级", level_no: 7, academic_year: "2026", education_stage: "junior", status: "active" }];
const classes: SchoolClass[] = [
  { id: "class-1", tenant_id: "tenant-a", school_id: "school-1", grade_id: "grade-1", name: "一班", code: "1", status: "active" },
  { id: "class-2", tenant_id: "tenant-a", school_id: "school-2", grade_id: "missing", name: "二班", code: "2", status: "active" },
];
const activation = { token: "token", expires_at: "2026-10-05T00:00:00Z", path: "/activate/token" };
function member(id: string, roles: string[] = ["teacher"], overrides: Partial<ManagedUser> = {}): ManagedUser {
  return { id, username: id, display_name: id, roles, status: "active", ...overrides };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("教师管理展示重构边界", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.forms.length = 0;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.users.mockResolvedValue({ users: [member("teacher-a")], next_cursor: "", has_more: false });
    mocks.roles.mockResolvedValue({ roles: ["teacher", "grader", "arbitrator", "school_admin"].map((code) => ({ code, name: code, scope_type: "school" })) });
    mocks.schools.mockResolvedValue({ schools });
    mocks.grades.mockResolvedValue({ grades });
    mocks.classes.mockResolvedValue({ classes });
    mocks.create.mockResolvedValue({ user: member("created") });
    mocks.reissue.mockResolvedValue({ activation });
    mocks.recovery.mockResolvedValue({ recovery: { ...activation, display_name: "teacher-a" } });
    mocks.stepUp.mockImplementation(({ action }: { action: () => Promise<unknown> }) => action());
    mocks.status.mockResolvedValue({ user: member("teacher-a", ["teacher"], { status: "disabled" }) });
    mocks.clipboard.mockResolvedValue(undefined);
    vi.stubGlobal("navigator", Object.assign(Object.create(navigator), { clipboard: { writeText: mocks.clipboard } }));
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
  async function render(user = currentUser) {
    await act(async () => { root.render(<TeacherManagementPage currentUser={user} />); });
  }
  function button(text: string, scope: ParentNode = host) {
    const found = [...scope.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text);
    expect(found).toBeDefined();
    return found!;
  }
  async function click(text: string, scope: ParentNode = host) {
    await act(async () => { button(text, scope).click(); });
  }
  function row(id: string) {
    const result = [...host.querySelectorAll<HTMLTableRowElement>("tbody tr")].find((item) => item.dataset.userId === id);
    expect(result).toBeDefined();
    return result!;
  }
  function field(name: string) {
    const found = host.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${name}"] input, [data-field="${name}"] select`);
    expect(found).not.toBeNull();
    return found!;
  }
  async function input(element: HTMLInputElement, value: string) {
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(element, value);
      element.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
  async function select(name: string, values: string[]) {
    const element = field(name) as HTMLSelectElement;
    await act(async () => {
      for (const option of element.options) option.selected = values.includes(option.value);
      element.dispatchEvent(new Event("change", { bubbles: true }));
    });
  }
  async function fill(role = "teacher") {
    await input(field("display_name") as HTMLInputElement, "新教师");
    await input(field("phone") as HTMLInputElement, "13800000000");
    await select("school_id", ["school-2"]);
    if (host.querySelector('[data-field="role_codes"]')) await select("role_codes", [role]);
  }
  async function submit() {
    await act(async () => { host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
  }
  async function administrators() {
    await act(async () => { host.querySelectorAll<HTMLButtonElement>('[role="tab"]')[1].click(); });
  }

  it("教师和管理员名单保持多角色优先级，搜索去空格且忽略大小写，切换清空查询", async () => {
    mocks.users.mockResolvedValue({ users: [member("teacher-a", ["teacher"], { employee_no: "ABC" }), member("grader", ["grader"]), member("reviewer", ["arbitrator"]), member("mixed", ["teacher", "school_admin"]), member("empty", []), member("student", ["student"]) ] });
    await render();
    expect(mocks.users).toHaveBeenCalledWith({ limit: 200 });
    expect([...host.querySelectorAll("tbody tr")].map((item) => (item as HTMLElement).dataset.userId)).toEqual(["teacher-a", "grader", "reviewer"]);
    await input(host.querySelector<HTMLInputElement>("input[placeholder]")!, " abc ");
    expect(host.querySelectorAll("tbody tr")).toHaveLength(1);
    await administrators();
    expect(host.querySelector<HTMLInputElement>("input[placeholder]")!.value).toBe("");
    expect(row("mixed").querySelector('[data-column="所属学校"]')?.textContent).toBe("未标明学校");
  });

  it("本人和部分角色越权账号没有动作，所有角色均可分配才可管理", async () => {
    mocks.roles.mockResolvedValue({ roles: ["teacher", "grader"].map((code) => ({ code, name: code, scope_type: "school" })) });
    mocks.users.mockResolvedValue({ users: [member("self"), member("outside", ["teacher", "arbitrator"]), member("allowed", ["teacher", "grader"])] });
    await render();
    expect(row("self").querySelectorAll("button")).toHaveLength(0);
    expect(row("outside").querySelectorAll("button")).toHaveLength(0);
    expect(button("停用", row("allowed"))).toBeDefined();
    await administrators();
    expect([...host.querySelectorAll("button")].some((item) => item.textContent === "新增管理员")).toBe(false);
    expect(host.querySelector("aside p")?.textContent).toContain("请联系机构管理员");
  });

  it.each([
    { status: "active", label: "已启用", tone: "success", recent: "尚未登录" },
    { status: "invited", label: "待激活", tone: "warning", recent: "尚未激活" },
    { status: "disabled", label: "已停用", tone: "neutral", recent: "尚未登录" },
    { status: "unknown", label: "未知状态", tone: "neutral", recent: "尚未登录" },
  ])("$status 的教师和管理员展示保持默认规则", async ({ status, label, tone, recent }) => {
    mocks.users.mockResolvedValue({ users: [member("teacher-a", ["teacher"], { status }), member("admin", ["school_admin"], { status, school_id: "missing" })] });
    await render();
    expect(row("teacher-a").querySelector(`[data-tone="${tone}"]`)?.textContent).toBe(label);
    expect(row("teacher-a").querySelector('[data-column="最近使用"]')?.textContent).toBe(recent);
    await administrators();
    expect(row("admin").querySelector(`[data-tone="${tone}"]`)?.textContent).toBe(label);
    expect(row("admin").querySelector('[data-column="所属学校"]')?.textContent).toBe("学校信息已变更");
  });

  it("最近登录日期优先于邀请状态", async () => {
    const date = "2026-10-01T03:04:05Z";
    mocks.users.mockResolvedValue({ users: [member("teacher-a", ["teacher"], { status: "invited", last_login_at: date })] });
    await render();
    expect(row("teacher-a").querySelector('[data-column="最近使用"]')?.textContent).toBe(new Date(date).toLocaleString("zh-CN"));
  });

  it.each([0, 1, 2])("管理员人数 %i 保留完整提示及权限后缀", async (count) => {
    mocks.schools.mockResolvedValue({ schools: [schools[0]] });
    mocks.users.mockResolvedValue({ users: [...Array.from({ length: count }, (_, index) => member(`admin-${index}`, ["school_admin"], { school_id: "school-1" })), member("inactive", ["school_admin"], { school_id: "school-1", status: "disabled" })] });
    await render();
    await administrators();
    const expected = count < 2 ? `学校甲当前有 ${count} 位。建议至少保留 2 位启用的学校管理员，系统不允许停用最后一位。` : "当前每所学校均有至少 2 位启用的学校管理员，系统不允许停用最后一位。";
    expect(host.querySelector("aside p")?.textContent).toBe(expected);
    expect(host.querySelector("aside")?.getAttribute("data-alert-type")).toBe(count < 2 ? "warning" : "success");
  });

  it("普通重渲染和刷新保持表单实例、输入值与焦点", async () => {
    await render();
    await click("新增教师");
    const name = field("display_name") as HTMLInputElement;
    await input(name, "保留草稿");
    name.focus();
    const form = mocks.forms[0];
    await render({ ...currentUser, displayName: "更新的管理员名" });
    expect(field("display_name")).toBe(name);
    expect(document.activeElement).toBe(name);
    await click("刷新");
    expect(field("display_name")).toBe(name);
    expect(name.value).toBe("保留草稿");
    expect(mocks.forms).toEqual([form]);
  });

  it("同一人能同时承担教师与阅卷职责，移除教师职责时不提交隐藏班级", async () => {
    await render();
    await click("新增教师");
    await fill();
    expect([...field("role_codes").querySelectorAll("option")].map((option) => option.value)).toEqual(["teacher", "grader", "arbitrator"]);
    expect(field("class_ids").textContent).toBe("未分年级 · 二班");
    await select("class_ids", ["class-2"]);
    await select("role_codes", ["teacher", "grader"]);
    expect(host.querySelector('[data-field="class_ids"]')).not.toBeNull();
    await select("role_codes", ["grader", "arbitrator"]);
    expect(host.querySelector('[data-field="class_ids"]')).toBeNull();
    expect(host.textContent).toContain("阅卷和复核范围由具体分配的任务决定。");
    await submit();
    expect(mocks.create).toHaveBeenCalledWith({ display_name: "新教师", phone: "13800000000", employee_no: undefined, role_codes: ["grader", "arbitrator"], school_id: "school-2", class_ids: [] });
  });

  it.each([
    { selected: ["class-2"], expectedSchool: "school-2" },
    { selected: [], expectedSchool: "school-2" },
  ])("选中班级 $selected 提交明确选择的学校及同校班级", async ({ selected, expectedSchool }) => {
    await render();
    await click("新增教师");
    await fill();
    await select("class_ids", selected);
    await submit();
    expect(mocks.create).toHaveBeenCalledWith({ display_name: "新教师", phone: "13800000000", employee_no: undefined, role_codes: ["teacher"], school_id: expectedSchool, class_ids: selected });
    expect(mocks.message.success).toHaveBeenCalledWith("教师邀请已创建");
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(mocks.users).toHaveBeenCalledTimes(2);
    expect(mocks.modal.success).not.toHaveBeenCalled();
  });

  it("管理员创建先重置旧字段并设置默认角色，提交仍强制管理员角色", async () => {
    await render();
    await click("新增教师");
    await fill();
    await select("class_ids", ["class-1"]);
    await click("取消");
    await administrators();
    await click("新增管理员");
    expect(field("display_name").value).toBe("");
    expect(field("role_code").value).toBe("school_admin");
    expect(host.querySelector('[data-field="class_ids"]')).toBeNull();
    await fill();
    await act(async () => { mocks.forms[0].setFieldValue("role_code", "grader"); });
    await submit();
    expect(mocks.create).toHaveBeenCalledWith({ display_name: "新教师", phone: "13800000000", employee_no: undefined, role_codes: ["school_admin"], school_id: "school-2", class_ids: [] });
    expect(mocks.message.success).toHaveBeenCalledWith("管理员邀请已创建");
  });

  it("创建失败保留表单草稿，重试成功先显示邀请再刷新", async () => {
    mocks.create.mockRejectedValueOnce(new Error("创建失败")).mockResolvedValueOnce({ user: member("created"), activation });
    await render();
    await click("新增教师");
    await fill();
    await submit();
    expect(mocks.message.error).toHaveBeenCalledWith("创建失败");
    expect(field("display_name").value).toBe("新教师");
    expect(button("创建并生成邀请").disabled).toBe(false);
    expect(mocks.users).toHaveBeenCalledTimes(1);
    await submit();
    expect(mocks.modal.success).toHaveBeenCalledWith(expect.objectContaining({ title: "激活邀请已生成" }));
    expect(mocks.message.success.mock.invocationCallOrder[0]).toBeLessThan(mocks.modal.success.mock.invocationCallOrder[0]);
    expect(mocks.modal.success.mock.invocationCallOrder[0]).toBeLessThan(mocks.users.mock.invocationCallOrder[1]);
  });

  it("既有人员职责编辑发送旧授权，失败保留表单，再次认证后提交同校班级", async () => {
    mocks.users.mockResolvedValue({ users: [member("teacher-a", ["teacher"], { school_id: "school-1", class_ids: ["class-1"] })] });
    mocks.access.mockRejectedValueOnce(new Error("授权已由其他管理员变更"));
    await render();
    await click("职责与班级", row("teacher-a"));
    await select("role_codes", ["teacher", "grader"]);
    await input(field("reason") as HTMLInputElement, "兼任阅卷");
    await submit();
    expect(mocks.stepUp).toHaveBeenCalledWith(expect.objectContaining({ reason: "修改“teacher-a”的工作职责与班级", action: expect.any(Function) }));
    expect(mocks.access).toHaveBeenCalledWith("teacher-a", { role_codes: ["teacher", "grader"], class_ids: ["class-1"], expected_roles: ["teacher"], expected_class_ids: ["class-1"], reason: "兼任阅卷" });
    expect(field("reason").value).toBe("兼任阅卷");
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    expect(mocks.message.error).toHaveBeenCalledWith("授权已由其他管理员变更");
    mocks.access.mockResolvedValueOnce({ user: member("teacher-a", ["teacher", "grader"], { school_id: "school-1", class_ids: ["class-1"] }) });
    await submit();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(mocks.message.success).toHaveBeenCalledWith("职责与班级已保存，请通知该人员重新登录");
  });

  it("切换学校后不能提交旧学校的隐藏班级，不会自行改推所属学校", async () => {
    await render();
    await click("新增教师");
    await fill();
    await act(async () => { mocks.forms[0].setFieldValue("class_ids", ["class-1"]); });
    await submit();
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.message.error).toHaveBeenCalledWith("关联班级必须属于所选学校，请重新选择班级");
    expect(field("school_id").value).toBe("school-2");
  });

  it("刷新失败保留已有名单，首次加载失败只显示可重试错误", async () => {
    mocks.users.mockRejectedValueOnce(new Error("首次失败"));
    await render();
    expect(host.textContent).toContain("首次失败");
    expect(host.querySelector("table")).toBeNull();
    await click("重试");
    expect(row("teacher-a")).toBeDefined();
    mocks.users.mockRejectedValueOnce(new Error("刷新失败"));
    await click("刷新");
    expect(row("teacher-a")).toBeDefined();
    expect(host.textContent).toContain("刷新失败");
  });

  it("邀请重发等待请求完成，复制提示等待剪贴板完成", async () => {
    const pending = deferred<{ activation: typeof activation }>();
    mocks.users.mockResolvedValue({ users: [member("teacher-a", ["teacher"], { status: "invited" })] });
    mocks.reissue.mockReturnValue(pending.promise);
    await render();
    await click("重新生成邀请", row("teacher-a"));
    expect(mocks.reissue).toHaveBeenCalledWith("teacher-a");
    expect(button("重新生成邀请", row("teacher-a")).disabled).toBe(true);
    expect(mocks.modal.success).not.toHaveBeenCalled();
    await act(async () => { pending.resolve({ activation }); });
    expect(button("重新生成邀请", row("teacher-a")).disabled).toBe(false);
    const dialog = document.createElement("div");
    host.append(dialog);
    const modalRoot = createRoot(dialog);
    await act(async () => { modalRoot.render(mocks.modal.success.mock.calls[0][0].content); });
    const copied = deferred<void>();
    mocks.clipboard.mockReturnValue(copied.promise);
    await click("复制", dialog);
    expect(mocks.clipboard).toHaveBeenCalledWith(`${window.location.origin}${activation.path}`);
    expect(mocks.message.success).not.toHaveBeenCalled();
    await act(async () => { copied.resolve(); });
    expect(mocks.message.success).toHaveBeenCalledWith("激活链接已复制");
    await act(async () => { modalRoot.unmount(); });
  });

  it.each(["cancelled", "failed"])("恢复链接 $0 后清理 loading，取消不报普通错误", async (kind) => {
    const error = new Error("恢复失败");
    if (kind === "cancelled") error.name = "StepUpCancelledError";
    mocks.stepUp.mockRejectedValue(error);
    await render();
    await click("协助恢复", row("teacher-a"));
    expect(mocks.stepUp).toHaveBeenCalledWith(expect.objectContaining({ reason: "为“teacher-a”生成账号恢复链接", action: expect.any(Function) }));
    expect(mocks.recovery).not.toHaveBeenCalled();
    expect(button("协助恢复", row("teacher-a")).disabled).toBe(false);
    expect(mocks.modal.success).not.toHaveBeenCalled();
    if (kind === "cancelled") expect(mocks.message.error).not.toHaveBeenCalled();
    else expect(mocks.message.error).toHaveBeenCalledWith("恢复失败");
  });

  it("恢复链接经身份验证调用正确账号并显示返回邀请", async () => {
    await render();
    await click("协助恢复", row("teacher-a"));
    expect(mocks.recovery).toHaveBeenCalledWith("teacher-a");
    expect(mocks.modal.success).toHaveBeenCalledWith(expect.objectContaining({ title: "已为teacher-a生成恢复链接" }));
    expect(button("协助恢复", row("teacher-a")).disabled).toBe(false);
  });

  it("停用确认前无请求，确认成功按返回账号更新名单", async () => {
    await render();
    await click("停用", row("teacher-a"));
    expect(mocks.status).not.toHaveBeenCalled();
    const confirm = mocks.modal.confirm.mock.calls[0][0];
    expect(confirm).toMatchObject({ title: "停用teacher-a？", okText: "确认停用", okButtonProps: { danger: true } });
    await act(async () => { await confirm.onOk(); });
    expect(mocks.status).toHaveBeenCalledWith("teacher-a", "disabled");
    expect(row("teacher-a").querySelector('[data-tone="neutral"]')?.textContent).toBe("已停用");
    expect(button("恢复", row("teacher-a")).disabled).toBe(false);
  });

  it("恢复失败继续拒绝确认回调，保留状态并清理 loading", async () => {
    const failure = new Error("状态失败");
    mocks.users.mockResolvedValue({ users: [member("teacher-a", ["teacher"], { status: "disabled" })] });
    mocks.status.mockRejectedValue(failure);
    await render();
    await click("恢复", row("teacher-a"));
    const confirm = mocks.modal.confirm.mock.calls[0][0];
    expect(confirm).toMatchObject({ title: "恢复teacher-a？", okText: "确认恢复", okButtonProps: { danger: false } });
    await act(async () => { await expect(confirm.onOk()).rejects.toBe(failure); });
    expect(mocks.status).toHaveBeenCalledWith("teacher-a", "active");
    expect(mocks.message.error).toHaveBeenCalledWith("状态失败");
    expect(row("teacher-a").querySelector('[data-tone="neutral"]')?.textContent).toBe("已停用");
    expect(button("恢复", row("teacher-a")).disabled).toBe(false);
    expect(mocks.message.success).not.toHaveBeenCalled();
  });
});
