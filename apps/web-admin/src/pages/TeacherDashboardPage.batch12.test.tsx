// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArbitrationTask, ReviewTask } from "../api/review";
import type { SessionUser } from "../auth/session";
import { TeacherDashboardPage } from "./TeacherDashboardPage";

const mocks = vi.hoisted(() => ({ reviews: vi.fn(), arbitrations: vi.fn(), navigate: vi.fn() }));
vi.mock("../api/review", () => ({ listReviewTasks: mocks.reviews, listArbitrationTasks: mocks.arbitrations }));
vi.mock("../api/client", () => ({
  getSafeUserText: (text: string) => text,
  getUserErrorMessage: (_error: unknown, fallback: string) => fallback,
}));
vi.mock("../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <p>{label}</p>,
  ErrorState: ({ message }: { message: string }) => <p>{message}</p>,
  EmptyState: ({ title, description }: { title: string; description: string }) => <div data-empty><strong>{title}</strong><p>{description}</p></div>,
}));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
// 只简化控件外观，页面的权限、异步读取和导航回调仍由实际组件执行。
vi.mock("antd", () => ({
  Button: ({ children, onClick, loading }: { children: ReactNode; onClick?: () => void; loading?: boolean }) => <button type="button" disabled={loading} onClick={onClick}>{children}</button>,
  Alert: ({ message, description }: { message: string; description?: string }) => <aside>{message}{description}</aside>,
}));

const user: SessionUser = {
  id: "self", username: "teacher", displayName: "教师", name: "教师", role: "teacher", roles: ["teacher"],
  tenant: "tenant-a", school: "学校甲", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-a"], gradeIds: [], classIds: [] },
};
function review(id: string, status: string): ReviewTask {
  return { id, status, question_no: id, anonymous_code: id, source: "returned" } as ReviewTask;
}
function arbitration(id: string, status: string): ArbitrationTask {
  return { id, status, question_no: id, anonymous_code: id, score_difference: 2 } as ArbitrationTask;
}

