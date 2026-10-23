// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LoginPage } from "./LoginPage";

vi.mock("../api/auth", () => ({ getCurrentUser: vi.fn(), pollWechatLogin: vi.fn(), startWechatLogin: vi.fn() }));

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
  window.history.replaceState({}, "", "/");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("login identity fields", () => {
  it("submits a username through the compatible identifier protocol", async () => {
    const login = vi.fn();
    render(<LoginPage onLogin={login} onWechatLogin={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("学校代码"), { target: { value: "demo" } });
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "demo_school_admin" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "existing-password" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(login).toHaveBeenCalledWith({
      tenant_code: "demo", identifier: "demo_school_admin", password: "existing-password",
      tenant_hint: undefined, remember_device: false, public_device: false
    }));
    expect(screen.getByPlaceholderText("请输入用户名").getAttribute("autocomplete")).toBe("username");
  });

  it("asks for a username and retains WeChat without advertising an unconfigured SMS service", async () => {
    const login = vi.fn();
    render(<LoginPage onLogin={login} onWechatLogin={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    expect(await screen.findByText("请输入用户名")).toBeTruthy();
    expect(login).not.toHaveBeenCalled();
    expect(screen.getByRole("tab", { name: "用户名密码" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.queryByRole("tab", { name: /手机|短信/ })).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "微信扫码" }));
    expect(screen.getByRole("button", { name: "生成微信登录二维码" })).toBeTruthy();
  });
});
