// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LoginPage } from "./LoginPage";
import { getCurrentUser, pollWechatLogin, startWechatLogin, type AuthUser, type LoginResponse, type WechatLoginChallengeResponse } from "../api/auth";

vi.mock("../api/auth", () => ({ getCurrentUser: vi.fn(), pollWechatLogin: vi.fn(), startWechatLogin: vi.fn() }));

beforeEach(() => {
  vi.resetAllMocks();
  vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
  window.history.replaceState({}, "", "/");
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

const authenticate = async (action: () => Promise<LoginResponse | undefined>) => { await action(); };
const challenge: WechatLoginChallengeResponse = { challenge_id: "synthetic-challenge", poll_token: "synthetic-token", qr_code_data_url: "data:image/png;base64,c3ludGhldGlj", expires_at: "2099-01-01" };

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

async function generateWechat() {
  fireEvent.click(screen.getByRole("tab", { name: "微信扫码" }));
  fireEvent.change(screen.getByLabelText("学校代码"), { target: { value: "demo" } });
  fireEvent.click(screen.getByRole("button", { name: "生成微信登录二维码" }));
  await waitFor(() => expect(startWechatLogin).toHaveBeenCalledOnce());
}

describe("login identity fields", () => {
  it("所有身份直接使用同一登录表单，不再选择教师端或学生端", () => {
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={authenticate} />);
    expect(screen.queryByRole("tablist", { name: "产品入口" })).toBeNull();
    expect(screen.queryByRole("link", { name: "进入学生端登录" })).toBeNull();
    expect(screen.queryByRole("link", { name: "平台管理入口" })).toBeNull();
    expect(screen.getByLabelText("学校代码")).toBeTruthy();
    expect(screen.getByLabelText("账号")).toBeTruthy();
    expect(screen.getByLabelText("密码")).toBeTruthy();
    expect(screen.getByRole("tab", { name: "微信扫码" })).toBeTruthy();
  });

  it("学校专属入口直接显示账号密码表单并保留学校定位", async () => {
    window.history.replaceState({}, "", "/?tenant=school-a");
    const login = vi.fn();
    render(<LoginPage onLogin={login} onWechatAuthenticate={authenticate} />);
    expect(screen.getByText("学校入口：school-a")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("账号"), { target: { value: "20260001" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "student-initial-password" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(login).toHaveBeenCalledWith(expect.objectContaining({ tenant_hint: "school-a", identifier: "20260001", password: "student-initial-password" })));
    expect(login.mock.calls[0][0].tenant_code).toBeUndefined();
  });

  it("旧平台入口也使用统一表单，平台代码和账号仍提交既有认证协议", async () => {
    window.history.replaceState({}, "", "/?portal=platform");
    const login = vi.fn();
    render(<LoginPage onLogin={login} onWechatAuthenticate={authenticate} />);
    expect(screen.queryByRole("heading", { name: "平台管理登录" })).toBeNull();
    expect(screen.getByRole("tab", { name: "微信扫码" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("学校代码"), { target: { value: "platform" } });
    fireEvent.change(screen.getByLabelText("账号"), { target: { value: "operator-admin" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "existing-platform-password" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(login).toHaveBeenCalledWith(expect.objectContaining({ tenant_code: "platform", identifier: "operator-admin" })));
    expect(login.mock.calls[0][0].tenant_hint).toBeUndefined();
  });

  it("登录请求期间不能切换认证方式", () => {
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={authenticate} loading />);
    expect((screen.getByRole("tab", { name: "微信扫码" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByLabelText("账号")).toBeTruthy();
  });

  it("外部门户配置不会让统一登录表单携带凭据跳到另一个页面", () => {
    vi.stubEnv("VITE_STUDENT_PORTAL_URL", "https://students.synthetic.example/entry");
    window.history.replaceState({}, "", "/?tenant=school-a");
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={authenticate} />);
    fireEvent.change(screen.getByLabelText("账号"), { target: { value: "20260001" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "never-include-in-url" } });
    expect(screen.queryByRole("link")).toBeNull();
    expect(window.location.search).toBe("?tenant=school-a");
  });

  it("学校专属入口切换微信即生成普通会话二维码，改设备后显式重建", async () => {
    window.history.replaceState({}, "", "/?tenant=demo");
    vi.mocked(startWechatLogin).mockResolvedValue(challenge);
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={authenticate} />);
    fireEvent.click(screen.getByRole("tab", { name: "微信扫码" }));
    await screen.findByRole("img", { name: "微信授权登录二维码" });
    expect(startWechatLogin).toHaveBeenCalledWith({ tenant_hint: "demo", remember_device: false, public_device: false });
    fireEvent.click(screen.getByRole("button", { name: "更改登录选项" }));
    expect(screen.queryByRole("img", { name: "微信授权登录二维码" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "公共电脑登录" }));
    fireEvent.click(screen.getByRole("button", { name: "生成微信登录二维码" }));
    await waitFor(() => expect(startWechatLogin).toHaveBeenCalledTimes(2));
    expect(startWechatLogin).toHaveBeenLastCalledWith(expect.objectContaining({ tenant_hint: "demo", remember_device: false, public_device: true }));
  });
  it("submits a username through the compatible identifier protocol", async () => {
    const login = vi.fn();
    render(<LoginPage onLogin={login} onWechatAuthenticate={authenticate} />);
    expect(screen.queryByRole("radio")).toBeNull();
    expect((screen.getByRole("checkbox", { name: "保持登录" }) as HTMLInputElement).checked).toBe(false);
    fireEvent.change(screen.getByLabelText("学校代码"), { target: { value: "demo" } });
    fireEvent.change(screen.getByLabelText("账号"), { target: { value: "demo_school_admin" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "existing-password" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(login).toHaveBeenCalledWith({
      tenant_code: "demo", identifier: "demo_school_admin", password: "existing-password",
      tenant_hint: undefined, remember_device: false, public_device: false
    }));
    expect(screen.getByPlaceholderText("学号、用户名、教职工号或手机号").getAttribute("autocomplete")).toBe("username");
  });

  it("asks for a username and retains WeChat without advertising an unconfigured SMS service", async () => {
    const login = vi.fn();
    render(<LoginPage onLogin={login} onWechatAuthenticate={authenticate} />);
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    expect(await screen.findByText("请输入账号", {}, { timeout: 5000 })).toBeTruthy();
    expect(login).not.toHaveBeenCalled();
    expect(screen.getByRole("tab", { name: "账号密码" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.queryByRole("tab", { name: /手机|短信/ })).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "微信扫码" }));
    expect(screen.getByRole("button", { name: "生成微信登录二维码" })).toBeTruthy();
    expect(screen.queryByText("微信登录会读取什么？")).toBeNull();
    expect(screen.queryByText("不会读取微信好友或聊天信息。")).toBeNull();
  });

  it("keeps persistent login opt-in and clears it when entering or leaving public mode", async () => {
    const login = vi.fn();
    render(<LoginPage onLogin={login} onWechatAuthenticate={authenticate} />);
    fireEvent.change(screen.getByLabelText("学校代码"), { target: { value: "demo" } });
    fireEvent.change(screen.getByLabelText("账号"), { target: { value: "teacher" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "existing-password" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "保持登录" }));
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(login).toHaveBeenLastCalledWith(expect.objectContaining({ remember_device: true, public_device: false })));
    fireEvent.click(screen.getByRole("button", { name: "公共电脑登录" }));
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.getByText("公共电脑模式已启用，离开前请退出登录。")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(login).toHaveBeenLastCalledWith(expect.objectContaining({ remember_device: false, public_device: true })));
    fireEvent.click(screen.getByRole("button", { name: "切回个人电脑" }));
    expect((screen.getByRole("checkbox", { name: "保持登录" }) as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(login).toHaveBeenLastCalledWith(expect.objectContaining({ remember_device: false, public_device: false })));
  });

  it("freezes device policy while creating a challenge, then uses that policy for the displayed QR", async () => {
    const pending = deferred<WechatLoginChallengeResponse>();
    vi.mocked(startWechatLogin).mockReturnValue(pending.promise);
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={authenticate} />);
    fireEvent.click(screen.getByRole("tab", { name: "微信扫码" }));
    fireEvent.change(screen.getByLabelText("学校代码"), { target: { value: "demo" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "保持登录" }));
    fireEvent.click(screen.getByRole("button", { name: "生成微信登录二维码" }));
    await waitFor(() => expect(startWechatLogin).toHaveBeenCalledWith(expect.objectContaining({ remember_device: true, public_device: false })));
    expect((screen.getByRole("button", { name: "公共电脑登录" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("checkbox", { name: "保持登录" }) as HTMLInputElement).disabled).toBe(true);
    await act(async () => { pending.resolve(challenge); });
    expect(screen.getByRole("img", { name: "微信授权登录二维码" })).toBeTruthy();
    expect(screen.queryByText(/公共电脑会话最长/)).toBeNull();
  });

  it("drops a challenge created for a login method the user has left", async () => {
    const pending = deferred<WechatLoginChallengeResponse>();
    vi.mocked(startWechatLogin).mockReturnValue(pending.promise);
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={authenticate} />);
    await generateWechat();
    fireEvent.click(screen.getByRole("tab", { name: "账号密码" }));
    await act(async () => { pending.resolve(challenge); });
    fireEvent.click(screen.getByRole("tab", { name: "微信扫码" }));
    expect(screen.queryByRole("img", { name: "微信授权登录二维码" })).toBeNull();
    expect(screen.getByRole("button", { name: "生成微信登录二维码" })).toBeTruthy();
  });

  it("queues the actual poll and ignores its late response after switching authentication method", async () => {
    vi.mocked(startWechatLogin).mockResolvedValue(challenge);
    const pending = deferred<Awaited<ReturnType<typeof pollWechatLogin>>>();
    vi.mocked(pollWechatLogin).mockReturnValue(pending.promise);
    const done = vi.fn();
    const queue = vi.fn(async (action: () => Promise<LoginResponse | undefined>) => { done(await action()); });
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={queue} />);
    await generateWechat();
    await screen.findByRole("img", { name: "微信授权登录二维码" });
    await waitFor(() => expect(queue).toHaveBeenCalledOnce(), { timeout: 2500 });
    expect(pollWechatLogin).toHaveBeenCalledWith({ challenge_id: challenge.challenge_id, poll_token: challenge.poll_token });
    fireEvent.click(screen.getByRole("tab", { name: "账号密码" }));
    expect(done).not.toHaveBeenCalled();
    await act(async () => { pending.resolve({ status: "authenticated", user: { id: "old-wechat" } as AuthUser, expires_at: challenge.expires_at }); });
    expect(done).toHaveBeenCalledWith(undefined);
    expect(screen.getByRole("tab", { name: "账号密码" }).getAttribute("aria-selected")).toBe("true");
  });

  it("recovers a consumed challenge inside the same queued authentication action", async () => {
    vi.mocked(startWechatLogin).mockResolvedValue(challenge);
    vi.mocked(pollWechatLogin).mockResolvedValue({ status: "consumed", expires_at: challenge.expires_at });
    vi.mocked(getCurrentUser).mockResolvedValue({ user: { id: "recovered" } as AuthUser });
    const done = vi.fn();
    const queue = vi.fn(async (action: () => Promise<LoginResponse | undefined>) => { done(await action()); });
    render(<LoginPage onLogin={vi.fn()} onWechatAuthenticate={queue} />);
    await generateWechat();
    await screen.findByRole("img", { name: "微信授权登录二维码" });
    await waitFor(() => expect(done).toHaveBeenCalledWith({ user: { id: "recovered" }, expires_at: challenge.expires_at }), { timeout: 2500 });
    expect(queue).toHaveBeenCalledOnce();
  });
});
