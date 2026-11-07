// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../auth/session";
import { AccountProfilePage } from "./AccountProfilePage";

const mocks = vi.hoisted(() => ({ load: vi.fn(), navigate: vi.fn(), logout: vi.fn(), security: vi.fn() }));
vi.mock("../api/auth", () => ({ getAccountProfile: mocks.load }));
vi.mock("./SessionManagementPage", () => ({ SessionManagementPage: (props: { onLoggedOut: () => void; embedded: boolean; refreshVersion: number; minimumPasswordLength: number }) => {
  mocks.security(props);
  return <section aria-label="账户安全"><h2>修改密码</h2></section>;
} }));
vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button type="button" onClick={onClick}>{children}</button>,
  Alert: ({ message, description }: { message: string; description: string }) => <div role="status"><strong>{message}</strong><p>{description}</p></div>
}));
vi.mock("../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <div>{label}</div>,
  ErrorState: ({ message, onRetry }: { message: string; onRetry: () => void }) => <div role="alert">{message}<button type="button" onClick={onRetry}>重试</button></div>
}));
const teacher: SessionUser = {
  id: "self", username: "grader-self", displayName: "本人教师", name: "本人教师", role: "grader", roles: ["grader"],
  tenant: "self-school", school: "本校", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-self"], gradeIds: [], classIds: [] }
};
const profile = { id: "self", username: "grader-self", display_name: "本人教师", phone_masked: "138****8000", employee_no: "T-001", school_id: "school-self", teaching_subjects: ["mathematics", "physics"], roles: ["grader"] };
beforeEach(() => { vi.clearAllMocks(); mocks.load.mockResolvedValue({ profile }); });
afterEach(cleanup);

describe("个人信息和教师学科入口", () => {
  it("教师查看本人任教学科并明确管理员维护，不提供自行授权或班级绑定", async () => {
    const view = render(<AccountProfilePage currentUser={teacher} onNavigate={mocks.navigate} onLoggedOut={mocks.logout} />);
    await screen.findByText("数学、物理");
    expect(screen.getByText("任教学科由学校管理员在“人员与权限”中维护；如需调整，请联系管理员。")).toBeTruthy();
    expect(screen.getByText("138****8000")).toBeTruthy();
    expect(view.container.querySelector("input,select,textarea")).toBeNull();
    expect(screen.queryByText("管理阅卷教师学科")).toBeNull();
    expect(view.container.textContent).not.toContain("班级");
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("个人信息与账户安全");
    expect(screen.getByRole("region", { name: "账户安全" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "账户安全" })).toBeNull();
    expect(mocks.security).toHaveBeenLastCalledWith(expect.objectContaining({ embedded: true, onLoggedOut: mocks.logout, minimumPasswordLength: 8 }));
  });
  it("未配置学科时说明真实处理方式", async () => {
    mocks.load.mockResolvedValue({ profile: { ...profile, teaching_subjects: [] } });
    render(<AccountProfilePage currentUser={teacher} onNavigate={mocks.navigate} onLoggedOut={mocks.logout} />);
    await screen.findByText("未设置");
    expect(screen.getByText("尚未设置任教学科，请联系学校管理员补充后再分配阅卷任务。")).toBeTruthy();
  });
  it("学校管理员兼任教师也不强制学科，并可进入本校人员维护", async () => {
    render(<AccountProfilePage currentUser={{ ...teacher, roles: ["school_admin", "grader"], permissions: ["org:manage"] }} onNavigate={mocks.navigate} onLoggedOut={mocks.logout} />);
    await screen.findByText("本人教师");
    expect(screen.queryByRole("region", { name: "本人任教学科" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "管理阅卷教师学科" }));
    expect(mocks.navigate).toHaveBeenCalledWith("/members/teachers");
    expect(mocks.security).toHaveBeenLastCalledWith(expect.objectContaining({ minimumPasswordLength: 12 }));
  });
  it("平台账号不会出现无权访问的学校人员入口", async () => {
    render(<AccountProfilePage currentUser={{ ...teacher, roles: ["platform_admin"], permissions: ["org:manage"] }} onNavigate={mocks.navigate} onLoggedOut={mocks.logout} />);
    await screen.findByText("本人教师");
    expect(screen.queryByRole("button", { name: "管理阅卷教师学科" })).toBeNull();
    expect(screen.queryByRole("region", { name: "本人任教学科" })).toBeNull();
  });
  it("资料读取失败可以重试，不将缺失资料显示为未设置学科", async () => {
    mocks.load.mockRejectedValueOnce(new Error("unavailable"));
    render(<AccountProfilePage currentUser={teacher} onNavigate={mocks.navigate} onLoggedOut={mocks.logout} />);
    await screen.findByRole("alert");
    expect(screen.queryByText("未设置")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(2));
    await screen.findByText("数学、物理");
  });
  it("统一刷新同时刷新资料和同页安全区域，不重新跳转", async () => {
    render(<AccountProfilePage currentUser={teacher} onNavigate={mocks.navigate} onLoggedOut={mocks.logout} />);
    await screen.findByText("数学、物理");
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(2));
    expect(mocks.security).toHaveBeenLastCalledWith(expect.objectContaining({ refreshVersion: 1 }));
    expect(mocks.navigate).not.toHaveBeenCalled();
  });
});