describe("教师工作台展示分支", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.reviews.mockResolvedValue({ tasks: [] });
    mocks.arbitrations.mockResolvedValue({ arbitration_tasks: [] });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
    vi.restoreAllMocks();
  });
  async function render(roles = user.roles, permissions = user.permissions) {
    await act(async () => { root.render(<TeacherDashboardPage user={{ ...user, roles, permissions }} onNavigate={mocks.navigate} />); });
  }
  function counts() {
    return [...host.querySelectorAll(".teacher-summary strong")].map((item) => item.textContent);
  }

  it.each([
    { roles: ["teacher", "grader", "arbitrator"], title: "我的工作" },
    { roles: ["grader", "arbitrator"], title: "我的阅卷" },
    { roles: ["arbitrator"], title: "我的仲裁" },
  ])("角色 $roles 保留标题优先级，无工作权限时隐藏摘要和空任务提示", async ({ roles, title }) => {
    await render(roles);
    expect(host.querySelector("h1")?.textContent).toBe(title);
    expect(host.querySelector(".teacher-summary")).toBeNull();
    expect(host.querySelector("[data-empty]")).toBeNull();
    expect(mocks.reviews).not.toHaveBeenCalled();
    expect(mocks.arbitrations).not.toHaveBeenCalled();
  });

  it("摘要使用已加载状态计数，先展示仲裁四项再展示阅卷四项并保持导航编码", async () => {
    const reviewStatuses = ["assigned", "in_progress", "returned", "assigned", "in_progress", "returned", "submitted", "completed", "unknown"];
    const arbitrationStatuses = ["assigned", "in_progress", "pending", "assigned", "pending", "completed", "unknown"];
    mocks.reviews.mockResolvedValue({ tasks: reviewStatuses.map((status, index) => review(`r/${index + 1}`, status)) });
    mocks.arbitrations.mockResolvedValue({ arbitration_tasks: arbitrationStatuses.map((status, index) => arbitration(`a/${index + 1}`, status)) });
    await render(user.roles, ["review:work", "arbitration:work"]);
    expect(mocks.reviews).toHaveBeenCalledWith({ assigned_to: user.id });
    expect(mocks.arbitrations).toHaveBeenCalledWith({ assigned_to: user.id });
    expect(counts()).toEqual(["6", "5", "2"]);
    const buttons = [...host.querySelectorAll<HTMLButtonElement>(".teacher-task-list button")];
    expect(buttons.map((button) => button.querySelector("strong")?.textContent)).toEqual([
      "第 a/1 题仲裁", "第 a/2 题仲裁", "第 a/3 题仲裁", "第 a/4 题仲裁",
      "第 r/1 题阅卷", "第 r/2 题阅卷", "第 r/3 题阅卷", "第 r/4 题阅卷",
    ]);
    await act(async () => { buttons[0].click(); buttons[4].click(); });
    expect(mocks.navigate.mock.calls).toEqual([["/arbitration?task=a%2F1"], ["/grading?task=r%2F1"]]);
  });

  it.each([
    { examName: "高一期中", examId: "raw-exam-id", subject: "english", source: "returned", expected: "高一期中 · 英语 · 密号 r1 · 退回重评" },
    { examName: "", examId: "exam-123456", subject: "", source: "unknown", expected: "考试 exam-123 · 学科未标注 · 密号 r1" },
    { examName: "", examId: "", subject: "mathematics", source: "double_mark_required", expected: "考试名称未提供 · 数学 · 密号 r1 · 双评任务" },
    { examName: "  原始名称  ", examId: "exam-123456", subject: "unknown", source: "unknown", expected: "  原始名称   · 其他学科 · 密号 r1" },
  ])("阅卷任务摘要保留名称、缺项和来源展示：$expected", async ({ examName, examId, subject, source, expected }) => {
    mocks.reviews.mockResolvedValue({ tasks: [{ ...review("r1", "assigned"), exam_name: examName, exam_id: examId, subject, source }] });
    await render(user.roles, ["review:work"]);
    expect(host.querySelector(".teacher-task-list small")?.textContent).toBe(expected);
    expect(mocks.reviews).toHaveBeenCalledExactlyOnceWith({ assigned_to: user.id });
    expect(mocks.arbitrations).not.toHaveBeenCalled();
  });

  it.each([
    { failed: "review", warning: "阅卷任务暂时不可用", expected: ["—", "1", "—"], taskTitle: "第 a1 题仲裁" },
    { failed: "arbitration", warning: "仲裁任务暂时不可用", expected: ["1", "—", "0"], taskTitle: "第 r1 题阅卷" },
  ])("$failed 读取失败只降级对应摘要，另一类已加载任务仍可展示", async ({ failed, warning, expected, taskTitle }) => {
    mocks.reviews.mockResolvedValue({ tasks: [review("r1", "returned")] });
    mocks.arbitrations.mockResolvedValue({ arbitration_tasks: [arbitration("a1", "pending")] });
    if (failed === "review") mocks.reviews.mockRejectedValue(new Error("读取失败"));
    else mocks.arbitrations.mockRejectedValue(new Error("读取失败"));
    await render(user.roles, ["review:manage", "arbitration:manage"]);
    expect(counts()).toEqual(expected);
    expect(host.querySelector("aside")?.textContent).toBe(warning);
    expect(host.querySelector(".teacher-task-list strong")?.textContent).toBe(taskTitle);
    expect(host.querySelector("[data-empty]")).toBeNull();
  });

  it.each([
    { permission: "review:work", expected: ["0", "0"], label: "已加载待阅卷" },
    { permission: "arbitration:work", expected: ["0"], label: "已加载待仲裁" },
  ])("仅有 $permission 时显示对应摘要及空任务提示", async ({ permission, expected, label }) => {
    await render(user.roles, [permission]);
    expect(counts()).toEqual(expected);
    expect(host.querySelector(".teacher-summary span")?.textContent).toBe(label);
    expect(host.querySelector("[data-empty] strong")?.textContent).toBe("当前没有已加载的待办");
    if (permission === "review:work") {
      expect(mocks.reviews).toHaveBeenCalledWith({ assigned_to: user.id });
      expect(mocks.arbitrations).not.toHaveBeenCalled();
    } else {
      expect(mocks.arbitrations).toHaveBeenCalledWith({ assigned_to: user.id });
      expect(mocks.reviews).not.toHaveBeenCalled();
    }
  });
});
