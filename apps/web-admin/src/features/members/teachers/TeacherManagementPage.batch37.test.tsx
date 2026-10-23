// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ManagedUser } from "../../../api/users";
import type { SessionUser } from "../../../auth/session";
import { TeacherManagementPage } from "./TeacherManagementPage";

const mocks = vi.hoisted(() => ({
  users: vi.fn(), access: vi.fn(), create: vi.fn(), stepUp: vi.fn(),
  success: vi.fn(), error: vi.fn(), modal: { success: vi.fn(), confirm: vi.fn() }
}));
vi.mock("../../../api/users", () => ({
  listAllManagedUsers: mocks.users,
  listAssignableRoles: async () => ({ roles: ["teacher", "grader", "school_admin"].map((code) => ({ code, name: code, scope_type: "school" })) }),
  updateManagedUserAccess: mocks.access, createManagedUser: mocks.create,
  createCredentialRecovery: vi.fn(), reissueManagedUserActivation: vi.fn(), updateManagedUserStatus: vi.fn()
}));
vi.mock("../../../api/org", () => ({
  listSchools: async () => ({ schools: [{ id: "school-a", name: "学校甲" }] }),
  listGrades: async () => ({ grades: [] }), listClasses: async () => ({ classes: [] })
}));
vi.mock("../../../auth/stepUpContext", () => ({
  useStepUp: () => ({ runWithStepUp: mocks.stepUp }), isStepUpCancelledError: () => false
}));
vi.mock("../../../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <p>{label}</p>,
  ErrorState: ({ message }: { message: string }) => <p>{message}</p>
}));
vi.mock("../../../components/StatusTag", () => ({
  StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span>
}));
vi.mock("../../../components/ResponsiveTable", () => ({
  ResponsiveTable: ({ dataSource, columns }: {
    dataSource: ManagedUser[];
    columns: Array<{ key?: string; render?: (value: undefined, user: ManagedUser) => ReactNode }>;
  }) => <div>{dataSource.map((user) => <div key={user.id} data-user-id={user.id}>{columns.find((column) => column.key === "actions")?.render?.(undefined, user)}</div>)}</div>
}));
vi.mock("antd", async () => {
  const React = await import("react");
  type Store = { values: Record<string, unknown>; resetFields: () => void; setFieldValue: (name: string, value: unknown) => void };
  function makeStore(): Store {
    const store: Store = {
      values: {}, resetFields() { store.values = {}; },
      setFieldValue(name, value) { store.values[name] = value; }
    };
    return store;
  }
  const Form = Object.assign(({ form, onFinish, children }: {
    form: Store; onFinish: (values: Record<string, unknown>) => void; children: ReactNode;
  }) => <form onSubmit={(event) => { event.preventDefault(); onFinish({ ...form.values, reason: "核对职责" }); }}>{children}</form>, {
    useForm: () => [React.useState(makeStore)[0]],
    useWatch: (name: string, form: Store) => form.values[name],
    Item: ({ label, children, hidden }: { label?: string; children: ReactNode; hidden?: boolean }) => <div hidden={hidden}>{label}{children}</div>
  });
  const Input = Object.assign(({ value, onChange, placeholder }: { value?: string; onChange?: React.ChangeEventHandler<HTMLInputElement>; placeholder?: string }) =>
    <input value={value ?? ""} onChange={onChange} readOnly={!onChange} placeholder={placeholder} />, {
    Search: ({ value, onChange }: { value: string; onChange: React.ChangeEventHandler<HTMLInputElement> }) => <input value={value} onChange={onChange} />
  });
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error }, modal: mocks.modal }) },
    Form, Input,
    Button: ({ children, onClick, htmlType, disabled, loading }: { children?: ReactNode; onClick?: () => void; htmlType?: "button" | "submit"; disabled?: boolean; loading?: boolean }) =>
      <button type={htmlType ?? "button"} onClick={onClick} disabled={disabled || loading}>{children}</button>,
    Dropdown: ({ children, disabled, menu }: { children: ReactNode; disabled?: boolean; menu: { items: Array<{ key: string; label: ReactNode; onClick?: () => void }> } }) =>
      <div>{children}{menu.items.map((item) => <button key={item.key} type="button" disabled={disabled} onClick={item.onClick}>{item.label}</button>)}</div>,
    Modal: ({ open, title, onCancel, children }: { open: boolean; title: string; onCancel: () => void; children: ReactNode }) =>
      open ? <div role="dialog"><h2>{title}</h2><button type="button" onClick={onCancel}>取消</button>{children}</div> : null,
    Tabs: ({ items, onChange }: { items: Array<{ key: string; label: string }>; onChange: (key: string) => void }) =>
      <div>{items.map((item) => <button key={item.key} type="button" data-tab={item.key} onClick={() => onChange(item.key)}>{item.label}</button>)}</div>,
    Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
    Alert: ({ message, description }: { message: string; description?: string }) => <aside>{message}{description}</aside>,
    Select: () => <select aria-label="模拟字段" />
  };
});

