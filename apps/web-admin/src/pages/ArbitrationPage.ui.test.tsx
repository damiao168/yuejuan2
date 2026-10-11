// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArbitrationTask } from "../api/review";
import type { SessionUser } from "../auth/session";
import { ArbitrationPage } from "./ArbitrationPage";

const mocks = vi.hoisted(() => ({
  listTasks: vi.fn(), getTask: vi.fn(), submit: vi.fn(), error: vi.fn()
}));

vi.mock("../api/review", () => ({
  listArbitrationTasks: mocks.listTasks,
  getArbitrationTask: mocks.getTask,
  submitArbitration: mocks.submit
}));
vi.mock("../api/exams", () => ({ listExams: vi.fn().mockResolvedValue({ exams: [{ id: "exam", name: "考试" }] }) }));
vi.mock("../api/papers", () => ({ listQuestions: vi.fn().mockResolvedValue({ questions: [{ id: "question", score: 10 }] }) }));
vi.mock("../router/query", () => ({ hashQueryParam: () => "" }));
vi.mock("../features/gold-papers", () => ({ GoldCoverageGaps: () => null }));
vi.mock("../features/answer-groups", () => ({ AnswerGroupingDrawer: () => null }));
vi.mock("./ArbitrationPanels", () => ({
  ArbitrationAiTab: () => null, ArbitrationAuditList: () => null,
  ArbitrationContextTab: () => null, ArbitrationRubricTab: () => null,
  ArbitrationScoreComparison: () => null
}));
vi.mock("../components/PageState", async () => {
  const React = await import("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", null, label),
    EmptyState: () => React.createElement("div"),
    ErrorState: () => React.createElement("div")
  };
});
vi.mock("../components/ResponsiveTable", async () => {
  const React = await import("react");
  return {
    ResponsiveTable: ({ dataSource, onRow }: { dataSource: ArbitrationTask[]; onRow: (task: ArbitrationTask) => { onClick: () => void } }) =>
      React.createElement("div", null, dataSource.map((task) => React.createElement("button", {
        key: task.id, "data-row-id": task.id, onClick: onRow(task).onClick
      }, task.id)))
  };
});
vi.mock("antd", async () => {
  const React = await import("react");
  const Input = Object.assign(() => null, { TextArea: ({ value, disabled }: { value: string; disabled?: boolean }) =>
    React.createElement("textarea", { value, disabled, readOnly: true }) });
  const Descriptions = Object.assign(({ children }: { children: React.ReactNode }) => React.createElement("div", null, children), {
    Item: ({ children }: { children: React.ReactNode }) => React.createElement("span", null, children)
  });
  const Empty = Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" });
  return {
    App: { useApp: () => ({ message: { error: mocks.error, success: vi.fn(), warning: vi.fn() } }) },
    Alert: () => null,
    Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) =>
      React.createElement("button", { onClick, disabled }, children),
    Descriptions, Empty, Input, InputNumber: () => null, Select: () => null,
    Space: ({ children }: { children: React.ReactNode }) => React.createElement("div", null, children),
    Tag: ({ children }: { children: React.ReactNode }) => React.createElement("span", null, children),
    Tabs: () => null
  };
});

function task(id: string): ArbitrationTask {
  return {
    id, exam_id: "exam", question_id: "question", question_no: id,
    tenant_id: "school", double_mark_session_id: "session", answer_segment_id: "segment", submission_id: "submission",
    first_reviewer_id: "first", second_reviewer_id: "second", difference_reason: "分差超过阈值", allow_same_arbitrator: false,
    created_by: "manager", created_at: "2026-10-08T00:00:00Z", updated_at: "2026-10-08T00:00:00Z",
    anonymous_code: id, status: "assigned", assigned_to: "teacher", revision: 1,
    first_score: 3, second_score: 5, score_difference: 2, reason: "评分依据",
    context: { question: { id: "question", exam_id: "exam", question_no: id, question_type: "short_answer", score: 10 }, rubric: { id: "rubric", question_id: "question", version: "v1", status: "approved", max_score: 10, points: [], deductions: [], examples: [] } }
  };
}

describe("arbitration page task switching", () => {
  let root: Root;
  let host: HTMLDivElement;
  let finishB: (value: { arbitration_task: ArbitrationTask }) => void;

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    mocks.listTasks.mockResolvedValue({ arbitration_tasks: [task("A"), task("B")], has_more: false });
    mocks.getTask.mockImplementation((id: string) => id === "B"
      ? new Promise((resolve) => { finishB = resolve; })
      : Promise.resolve({ arbitration_task: task("A") }));
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
  });

  it("clears A's decision and blocks submission while B's detail is loading", async () => {
    await act(async () => {
      root.render(<ArbitrationPage canAssign={false} canWork canReadAudit={false} canReadExams
        currentUser={{ id: "teacher" } as SessionUser} />);
    });
    expect(host.querySelector(".arbitration-context-row")?.textContent).toContain("A");
    const taskB = [...host.querySelectorAll<HTMLButtonElement>(".arbitration-task-item")].find((button) => button.querySelector(".arbitration-task-meta > span:first-child")?.textContent?.startsWith("B ·"));
    expect(taskB).toBeDefined();
    await act(async () => { taskB?.click(); });
    const submit = [...host.querySelectorAll("button")].find((button) => button.textContent === "提交仲裁");
    expect(submit?.disabled).toBe(true);
    expect(host.querySelector(".arbitration-context-row")).toBeNull();
    await act(async () => { submit?.click(); });
    expect(mocks.submit).not.toHaveBeenCalled();
    await act(async () => { finishB({ arbitration_task: task("B") }); });
    expect(host.querySelector(".arbitration-context-row")?.textContent).toContain("B");
    expect(submit?.disabled).toBe(false);
  });

  it("freezes editing and task navigation until submission finishes, retaining the draft on failure", async () => {
    mocks.getTask.mockResolvedValue({ arbitration_task: { ...task("A"), final_score: 6 } });
    let rejectSubmit!: (error: Error) => void;
    mocks.submit.mockImplementation(() => new Promise((_, reject) => { rejectSubmit = reject; }));
    await act(async () => {
      root.render(<ArbitrationPage canAssign={false} canWork canReadAudit={false} canReadExams
        currentUser={{ id: "teacher" } as SessionUser} />);
    });
    const submit = [...host.querySelectorAll("button")].find((button) => button.textContent === "提交仲裁")!;
    await act(async () => { submit.click(); });
    expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect([...host.querySelectorAll("textarea")].every((input) => input.disabled)).toBe(true);
    expect([...host.querySelectorAll<HTMLButtonElement>(".arbitration-task-item")].every((button) => button.disabled)).toBe(true);
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "刷新")?.disabled).toBe(true);
    await act(async () => { rejectSubmit(new Error("network failed")); });
    expect(host.querySelector("textarea")?.value).toBe("评分依据");
    expect(host.querySelector("textarea")?.disabled).toBe(false);
    expect(submit.disabled).toBe(false);
  });
});
