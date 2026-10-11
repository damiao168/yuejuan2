// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

const data = vi.hoisted(() => ({ offlineAccess: null as null | {
  actorId: string; tenantId: string; server: string; expiresAt: string; username: string; tenantCode: string;
} }));

vi.mock("@edugrade/design-tokens", () => ({ readReadingSize: () => "standard", applyReadingSize: vi.fn() }));
vi.mock("antd", () => {
  const Wrapper = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  const Input = ({ value }: { value: string }) => <input value={value} readOnly />;
  return {
    ConfigProvider: Wrapper, Form: Object.assign(Wrapper, { Item: Wrapper }), Space: Wrapper, Tooltip: Wrapper,
    Button: Wrapper, Tag: Wrapper, Checkbox: Wrapper, Table: Wrapper,
    Input: Object.assign(Input, { Password: Input }),
    Alert: ({ message, description, type }: { message: ReactNode; description?: ReactNode; type: string }) =>
      <div data-alert-type={type}><p>{message}</p><p data-alert-description>{description}</p></div>,
    Empty: ({ description }: { description: ReactNode }) => <p>{description}</p>
  };
});
vi.mock("framer-motion", () => ({ motion: { div: ({ children }: { children: ReactNode }) => <div>{children}</div> } }));
vi.mock("./components/OfflineWorkbench", () => ({ OfflineWorkbench: () => <div>离线工作台</div> }));
vi.mock("./components/NativeStoragePanel", () => ({ NativeStoragePanel: () => null }));
vi.mock("@tauri-apps/api/window", () => ({ getCurrentWindow: () => ({ onCloseRequested: async () => vi.fn() }) }));
vi.mock("./components/DesktopStatusViews", () => ({
  SectionHead: ({ title }: { title: string }) => <h3>{title}</h3>, StatusLine: () => null,
  CapabilityTag: () => null, QueueList: () => null, ScanQueueRecovery: () => null
}));
vi.mock("./lib/durableStore", () => ({ claimLegacyDurableStore: vi.fn(), hasDurableDesktopStore: () => true }));
vi.mock("./lib/offlineStore", () => ({ listOfflineDraftEnvelopes: vi.fn(async () => []) }));
vi.mock("./features/session/useDesktopSession", () => ({ useDesktopSession: () => ({
  client: {}, serverUrl: "http://localhost:8080", tenantCode: "school", username: "teacher", password: "",
  user: { id: "teacher", tenant_id: "school", roles: [] }, token: null, durableScopeKey: "lease", editorAccountKey: "teacher",
  offlineAccess: data.offlineAccess, offlineUnlocked: true, credentialStoreReady: false,
  isLoggingIn: false, isLoggingOut: false
}) }));
vi.mock("./features/runtime/useLocalLogs", () => ({ useLocalLogs: () => ({ logs: [], logEvent: vi.fn(), clearLogs: vi.fn() }) }));
vi.mock("./features/runtime/useRuntimeDiagnostics", () => ({ useRuntimeDiagnostics: () => ({
  capabilities: [], refreshCapabilities: vi.fn()
}) }));
vi.mock("./features/tasks/useReviewTasks", () => ({ useReviewTasks: () => ({ tasks: [] }) }));
vi.mock("./features/capture/useExamCaptureContext", () => ({ useExamCaptureContext: () => ({
  exams: [], selectedExamId: "", scanSubmissionId: "", captureBatchId: "", scanStartPage: 1
}) }));
// App 会同步扫描模板并渲染队列恢复入口；这些 stub 不替代离线租约说明分支。
vi.mock("./features/scanner/useScannerController", () => ({ useScannerController: () => ({
  setExpectedTemplatePreset: vi.fn(), setScannerPreflight: vi.fn()
}) }));
vi.mock("./features/scan-queue/useScanQueue", () => ({ useScanQueue: () => ({
  queue: [], setOfflineDraftCount: vi.fn(), queueRestoreStatus: "ready", queueRestoreError: null,
  restoreQueue: vi.fn(), queueContextError: null, loadingQueueContext: false, refreshQueueContext: vi.fn()
}) }));
vi.mock("./features/upload/useUploadSync", () => ({ useUploadSync: () => ({}) }));
vi.mock("./features/quality/useSubmissionQuality", () => ({ useSubmissionQuality: () => ({}) }));
vi.mock("./features/scan/ScanWorkspace", () => ({ ScanWorkspace: () => null }));

describe("离线恢复说明通过 App 原入口展示", () => {
  let root: Root;
  let container: HTMLDivElement;
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-09T14:00:00Z"));
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.restoreAllMocks();
  });

  it.each([
    {
      name: "匹配且有效的租约先展示重启恢复说明", expiresAt: "2026-10-10T14:00:00Z", type: "info",
      description: "请先加密保存草稿；重启后可在连接登录页解锁本账号草稿。答卷图片仍需联网恢复，上传和下载任务需在线登录。"
    },
    {
      name: "已有账号但租约过期时展示本页续期说明", expiresAt: "2026-10-08T14:00:00Z", type: "warning",
      description: "当前未保存评分仍保留。请在本页联网验证同一账号后续期，再加密保存；续期失败也可以留在本页重试。"
    },
    {
      name: "缺少授权时展示在线登录授权说明", expiresAt: null, type: "warning",
      description: "跨重启继续阅卷前，请到连接登录页勾选本机离线授权并再次在线登录。当前编辑内容仍须加密保存。"
    }
  ])("$name", async ({ expiresAt, type, description }) => {
    data.offlineAccess = expiresAt ? { actorId: "teacher", tenantId: "school", server: "http://localhost:8080",
      username: "teacher", tenantCode: "school", expiresAt } : null;
    // 从真实导航触发 renderOffline；mock 只展示 Alert 属性，不替代说明分支。
    await act(async () => root.render(<App />));
    const offline = [...container.querySelectorAll<HTMLButtonElement>(".desktop-nav button")]
      .find((button) => button.textContent === "离线阅卷");
    expect(offline).toBeDefined();
    await act(async () => offline!.click());
    expect(container.querySelector(".desktop-topbar h2")?.textContent).toBe("离线阅卷");
    expect(container.querySelector("[data-alert-description]")?.textContent).toBe(description);
    expect(container.querySelector("[data-alert-type]")?.getAttribute("data-alert-type")).toBe(type);
  });
});
