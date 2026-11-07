// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DashboardActiveExam, DashboardGradingQuestionProgress, DashboardReviewTeacherSummary, DashboardStatistics } from "../../api/dashboard";
import { SchoolDashboard, type SchoolDashboardProps } from "./SchoolDashboard";

vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button type="button" onClick={onClick}>{children}</button>,
  Select: ({ value, options, onChange, "aria-label": label }: { value?: string; options: Array<{ value: string; label: string }>; onChange: (value: string) => void; "aria-label": string }) => <select aria-label={label} value={value || ""} onChange={event => onChange(event.target.value)}><option value="">全部学科</option>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
  Segmented: ({ options, value, onChange, "aria-label": label }: { options: Array<{ label: string; value: string }>; value: string; onChange: (value: string) => void; "aria-label": string }) => <div role="group" aria-label={label}>{options.map(option => <button key={option.value} type="button" aria-pressed={option.value === value} onClick={() => onChange(option.value)}>{option.label}</button>)}</div>
}));
afterEach(cleanup);

function stats(overrides: Partial<DashboardStatistics> = {}): DashboardStatistics {
  return {
    active_exam_count: 0, collecting_exam_count: 0, pending_review_question_count: 0, pending_review_submission_count: 0,
    pending_arbitration_count: 0, pending_arbitration_submission_count: 0, failed_submission_count: 0,
    unmatched_submission_count: 0, quality_issue_submission_count: 0, finalized_exam_count: 0, ...overrides
  };
}

function exam(id: string, overrides: Partial<DashboardActiveExam> = {}): DashboardActiveExam {
  return {
    id, name: `考试${id}`, subject: "math", status: "collecting", submission_count: 0, failed_count: 0,
    quality_issue_count: 0, unmatched_count: 0, created_at: "2026-10-01T00:00:00Z", ...overrides
  };
}

function question(id: string, remaining: number, recent: number): DashboardGradingQuestionProgress {
  return { exam_id: "g", question_id: id, question_no: id.slice(1), task_count: 20, done_count: 20 - remaining, remaining_count: remaining, recent_done_count: recent };
}

const teacherSummary: DashboardReviewTeacherSummary = { teacher_count: 1, online_count: 0, online_window_seconds: 300, task_counting_note: "按教师计数。", online_note: "近 5 分钟有活动。" };

function renderDashboard(overrides: Partial<SchoolDashboardProps> = {}) {
  const onNavigate = vi.fn();
  render(<SchoolDashboard statistics={stats()} exams={[]} warnings={[]} activities={[]} onNavigate={onNavigate} {...overrides} />);
  return onNavigate;
}

const section = (name: string) => screen.getByRole("region", { name });

