// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useDesktopSession } from "./useDesktopSession";

const auth = vi.hoisted(() => ({ login: vi.fn(), logout: vi.fn(), getCurrentUser: vi.fn() }));
const durable = vi.hoisted(() => ({ bindDurableSession: vi.fn(), clearDurableSession: vi.fn(), readOfflineAccess: vi.fn(), authorizeOfflineAccess: vi.fn(), revokeOfflineAccess: vi.fn(), unlockOfflineAccess: vi.fn() }));
const runtime = vi.hoisted(() => ({
  isTauriRuntime: vi.fn(), loadStoredCredentials: vi.fn(),
  saveStoredCredentials: vi.fn(), deleteStoredCredentials: vi.fn()
}));
vi.mock("../../api/auth", () => auth);
vi.mock("../../lib/durableStore", () => durable);
vi.mock("../../lib/localRuntime", () => runtime);

describe("desktop session choices", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useDesktopSession>;
  const logEvent = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    vi.clearAllMocks();
    window.sessionStorage.clear();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    auth.login.mockResolvedValue({
      access_token: "active-bearer", expires_at: "2030-01-01T00:00:00Z",
      user: { id: "user-1", tenant_id: "tenant-1", username: "admin", tenant_code: "demo" }
    });
    auth.logout.mockResolvedValue({ status: "logged_out" });
    durable.bindDurableSession.mockResolvedValue("scope-1");
    durable.clearDurableSession.mockResolvedValue(undefined);
    durable.readOfflineAccess.mockResolvedValue(null);
    durable.revokeOfflineAccess.mockResolvedValue(undefined);
    runtime.isTauriRuntime.mockReturnValue(false);
    runtime.loadStoredCredentials.mockResolvedValue(null);
    runtime.saveStoredCredentials.mockResolvedValue(undefined);
    runtime.deleteStoredCredentials.mockResolvedValue(undefined);
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.useRealTimers();
  });

  async function mount(storeReady = false) {
    runtime.isTauriRuntime.mockReturnValue(storeReady);
    function Probe() {
      current = useDesktopSession("http://localhost:8080", logEvent);
      return null;
    }
    await act(async () => root.render(<Probe />));
    expect(current.credentialStoreReady).toBe(storeReady);
  }

  it.each([
    [true, true, "save"], [true, false, "save"],
    [false, true, "delete"], [false, false, "none"]
  ] as const)("uses the original persistence action with remember=%s and storeReady=%s", async (remember, storeReady, action) => {
    await mount(storeReady);
    await act(async () => { current.setRememberLogin(remember); });
    await act(async () => { await current.handleLogin(); });
    expect(current.token).toBe("active-bearer");
    expect(runtime.saveStoredCredentials).toHaveBeenCalledTimes(action === "save" ? 1 : 0);
    expect(runtime.deleteStoredCredentials).toHaveBeenCalledTimes(action === "delete" ? 1 : 0);
  });

  it.each([false, true])("keeps the local cleanup message when revocation failure=%s", async (revokeFails) => {
    await mount();
    await act(async () => { await current.handleLogin(); });
    if (revokeFails) auth.logout.mockRejectedValue(new Error("network unavailable"));
    // 登录先完成，随后只令退出清理失败；双失败时本机清理提示仍优先，日志记录退出未完整完成。
    durable.clearDurableSession.mockRejectedValueOnce(new Error("local storage unavailable"));
    await act(async () => { await current.handleLogout(); });
    expect(auth.logout).toHaveBeenCalledOnce();
    expect(current.token).toBeNull();
    expect(current.user).toBeNull();
    expect(current.durableScopeKey).toBe("");
    expect(current.authError).toBe("本地保存的会话清理失败，请检查本机凭据存储。");
    expect(current.isLoggingOut).toBe(false);
    expect(logEvent).toHaveBeenLastCalledWith("warning", "session logout incomplete", expect.any(String));
  });

  function offlineGrant() {
    return { server: "https://school.example", tenantId: "tenant-1", actorId: "user-1", tenantCode: "school", username: "teacher", displayName: "教师",
      authorizedAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 3600000).toISOString() };
  }

  it("recovers the saved local identity after restart without network login or bearer permissions", async () => {
    const access = offlineGrant();
    durable.readOfflineAccess.mockResolvedValue(access);
    durable.unlockOfflineAccess.mockResolvedValue({ sessionId: "new-offline-lease", access });
    await mount(true);
    expect(current.token).toBeNull(); expect(current.durableScopeKey).toBe("");
    await act(async () => { expect(await current.handleUnlockOffline()).toBe(true); });
    expect(current.durableScopeKey).toBe("new-offline-lease");
    expect(current.user).toMatchObject({ id: "user-1", status: "offline_local", permissions: [], roles: [] });
    expect(current.client.authorizationHeader()).toBeNull();
    await act(async () => { await current.handleCheckSession(); });
    expect(auth.getCurrentUser).not.toHaveBeenCalled(); expect(auth.login).not.toHaveBeenCalled();
    await act(async () => root.unmount()); root = createRoot(container);
    await mount(true);
    expect(current.durableScopeKey).toBe("");
    await act(async () => { await current.handleUnlockOffline(); });
    expect(durable.unlockOfflineAccess).toHaveBeenCalledTimes(2);
    expect(current.token).toBeNull();
  });

  it("authorizes offline data independently from saving the online password", async () => {
    await mount(true);
    const access = offlineGrant(); durable.authorizeOfflineAccess.mockResolvedValue(access);
    await act(async () => current.setEnableOfflineAccess(true));
    await act(async () => { await current.handleLogin(); });
    expect(durable.authorizeOfflineAccess).toHaveBeenCalledWith(expect.objectContaining({ actorId: "user-1", tenantId: "tenant-1", expiresAt: "2030-01-01T00:00:00Z" }), "scope-1");
    expect(runtime.saveStoredCredentials).not.toHaveBeenCalled();
    expect(current.offlineAccess).toEqual(access);
  });

  it("does not bind a local identity when the native grant expired or belongs to another account", async () => {
    durable.readOfflineAccess.mockResolvedValue(offlineGrant());
    durable.unlockOfflineAccess.mockRejectedValue(new Error("本机离线授权已过期或账号不一致"));
    await mount(true);
    await act(async () => { expect(await current.handleUnlockOffline()).toBe(false); });
    expect(current.durableScopeKey).toBe(""); expect(current.token).toBeNull(); expect(current.user).toBeNull();
    expect(auth.login).not.toHaveBeenCalled();
  });

  it("revokes the grant on logout even when clearing the in-memory native session fails", async () => {
    const access = offlineGrant(); durable.readOfflineAccess.mockResolvedValue(access);
    durable.unlockOfflineAccess.mockResolvedValue({ sessionId: "offline-lease", access });
    await mount(true); await act(async () => { await current.handleUnlockOffline(); });
    durable.clearDurableSession.mockRejectedValueOnce(new Error("native clear failed"));
    await act(async () => { await current.handleLogout(); });
    expect(durable.revokeOfflineAccess).toHaveBeenCalled();
    expect(current.offlineAccess).toBeNull(); expect(current.durableScopeKey).toBe("");
    expect(auth.logout).not.toHaveBeenCalled();
  });

  it("keeps incomplete address input editable and validates only an explicit login", async () => {
    await mount();
    await act(async () => current.setServerUrl(""));
    expect(current.serverUrl).toBe(""); expect(current.client.url("/health")).toBe("http://localhost:8080/health");
    await act(async () => { expect(await current.handleLogin()).toBe(false); });
    expect(auth.login).not.toHaveBeenCalled(); expect(current.authError).toContain("地址");
    await act(async () => { current.setServerUrl("https://school.example/"); });
    await act(async () => { expect(await current.handleLogin()).toBe(true); });
    expect(current.client.url("/health")).toBe("https://school.example/health");
  });

  async function unlockForRenewal() {
    const access = offlineGrant();
    durable.readOfflineAccess.mockResolvedValue(access);
    durable.unlockOfflineAccess.mockResolvedValue({ sessionId: "expired-offline-lease", access });
    durable.authorizeOfflineAccess.mockResolvedValue({ ...access, expiresAt: new Date(Date.now() + 7200000).toISOString() });
    await mount(true); await act(async () => { await current.handleUnlockOffline(); });
    return access;
  }

  it("keeps the same editor identity while replacing the expired lease after same-account authentication", async () => {
    vi.useFakeTimers();
    const access = await unlockForRenewal();
    await act(async () => { vi.advanceTimersByTime(3600001); });
    expect(current.offlineAccessMessage).toContain("已过期");
    const editorKey = current.editorAccountKey;
    let finish!: (value: unknown) => void; auth.login.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    let pending!: Promise<boolean>;
    await act(async () => { pending = current.handleRenewOfflineAccess("renew-password"); });
    expect(current.editorAccountKey).toBe(editorKey); expect(current.durableScopeKey).toBe("expired-offline-lease");
    expect(current.user?.id).toBe("user-1"); expect(durable.clearDurableSession).not.toHaveBeenCalled();
    await act(async () => { expect(await current.handleRenewOfflineAccess("again")).toBe(false); });
    expect(auth.login).toHaveBeenCalledTimes(1);
    await act(async () => { finish({ access_token: "new-token", expires_at: "2030-01-01T00:00:00Z", user: { id: access.actorId, tenant_id: access.tenantId, tenant_code: access.tenantCode, username: access.username, display_name: access.displayName } }); expect(await pending).toBe(true); });
    expect(auth.login).toHaveBeenCalledWith(expect.objectContaining({ baseUrl: access.server }), { tenant_code: access.tenantCode, username: access.username, password: "renew-password" });
    expect(current.editorAccountKey).toBe(editorKey); expect(current.durableScopeKey).toBe("scope-1"); expect(current.token).toBe("new-token");
    expect(durable.authorizeOfflineAccess).toHaveBeenCalledWith(expect.objectContaining({ actorId: access.actorId, tenantId: access.tenantId }), "scope-1");
    expect(current.offlineUnlocked).toBe(false); expect(current.offlineAccessMessage).toContain("当前评分输入保留");
  });

  it.each(["password", "lease", "actor", "tenant"])("retains the unlocked identity and editor on renewal failure=%s", async (failure) => {
    await unlockForRenewal(); const key = current.editorAccountKey;
    if (failure === "password") auth.login.mockRejectedValueOnce(new Error("密码错误"));
    if (failure === "lease") durable.bindDurableSession.mockRejectedValueOnce(new Error("lease install failed"));
    if (failure === "actor" || failure === "tenant") auth.login.mockResolvedValueOnce({ access_token: "wrong-token", expires_at: "2030-01-01T00:00:00Z", user: { id: failure === "actor" ? "user-2" : "user-1", tenant_id: failure === "tenant" ? "tenant-2" : "tenant-1" } });
    await act(async () => { expect(await current.handleRenewOfflineAccess("password")).toBe(false); });
    expect(current.editorAccountKey).toBe(key); expect(current.durableScopeKey).toBe("expired-offline-lease");
    expect(current.offlineUnlocked).toBe(true); expect(current.token).toBeNull(); expect(current.user?.id).toBe("user-1");
    expect(current.authError).toBeTruthy(); expect(durable.clearDurableSession).not.toHaveBeenCalled();
    if (failure === "actor" || failure === "tenant") { expect(durable.bindDurableSession).not.toHaveBeenCalled(); expect(durable.authorizeOfflineAccess).not.toHaveBeenCalled(); }
  });

  it("keeps the editor and new online lease if the renewed offline grant cannot be saved", async () => {
    await unlockForRenewal(); const key = current.editorAccountKey;
    durable.authorizeOfflineAccess.mockRejectedValueOnce(new Error("credential store unavailable"));
    await act(async () => { expect(await current.handleRenewOfflineAccess("password")).toBe(true); });
    expect(current.editorAccountKey).toBe(key); expect(current.durableScopeKey).toBe("scope-1");
    expect(current.token).toBe("active-bearer"); expect(current.offlineAccessMessage).toContain("可加密保存当前输入");
  });

  it("does not install a late renewed lease after an explicit logout", async () => {
    await unlockForRenewal(); let finish!: (value: unknown) => void;
    auth.login.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    let pending!: Promise<boolean>; await act(async () => { pending = current.handleRenewOfflineAccess("password"); });
    await act(async () => { await current.handleLogout(); });
    await act(async () => { finish({ access_token: "late-token", user: { id: "user-1", tenant_id: "tenant-1" } }); expect(await pending).toBe(false); });
    expect(current.durableScopeKey).toBe(""); expect(current.user).toBeNull(); expect(durable.bindDurableSession).not.toHaveBeenCalled();
  });
});
