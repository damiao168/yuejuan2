// @vitest-environment jsdom
import { act, lazy, StrictMode, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthUser, LoginResponse } from "./api/auth";
import type { LoginFormValues } from "./pages/LoginPage";
import type { SessionUser } from "./auth/session";
import { executeBusinessCommand, getBusinessCommandSession } from "./api/businessCommand";
import { AppShell } from "./AppShell";
import type { RouteRenderingContext } from "./router/pageRendering";
import { CONVERSATION_REFRESH_PREFIX } from "./features/school-assistant/conversationRefresh";

type LoginProps = { onLogin: (values: LoginFormValues) => Promise<void>; onWechatAuthenticate: (authenticate: () => Promise<LoginResponse | undefined>) => Promise<void>; loading: boolean; error?: string };
type LayoutProps = { user: SessionUser; onLogout: () => void; onLockSession: () => Promise<void>; onReauthenticate: (password: string) => Promise<void>; children: ReactNode };
const state = vi.hoisted(() => ({
  me: vi.fn(), login: vi.fn(), logout: vi.fn(), lock: vi.fn(), reauthenticate: vi.fn(), changePassword: vi.fn(), schools: vi.fn(),
  push: vi.fn(), replace: vi.fn(), clear: vi.fn(), cancelQueries: vi.fn(),
  loginProps: null as LoginProps | null, layoutProps: null as LayoutProps | null,
  path: "/admin/dashboard", renderContent: vi.fn(),
  openStudent: vi.fn(),
  firstChange: null as { onChangePassword: (input: { current_password: string; new_password: string }) => Promise<void>; onLogout: () => void } | null,
}));
vi.mock("./api/auth", () => ({ changePassword: state.changePassword, getCurrentUser: state.me, login: state.login, logout: state.logout, lockCurrentSession: state.lock, reauthenticate: state.reauthenticate }));
vi.mock("./api/org", () => ({ listSchools: state.schools }));
vi.mock("./auth/loginDestination", async (importOriginal) => ({ ...await importOriginal<typeof import("./auth/loginDestination")>(), openStudentPortal: state.openStudent }));
vi.mock("./auth/FirstPasswordChange", () => ({ FirstPasswordChange: (props: NonNullable<typeof state.firstChange>) => { state.firstChange = props; return <section>首次登录请修改密码</section>; } }));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => ({ clear: state.clear, cancelQueries: state.cancelQueries }) }));
vi.mock("@tanstack/react-router", () => ({ useRouter: () => ({ history: { push: state.push, replace: state.replace } }), useRouterState: () => state.path }));
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
  const { usePageLoading } = await import("./components/pageReadiness");
  return { LoadingState: ({ label }: { label: string }) => { usePageLoading(); return createElement("span", null, label); } };
});
vi.mock("./components/ErrorBoundary", () => ({ ErrorBoundary: ({ children }: { children: ReactNode }) => children }));
vi.mock("./auth/stepUpContext", () => ({ StepUpProvider: ({ children }: { children: ReactNode }) => children }));
vi.mock("./features/school-assistant/SchoolAssistantProvider", () => ({ SchoolAssistantProvider: ({ children }: { children: ReactNode }) => children }));
vi.mock("./features/onboarding/OnboardingGate", () => ({ OnboardingGate: () => null }));
vi.mock("./components/PendingBusinessCommands", () => ({ PendingBusinessCommands: () => null }));
vi.mock("./router/pageRendering", () => ({ renderAppRouteContent: state.renderContent }));

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
const refreshKey = (actor: string) => `${CONVERSATION_REFRESH_PREFIX}${JSON.stringify(["school", actor, ["school_admin"], ["ai_chat:use"], { schoolIds: ["school"] }, false])}`;

