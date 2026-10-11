// @vitest-environment jsdom
import { act, type HTMLAttributes, type MouseEventHandler, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuditLog } from "../api/audit";
import { AuditLogPage } from "./AuditLogPage";

const mocks = vi.hoisted(() => ({
  listLogs: vi.fn(), exportLogs: vi.fn(), listExams: vi.fn(), listUsers: vi.fn(),
  confirm: vi.fn(), success: vi.fn(), error: vi.fn()
}));
vi.mock("../api/audit", () => ({ listAuditLogs: mocks.listLogs, exportAuditLogs: mocks.exportLogs }));
vi.mock("../api/exams", () => ({ listExams: mocks.listExams }));
vi.mock("../api/users", () => ({ listManagedUsers: mocks.listUsers }));
vi.mock("../components/ResponsiveTable", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    ResponsiveTable: ({ dataSource, onRow }: { dataSource: AuditLog[]; onRow: (record: AuditLog) => HTMLAttributes<HTMLDivElement> }) =>
      React.createElement("div", { "data-table": "audit" }, dataSource.map((record) =>
        React.createElement("div", { ...onRow(record), "data-row": record.id, key: record.id }, record.id)
      ))
  };
});
vi.mock("../components/PageState", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", { "data-state": "loading" }, label),
    ErrorState: ({ message }: { message: string }) => React.createElement("div", { "data-state": "error" }, message),
    EmptyState: ({ title, description }: { title: string; description: string }) => React.createElement("div", { "data-state": "empty" }, title, description)
  };
});
vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  const wrapper = ({ children }: { children: ReactNode }) => React.createElement("div", null, children);
  const app = { message: { success: mocks.success, error: mocks.error }, modal: { confirm: mocks.confirm } };
  return {
    App: { useApp: () => app },
    Alert: ({ type, message, description }: { type: string; message: ReactNode; description: ReactNode }) => React.createElement("div", { "data-alert": type }, message, description),
    Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: MouseEventHandler<HTMLButtonElement>; disabled?: boolean }) => React.createElement("button", { onClick, disabled }, children),
    DatePicker: { RangePicker: () => React.createElement("div") },
    Descriptions: Object.assign(wrapper, { Item: ({ label, children }: { label: string; children: ReactNode }) => React.createElement("div", { "data-description": label }, children) }),
    // 保留关闭时的 children，只核对页面传入的详情分支，不覆盖 Ant Design 的挂载策略。
    Drawer: ({ children, open, onClose, title, width }: { children: ReactNode; open: boolean; onClose: () => void; title: string; width: number }) =>
      React.createElement("section", { "data-drawer-open": String(open), "data-drawer-width": width }, title, React.createElement("button", { onClick: onClose }, "关闭详情"), children),
    Input: ({ value, placeholder, className }: { value: string; placeholder: string; className?: string }) => React.createElement("input", { value, placeholder, className, readOnly: true }),
    Select: ({ placeholder }: { placeholder: string }) => React.createElement("div", { "data-select": placeholder }),
    Space: wrapper,
    Tooltip: wrapper,
    Tag: ({ children }: { children: ReactNode }) => React.createElement("span", null, children)
  };
});

function log(overrides: Partial<AuditLog> = {}): AuditLog {
  return { id: "audit-1", tenant_id: "tenant-1", action: "auth.login_succeeded", target_type: "", created_at: "invalid-time", ...overrides };
}