const currentUser: SessionUser = {
  id: "self", username: "manager", displayName: "管理员", name: "管理员", role: "tenant_admin", roles: ["tenant_admin"],
  tenant: "tenant-a", school: "学校甲", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: true, schoolIds: [], gradeIds: [], classIds: [] }
};
const teacher: ManagedUser = { id: "teacher-a", username: "teacher-name", display_name: "教师甲", roles: ["teacher"], class_ids: [], school_id: "school-a", status: "active" };

describe("人员管理弹窗文案优先级", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.users.mockResolvedValue([teacher]);
    mocks.access.mockResolvedValue({ user: teacher });
    mocks.stepUp.mockImplementation(({ action }: { action: () => Promise<unknown> }) => action());
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
  });
  async function render() {
    await act(async () => { root.render(<TeacherManagementPage currentUser={currentUser} />); });
  }
  async function click(text: string, scope: ParentNode = host) {
    const button = [...scope.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text);
    expect(button).toBeDefined();
    await act(async () => { button!.click(); });
  }
  async function changeTab(key: string) {
    await act(async () => { host.querySelector<HTMLButtonElement>(`[data-tab="${key}"]`)!.click(); });
  }

  it("新增教师显示教师标题和邀请按钮", async () => {
    await render();
    await click("新增教师");
    const dialog = host.querySelector('[role="dialog"]')!;
    expect(dialog.querySelector("h2")?.textContent).toBe("新增教师");
    expect(dialog.querySelector('button[type="submit"]')?.textContent).toBe("创建并生成邀请");
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it("新增管理员显示管理员标题和邀请按钮", async () => {
    await render();
    await changeTab("administrators");
    await click("新增管理员");
    const dialog = host.querySelector('[role="dialog"]')!;
    expect(dialog.querySelector("h2")?.textContent).toBe("新增学校管理员");
    expect(dialog.querySelector('button[type="submit"]')?.textContent).toBe("创建管理员邀请");
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it.each(["教师甲", ""])("新增管理员取消后编辑人员，名称“%s”仍按编辑入口提交", async (displayName) => {
    // 先打开管理员新增入口，保留 createMode，再从名单进入编辑，保护编辑优先级。
    const target = { ...teacher, display_name: displayName };
    mocks.users.mockResolvedValue([target]);
    mocks.access.mockResolvedValue({ user: target });
    await render();
    await changeTab("administrators");
    await click("新增管理员");
    await click("取消", host.querySelector('[role="dialog"]')!);
    await changeTab("teachers");
    await click("职责与班级", host.querySelector('[data-user-id="teacher-a"]')!);
    const dialog = host.querySelector('[role="dialog"]')!;
    expect(dialog.querySelector("h2")?.textContent).toBe(`职责与班级 · ${displayName}`);
    expect(dialog.querySelector('button[type="submit"]')?.textContent).toBe("验证身份并保存");
    await act(async () => { dialog.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(mocks.stepUp).toHaveBeenCalledWith(expect.objectContaining({ reason: `修改“${displayName}”的工作职责与班级` }));
    expect(mocks.access).toHaveBeenCalledWith("teacher-a", { role_codes: ["teacher"], class_ids: [], expected_roles: ["teacher"], expected_class_ids: [], reason: "核对职责" });
    expect(mocks.create).not.toHaveBeenCalled();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
});
