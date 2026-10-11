// @vitest-environment jsdom
import { act, StrictMode, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthUser, LoginResponse } from "./api/auth";
import type { LoginFormValues } from "./pages/LoginPage";
import type { SessionUser } from "./auth/session";
import { executeBusinessCommand, getBusinessCommandSession } from "./api/businessCommand";
import { AppShell } from "./AppShell";

type LoginProps = { onLogin: (values: LoginFormValues) => Promise<void>; onWechatLogin: (response: LoginResponse) => void; loading: boolean; error?: string };
type LayoutProps = { user: SessionUser; onLogout: () => void; onLockSession: () => Promise<void>; onReauthenticate: (password: string) => Promise<void>; children: ReactNode };
const state = vi.hoisted(() => ({
  me: vi.fn(), login: vi.fn(), logout: vi.fn(), lock: vi.fn(), reauthenticate: vi.fn(),
  push: vi.fn(), replace: vi.fn(), clear: vi.fn(), cancelQueries: vi.fn(),
  loginProps: null as LoginProps | null, layoutProps: null as LayoutProps | null,
}));
vi.mock("./api/auth", () => ({ getCurrentUser: state.me, login: state.login, logout: state.logout, lockCurrentSession: state.lock, reauthenticate: state.reauthenticate }));
vi.mock("./api/org", () => ({ listSchools: vi.fn(async () => ({ schools: [] })) }));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => ({ clear: state.clear, cancelQueries: state.cancelQueries }) }));
vi.mock("@tanstack/react-router", () => ({ useRouter: () => ({ history: { push: state.push, replace: state.replace } }), useRouterState: () => "/admin/dashboard" }));
vi.mock("antd", async () => {
  const { createElement } = await import("react");
  const wrapper = ({ children }: { children?: ReactNode }) => createElement("div", null, children);
  return { App: wrapper, ConfigProvider: wrapper };
});
vi.mock("./components/AppLayout", async () => {
  const { createElement } = await import("react");
  return { AppLayout: (props: LayoutProps) => { state.layoutProps = props; return createElement("main", null, props.user.id, props.children); } };
});
vi.mock("./pages/LoginPage", async () => {
  const { createElement } = await import("react");
  return { LoginPage: (props: LoginProps) => { state.loginProps = props; return createElement("section", null, "Login", props.error); } };
});
vi.mock("./pages/ActivationPage", () => ({ ActivationPage: () => null }));
vi.mock("./pages/RecoveryPage", () => ({ RecoveryPage: () => null }));
vi.mock("./components/PageState", async () => {
  const { createElement } = await import("react");
  return { LoadingState: ({ label }: { label: string }) => createElement("span", null, label) };
});
vi.mock("./components/ErrorBoundary", () => ({ ErrorBoundary: ({ children }: { children: ReactNode }) => children }));
vi.mock("./auth/stepUpContext", () => ({ StepUpProvider: ({ children }: { children: ReactNode }) => children }));
vi.mock("./features/school-assistant/SchoolAssistantProvider", () => ({ SchoolAssistantProvider: ({ children }: { children: ReactNode }) => children }));
vi.mock("./features/onboarding/OnboardingGate", () => ({ OnboardingGate: () => null }));
vi.mock("./components/PendingBusinessCommands", () => ({ PendingBusinessCommands: () => null }));
vi.mock("./router/pageRendering", () => ({ renderAppRouteContent: () => null }));

function authUser(id: string, publicComputer = false): AuthUser {
  return { id, tenant_id: "school", tenant_code: "school", username: id, display_name: id, status: "active", roles: ["school_admin"], permissions: [], data_scope: {}, current_session_type: publicComputer ? "public_device" : "standard" };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((onResolve) => { resolve = onResolve; });
  return { promise, resolve };
}
const form = (identifier: string): LoginFormValues => ({ identifier, password: "test-password", tenant_code: "school", remember_device: false, public_device: false });
const response = (id: string): LoginResponse => ({ user: authUser(id), expires_at: "2099-01-01" });

