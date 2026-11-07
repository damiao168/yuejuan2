// @vitest-environment jsdom
import { App } from "antd";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../auth/session";
import { AccountProfilePage } from "./AccountProfilePage";
import { SessionManagementPage } from "./SessionManagementPage";

const mocks = vi.hoisted(() => ({ profile: vi.fn(), sessions: vi.fn(), events: vi.fn(), password: vi.fn(), revoke: vi.fn(), all: vi.fn() }));
vi.mock("../api/auth", () => ({
  getAccountProfile: mocks.profile, listSessions: mocks.sessions, listSecurityEvents: mocks.events,
  changePassword: mocks.password, revokeSession: mocks.revoke, logoutAll: mocks.all,
}));
const teacher: SessionUser = {
  id: "self", username: "grader-self", displayName: "本人教师", name: "本人教师", role: "grader", roles: ["grader"],
  tenant: "self-school", school: "本校", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-self"], gradeIds: [], classIds: [] },
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
  const computedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation(element => computedStyle(element));
  mocks.profile.mockResolvedValue({ profile: { id: "self", username: "grader-self", display_name: "本人教师", phone_masked: "138****8000", employee_no: "T-001", school_id: "school-self", teaching_subjects: ["mathematics"], roles: ["grader"] } });
  mocks.sessions.mockResolvedValue({ sessions: [] });
  mocks.events.mockResolvedValue({ events: [] });
  mocks.password.mockResolvedValue({});
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("unified account settings", () => {
  it("shows profile, teaching subjects and password controls on one page with a single heading", async () => {
    const view = render(<App><AccountProfilePage currentUser={teacher} onNavigate={vi.fn()} onLoggedOut={vi.fn()} /></App>);
    await screen.findByText("数学");
    await screen.findByRole("button", { name: "更新密码" });
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("个人信息与账户安全");
    expect(view.container.querySelectorAll('input[type="password"]')).toHaveLength(3);
    expect(screen.getAllByRole("button", { name: "刷新" })).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    await waitFor(() => expect(mocks.profile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(mocks.sessions).toHaveBeenCalledTimes(2));
    expect(mocks.events).toHaveBeenCalledTimes(2);
  });

  it("keeps same-page password changes and the logout callback wired to the original API", async () => {
    const logout = vi.fn();
    render(<App><AccountProfilePage currentUser={teacher} onNavigate={vi.fn()} onLoggedOut={logout} /></App>);
    await screen.findByRole("button", { name: "更新密码" });
    fireEvent.change(screen.getByLabelText("当前密码"), { target: { value: "synthetic-current-password" } });
    fireEvent.change(screen.getByLabelText("新密码", { exact: true }), { target: { value: "synthetic-next-password" } });
    fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: "synthetic-next-password" } });
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    await waitFor(() => expect(mocks.password).toHaveBeenCalledWith({ current_password: "synthetic-current-password", new_password: "synthetic-next-password" }));
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("ignores a superseded security refresh and keeps the newest device result", async () => {
    let resolveFirst!: (value: unknown) => void;
    mocks.sessions.mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve; }));
    mocks.sessions.mockResolvedValueOnce({ sessions: [{ id: "new", device_name: "最新设备", current: true, session_type: "browser", last_seen_at: "2026-11-02T00:00:00Z", expires_at: "2026-11-03T00:00:00Z" }] });
    const view = render(<App><SessionManagementPage embedded refreshVersion={0} onLoggedOut={vi.fn()} /></App>);
    view.rerender(<App><SessionManagementPage embedded refreshVersion={1} onLoggedOut={vi.fn()} /></App>);
    fireEvent.click(await screen.findByRole("button", { name: "展开安全活动" }));
    await screen.findByText("最新设备");
    await act(async () => resolveFirst({ sessions: [{ id: "old", device_name: "过期设备", current: false, session_type: "browser", last_seen_at: "2026-11-01T00:00:00Z", expires_at: "2026-11-03T00:00:00Z" }] }));
    await waitFor(() => expect(screen.queryByText("过期设备")).toBeNull());
    expect(screen.getByText("最新设备")).toBeTruthy();
  });
});
