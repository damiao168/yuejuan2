import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from "./App";
import { studentUser } from "./test/studentAppBatch8Fixtures";

beforeEach(() => { window.localStorage.clear(); window.sessionStorage.clear(); window.history.replaceState(null, "", "/#/exams/forged-exam"); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

it("初始密码会话不预读成绩；确认错误不发请求，改密成功退出到登录", async () => {
  let fail = true;
  const fetch = vi.fn(async (url: string, init: RequestInit = {}) => {
    const path = new URL(url, "http://local").pathname;
    if (path === "/api/v1/auth/me") return json({ user: { ...studentUser, must_change_password: true } });
    if (path === "/api/v1/auth/password") return fail ? json({ error: { code: "current_password_invalid" } }, 401) : json({ status: "password_changed", revoked_count: 1 });
    throw new Error(`unexpected business read ${path} ${init.method}`);
  });
  vi.stubGlobal("fetch", fetch); render(<App />);
  await screen.findByText("首次登录请修改密码");
  expect(fetch.mock.calls.map(([url]) => url)).toEqual(["/api/v1/auth/me"]);
  fireEvent.change(screen.getByLabelText("当前初始密码"), { target: { value: "InitialPass123!" } });
  fireEvent.change(screen.getByLabelText("新密码"), { target: { value: " PersonalPass123! " } });
  fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: "different" } });
  fireEvent.click(screen.getByRole("button", { name: "修改密码并重新登录" }));
  expect(await screen.findByRole("alert")).toHaveProperty("textContent", "两次输入的密码不一致。");
  expect(fetch).toHaveBeenCalledTimes(1);
  fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: " PersonalPass123! " } });
  fireEvent.click(screen.getByRole("button", { name: "修改密码并重新登录" }));
  await screen.findByText("当前初始密码不正确，请重新输入。");
  expect(screen.queryByText("正在前往登录…")).toBeNull();
  fail = false; fireEvent.click(screen.getByRole("button", { name: "修改密码并重新登录" }));
  await screen.findByText("密码已更新，请使用新密码重新登录。");
  const last = fetch.mock.calls[fetch.mock.calls.length - 1];
  expect(JSON.parse(last[1]!.body as string)).toEqual({ current_password: "InitialPass123!", new_password: " PersonalPass123! " });
  expect(screen.queryByText("首次登录请修改密码")).toBeNull();
});

it("首次改密页面仍可安全退出账号", async () => {
  const fetch = vi.fn(async (url: string) => url.endsWith("/me") ? json({ user: { ...studentUser, must_change_password: true } }) : json({ status: "logged_out" }));
  vi.stubGlobal("fetch", fetch); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "退出登录" }));
  await waitFor(() => expect(screen.queryByText("首次登录请修改密码")).toBeNull());
  expect(fetch.mock.calls.some(([url]) => url === "/api/v1/auth/logout")).toBe(true);
});
