// @vitest-environment jsdom
import { act, useEffect, useState, type ReactNode, type ComponentProps } from "react";
import type { OfflineLeaveGuard } from "./components/OfflineWorkbench";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

const data = vi.hoisted(() => ({
  serviceStatus: null as null | { status: string; service: string; environment: string; generated_at: string; dependencies: unknown[] },
  logs: [] as { id: string; at: string; level: string; message: string }[],
  native: false, requestLeave: vi.fn(), shouldBlockClose: vi.fn(), closeRequested: undefined as undefined | ((event: { preventDefault: () => void }) => Promise<void>),
  session: {} as Record<string, unknown>, renew: vi.fn(), setOfflineDraftCount: vi.fn(), editScore: undefined as undefined | ((value: string) => void), editorLease: ""
}));

vi.mock("@edugrade/design-tokens", () => ({ readReadingSize: () => "standard", applyReadingSize: vi.fn() }));
vi.mock("antd", () => {
  const Wrapper = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  return {
    ConfigProvider: Wrapper, Form: Object.assign(Wrapper, { Item: Wrapper }), Space: Wrapper, Tooltip: Wrapper,
    Button: ({ children, onClick, disabled }: ComponentProps<"button">) => <button onClick={onClick} disabled={disabled}>{children}</button>,
    Tag: ({ children, color }: { children?: ReactNode; color?: string }) => <span data-color={color}>{children}</span>,
    Alert: ({ message }: { message?: ReactNode }) => <p>{message}</p>, Empty: ({ description }: { description?: ReactNode }) => <p>{description}</p>,
    Input: Object.assign(({ value, onChange }: ComponentProps<"input">) => <input value={value} onChange={onChange} />, {
      Password: ({ value, onChange }: ComponentProps<"input">) => <input value={value} onChange={onChange} />
    }),
    Checkbox: ({ children }: { children?: ReactNode }) => <span>{children}</span>, Table: Wrapper
  };
});
vi.mock("framer-motion", () => ({ motion: { div: ({ children }: { children: ReactNode }) => <div>{children}</div> } }));
vi.mock("./components/OfflineWorkbench", () => ({ OfflineWorkbench: ({ registerLeaveGuard, durableScopeKey }: { registerLeaveGuard: (guard: OfflineLeaveGuard | null) => void; durableScopeKey: string }) => {
  const [score, setScore] = useState("初始评分"); data.editScore = setScore; data.editorLease = durableScopeKey;
  useEffect(() => { registerLeaveGuard({ requestLeave: data.requestLeave, shouldBlockClose: data.shouldBlockClose }); return () => registerLeaveGuard(null); }, [registerLeaveGuard]);
  return <div>离线工作台内容<input aria-label="工作台未保存输入" value={score} readOnly /></div>;
} }));
vi.mock("@tauri-apps/api/window", () => ({ getCurrentWindow: () => ({ onCloseRequested: async (handler: typeof data.closeRequested) => { data.closeRequested = handler; return vi.fn(); } }) }));
vi.mock("./components/DesktopStatusViews", () => ({
  SectionHead: ({ title, action }: { title: string; action?: ReactNode }) => <div><h3>{title}</h3>{action}</div>,
  StatusLine: ({ label, value, tone }: { label: string; value: string; tone: "ready" | "warning" | "idle" }) => <div data-status-line={label} data-tone={tone}>{value}</div>,
  CapabilityTag: () => null, QueueList: () => null, ScanQueueRecovery: () => null
}));
vi.mock("./lib/durableStore", () => ({ claimLegacyDurableStore: vi.fn(), hasDurableDesktopStore: () => data.native }));
vi.mock("./lib/offlineStore", () => ({ listOfflineDraftEnvelopes: vi.fn(async () => []) }));
vi.mock("./lib/scanFiles", () => ({
  dependencyLabel: (value: string) => value, dependencyTone: () => "warning",
  estimateLocalCacheBytes: () => 0, formatBytes: () => "0 B", formatDate: (value: string) => value
}));
vi.mock("./features/session/useDesktopSession", () => ({ useDesktopSession: () => ({
  client: {}, serverUrl: "http://localhost:8080", setServerUrl: vi.fn(), tenantCode: "demo", setTenantCode: vi.fn(),
  username: "teacher", setUsername: vi.fn(), password: "", setPassword: vi.fn(), rememberLogin: false,
  setRememberLogin: vi.fn(), credentialStoreMessage: null, credentialStoreReady: false, token: null,
  expiresAt: null, user: null, authError: null, isLoggingIn: false, isLoggingOut: false, durableScopeKey: "",
  handleLogin: vi.fn(), handleLogout: vi.fn(), handleForgetStoredLogin: vi.fn(), handleCheckSession: vi.fn(), handleRenewOfflineAccess: data.renew, ...data.session
}) }));
vi.mock("./features/runtime/useLocalLogs", () => ({ useLocalLogs: () => ({ logs: data.logs, logEvent: vi.fn(), clearLogs: vi.fn() }) }));
vi.mock("./features/runtime/useRuntimeDiagnostics", () => ({ useRuntimeDiagnostics: () => ({
  capabilities: [], diagnostics: null, localCacheSecurity: { status: "passed", issues: [], scannedKeys: 0, checkedAt: "2026-10-07" },
  serviceStatus: data.serviceStatus, isCheckingServiceStatus: false, diagnosticError: null, setDiagnosticError: vi.fn(),
  refreshCapabilities: vi.fn(), saveServerForSession: vi.fn(), handleHealthCheck: vi.fn(), handleSystemStatusCheck: vi.fn()
}) }));
vi.mock("./features/tasks/useReviewTasks", () => ({ useReviewTasks: () => ({ tasks: [], taskError: null, isLoadingTasks: false, handleLoadTasks: vi.fn() }) }));
vi.mock("./features/capture/useExamCaptureContext", () => ({ useExamCaptureContext: () => ({ exams: [], selectedExamId: "", scanSubmissionId: "", captureBatchId: "", scanStartPage: 1 }) }));
// App 会同步扫描模板并渲染队列恢复入口；这里只补接口 stub，原有页面断言仍从真实 App 执行。
vi.mock("./features/scanner/useScannerController", () => ({ useScannerController: () => ({
  scannerPreflight: null, setExpectedTemplatePreset: vi.fn(), setScannerPreflight: vi.fn()
}) }));
vi.mock("./features/scan-queue/useScanQueue", () => ({ useScanQueue: () => ({
  // 生产 hook 返回稳定的 React setter；不能每次 mock 渲染换函数而制造同步队列读取循环。
  queue: [], offlineDraftCount: 0, setOfflineDraftCount: data.setOfflineDraftCount, queueRestoreStatus: "ready", queueRestoreError: null,
  restoreQueue: vi.fn(), queueContextError: null, loadingQueueContext: false, refreshQueueContext: vi.fn()
}) }));
vi.mock("./features/upload/useUploadSync", () => ({ useUploadSync: () => ({}) }));
vi.mock("./features/quality/useSubmissionQuality", () => ({ useSubmissionQuality: () => ({}) }));
vi.mock("./features/scan/ScanWorkspace", () => ({ ScanWorkspace: () => null }));

