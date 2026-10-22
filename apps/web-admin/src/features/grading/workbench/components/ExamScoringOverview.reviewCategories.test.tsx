// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ScoringRunItem } from "../../../../api/review";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { ExamScoringOverview } from "./ExamScoringOverview";

vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    Button: (props: Record<string, unknown>) => React.createElement("button", { onClick: props.onClick, disabled: props.disabled, "aria-expanded": props["aria-expanded"], "data-variant": props.type }, props.children as ReactNode),
    Alert: (props: Record<string, unknown>) => React.createElement("div", {}, props.message as ReactNode, props.description as ReactNode),
    Popconfirm: (props: Record<string, unknown>) => React.createElement("div", {}, props.children as ReactNode),
    Space: (props: Record<string, unknown>) => React.createElement("div", {}, props.children as ReactNode)
  };
});
vi.mock("../../../../components/ResponsiveTable", () => ({ ResponsiveTable: () => <div>各题进度表</div> }));

function reviewItems(): ScoringRunItem[] {
  const items = [
    ...Array.from({ length: 11 }, () => ({ state: "review", question_type: "single_choice", reason_code: "omr_manual_only_profile" })),
    ...Array.from({ length: 5 }, () => ({ state: "review", question_type: "short_answer", decision_status: "suggestion_ready" })),
    { state: "review", question_type: "fill_blank", reason_code: "answer_observation_uncertain" },
    { state: "confirmed", question_type: "fill_blank", decision_status: "auto_confirmed" },
    { state: "confirmed", question_type: "short_answer", decision_status: "human_confirmed" }
  ];
  return items.map((entry, index) => ({ ...entry, answer_segment_id: `segment-${index}` }) as ScoringRunItem);
}

function controller(overrides: Partial<ExamScoringController> = {}): ExamScoringController {
  return {
    pendingCommand: null, summary: {
      run: { id: "run-1", tenant_id: "tenant", exam_id: "exam", idempotency_key: "fixture", started_by: "teacher", created_at: "", updated_at: "", status: "needs_review", total_count: 19, queued_count: 0, auto_confirmed_count: 1, human_confirmed_count: 1, review_count: 17, failed_count: 0 },
      questions: [{ question_id: "question", question_no: "1", question_type: "single_choice", total: 19, queued: 0, confirmed: 2, review: 17, failed: 0 }]
    },
    readiness: null, runDetail: { items: reviewItems() }, loading: false, actioning: null,
    detailOpen: false, detailError: null, setDetailOpen: vi.fn(), resultType: "all", setResultType: vi.fn(), resultState: "all", setResultState: vi.fn(), resultKeyword: "", setResultKeyword: vi.fn(), resultReviewFilter: "all", setResultReviewFilter: vi.fn(),
    image: null, imageLoading: "", plan: null, planOpen: false, setPlanOpen: vi.fn(),
    aiProgress: null, aiBatches: [], aiIssue: null, retryableAIBatches: [], blockingChecks: [], warningChecks: [], hasUnresolvedRun: true, filteredItems: [], resultMetrics: { total: 19, auto: 1, review: 17, failed: 0 },
    loadSummary: vi.fn(async () => undefined), showDetail: vi.fn(async () => undefined), prepareStart: vi.fn(async () => undefined), start: vi.fn(async () => undefined), retry: vi.fn(async () => undefined), cancel: vi.fn(async () => undefined), retryAI: vi.fn(async () => undefined), retryFailedAI: vi.fn(async () => undefined), showImage: vi.fn(async () => undefined), closeImage: vi.fn(),
    ...overrides
  };
}

afterEach(cleanup);

describe("阅卷概览按实际待办分类", () => {
  it("平静呈现19道题的11+5+1待办，每行直接进入对应结果筛选", () => {
    const onOpenResults = vi.fn();
    const onOpenAssignments = vi.fn();
    render(<ExamScoringOverview scoring={controller()} canManageTasks onOpenResults={onOpenResults} onOpenAssignments={onOpenAssignments} fastConfirm={<span>快捷确认入口</span>} />);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("阅卷概览");
    expect(screen.getByText("共 19 道题 · 已确认 2 · 待处理 17")).toBeTruthy();
    const tasks = screen.getByLabelText("待确认事项");
    expect(tasks.children).toHaveLength(3);
    expect(tasks.textContent).toContain("选择题核对11 道题");
    expect(tasks.textContent).toContain("建议分确认5 道题");
    expect(tasks.textContent).toContain("识别不确定1 道题");
    expect(screen.queryByText("主观题确认")).toBeNull();
    expect(screen.queryByText("6 道题")).toBeNull();
    for (const [action, filter] of [["核对选项", "choice"], ["确认建议分", "suggestion"], ["查看原图", "recognition"]]) {
      fireEvent.click(within(tasks).getByRole("button", { name: action }));
      expect(onOpenResults).toHaveBeenLastCalledWith(filter);
    }
    expect(screen.queryByRole("button", { name: "查看答卷结果" })).toBeNull();
    const assign = screen.getByRole("button", { name: "安排阅卷教师" });
    expect(assign.getAttribute("data-variant")).toBe("primary");
    fireEvent.click(assign);
    expect(onOpenAssignments).toHaveBeenCalledOnce();
    expect(screen.getByText("快捷确认入口")).toBeTruthy();
    expect(screen.queryByText("各题进度表")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "各题进度" }));
    expect(screen.getByText("各题进度表")).toBeTruthy();
  });

  it.each(["missing", "partial", "stale"])("%s明细只呈现总数，不把差额虚构成建议分", variant => {
    const onOpenResults = vi.fn();
    const items = reviewItems();
    const runDetail = variant === "missing" ? null : { items: variant === "partial" ? items.slice(0, 11) : items.map((entry, index) => index === 16 ? { ...entry, state: "confirmed" } : entry) };
    render(<ExamScoringOverview scoring={controller({ runDetail })} canManageTasks onOpenResults={onOpenResults} />);
    const tasks = screen.getByLabelText("待确认事项");
    expect(tasks.children).toHaveLength(1);
    expect(tasks.textContent).toContain("待确认17 道题");
    expect(screen.queryByText("选择题核对")).toBeNull();
    expect(screen.queryByText("建议分确认")).toBeNull();
    expect(screen.queryByText("识别不确定")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "查看答卷结果" }));
    expect(onOpenResults).toHaveBeenCalledWith("all");
  });

  it("人工待办有明确按钮，已确认条目上的旧原因不增加待办", () => {
    const scoring = controller();
    const items = reviewItems().map((entry, index) => index === 16 ? { ...entry, reason_code: "human_primary", decision_status: "manual_only" } : index === 18 ? { ...entry, reason_code: "answer_observation_uncertain" } : entry);
    const onOpenResults = vi.fn();
    render(<ExamScoringOverview scoring={{ ...scoring, runDetail: { items } }} canManageTasks onOpenResults={onOpenResults} />);
    expect(screen.queryByText("识别不确定")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "人工评分" }));
    expect(onOpenResults).toHaveBeenCalledWith("manual");
    expect(screen.getByLabelText("待确认事项").textContent).toContain("人工评分1 道题");
  });

  it("没有外部结果入口时保留现有结果读取动作", () => {
    const scoring = controller({ runDetail: null });
    render(<ExamScoringOverview scoring={scoring} canManageTasks />);
    fireEvent.click(screen.getByRole("button", { name: "查看答卷结果" }));
    expect(scoring.showDetail).toHaveBeenCalledOnce();
  });
});
