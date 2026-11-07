import { render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { LoginRedirect } from "./SessionScreens";
import { leaveForUnifiedLogin } from "../../loginEntry";

beforeEach(() => vi.clearAllMocks());

it("旧学生入口自动前往统一登录，保留学校和考试深链接，不再显示第二套表单", () => {
  window.history.replaceState({}, "", "/student/exams/exam-1?school=S053");
  render(<LoginRedirect />);
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(screen.queryByRole("button", { name: /登录/ })).toBeNull();
  const target = new URL(vi.mocked(leaveForUnifiedLogin).mock.calls[0][0]);
  expect(target.pathname).toBe("/");
  expect(target.searchParams.get("tenant")).toBe("S053");
  expect(target.searchParams.get("return_to")).toBe("/student/exams/exam-1?school=S053");
});

it("改密或重新登录说明保留为状态输出，自动跳转不要求再点击进入", () => {
  render(<LoginRedirect notice="密码已更新，请使用新密码重新登录。" />);
  expect(screen.getByText("密码已更新，请使用新密码重新登录。").tagName).toBe("OUTPUT");
  expect(screen.getByText("正在前往登录…")).toBeTruthy();
  expect(leaveForUnifiedLogin).toHaveBeenCalledOnce();
});
