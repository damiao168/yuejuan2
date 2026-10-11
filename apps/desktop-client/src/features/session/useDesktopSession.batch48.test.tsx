// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OfflineAccess } from "../../lib/durableStore";
import type { StoredDesktopCredentials } from "../../lib/localRuntime";
import { useDesktopSession } from "./useDesktopSession";

const auth = vi.hoisted(() => ({ login: vi.fn(), logout: vi.fn(), getCurrentUser: vi.fn() }));
const durable = vi.hoisted(() => ({
  bindDurableSession: vi.fn(), clearDurableSession: vi.fn(), readOfflineAccess: vi.fn(),
  authorizeOfflineAccess: vi.fn(), revokeOfflineAccess: vi.fn(), unlockOfflineAccess: vi.fn()
}));
const runtime = vi.hoisted(() => ({
  isTauriRuntime: vi.fn(), loadStoredCredentials: vi.fn(), saveStoredCredentials: vi.fn(), deleteStoredCredentials: vi.fn()
}));
vi.mock("../../api/auth", () => auth);
vi.mock("../../lib/durableStore", () => durable);
vi.mock("../../lib/localRuntime", () => runtime);

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((finish) => { resolve = finish; });
  return { promise, resolve };
}

function offlineGrant(): OfflineAccess {
  return {
    server: "https://school.example", tenantId: "tenant-1", actorId: "user-1",
    tenantCode: "school", username: "teacher", displayName: "教师",
    authorizedAt: "2026-10-10T00:00:00Z", expiresAt: "2030-01-01T00:00:00Z"
  };
}

