// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DashboardReviewTeacher, DashboardReviewTeacherSummary } from "../../api/dashboard";
import { ReviewTeacherProgress } from "./ReviewTeacherProgress";

vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button type="button" onClick={onClick}>{children}</button>,
  Select: ({ value, options, onChange, "aria-label": label }: { value?: string; options: Array<{ value: string; label: string }>; onChange: (value: string) => void; "aria-label": string }) => <select aria-label={label} value={value || ""} onChange={event => onChange(event.target.value)}><option value="">全部学科</option>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
  Segmented: ({ options, value, onChange, "aria-label": label }: { options: Array<{ label: string; value: string }>; value: string; onChange: (value: string) => void; "aria-label": string }) => <div role="group" aria-label={label}>{options.map(option => <button key={option.value} type="button" aria-pressed={option.value === value} onClick={() => onChange(option.value)}>{option.label}</button>)}</div>
}));
afterEach(cleanup);
const summary: DashboardReviewTeacherSummary = { teacher_count: 2, online_count: 1, online_window_seconds: 300, task_counting_note: "每位教师的小题任务各计一项，排除已发布及已归档考试。", online_note: "近 5 分钟有活动且会话未退出、未锁定、未过期。" };
const teachers: DashboardReviewTeacher[] = [
  { user_id: "english", username: "english_teacher", display_name: "李老师", teaching_subjects: ["english"], assigned_count: 0, in_progress_count: 0, returned_count: 0, submitted_count: 0, remaining_count: 0, is_online: false },
  { user_id: "math", username: "math_teacher", display_name: "王老师", teaching_subjects: ["math", "physics"], assigned_count: 2, in_progress_count: 2, returned_count: 1, submitted_count: 3, remaining_count: 5, is_online: true }
];
describe("工作台教师阅卷进度", () => {
  it("待阅包含退回，总任务不重复累计进行中；没有任务的教师默认隐藏", () => {
    render(<ReviewTeacherProgress teachers={teachers} summary={summary} />);
    expect(screen.getByRole("heading", { level: 3, name: "按老师" })).toBeTruthy();
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("王老师");
    expect(rows[0].textContent).toContain("数学、物理");
    expect(rows[0].textContent).toContain("3 / 8");
    expect(rows[0].textContent).toContain("38%");
    const cells = within(rows[0]).getAllByRole("cell");
    expect(cells[4].textContent).toBe("3");
    expect(cells[4].getAttribute("title")).toContain("退回待阅 1 项");
    expect(cells[5].textContent).toBe("2");
    expect(screen.getByText(/近 5 分钟有活动且会话有效/)).toBeTruthy();
    expect(screen.queryByText("李老师")).toBeNull();
  });
  it("可切换显示没有任务的教师，按钮人数与多出的行数一致", () => {
    render(<ReviewTeacherProgress teachers={teachers} summary={summary} />);
    fireEvent.click(screen.getByRole("button", { name: "显示无任务教师（1）" }));
    expect(screen.getAllByRole("row")).toHaveLength(3);
    expect(screen.getByText("李老师")).toBeTruthy();
    expect(screen.getByText("暂无任务")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "隐藏无任务教师" }));
    expect(screen.queryByText("李老师")).toBeNull();
  });
  it("剩余多的在前，同数时在线优先，再按用户名排列", () => {
    const busy = (user: string, display: string, remaining: number, online: boolean): DashboardReviewTeacher => ({
      user_id: user, username: user, display_name: display, teaching_subjects: [], assigned_count: remaining,
      in_progress_count: 0, returned_count: 0, submitted_count: 0, remaining_count: remaining, is_online: online
    });
    render(<ReviewTeacherProgress teachers={[busy("d", "丁老师", 2, true), busy("c", "丙老师", 9, false), busy("b", "乙老师", 9, true), busy("a", "甲老师", 9, false)]} summary={summary} />);
    const names = screen.getAllByRole("row").slice(1).map(row => within(row).getAllByRole("cell")[0].querySelector("strong")?.textContent);
    expect(names).toEqual(["乙老师", "甲老师", "丙老师", "丁老师"]);
  });
  it("本校全部和在线切换可与学科组合，数学历史别名可筛选", () => {
    render(<ReviewTeacherProgress teachers={teachers} summary={summary} />);
    fireEvent.change(screen.getByRole("combobox", { name: "按任教学科查看进度" }), { target: { value: "mathematics" } });
    expect(screen.queryByText("李老师")).toBeNull();
    expect(screen.getByText("王老师")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "只看在线" }));
    fireEvent.change(screen.getByRole("combobox", { name: "按任教学科查看进度" }), { target: { value: "english" } });
    expect(screen.getByText("当前筛选下暂无在线阅卷教师")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "本校全部" }));
    expect(screen.getByText("当前筛选下的教师都没有阅卷任务")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "显示无任务教师（1）" }));
    expect(screen.getByText("李老师")).toBeTruthy();
    expect(screen.getByText(summary.task_counting_note)).toBeTruthy();
  });
});