describe("操作记录页展示拆分回归", () => {
  let root: Root;
  let host: HTMLDivElement;
  let frame: FrameRequestCallback | null;

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    frame = null;
    vi.stubGlobal("requestAnimationFrame", vi.fn((callback: FrameRequestCallback) => { frame = callback; return 1; }));
    vi.stubGlobal("URL", { createObjectURL: vi.fn(() => "blob:audit"), revokeObjectURL: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    mocks.listLogs.mockResolvedValue({ audit_logs: [log()], next_cursor: "", has_more: false });
    mocks.listExams.mockResolvedValue({ exams: [] });
    mocks.listUsers.mockResolvedValue({ users: [] });
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  async function render() {
    await act(async () => root.render(<AuditLogPage canRead canExport tenantName="测试机构" />));
  }

  async function click(label: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label);
    expect(button).toBeDefined();
    await act(async () => button!.click());
  }

  function drawer() {
    return host.querySelector<HTMLElement>("[data-drawer-open]")!;
  }

  function field(label: string) {
    return drawer().querySelector<HTMLElement>(`[data-description="${label}"]`)!;
  }

  it.each([
    ["", "每次导出都会自动记录在操作日志中"],
    ["ABCDEFGHIJKL", "最近一次导出已记录（水印编号 ABCDEFGHIJKL）"],
    ["ABCDEFGHIJKLM", "最近一次导出已记录（水印编号 ABCDEFGHIJKL…）"]
  ])("保持水印 %s 的展示边界", async (watermark, expected) => {
    await render();
    expect(host.querySelector(".audit-export-panel span")?.textContent).toBe("每次导出都会自动记录在操作日志中");
    mocks.exportLogs.mockResolvedValue({ blob: new Blob(["csv"]), filename: "audit.csv", watermark });
    await click("导出 CSV");
    const confirm = mocks.confirm.mock.calls[0][0] as { onOk: () => Promise<void> };
    await act(async () => confirm.onOk());
    expect(host.querySelector(".audit-export-panel span")?.textContent).toBe(expected);
  });

  it("保持未选择记录和系统记录的字段兜底", async () => {
    await render();
    expect(drawer().dataset.drawerOpen).toBe("false");
    expect(drawer().textContent).toContain("未选择记录请选择一条操作记录查看详情。");
    await act(async () => host.querySelector<HTMLElement>('[data-row="audit-1"]')!.click());
    expect(drawer().dataset.drawerOpen).toBe("true");
    expect(drawer().dataset.drawerWidth).toBe("640");
    expect(field("操作人").textContent).toBe("系统自动处理");
    expect(field("操作人").querySelector("span")).toBeNull();
    expect(field("操作内容").textContent).toBe("登录成功");
    expect(field("操作内容").querySelector("span")?.title).toBe("auth.login_succeeded");
    expect(field("操作对象").textContent).toBe("系统");
    expect(field("操作对象").querySelector("span")?.title).toBe("");
    expect(["说明", "IP 地址", "浏览器", "请求编号"].map((label) => field(label).textContent)).toEqual(["-", "-", "-", "-"]);
    expect(field("时间").textContent).toBe("invalid-time");
    expect([...drawer().querySelectorAll("pre")].map((node) => node.textContent)).toEqual(["未记录", "未记录"]);
    expect(drawer().textContent).toContain("只读记录操作记录不可修改、不可删除；密码等敏感内容显示时已自动隐藏。");
  });

  it("保持操作人和对象编号及敏感字段的展示遮盖", async () => {
    const record = log({
      actor_id: "actor-123456789", target_type: "exam", target_id: "exam-123456789",
      reason: "password=reason-secret token=reason-token safe=ok", user_agent: "Bearer browser-secret",
      ip_address: "127.0.0.1", request_id: "request-1",
      before_value: { password: "before-secret", profile: { api_token: "before-token" }, entries: [{ credential: "before-credential" }, "Bearer before-bearer"], note: "password=before-query&safe=ok" },
      after_value: { authorization: "Bearer after-bearer", refresh_token: "after-token", normal: "kept" }
    });
    mocks.listLogs.mockResolvedValue({ audit_logs: [record], next_cursor: "", has_more: false });
    await render();
    await act(async () => host.querySelector<HTMLElement>('[data-row="audit-1"]')!.click());
    expect(field("操作人").textContent).toBe("操作人员（ID actor-12）");
    expect(field("操作人").querySelector("span")?.title).toBe(record.actor_id);
    expect(field("操作对象").textContent).toBe("考试（编号 exam-123）");
    expect(field("操作对象").querySelector("span")?.title).toBe("exam:exam-123456789");
    expect(field("说明").textContent).toBe("password=*** token=*** safe=ok");
    expect(field("浏览器").textContent).toBe("Bearer ***");
    expect(field("IP 地址").textContent).toBe("127.0.0.1");
    expect(field("请求编号").textContent).toBe("request-1");
    const values = [...drawer().querySelectorAll("pre")].map((node) => JSON.parse(node.textContent!));
    expect(values).toEqual([
      { password: "***", profile: { api_token: "***" }, entries: [{ credential: "***" }, "Bearer ***"], note: "password=***&safe=ok" },
      { authorization: "***", refresh_token: "***", normal: "kept" }
    ]);
  });

  it.each(["Enter", " "])("按 %s 打开详情，关闭后的下一帧恢复行焦点", async (key) => {
    await render();
    const row = host.querySelector<HTMLElement>('[data-row="audit-1"]')!;
    row.focus();
    const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
    await act(async () => { row.dispatchEvent(event); });
    expect(event.defaultPrevented).toBe(true);
    expect(drawer().dataset.drawerOpen).toBe("true");
    const close = [...drawer().querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "关闭详情")!;
    close.focus();
    await click("关闭详情");
    expect(drawer().dataset.drawerOpen).toBe("false");
    expect(document.activeElement).toBe(close);
    expect(frame).not.toBeNull();
    await act(async () => { frame!(0); });
    expect(document.activeElement).toBe(row);
  });
});
