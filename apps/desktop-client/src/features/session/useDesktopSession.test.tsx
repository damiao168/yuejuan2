// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useDesktopSession } from "./useDesktopSession";

const auth = vi.hoisted(() => ({
  login: vi.fn(),
  logout: vi.fn(),
  getCurrentUser: vi.fn()
}));
const durable = vi.hoisted(() => ({
  bindDurableSession: vi.fn(),
  clearDurableSession: vi.fn()
}));
vi.mock("../../api/auth", () => auth);
vi.mock("../../lib/durableStore", () => durable);
vi.mock("../../lib/localRuntime", () => ({
  isTauriRuntime: () => false,
  loadStoredCredentials: vi.fn(),
  saveStoredCredentials: vi.fn(),
  deleteStoredCredentials: vi.fn()
}));

describe("desktop logout", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useDesktopSession>;

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    auth.login.mockResolvedValue({
      access_token: "active-bearer",
      expires_at: "2030-01-01T00:00:00Z",
      user: { id: "user-1", tenant_id: "tenant-1", username: "admin", tenant_code: "demo" }
    });
    durable.bindDurableSession.mockResolvedValue("scope-1");
    durable.clearDurableSession.mockResolvedValue(undefined);
    auth.logout.mockResolvedValue({ status: "logged_out" });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  async function mountAndLogin() {
    function Probe() {
      current = useDesktopSession("http://localhost:8080", vi.fn().mockResolvedValue(undefined));
      return null;
    }
    await act(async () => { root.render(<Probe />); });
    await act(async () => { await current.handleLogin(); });
    expect(current.token).toBe("active-bearer");
  }

  it("revokes the active bearer before clearing local state", async () => {
    await mountAndLogin();
    auth.logout.mockImplementation(async (client) => {
      expect(client.authorizationHeader()).toBe("Bearer active-bearer");
      expect(current.token).toBe("active-bearer");
      return { status: "logged_out" };
    });
    await act(async () => { await current.handleLogout(); });
    expect(auth.logout).toHaveBeenCalledOnce();
    expect(current.token).toBeNull();
    expect(current.authError).toBeNull();
  });

  it("clears local state and reports an unconfirmed revocation when offline", async () => {
    await mountAndLogin();
    auth.logout.mockRejectedValue(new Error("network unavailable"));
    await act(async () => { await current.handleLogout(); });
    expect(current.token).toBeNull();
    expect(durable.clearDurableSession).toHaveBeenCalledTimes(2);
    expect(current.authError).toContain("会话吊销");
  });
});