describe("AppShell session boundaries", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    for (const mock of [state.me, state.login, state.logout, state.lock, state.reauthenticate, state.push, state.replace, state.clear, state.cancelQueries]) mock.mockReset();
    state.loginProps = null;
    state.layoutProps = null;
    state.me.mockRejectedValue(new Error("not logged in"));
    state.logout.mockResolvedValue({ status: "logged_out" });
    state.lock.mockResolvedValue({ status: "locked" });
    state.reauthenticate.mockResolvedValue({ status: "authenticated" });
    state.cancelQueries.mockResolvedValue(undefined);
    window.localStorage.clear();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); });

  it("serializes rapid A/B logins and never publishes A after B starts", async () => {
    await act(async () => root.render(<AppShell />));
    const A = deferred<LoginResponse>();
    const B = deferred<LoginResponse>();
    state.login.mockImplementationOnce(() => A.promise).mockImplementationOnce(() => B.promise);
    let first!: Promise<void>;
    let second!: Promise<void>;
    await act(async () => { first = state.loginProps!.onLogin(form("A")); });
    await act(async () => { second = state.loginProps!.onLogin(form("B")); });
    expect(state.login).toHaveBeenCalledTimes(1);
    await act(async () => { A.resolve(response("A")); await first; });
    expect(host.textContent).toContain("Login");
    expect(getBusinessCommandSession().scope).toBe("");
    expect(state.login).toHaveBeenCalledTimes(2);
    await act(async () => { B.resolve(response("B")); await second; });
    expect(host.textContent).toBe("B");
    expect(getBusinessCommandSession().scope).toBe("school:B");
    expect(state.push).toHaveBeenCalledExactlyOnceWith("/admin/dashboard");
  });

  it("does not revive a StrictMode restoration that finishes after logout", async () => {
    const first = deferred<{ user: AuthUser }>();
    const current = deferred<{ user: AuthUser }>();
    state.me.mockImplementationOnce(() => first.promise).mockImplementationOnce(() => current.promise);
    await act(async () => root.render(<StrictMode><AppShell /></StrictMode>));
    await act(async () => current.resolve({ user: authUser("A") }));
    expect(host.textContent).toBe("A");
    await act(async () => state.layoutProps!.onLogout());
    await act(async () => first.resolve({ user: authUser("A") }));
    expect(host.textContent).toContain("Login");
    expect(getBusinessCommandSession().scope).toBe("");
    expect(state.cancelQueries).toHaveBeenCalledTimes(1);
    expect(state.clear).toHaveBeenCalledTimes(1);
  });

  it("blocks business commands during public-computer locking and binds the scope only after reauthentication", async () => {
    state.me.mockResolvedValue({ user: authUser("A", true) });
    await act(async () => root.render(<AppShell />));
    const initialLayout = state.layoutProps!;
    const lock = deferred<{ status: string }>();
    const unlock = deferred<{ status: string }>();
    state.lock.mockImplementationOnce(() => lock.promise);
    state.reauthenticate.mockImplementationOnce(() => unlock.promise);
    let locking!: Promise<void>;
    await act(async () => { locking = state.layoutProps!.onLockSession(); });
    const send = vi.fn();
    await expect(executeBusinessCommand("score.publish", "exam", {}, send, value => value)).rejects.toMatchObject({ name: "BusinessCommandSessionChangedError" });
    expect(send).not.toHaveBeenCalled();
    await act(async () => { lock.resolve({ status: "locked" }); await locking; });
    expect(state.layoutProps!.onReauthenticate).not.toBe(initialLayout.onReauthenticate);
    const lockedLayout = state.layoutProps;
    let unlocking!: Promise<void>;
    await act(async () => { unlocking = state.layoutProps!.onReauthenticate("test-password"); });
    expect(getBusinessCommandSession().scope).toBe("");
    await act(async () => { unlock.resolve({ status: "authenticated" }); await unlocking; });
    expect(getBusinessCommandSession().scope).toBe("school:A");
    expect(host.textContent).toBe("A");
    // 解锁不改变 generation；仍应触发渲染，不能只依赖用户或代次改变来刷新布局。
    expect(state.layoutProps).not.toBe(lockedLayout);
  });

  it("does not unlock or erase a later account's data when old reauthentication finishes", async () => {
    state.me.mockResolvedValue({ user: authUser("A", true) });
    await act(async () => root.render(<AppShell />));
    await act(async () => state.layoutProps!.onLockSession());
    const unlock = deferred<{ status: string }>();
    state.reauthenticate.mockImplementationOnce(() => unlock.promise);
    let unlocking!: Promise<void>;
    await act(async () => { unlocking = state.layoutProps!.onReauthenticate("test-password"); });
    const result = expect(unlocking).rejects.toMatchObject({ name: "SessionTransitionChangedError" });
    await act(async () => state.layoutProps!.onLogout());
    const B = state.login.mockResolvedValueOnce(response("B"));
    let login!: Promise<void>;
    await act(async () => { login = state.loginProps!.onLogin(form("B")); });
    // B 的认证写请求排在旧再认证和退出后，不能被它们迟到的 Cookie 响应覆盖。
    expect(B).not.toHaveBeenCalled();
    window.localStorage.setItem("business-command:school:B:score.publish:exam", JSON.stringify({ id: "B-command" }));
    await act(async () => { unlock.resolve({ status: "authenticated" }); await result; await login; });
    expect(host.textContent).toBe("B");
    expect(getBusinessCommandSession().scope).toBe("school:B");
    expect(window.localStorage.getItem("business-command:school:B:score.publish:exam")).toBe(JSON.stringify({ id: "B-command" }));
  });

  it("preserves ordinary pending receipts but retains public-computer logout cleanup", async () => {
    state.me.mockResolvedValue({ user: authUser("A") });
    await act(async () => root.render(<AppShell />));
    const key = "business-command:school:A:score.publish:exam";
    window.localStorage.setItem(key, JSON.stringify({ id: "original" }));
    await act(async () => state.layoutProps!.onLogout());
    expect(window.localStorage.getItem(key)).toBe(JSON.stringify({ id: "original" }));
    state.login.mockResolvedValueOnce({ ...response("A"), user: authUser("A", true) });
    await act(async () => state.loginProps!.onLogin(form("A")));
    await act(async () => state.layoutProps!.onLogout());
    expect(window.localStorage.getItem(key)).toBeNull();
  });
});
