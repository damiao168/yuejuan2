// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../api/client";
import { FirstPasswordChange } from "./FirstPasswordChange";

afterEach(cleanup);
beforeEach(() => { window.matchMedia = vi.fn().mockReturnValue({ matches: false, addListener: vi.fn(), removeListener: vi.fn() }); });
describe("首次改密表单", () => {
  it("确认不一致不能提交，核对后只发送当前和新密码", async () => {
    const change = vi.fn().mockResolvedValue(undefined);
    render(<FirstPasswordChange onChangePassword={change} onLogout={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("当前初始密码"), { target: { value: "InitialPass123!" } });
    fireEvent.change(screen.getByLabelText("新密码"), { target: { value: " PersonalPass123! " } });
    fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: "different" } });
    fireEvent.click(screen.getByRole("button", { name: "修改密码并重新登录" }));
    await screen.findByText("两次输入的密码不一致"); expect(change).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: " PersonalPass123! " } });
    fireEvent.click(screen.getByRole("button", { name: "修改密码并重新登录" }));
    await waitFor(() => expect(change).toHaveBeenCalledWith({ current_password: "InitialPass123!", new_password: " PersonalPass123! " }));
  });
  it("当前初始密码错误时显示中文并保留输入供修正", async () => {
    render(<FirstPasswordChange onChangePassword={vi.fn().mockRejectedValue(new ApiClientError(401, "current_password_invalid", "invalid"))} onLogout={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("当前初始密码"), { target: { value: "wrong-initial" } });
    for (const name of ["新密码", "确认新密码"]) fireEvent.change(screen.getByLabelText(name), { target: { value: "PersonalPass123!" } });
    fireEvent.click(screen.getByRole("button", { name: "修改密码并重新登录" }));
    await screen.findByText("当前密码不正确，请重新输入。");
    expect((screen.getByLabelText("新密码") as HTMLInputElement).value).toBe("PersonalPass123!");
  });
});