describe("desktop login persistence boundaries", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useDesktopSession>;
  const logEvent = vi.fn();

  beforeEach(() => {
    vi.resetAllMocks();
    window.sessionStorage.clear();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    runtime.isTauriRuntime.mockReturnValue(true);
    runtime.loadStoredCredentials.mockResolvedValue(null);
    runtime.saveStoredCredentials.mockResolvedValue(undefined);
    runtime.deleteStoredCredentials.mockResolvedValue(undefined);
    durable.readOfflineAccess.mockResolvedValue(null);
    durable.bindDurableSession.mockResolvedValue("scope-1");
    durable.clearDurableSession.mockResolvedValue(undefined);
    durable.authorizeOfflineAccess.mockResolvedValue(offlineGrant());
    durable.revokeOfflineAccess.mockResolvedValue(undefined);
    auth.login.mockResolvedValue({
      token_type: "Bearer", access_token: "online-token", expires_at: "2030-01-01T00:00:00Z",
      user: { id: "user-1", tenant_id: "tenant-1", tenant_code: "school", username: "teacher", display_name: "教师" }
    });
    auth.logout.mockResolvedValue({ status: "logged_out" });
    logEvent.mockResolvedValue(undefined);
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  async function mount() {
    function Probe() {
      current = useDesktopSession("https://school.example", logEvent);
      return null;
    }
    await act(async () => root.render(<Probe />));
  }

  it("publishes the authenticated identity only after its local lease is bound", async () => {
    await mount();
    const lease = deferred<string>();
    durable.bindDurableSession.mockReturnValueOnce(lease.promise);
    await act(async () => {
      current.setTenantCode(" school "); current.setUsername(" teacher ");
      current.setPassword("password"); current.setRememberLogin(true); current.setEnableOfflineAccess(true);
    });
    let pending!: Promise<boolean>;
    await act(async () => { pending = current.handleLogin(); });
    expect(auth.login).toHaveBeenCalledWith(expect.objectContaining({ baseUrl: "https://school.example" }), {
      tenant_code: "school", username: "teacher", password: "password"
    });
    expect(current.token).toBeNull(); expect(current.user).toBeNull();
    expect(durable.authorizeOfflineAccess).not.toHaveBeenCalled();
    expect(runtime.saveStoredCredentials).not.toHaveBeenCalled();
    await act(async () => { lease.resolve("bound-lease"); expect(await pending).toBe(true); });
    expect(durable.authorizeOfflineAccess).toHaveBeenCalledWith(expect.objectContaining({ actorId: "user-1", tenantId: "tenant-1" }), "bound-lease");
    expect(runtime.saveStoredCredentials).toHaveBeenCalledWith({
      server_url: "https://school.example", tenant_code: " school ", username: " teacher ", password: "password"
    });
    expect(durable.authorizeOfflineAccess.mock.invocationCallOrder[0]).toBeLessThan(runtime.saveStoredCredentials.mock.invocationCallOrder[0]);
    expect(current.token).toBe("online-token"); expect(current.durableScopeKey).toBe("bound-lease");
    expect(current.password).toBe("");
  });

  it.each([true, false])("keeps online login and continues credential storage when offline grant update fails with allowOffline=%s", async (allowOffline) => {
    await mount();
    const update = allowOffline ? durable.authorizeOfflineAccess : durable.revokeOfflineAccess;
    update.mockRejectedValueOnce(new Error("offline store unavailable"));
    await act(async () => { current.setEnableOfflineAccess(allowOffline); current.setRememberLogin(true); });
    await act(async () => { expect(await current.handleLogin()).toBe(true); });
    expect(current.offlineAccessMessage).toBe("本机离线授权保存失败；重启后需联网登录。");
    expect(current.token).toBe("online-token"); expect(current.durableScopeKey).toBe("scope-1");
    expect(runtime.saveStoredCredentials).toHaveBeenCalledOnce();
    expect(durable.clearDurableSession).toHaveBeenCalledOnce();
    expect(current.authError).toBeNull();
    expect(logEvent).toHaveBeenLastCalledWith("info", "login succeeded", "teacher@school");
  });

  it.each(["save", "delete"] as const)("retains the online identity after a system credential %s failure", async (action) => {
    await mount();
    const operation = action === "save" ? runtime.saveStoredCredentials : runtime.deleteStoredCredentials;
    operation.mockRejectedValueOnce(new Error("credential store unavailable"));
    await act(async () => current.setRememberLogin(action === "save"));
    await act(async () => { expect(await current.handleLogin()).toBe(true); });
    expect(operation).toHaveBeenCalledOnce();
    expect(current.credentialStoreMessage).toBe(action === "save"
      ? "系统凭据库保存失败；未写入其他本地存储。" : "系统凭据清除失败。");
    expect(current.credentialStoreReady).toBe(action === "delete");
    expect(current.token).toBe("online-token"); expect(current.user?.id).toBe("user-1");
    expect(current.durableScopeKey).toBe("scope-1"); expect(current.authError).toBeNull();
    expect(durable.clearDurableSession).toHaveBeenCalledOnce();
  });

  it("discards a startup credential read that completes after manual login", async () => {
    const stored = deferred<StoredDesktopCredentials | null>();
    runtime.loadStoredCredentials.mockReturnValueOnce(stored.promise);
    durable.readOfflineAccess.mockResolvedValueOnce({ ...offlineGrant(), actorId: "old-user", username: "old-user" });
    await mount();
    await act(async () => current.setUsername("teacher"));
    await act(async () => { expect(await current.handleLogin()).toBe(true); });
    await act(async () => stored.resolve({ server_url: "https://old.example", tenant_code: "old-school", username: "old-user", password: "old-password" }));
    expect(auth.login).toHaveBeenCalledOnce();
    expect(current.serverUrl).toBe("https://school.example"); expect(current.username).toBe("teacher");
    expect(current.user?.id).toBe("user-1"); expect(current.offlineAccess).toBeNull();
    expect(current.rememberLogin).toBe(false);
  });

  it("stops a late offline grant result after logout before credential persistence", async () => {
    await mount();
    const grant = deferred<OfflineAccess>();
    durable.authorizeOfflineAccess.mockReturnValueOnce(grant.promise);
    await act(async () => { current.setEnableOfflineAccess(true); current.setRememberLogin(true); });
    let pending!: Promise<boolean>;
    // 授权保存仍在等待时退出；迟到结果不能恢复离线身份，也不能继续保存这次登录的密码。
    await act(async () => { pending = current.handleLogin(); });
    expect(current.token).toBe("online-token");
    await act(async () => { await current.handleLogout(); });
    await act(async () => { grant.resolve(offlineGrant()); expect(await pending).toBe(false); });
    expect(current.token).toBeNull(); expect(current.user).toBeNull(); expect(current.offlineAccess).toBeNull();
    expect(current.durableScopeKey).toBe(""); expect(runtime.saveStoredCredentials).not.toHaveBeenCalled();
    expect(logEvent).not.toHaveBeenCalledWith("info", "login succeeded", expect.any(String));
  });
});
