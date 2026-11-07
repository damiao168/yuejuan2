// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useDesktopSession } from "./useDesktopSession";

const runtime = vi.hoisted(() => ({
  isTauriRuntime: vi.fn(), loadStoredCredentials: vi.fn(), saveStoredCredentials: vi.fn(), deleteStoredCredentials: vi.fn()
}));
const durable = vi.hoisted(() => ({
  clearDurableSession: vi.fn(), readOfflineAccess: vi.fn()
}));
vi.mock("../../lib/localRuntime", () => runtime);
vi.mock("../../lib/durableStore", () => durable);

describe("desktop login transport validation", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useDesktopSession>;
  const fetchMock = vi.fn();
  const logEvent = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    vi.clearAllMocks();
    window.sessionStorage.clear();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("fetch", fetchMock);
    runtime.isTauriRuntime.mockReturnValue(false);
    runtime.loadStoredCredentials.mockResolvedValue(null);
    durable.readOfflineAccess.mockResolvedValue(null);
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.unstubAllGlobals();
  });

  async function mount() {
    function Probe() {
      current = useDesktopSession("https://school.example", logEvent);
      return null;
    }
    await act(async () => root.render(<Probe />));
  }

  it.each(["http://127.review.example", "http://127.0.0.1.review.example"])("blocks manual login to %s before sending credentials", async (server) => {
    // 使用真实登录函数和网络客户端，验证地址拒绝发生在密码进入 fetch 之前。
    await mount();
    await act(async () => {
      current.setServerUrl(server);
      current.setTenantCode("school");
      current.setUsername("teacher");
      current.setPassword("synthetic-test-password");
    });
    await act(async () => { expect(await current.handleLogin()).toBe(false); });
    expect(current.authError).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(durable.clearDurableSession).not.toHaveBeenCalled();
  });

  it.each(["http://127.review.example", "http://127.0.0.1.review.example"])("blocks stored automatic login to %s before sending credentials", async (server) => {
    runtime.isTauriRuntime.mockReturnValue(true);
    runtime.loadStoredCredentials.mockResolvedValue({
      server_url: server, tenant_code: "school", username: "teacher", password: "synthetic-test-password"
    });
    await mount();
    expect(runtime.loadStoredCredentials).toHaveBeenCalledOnce();
    expect(current.authError).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(durable.clearDurableSession).not.toHaveBeenCalled();
  });
});
