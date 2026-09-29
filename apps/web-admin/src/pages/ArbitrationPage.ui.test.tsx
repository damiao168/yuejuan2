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
  const Input = Object.assign(() => null, { TextArea: () => null });
  const Descriptions = Object.assign(({ children }: { children: React.ReactNode }) => React.createElement("div", null, children), {
    Item: ({ children }: { children: React.ReactNode }) => React.createElement("span", null, children)
  });
  const Empty = Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" });
  return {
    App: { useApp: () => ({ message: { error: mocks.error, success: vi.fn() } }) },
    Alert: () => null,
    Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) =>
      React.createElement("button", { onClick, disabled }, children),
    Descriptions, Empty, Input, InputNumber: () => null, Select: () => null,
    Space: ({ children }: { children: React.ReactNode }) => React.createElement("div", null, children),
    Tabs: () => null
  };
});

function task(id: string): ArbitrationTask {
  return {
    id, exam_id: "exam", question_id: "question", question_no: id,
    anonymous_code: id, status: "assigned", assigned_to: "teacher", revision: 1,
    first_score: 3, second_score: 5, score_difference: 2, reason: "评分依据"
  } as ArbitrationTask;
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
    await act(async () => { host.querySelector<HTMLButtonElement>('[data-row-id="B"]')?.click(); });
    const submit = [...host.querySelectorAll("button")].find((button) => button.textContent === "提交仲裁");
    expect(submit?.disabled).toBe(true);
    expect(host.querySelector(".arbitration-context-row")).toBeNull();
    await act(async () => { submit?.click(); });
    expect(mocks.submit).not.toHaveBeenCalled();
    await act(async () => { finishB({ arbitration_task: task("B") }); });
    expect(host.querySelector(".arbitration-context-row")?.textContent).toContain("B");
    expect(submit?.disabled).toBe(false);
  });
});
