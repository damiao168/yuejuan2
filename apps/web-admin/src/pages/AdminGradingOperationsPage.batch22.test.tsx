// @vitest-environment jsdom
import { act, type ChangeEventHandler, type MouseEventHandler, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../api/exams";
import type { ScoringRun } from "../api/review";
import { AdminGradingOperationsPage } from "./AdminGradingOperationsPage";

const mocks = vi.hoisted(() => ({ listExams: vi.fn(), summary: vi.fn(), navigate: vi.fn() }));
vi.mock("../api/exams", () => ({ listAllExams: mocks.listExams }));
vi.mock("../api/review", () => ({ getScoringSummary: mocks.summary }));
vi.mock("../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span data-status>{children}</span> }));
vi.mock("../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <div>{label}</div>,
  ErrorState: ({ message }: { message: string }) => <div>{message}</div>,
  EmptyState: ({ title }: { title: string }) => <div>{title}</div>
}));
vi.mock("antd", () => ({
  Alert: ({ message }: { message: ReactNode }) => <div>{message}</div>,
  Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: MouseEventHandler<HTMLButtonElement>; disabled?: boolean }) => <button onClick={onClick} disabled={disabled}>{children}</button>,
  Input: ({ value, onChange }: { value: string; onChange: ChangeEventHandler<HTMLInputElement> }) => <input value={value} onChange={onChange} />,
  Progress: ({ percent, status }: { percent: number; status: string }) => <div data-percent={percent} data-progress-status={status} />,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>
}));

function exam(status: string): Exam {
  return { id: `${status}/exam`, tenant_id: "tenant", school_id: "school", name: status, subject: "math", exam_type: "test", total_score: 100,
    status, grading_mode: "human_review_required", appeal_enabled: false, publish_policy: "manual", created_by: "owner", class_ids: ["class"], revision: 1 };
}
function run(examId: string, total: number): ScoringRun {
  return { id: "run", tenant_id: "tenant", exam_id: examId, idempotency_key: "key", status: "processing", started_by: "owner", created_at: "", updated_at: "",
    total_count: total, queued_count: 3, auto_confirmed_count: 2, human_confirmed_count: 3, review_count: 1, failed_count: 1 };
}

describe("阅卷管理展示拆分", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.listExams.mockResolvedValue({ exams: ["ready", "collecting", "grading", "reviewing", "configured"].map(exam) });
    mocks.summary.mockImplementation(async (id: string) => ({ scoring_summary: { run: run(id, id.startsWith("grading") ? 10 : 0), questions: [] } }));
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(canImport = true) {
    await act(async () => root.render(<AdminGradingOperationsPage canImport={canImport} onNavigate={mocks.navigate} />));
  }
  function row(status: string) {
    return [...host.querySelectorAll<HTMLElement>("article")].find((item) => item.querySelector("h2")?.textContent === status)!;
  }
  function button(scope: HTMLElement, label: string) {
    return [...scope.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label)!;
  }

  it("保留阶段文案、正数进度及导入和监控入口", async () => {
    await render();
    expect(host.querySelectorAll("article")).toHaveLength(4);
    expect(mocks.summary.mock.calls.map(([id]) => id)).toEqual(["grading/exam", "reviewing/exam"]);
    expect(["ready", "collecting"].map((status) => row(status).querySelector("[data-status]")?.textContent)).toEqual(["待导入", "导入中"]);
    expect(row("ready").textContent).toContain("准备已完成，等待导入答卷");
    expect(row("collecting").textContent).toContain("正在导入和处理答卷");
    expect(row("reviewing").textContent).toContain("等待生成阅卷任务");
    expect(row("reviewing").querySelector(".grading-monitor-counts")).toBeNull();
    expect(row("grading").querySelector("[data-percent]")?.getAttribute("data-percent")).toBe("50");
    expect(row("grading").querySelector("[data-progress-status]")?.getAttribute("data-progress-status")).toBe("exception");
    expect(row("grading").textContent).toContain("5 / 10 项已确认");
    expect(row("grading").querySelector(".danger")?.textContent).toBe("1失败");
    expect(button(row("ready"), "任务分配与监控")).toBeUndefined();
    await act(async () => button(row("ready"), "导入答卷").click());
    await act(async () => button(row("collecting"), "任务分配与监控").click());
    expect(mocks.navigate.mock.calls.map(([path]) => path)).toEqual(["/exams/ready%2Fexam/capture", "/exams/collecting%2Fexam/grading"]);
  });

  it("导入权限关闭后仍保留监控入口，禁用导入并隐藏查看答卷", async () => {
    await render(false);
    expect(button(row("ready"), "导入答卷").disabled).toBe(true);
    expect(button(row("collecting"), "继续导入").disabled).toBe(true);
    expect(button(row("grading"), "查看答卷")).toBeUndefined();
    expect(button(row("grading"), "任务分配与监控").disabled).toBe(false);
  });
});