describe("学校工作台", () => {
  it("只有图像质量问题的考试进入“需要我处理”，并计入该考试的异常", () => {
    const onNavigate = renderDashboard({
      statistics: stats({ quality_issue_submission_count: 2 }),
      exams: [exam("q/1", { name: "期中数学", quality_issue_count: 2, submission_count: 30 })]
    });
    const todoRows = within(section("需要我处理")).getAllByRole("row");
    expect(todoRows).toHaveLength(2);
    expect(todoRows[1].textContent).toContain("图像质量需要处理");
    expect(todoRows[1].textContent).toContain("期中数学");
    expect(todoRows[1].textContent).toContain("2 份");
    fireEvent.click(within(todoRows[1]).getByRole("button", { name: "查看质量问题" }));
    expect(onNavigate).toHaveBeenCalledWith("/exams/q%2F1/capture");
    const examRow = within(section("考试进度")).getAllByRole("row")[1];
    const issueCell = within(examRow).getAllByRole("cell")[2];
    expect(issueCell.textContent).toBe("2");
    expect(issueCell.className).toContain("has-issue");
    expect(within(section("当前状态")).getByText("1 件事会卡住出成绩：2 份图像质量需要处理")).toBeTruthy();
  });

  it("准备中的考试收成一行，表格只列采集中及之后的考试，阶段用有序步骤标出当前步", () => {
    const onNavigate = renderDashboard({
      exams: [
        exam("draft", { name: "草稿考试", status: "draft" }), exam("configured", { status: "configured" }), exam("ready", { status: "ready" }),
        exam("collecting", { name: "月考英语", status: "collecting", submission_count: 12, roster_count: 40, roster_submitted_count: 11 })
      ]
    });
    const examSection = section("考试进度");
    expect(within(examSection).getAllByRole("columnheader").map(cell => cell.textContent)).toEqual(["考试", "阶段", "当前进度", "异常", "下一步"]);
    expect(within(examSection).getAllByRole("row")).toHaveLength(2);
    expect(within(examSection).queryByText("草稿考试")).toBeNull();
    expect(within(examSection).getByText("已收 11/40 人")).toBeTruthy();
    expect(within(examSection).getByText("另有 3 场考试在准备中")).toBeTruthy();
    const steps = within(examSection).getByRole("list", { name: "考试阶段" });
    expect(steps.tagName).toBe("OL");
    expect(within(steps).getByText("采集").getAttribute("aria-current")).toBe("step");
    expect(within(steps).getByText("试卷").textContent).toBe("试卷（已完成）");
    expect(within(steps).getByText("阅卷").getAttribute("aria-current")).toBeNull();
    // 准备中只给提示文字；整个区块只保留标题右侧一个考试列表入口，避免重复入口。
    expect(within(examSection).queryByRole("button", { name: "查看全部考试" })).toBeNull();
    fireEvent.click(within(examSection).getByRole("button", { name: /全部考试/ }));
    expect(onNavigate).toHaveBeenCalledWith("/exams");
  });

  it("只有准备中考试时显示对应空状态；一场都没有时显示创建提示", () => {
    renderDashboard({ exams: [exam("draft", { status: "draft" })] });
    expect(within(section("考试进度")).getByText("暂无采集中或阅卷中的考试")).toBeTruthy();
    cleanup();
    renderDashboard();
    expect(within(section("考试进度")).getByText("暂无进行中的考试")).toBeTruthy();
    expect(within(section("考试进度")).getByText("创建考试后，可从这里直接继续下一步。")).toBeTruthy();
    expect(within(section("考试进度")).queryByText(/在准备中/)).toBeNull();
  });

  it("没有剩余阅卷任务时不显示“阅卷进度”", () => {
    renderDashboard({
      exams: [exam("g", { status: "grading", review_task_count: 20, review_done_count: 20 })],
      gradingProgress: { window_seconds: 3600, questions: [question("q1", 0, 0)] },
      reviewTeachers: [{ user_id: "t", username: "t", display_name: "王老师", teaching_subjects: [], assigned_count: 0, in_progress_count: 0, returned_count: 0, submitted_count: 20, remaining_count: 0, is_online: true }],
      reviewTeacherSummary: teacherSummary
    });
    expect(screen.queryByRole("heading", { name: "阅卷进度" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "按老师" })).toBeNull();
  });

  it("有剩余任务时先按题再按老师；按题按剩余降序，默认 5 行，可展开和收起", () => {
    renderDashboard({
      exams: [exam("g", { name: "期中数学", status: "grading" })],
      gradingProgress: { window_seconds: 3600, questions: [question("q1", 0, 0), question("q2", 12, 24), question("q3", 18, 10), question("q4", 4, 3), question("q5", 6, 30), question("q6", 3, 0)] },
      reviewTeachers: [{ user_id: "t", username: "t", display_name: "王老师", teaching_subjects: [], assigned_count: 4, in_progress_count: 0, returned_count: 0, submitted_count: 2, remaining_count: 4, is_online: true }],
      reviewTeacherSummary: teacherSummary
    });
    const headings = within(section("阅卷进度")).getAllByRole("heading").map(heading => `${heading.tagName}:${heading.textContent}`);
    expect(headings).toEqual(["H2:阅卷进度", "H3:按题", "H3:按老师"]);
    const byQuestion = section("按题");
    expect(within(byQuestion).getAllByRole("columnheader").map(cell => cell.textContent)).toEqual(["考试", "题号", "进度", "剩余", "近 1 小时已阅", "预计剩余时间"]);
    const rows = within(byQuestion).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(5);
    expect(rows.map(row => within(row).getAllByRole("cell")[1].textContent)).toEqual(["3", "2", "5", "4", "6"]);
    expect(rows[0].textContent).toContain("2/20");
    expect(within(rows[0]).getAllByRole("cell")[5].textContent).toBe("约 2 小时");
    expect(within(rows[1]).getAllByRole("cell")[5].textContent).toBe("不到 1 小时");
    expect(within(rows[3]).getAllByRole("cell")[5].textContent).toBe("样本不足");
    expect(within(byQuestion).getByRole("progressbar", { name: "期中数学 第 3 题阅卷进度" })).toBeTruthy();
    expect(within(byQuestion).getByText(/双评时每位教师各计一项/)).toBeTruthy();
    fireEvent.click(within(byQuestion).getByRole("button", { name: "展开全部" }));
    expect(within(byQuestion).getAllByRole("row")).toHaveLength(7);
    fireEvent.click(within(byQuestion).getByRole("button", { name: "收起" }));
    expect(within(byQuestion).getAllByRole("row")).toHaveLength(6);
    expect(within(section("按老师")).getByText("王老师")).toBeTruthy();
  });

  it("旧后端缺少新字段时不把名册、阅卷任务和成绩复核显示成 0", () => {
    renderDashboard({
      statistics: stats({ pending_arbitration_count: 2 }),
      organizationStatistics: { active_student_count: 0, grade_count: 0, class_count: 0, teacher_count: 0, grader_count: 0, empty_class_count: 0, unassigned_teacher_count: 0 },
      canManageOrganization: true,
      exams: [exam("c", { status: "collecting", submission_count: 12 }), exam("g", { name: "期末物理", status: "grading" })]
    });
    const examRows = within(section("考试进度")).getAllByRole("row").slice(1);
    // 阅卷中离发布更近，排在采集中之前。
    expect(examRows.map(row => within(row).getAllByRole("cell")[1].textContent)).toEqual(["暂无阅卷任务", "已收 12 份"]);
    const todo = within(section("需要我处理")).getAllByRole("row");
    expect(todo).toHaveLength(2);
    expect(within(todo[1]).getAllByRole("cell")[0].textContent).toBe("—");
    expect(screen.queryByText(/成绩复核/)).toBeNull();
    expect(screen.queryByText(/班级没有学生|教师未设置任教学科/)).toBeNull();
    expect(screen.queryByText(/\/0/)).toBeNull();
    expect(screen.queryByRole("heading", { name: "阅卷进度" })).toBeNull();
  });

  const statusCases: Array<{ name: string; props: Partial<SchoolDashboardProps>; text: string; tone: string; note?: string }> = [
    { name: "阻断", props: { statistics: stats({ failed_submission_count: 1, quality_issue_submission_count: 2 }) }, text: "2 件事会卡住出成绩：1 份答卷处理失败，2 份图像质量需要处理", tone: "is-danger" },
    { name: "等待", props: { statistics: stats({ finalized_exam_count: 1 }), exams: [exam("f", { status: "finalized" })] }, text: "有 1 件事等你处理：1 场成绩待发布", tone: "is-warning" },
    { name: "无事项", props: { exams: [exam("g", { status: "grading" })] }, text: "目前没有卡住出成绩的事，1 场考试正在阅卷", tone: "is-success" },
    { name: "有告警", props: { warnings: ["阅卷待办暂时无法统计"] }, text: "目前没有卡住出成绩的事", tone: "is-neutral", note: "部分统计暂时不可用，结论可能不完整。" }
  ];
  it.each(statusCases)("状态结论：$name", ({ props, text, tone, note }) => {
    renderDashboard(props);
    const status = section("当前状态");
    expect(status.className).toContain(tone);
    expect(within(status).getByText(text)).toBeTruthy();
    if (note) expect(within(status).getByText(note)).toBeTruthy();
    else expect(status.querySelector(".school-dashboard-status-note")).toBeNull();
  });

  it("基础数据行只对持有 org:manage 的用户显示，且不计入状态结论", () => {
    const organizationStatistics = { active_student_count: 30, grade_count: 1, class_count: 2, teacher_count: 3, grader_count: 0, empty_class_count: 1, unassigned_teacher_count: 2 };
    const onNavigate = renderDashboard({ organizationStatistics, canManageOrganization: true });
    const todo = section("需要我处理");
    expect(within(todo).getAllByRole("columnheader").map(cell => cell.textContent)).toEqual(["事项", "考试", "数量", "影响", "操作"]);
    const rows = within(todo).getAllByRole("row").slice(1);
    expect(rows.map(row => within(row).getByRole("rowheader").textContent)).toEqual(["班级没有学生", "教师未设置任教学科"]);
    expect(within(section("当前状态")).getByText("目前没有卡住出成绩的事")).toBeTruthy();
    fireEvent.click(within(rows[0]).getByRole("button", { name: "导入学生" }));
    expect(onNavigate).toHaveBeenCalledWith("/members/students");
    cleanup();
    renderDashboard({ organizationStatistics, canManageOrganization: false });
    expect(within(section("需要我处理")).getByText("当前没有需要处理的事")).toBeTruthy();
    expect(within(section("需要我处理")).getByText("新的答卷异常、复核申请和发布任务会显示在这里。")).toBeTruthy();
  });

  it("最近动态最多 5 条，没有动态（包括后端返回 null）时不显示", () => {
    renderDashboard({ activities: null });
    expect(screen.queryByRole("heading", { name: "最近动态" })).toBeNull();
    cleanup();
    const activities = Array.from({ length: 6 }, (_, index) => ({ id: `a${index}`, action: "exam.updated", target_type: "exam", title: `动态${index}`, created_at: "2026-10-30T08:00:00Z", drilldown_path: `/exams/e${index}/overview` }));
    const onNavigate = renderDashboard({ activities });
    expect(screen.getAllByRole("heading", { level: 2 }).map(heading => heading.textContent)).toEqual(["需要我处理", "考试进度", "最近动态"]);
    const recent = section("最近动态");
    expect(within(recent).getAllByRole("button")).toHaveLength(5);
    fireEvent.click(within(recent).getByRole("button", { name: /动态0/ }));
    expect(onNavigate).toHaveBeenCalledWith("/exams/e0/overview");
  });
});
