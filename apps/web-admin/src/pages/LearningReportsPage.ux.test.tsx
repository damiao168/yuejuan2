// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LearningReportsPage } from "./LearningReportsPage";
import { PageReadinessContext } from "../components/pageReadiness";
const mocks = vi.hoisted(() => ({ exams: vi.fn(), questions: vi.fn(), overview: vi.fn(), classes: vi.fn() }));
vi.mock("../api/exams", () => ({ listAllExams: mocks.exams }));
vi.mock("../components/PageState", () => ({ EmptyState: ({ title }: { title: string }) => <p>{title}</p>, ErrorState: ({ message }: { message: string }) => <p>{message}</p>, LoadingState: ({ label }: { label: string }) => <p>{label}</p> }));
vi.mock("../api/reports", () => ({ exportLearningReport: vi.fn(), getReportOverview: mocks.overview, listClassReports: mocks.classes, listQuestionReports: mocks.questions, getGradingQualityReport: async () => ({ grading_quality: null }) }));
vi.mock("../components/ResponsiveTable", () => ({ ResponsiveTable: ({ dataSource, columns }: { dataSource: Record<string, unknown>[]; columns: { title: string; dataIndex?: string; render?: (value: unknown, row: Record<string, unknown>) => ReactNode }[] }) => <table><thead><tr>{columns.map(column => <th key={column.title}>{column.title}</th>)}</tr></thead><tbody>{dataSource.map((row, index) => <tr key={index}>{columns.map(column => <td key={column.title}>{column.render ? column.render(row[column.dataIndex || ""], row) : String(row[column.dataIndex || ""] ?? "")}</td>)}</tr>)}</tbody></table> }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { error: vi.fn() }, modal: { confirm: vi.fn() } }) }, Alert: ({ message, description }: { message: string; description: string }) => <aside role="alert">{message}<span>{description}</span></aside>, Tag: ({ children }: { children: ReactNode }) => <span>{children}</span>, Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div>, Segmented: ({ options, value, onChange }: { options: { value: string; label: ReactNode }[]; value: string; onChange: (value: string) => void }) => <div>{options.map(option => <button key={option.value} aria-pressed={option.value === value} onClick={() => onChange(option.value)}>{option.label}</button>)}</div>, Select: ({ options, value, onChange, placeholder, "aria-label": label }: { options: { value: string; label: string }[]; value: string; onChange: (value: string) => void; placeholder?: string; "aria-label"?: string }) => <select aria-label={label || placeholder} value={value} onChange={event => onChange(event.target.value)}>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select> }));
vi.mock("recharts", () => { const passthrough = ({ children }: { children: ReactNode }) => <div>{children}</div>; return { ResponsiveContainer: passthrough, BarChart: ({ children, data }: { children: ReactNode; data: unknown }) => <div data-bar-chart-values={JSON.stringify(data)}>{children}</div>, LineChart: ({ children, data }: { children: ReactNode; data: unknown }) => <div data-chart-values={JSON.stringify(data)}>{children}</div>, CartesianGrid: () => null, XAxis: () => null, YAxis: ({ domain, label }: { domain?: number[]; label?: { value: string } }) => <div data-domain={JSON.stringify(domain)} data-axis-label={label?.value} />, Tooltip: () => null, Line: () => null, Bar: ({ dataKey, name, fill }: { dataKey: string; name: string; fill: string }) => <div data-bar-key={dataKey} data-bar-name={name} data-bar-fill={fill} /> }; });
beforeEach(() => {
  vi.clearAllMocks();
  mocks.overview.mockResolvedValue({ overview: { student_count: 10, stats: { distribution: [] }, empty: { empty: false } } });
  mocks.classes.mockResolvedValue({ classes: [] });
});
afterEach(cleanup);
describe("学情题目统计可解释与完整访问", () => {
  it("切页等待从考试目录交接到报告首读，报告完成后解除阻塞", async () => {
    let finishExams!: (value: { exams: { id: string; name: string; subject: string }[] }) => void;
    let finishOverview!: (value: { overview: { student_count: number; stats: { distribution: never[] }; empty: { empty: boolean } } }) => void;
    mocks.exams.mockReturnValue(new Promise((resolve) => { finishExams = resolve; }));
    mocks.overview.mockReturnValue(new Promise((resolve) => { finishOverview = resolve; }));
    mocks.questions.mockResolvedValue({ questions: [] });
    const blockers = new Set<symbol>();
    const readiness = { registerLoading: () => { const token = Symbol(); blockers.add(token); return () => { blockers.delete(token); }; } };
    render(<PageReadinessContext.Provider value={readiness}><LearningReportsPage canRead canExport={false} /></PageReadinessContext.Provider>);
    expect(blockers.size).toBeGreaterThan(0);
    await act(async () => finishExams({ exams: [{ id: "exam", name: "考试一", subject: "math" }] }));
    expect(mocks.overview).toHaveBeenCalledWith("exam");
    expect(blockers.size).toBeGreaterThan(0);
    await act(async () => finishOverview({ overview: { student_count: 10, stats: { distribution: [] }, empty: { empty: false } } }));
    expect(blockers.size).toBe(0);
  });

  it.each([true, false])("读取失败与无权限均可结束切页，读取权限=%s", async (canRead) => {
    mocks.exams.mockRejectedValue(new Error("考试目录无法读取"));
    const blockers = new Set<symbol>();
    const readiness = { registerLoading: () => { const token = Symbol(); blockers.add(token); return () => { blockers.delete(token); }; } };
    render(<PageReadinessContext.Provider value={readiness}><LearningReportsPage canRead={canRead} canExport={false} /></PageReadinessContext.Provider>);
    await screen.findByText("考试目录无法读取");
    expect(blockers.size).toBe(0);
    if (!canRead) expect(screen.getByText("无报告查看权限")).toBeTruthy();
  });

  it("空报告与无权限提示可同时出现，不互相遮掉", async () => {
    mocks.exams.mockResolvedValue({ exams: [{ id: "exam", name: "考试一", subject: "biology" }] });
    mocks.questions.mockResolvedValue({ questions: [] });
    mocks.overview.mockResolvedValue({ overview: { student_count: 0, stats: { distribution: [] }, empty: { empty: true, reason: "not_released" } } });
    render(<LearningReportsPage canRead={false} canExport={false} initialExamId="exam" />);
    await screen.findByText("报告为空");
    expect(screen.getByText("无报告查看权限")).toBeTruthy();
    expect(screen.getAllByRole("alert")).toHaveLength(2);
  });

  it("请求失败仍显示错误，且保留独立权限提示", async () => {
    mocks.exams.mockResolvedValue({ exams: [{ id: "exam", name: "考试一", subject: "biology" }] });
    mocks.questions.mockRejectedValue(new Error("报表请求失败"));
    render(<LearningReportsPage canRead={false} canExport={false} initialExamId="exam" />);
    await screen.findByText("报表请求失败");
    expect(screen.getByText("无报告查看权限")).toBeTruthy();
    expect(screen.queryByText("报告为空")).toBeNull();
  });

  it("区分度保留负值轴，选项题默认前4并能访问第5题与全部，切考试重置", async () => {
    mocks.exams.mockResolvedValue({ exams: [{ id: "exam", name: "考试一", subject: "biology" }, { id: "other", name: "考试二", subject: "biology" }] });
    mocks.questions.mockImplementation(async (exam: string) => ({ questions: Array.from({ length: exam === "exam" ? 6 : 1 }, (_, index) => ({ question_id: `${exam}-${index + 1}`, question_no: `Q${index + 1}`, question_type: "single_choice", score_rate: .8, correct_rate: .6, discrimination: -.25, option_distribution: [{ option: "A", count: 2 }], frequent_errors: [] })) }));
    render(<LearningReportsPage canRead canExport={false} initialExamId="exam" />);
    await screen.findByText("统计客观题各选项的作答人数。当前显示 4 / 6 题。");
    expect(document.querySelector('[data-domain="[-1,1]"]')).not.toBeNull();
    expect(document.querySelector('[data-chart-values]')?.getAttribute("data-chart-values")).toContain('"discrimination":-0.25');
    expect([...document.querySelectorAll("th")].map(item => item.textContent)).not.toContain("难度");
    const range = screen.getByLabelText("选项分布题目范围");
    fireEvent.change(range, { target: { value: "exam-5" } }); expect(document.querySelectorAll(".reports-option-block")).toHaveLength(1); expect(document.querySelector(".reports-option-block strong")?.textContent).toBe("Q5");
    fireEvent.change(range, { target: { value: "all" } }); expect(document.querySelectorAll(".reports-option-block")).toHaveLength(6);
    fireEvent.change(screen.getByLabelText("选择考试"), { target: { value: "other" } }); await screen.findByText("统计客观题各选项的作答人数。当前显示 1 / 1 题。"); expect((screen.getByLabelText("选项分布题目范围") as HTMLSelectElement).value).toBe("first-four");
  });

  it.each(["overview", "classes"] as const)("班级对比使用 %s 数据时，切换指标同步更新刻度、标题与柱形", async (source) => {
    const comparison = { class_id: "class-1", class_name: "一班", student_count: 10, average: 72.5, median: 75, pass_rate: 0.8, excellent_rate: 0.2 };
    mocks.exams.mockResolvedValue({ exams: [{ id: "exam", name: "考试一", subject: "biology" }] });
    mocks.questions.mockResolvedValue({ questions: [] });
    mocks.overview.mockResolvedValue({ overview: { student_count: 10, stats: { distribution: [] }, empty: { empty: false }, class_comparisons: source === "overview" ? [comparison] : [] } });
    mocks.classes.mockResolvedValue({ classes: source === "classes" ? [{ class_id: comparison.class_id, class_name: comparison.class_name, student_count: comparison.student_count, stats: comparison }] : [] });
    render(<LearningReportsPage canRead canExport={false} initialExamId="exam" />);
    await screen.findByText("按平均分比较，使用同一分数刻度。");
    const panel = screen.getByRole("heading", { name: "班级对比" }).closest("section")!;
    const readBar = () => panel.querySelector("[data-bar-key]")!;
    const readAxis = () => panel.querySelector("[data-axis-label]")!;
    expect(JSON.parse(panel.querySelector("[data-bar-chart-values]")!.getAttribute("data-bar-chart-values")!)).toEqual([{ name: "一班", average: 72.5, median: 75, passRate: 80, excellentRate: 20, studentCount: 10 }]);
    expect(readBar().getAttribute("data-bar-key")).toBe("average");
    expect(readBar().getAttribute("data-bar-name")).toBe("平均分");
    expect(readBar().getAttribute("data-bar-fill")).toBe("#1677ff");
    expect(readAxis().getAttribute("data-domain")).toBeNull();
    expect(readAxis().getAttribute("data-axis-label")).toBe("分数");
    fireEvent.click(screen.getByRole("button", { name: "及格率" }));
    expect(screen.getByText("按及格率比较，使用同一百分比刻度。")).toBeTruthy();
    expect(readBar().getAttribute("data-bar-key")).toBe("passRate");
    expect(readBar().getAttribute("data-bar-name")).toBe("及格率%");
    expect(readBar().getAttribute("data-bar-fill")).toBe("#52c41a");
    expect(readAxis().getAttribute("data-domain")).toBe("[0,100]");
    expect(readAxis().getAttribute("data-axis-label")).toBe("百分比");
    fireEvent.click(screen.getByRole("button", { name: "平均分" }));
    expect(readBar().getAttribute("data-bar-key")).toBe("average");
    expect(readAxis().getAttribute("data-domain")).toBeNull();
    expect(screen.getByText("按平均分比较，使用同一分数刻度。")).toBeTruthy();
  });
});
