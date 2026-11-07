// @vitest-environment jsdom
import { StrictMode, type ReactNode } from "react";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSummary } from "../api/dashboard";
import type { SessionUser } from "../auth/session";
import { DashboardPage } from "./DashboardPage";

const api = vi.hoisted(() => ({ summary: vi.fn() }));
vi.mock("../api/dashboard", () => ({ getDashboardSummary: api.summary }));
vi.mock("../features/onboarding/queries", () => ({ useOnboardingReadiness: () => ({ data: undefined }) }));
vi.mock("../features/onboarding/ReadinessBanner", () => ({ ReadinessBanner: () => null }));
vi.mock("../features/dashboard/SchoolDashboard", () => ({ SchoolDashboard: ({ reviewTeachers, reviewTeacherSummary }: { reviewTeachers?: Array<{ display_name: string }>; reviewTeacherSummary?: { online_count: number } }) => <div>{reviewTeachers?.[0]?.display_name}{reviewTeacherSummary ? <span>在线 {reviewTeacherSummary.online_count}</span> : null}</div> }));
vi.mock("../components/PageState", () => ({
  LoadingState: () => <p>读取中</p>, ErrorState: ({ message }: { message: string }) => <p>{message}</p>, EmptyState: () => <p>暂无数据</p>
}));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("framer-motion", () => ({ motion: { section: ({ children }: { children: ReactNode }) => <section>{children}</section> } }));
vi.mock("antd", () => ({
  Alert: ({ message }: { message: string }) => <p>{message}</p>, Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button type="button" onClick={onClick}>{children}</button>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div>
}));
const user = { id: "admin", tenant: "school", school: "示例学校", roles: ["school_admin"], permissions: [] } as unknown as SessionUser;
function summary(name = "王老师"): DashboardSummary {
  return { updated_at: "2026-10-30T00:00:00Z", statistics: {}, organization_statistics: {}, blocking_issues: [], active_exams: [], recent_activities: [], warnings: [], review_teachers: [{ display_name: name }], review_teacher_summary: { online_count: 1 } } as unknown as DashboardSummary;
}
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { resolve, promise }; }
beforeEach(() => { vi.clearAllMocks(); vi.useFakeTimers(); api.summary.mockResolvedValue(summary()); Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" }); });
afterEach(() => { cleanup(); vi.useRealTimers(); });
describe("工作台在线教师刷新边界", () => {
  it("当前可见页30秒刷新，隐藏停止请求，返回可见立即刷新，离开页面停止", async () => {
    const page = render(<DashboardPage user={user} onNavigate={vi.fn()} />);
    await act(async () => {});
    expect(screen.getByText("王老师")).toBeTruthy();
    expect(screen.getByText("在线 1")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(api.summary).toHaveBeenCalledTimes(2);
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(api.summary).toHaveBeenCalledTimes(2);
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(api.summary).toHaveBeenCalledTimes(3);
    page.unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(api.summary).toHaveBeenCalledTimes(3);
  });
  it("切换身份后旧学校响应不能覆盖新学校教师", async () => {
    const old = deferred<DashboardSummary>();
    api.summary.mockReturnValueOnce(old.promise).mockResolvedValueOnce(summary("新学校老师"));
    const page = render(<DashboardPage user={user} onNavigate={vi.fn()} />);
    page.rerender(<DashboardPage user={{ ...user, id: "next-admin", tenant: "next-school" }} onNavigate={vi.fn()} />);
    await act(async () => {});
    expect(screen.getByText("新学校老师")).toBeTruthy();
    await act(async () => { old.resolve(summary("旧学校老师")); });
    expect(screen.queryByText("旧学校老师")).toBeNull();
    expect(screen.getByText("新学校老师")).toBeTruthy();
  });

  it("同账号学校授权变化也清除旧工作台，迟到的校外统计不回填", async () => {
    const old = deferred<DashboardSummary>();
    api.summary.mockReturnValueOnce(old.promise).mockResolvedValueOnce(summary("本校老师"));
    const scope = { resolved: true, tenantWide: false, schoolIds: ["local", "foreign"], gradeIds: [], classIds: [] };
    const page = render(<DashboardPage user={{ ...user, organizationScope: scope }} onNavigate={vi.fn()} />);
    page.rerender(<DashboardPage user={{ ...user, organizationScope: { ...scope, schoolIds: ["local"] } }} onNavigate={vi.fn()} />);
    await act(async () => {});
    expect(screen.getByText("本校老师")).toBeTruthy();
    await act(async () => { old.resolve(summary("外校老师")); });
    expect(screen.queryByText("外校老师")).toBeNull();
    expect(screen.getByText("本校老师")).toBeTruthy();
  });
  it("机构管理员不显示学校教师看板，也不启用在线轮询", async () => {
    render(<DashboardPage user={{ ...user, roles: ["tenant_admin"] }} onNavigate={vi.fn()} />);
    await act(async () => {});
    expect(screen.queryByText("王老师")).toBeNull();
    expect(screen.queryByText("在线 1")).toBeNull();
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(api.summary).toHaveBeenCalledTimes(1);
  });
  it("严格模式重新挂载仍能读取结果，刷新进行中不重叠发起定时请求", async () => {
    const pending = deferred<DashboardSummary>();
    api.summary.mockReturnValue(pending.promise);
    render(<StrictMode><DashboardPage user={user} onNavigate={vi.fn()} /></StrictMode>);
    expect(api.summary).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(api.summary).toHaveBeenCalledTimes(2);
    await act(async () => { pending.resolve(summary()); });
    expect(screen.getByText("王老师")).toBeTruthy();
  });
});