describe("desktop status and log views", () => {
  let root: Root;
  let container: HTMLDivElement;
  beforeEach(() => {
    vi.clearAllMocks();
    data.serviceStatus = null;
    data.logs = [];
    data.native = false; data.closeRequested = undefined;
    data.session = {}; data.editScore = undefined; data.editorLease = "";
    data.requestLeave.mockResolvedValue(true); data.shouldBlockClose.mockReturnValue(false);
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });
  async function showView(label: string) {
    await act(async () => root.render(<App />));
    const button = [...container.querySelectorAll(".desktop-nav button")].find((item) => item.textContent === label);
    if (!button) throw new Error(`missing ${label}`);
    await act(async () => (button as HTMLButtonElement).click());
  }

  it.each([[null, "idle"], ["healthy", "ready"], ["degraded", "warning"]] as const)("keeps both service tones for status=%s", async (status, tone) => {
    if (status) data.serviceStatus = { status, service: "api", environment: "test", generated_at: "2026-10-07", dependencies: [] };
    await showView("系统诊断");
    expect(container.querySelector('[data-status-line="Server"]')?.getAttribute("data-tone")).toBe(tone);
    expect(container.querySelector('[data-status-line="Status"]')?.getAttribute("data-tone")).toBe(tone);
    expect(container.querySelector('[data-status-line="Status"]')?.textContent).toBe(status ?? "未检查");
  });

  it("keeps error, warning and info colors in the log view", async () => {
    data.logs = ["error", "warning", "info"].map((level) => ({ id: level, at: "2026-10-07", level, message: `${level} message` }));
    await showView("本地日志");
    const tags = [...container.querySelectorAll(".log-row [data-color]")];
    expect(tags.map((tag) => tag.textContent)).toEqual(["error", "warning", "info"]);
    expect(tags.map((tag) => tag.getAttribute("data-color"))).toEqual(["error", "warning", "blue"]);
    expect(container.textContent).toContain("info message");
  });

  it("uses the workbench save decision before every sidebar destination", async () => {
    await showView("离线阅卷"); data.requestLeave.mockResolvedValue(false);
    for (const label of ["连接登录", "任务列表", "扫描上传", "同步队列", "系统诊断", "本地日志"]) {
      const target = [...container.querySelectorAll<HTMLButtonElement>(".desktop-nav button")].find((item) => item.textContent === label)!;
      await act(async () => target.click());
      expect(container.querySelector(".desktop-topbar h2")?.textContent).toBe("离线阅卷");
      expect(container.textContent).toContain("离线工作台内容");
    }
    expect(data.requestLeave).toHaveBeenCalledTimes(6);
    data.requestLeave.mockResolvedValue(true);
    await showView("同步队列");
    expect(container.textContent).not.toContain("离线工作台内容");
  });

  it("protects the native title-bar close with the same asynchronous save decision", async () => {
    data.native = true; await showView("离线阅卷");
    await vi.waitFor(() => expect(data.closeRequested).toBeDefined());
    const event = { preventDefault: vi.fn() };
    data.requestLeave.mockResolvedValue(false);
    await act(async () => data.closeRequested!(event));
    expect(event.preventDefault).toHaveBeenCalledOnce();
    event.preventDefault.mockClear(); data.requestLeave.mockResolvedValue(true);
    await act(async () => data.closeRequested!(event));
    expect(event.preventDefault).not.toHaveBeenCalled();
  });

  it("读取当前离线 guard 取消卸载，保存后及离开工作台后恢复", async () => {
    await showView("离线阅卷");
    const dispatchUnload = () => {
      const event = new Event("beforeunload", { cancelable: true });
      window.dispatchEvent(event);
      return event;
    };
    expect(dispatchUnload().defaultPrevented).toBe(false);
    data.shouldBlockClose.mockReturnValue(true);
    expect(dispatchUnload().defaultPrevented).toBe(true);
    expect(window.onbeforeunload?.call(window, new Event("beforeunload", { cancelable: true }) as BeforeUnloadEvent)).toBe("unsaved");
    data.shouldBlockClose.mockReturnValue(false);
    expect(dispatchUnload().defaultPrevented).toBe(false);
    data.shouldBlockClose.mockReturnValue(true);
    await showView("任务列表");
    expect(dispatchUnload().defaultPrevented).toBe(false);
  });

  it("App 卸载后移除浏览器保护并恢复原属性处理器", async () => {
    const previous = window.onbeforeunload;
    await showView("离线阅卷");
    data.shouldBlockClose.mockReturnValue(true);
    await act(async () => root.render(null));
    expect(window.onbeforeunload).toBe(previous);
    const event = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
  });

  it("renews in the offline workspace without a leave decision or remounting the dirty editor", async () => {
    data.native = true;
    data.session = { durableScopeKey: "old-lease", editorAccountKey: "same-school-teacher", password: "password", offlineUnlocked: true,
      user: { id: "teacher", tenant_id: "school", roles: [] }, offlineAccess: { server: "http://localhost:8080", actorId: "teacher", tenantId: "school", username: "teacher", tenantCode: "school", expiresAt: "2020-01-01T00:00:00Z" } };
    await showView("离线阅卷"); await act(async () => data.editScore!("未保存分数7与评语"));
    data.renew.mockResolvedValue(true);
    const renew = [...container.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "联网续期并保留当前评分")!;
    expect(renew.disabled).toBe(false); await act(async () => renew.click());
    expect(data.renew).toHaveBeenCalledWith("password"); expect(data.requestLeave).not.toHaveBeenCalled();
    data.session = { ...data.session, durableScopeKey: "new-lease", token: "new-token", offlineUnlocked: false };
    await act(async () => root.render(<App />));
    expect(container.querySelector<HTMLInputElement>('[aria-label="工作台未保存输入"]')?.value).toBe("未保存分数7与评语");
    expect(data.editorLease).toBe("new-lease"); expect(container.querySelector(".desktop-topbar h2")?.textContent).toBe("离线阅卷");
  });
});