describe("AppShell session boundaries", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    for (const mock of [state.me, state.login, state.logout, state.lock, state.reauthenticate, state.changePassword, state.schools, state.push, state.replace, state.clear, state.cancelQueries, state.openStudent]) mock.mockReset();
    state.loginProps = null;
    state.layoutProps = null;
    state.firstChange = null;
    state.path = "/admin/dashboard";
    state.renderContent.mockReset().mockReturnValue(null);
    state.me.mockRejectedValue(new Error("not logged in"));
    state.logout.mockResolvedValue({ status: "logged_out" });
    state.lock.mockResolvedValue({ status: "locked" });
    state.reauthenticate.mockResolvedValue({ status: "authenticated" });
    state.cancelQueries.mockResolvedValue(undefined);
    state.changePassword.mockResolvedValue({ status: "password_changed" });
    state.schools.mockResolvedValue({ schools: [] });
    window.localStorage.clear();
    window.sessionStorage.clear();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); window.history.replaceState(null, "", "/"); vi.restoreAllMocks(); });

  it("统一表单的学生身份进入独立门户，保留学校和考试深链，初始密码不挂载教师业务", async () => {
    window.history.replaceState(null, "", "/?return_to=%2Fstudent%2Fexams%2Fexam-1");
    await act(async () => root.render(<AppShell />));
    state.login.mockResolvedValue({ ...response("student"), user: { ...authUser("student"), roles: ["student"], must_change_password: true } });
    await act(async () => state.loginProps!.onLogin({ ...form("00123"), tenant_code: "S053" }));
    expect(state.openStudent).toHaveBeenCalledExactlyOnceWith("/student/exams/exam-1?school=S053");
    expect(state.renderContent).not.toHaveBeenCalled();
    expect(state.firstChange).toBeNull();
    expect(state.push).not.toHaveBeenCalled();
  });

  it("已有学生会话从统一入口恢复后直接进入门户，不显示管理工作台", async () => {
    state.me.mockResolvedValue({ user: { ...authUser("student"), roles: ["student"], permissions: ["student:grade:read"] } });
    await act(async () => root.render(<AppShell />));
    expect(state.openStudent).toHaveBeenCalledExactlyOnceWith("/student/");
    expect(state.renderContent).not.toHaveBeenCalled(); expect(state.layoutProps).toBeNull();
  });

  it("初始密码会话不挂载业务路由或预读学校，改密成功直接清身份回登录", async () => {
    state.me.mockResolvedValue({ user: { ...authUser("new"), must_change_password: true, permissions: ["org:manage"] } });
    await act(async () => root.render(<AppShell />));
    expect(host.textContent).toContain("首次登录请修改密码");
    expect(state.renderContent).not.toHaveBeenCalled(); expect(state.layoutProps).toBeNull(); expect(state.schools).not.toHaveBeenCalled();
    state.path = "/admin/exams/another/scores";
    await act(async () => root.render(<AppShell />));
    expect(state.renderContent).not.toHaveBeenCalled();
    window.sessionStorage.setItem(refreshKey("new"), "initial-session checkpoint");
    await act(async () => state.firstChange!.onChangePassword({ current_password: "initial-123", new_password: "personal-456" }));
    expect(state.changePassword).toHaveBeenCalledWith({ current_password: "initial-123", new_password: "personal-456" });
    expect(host.textContent).toContain("Login"); expect(getBusinessCommandSession().scope).toBe("");
    expect(window.sessionStorage.getItem(refreshKey("new"))).toBeNull();
  });

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

  it.each(["password-first", "wechat-first"] as const)("serializes password and actual WeChat authentication requests: %s", async (order) => {
    await act(async () => root.render(<AppShell />));
    const password = deferred<LoginResponse>();
    const wechat = deferred<LoginResponse>();
    let cookieOwner = "";
    state.login.mockImplementation(async () => {
      const result = await password.promise;
      cookieOwner = result.user.id;
      return result;
    });
    const authenticate = vi.fn(async () => {
      const result = await wechat.promise;
      cookieOwner = result.user.id;
      return result;
    });
    let first!: Promise<void>;
    let last!: Promise<void>;
    // 替身按响应落定更新 Cookie 所有者；保护的是请求顺序，不把它视为真实浏览器网络集成。
    if (order === "password-first") {
      await act(async () => { first = state.loginProps!.onLogin(form("password")); });
      await act(async () => { last = state.loginProps!.onWechatAuthenticate(authenticate); });
      expect(authenticate).not.toHaveBeenCalled();
      await act(async () => { password.resolve(response("password")); await first; });
    } else {
      await act(async () => { first = state.loginProps!.onWechatAuthenticate(authenticate); });
      await act(async () => { last = state.loginProps!.onLogin(form("password")); });
      expect(state.login).not.toHaveBeenCalled();
      await act(async () => { wechat.resolve(response("wechat")); await first; });
    }
    expect(host.textContent).toContain("Login");
    expect(getBusinessCommandSession().scope).toBe("");
    await act(async () => {
      if (order === "password-first") wechat.resolve(response("wechat"));
      else password.resolve(response("password"));
      await last;
    });
    const expectedOwner = order === "password-first" ? "wechat" : "password";
    expect(cookieOwner).toBe(expectedOwner);
    expect(host.textContent).toBe(expectedOwner);
    expect(getBusinessCommandSession().scope).toBe(`school:${expectedOwner}`);
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
    // 服务端退出未确认前隐藏登录入口，旧再认证完成后仍不能恢复旧身份。
    expect(host.textContent).toContain("正在退出登录");
    expect(B).not.toHaveBeenCalled();
    window.localStorage.setItem("business-command:school:B:score.publish:exam", JSON.stringify({ id: "B-command" }));
    await act(async () => { unlock.resolve({ status: "authenticated" }); await result; });
    await act(async () => state.loginProps!.onLogin(form("B")));
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

  it("clears only chat refresh checkpoints on explicit logout without erasing other tab state or pending receipts", async () => {
    state.me.mockResolvedValue({ user: authUser("A") });
    await act(async () => root.render(<AppShell />));
    window.sessionStorage.setItem(refreshKey("A"), "A checkpoint");
    window.sessionStorage.setItem(refreshKey("B"), "B checkpoint");
    window.sessionStorage.setItem("exam-import-progress", "keep import");
    window.sessionStorage.setItem("edugrade.ai-chat.refresh.v10:other", "other version");
    const pending = "business-command:school:A:score.publish:exam";
    window.localStorage.setItem(pending, JSON.stringify({ id: "pending-command" }));
    window.localStorage.setItem("ordinary-preference", "keep preference");
    await act(async () => state.layoutProps!.onLogout());
    expect(window.sessionStorage.getItem(refreshKey("A"))).toBeNull();
    expect(window.sessionStorage.getItem(refreshKey("B"))).toBeNull();
    expect(window.sessionStorage.getItem("exam-import-progress")).toBe("keep import");
    expect(window.sessionStorage.getItem("edugrade.ai-chat.refresh.v10:other")).toBe("other version");
    expect(window.localStorage.getItem(pending)).toBe(JSON.stringify({ id: "pending-command" }));
    expect(window.localStorage.getItem("ordinary-preference")).toBe("keep preference");
  });

  it("clears chat refresh checkpoints when the current initial authentication restore fails", async () => {
    window.sessionStorage.setItem(refreshKey("A"), "old checkpoint");
    window.sessionStorage.setItem("exam-import-progress", "keep import");
    const pending = "business-command:school:A:score.publish:exam";
    window.localStorage.setItem(pending, JSON.stringify({ id: "pending-command" }));
    window.localStorage.setItem("ordinary-preference", "keep preference");
    await act(async () => root.render(<AppShell />));
    expect(host.textContent).toContain("Login");
    expect(window.sessionStorage.getItem(refreshKey("A"))).toBeNull();
    expect(window.sessionStorage.getItem("exam-import-progress")).toBe("keep import");
    expect(window.localStorage.getItem(pending)).toBe(JSON.stringify({ id: "pending-command" }));
    expect(window.localStorage.getItem("ordinary-preference")).toBe("keep preference");
  });

  it("keeps refresh checkpoints across ordinary unmount and successful cookie-session restoration", async () => {
    state.me.mockResolvedValue({ user: authUser("A") });
    window.sessionStorage.setItem(refreshKey("A"), "in-progress checkpoint");
    await act(async () => root.render(<AppShell />));
    expect(window.sessionStorage.getItem(refreshKey("A"))).toBe("in-progress checkpoint");
    await act(async () => root.unmount());
    expect(window.sessionStorage.getItem(refreshKey("A"))).toBe("in-progress checkpoint");
    root = createRoot(host);
    await act(async () => root.render(<AppShell />));
    expect(host.textContent).toBe("A");
    expect(window.sessionStorage.getItem(refreshKey("A"))).toBe("in-progress checkpoint");
  });

  it("does not reuse a prior account checkpoint after a successful explicit login", async () => {
    await act(async () => root.render(<AppShell />));
    window.sessionStorage.setItem(refreshKey("A"), "prior account checkpoint");
    window.sessionStorage.setItem("exam-import-progress", "keep import");
    state.login.mockResolvedValueOnce(response("B"));
    await act(async () => state.loginProps!.onLogin(form("B")));
    expect(host.textContent).toBe("B");
    expect(window.sessionStorage.getItem(refreshKey("A"))).toBeNull();
    expect(window.sessionStorage.getItem("exam-import-progress")).toBe("keep import");
  });

  it("keeps failed logout isolated across refresh and retries before restoring any cookie session", async () => {
    state.me.mockResolvedValue({ user: authUser("A") });
    await act(async () => root.render(<AppShell />));
    state.logout.mockRejectedValue(new Error("offline"));
    await act(async () => state.layoutProps!.onLogout());
    expect(host.textContent).toContain("退出尚未完成");
    expect(host.textContent).not.toContain("Login");
    expect(getBusinessCommandSession().scope).toBe("");
    window.sessionStorage.setItem(refreshKey("A"), "checkpoint written before the next page load");
    await act(async () => root.unmount());
    root = createRoot(host);
    state.me.mockClear();
    await act(async () => root.render(<AppShell />));
    expect(state.me).not.toHaveBeenCalled();
    expect(host.textContent).toContain("退出尚未完成");
    expect(window.sessionStorage.getItem(refreshKey("A"))).toBeNull();
    state.logout.mockResolvedValue({ status: "logged_out" });
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
    expect(host.textContent).toContain("Login");
    expect(window.localStorage.getItem("edugrade.web.logout-pending")).toBeNull();
  });

  it("does not restore the old cookie after failed logout when both storage types become unwritable", async () => {
    state.me.mockResolvedValue({ user: authUser("A") });
    await act(async () => root.render(<AppShell />));
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new DOMException("quota", "QuotaExceededError"); });
    state.logout.mockRejectedValue(new Error("offline"));
    await act(async () => state.layoutProps!.onLogout());
    expect(host.textContent).toContain("退出尚未完成");
    await act(async () => root.unmount());
    root = createRoot(host);
    state.me.mockClear();
    await act(async () => root.render(<AppShell />));
    expect(state.me).not.toHaveBeenCalled();
    expect(host.textContent).toContain("退出尚未完成");
    expect(getBusinessCommandSession().scope).toBe("");
  });

  it("keeps the current page visible while the next navigation module loads", async () => {
    const nextPage = deferred<{ default: () => ReactNode }>();
    const Students = lazy(() => nextPage.promise);
    state.me.mockResolvedValue({ user: authUser("A") });
    state.renderContent.mockImplementation(({ path }: RouteRenderingContext) => path.includes("/members/students")
      ? <Students /> : <div data-current-page>工作台内容</div>);
    await act(async () => root.render(<AppShell />));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 5)); });
    const previous = host.querySelector("[data-current-page]");
    state.path = "/admin/members/students";
    await act(async () => root.render(<AppShell />));
    expect(host.querySelector("[data-current-page]")).toBe(previous);
    expect(host.querySelector(".route-content-stage")?.hasAttribute("inert")).toBe(true);
    expect(host.querySelector(".route-content-stage:not(.route-content-stage-pending)")?.textContent).not.toContain("正在加载页面");
    await act(async () => { nextPage.resolve({ default: () => <h1>学生名单</h1> }); });
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 5)); });
    expect(host.querySelector("[data-current-page]")).toBeNull();
    expect(host.querySelector("h1")?.textContent).toBe("学生名单");
    expect(host.querySelector(".route-content-stage")?.hasAttribute("inert")).toBe(false);
  });

  it("does not discard the displayed page when the same account locks and unlocks", async () => {
    state.me.mockResolvedValue({ user: authUser("A", true) });
    state.renderContent.mockImplementation(() => <input aria-label="未保存内容" defaultValue="当前草稿" />);
    await act(async () => root.render(<AppShell />));
    const draft = host.querySelector<HTMLInputElement>("input")!;
    draft.value = "尚未提交的编辑";
    // 锁定会推进会话代次，但未改变身份或授权；外层锁屏负责遮挡，路由边界不能销毁草稿。
    await act(async () => { await state.layoutProps!.onLockSession(); });
    await act(async () => { await state.layoutProps!.onReauthenticate("test-password"); });
    expect(host.querySelector("input")).toBe(draft);
    expect(draft.value).toBe("尚未提交的编辑");
  });
});
