// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewTask, ScoringRunItem } from "../../../../api/review";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { ExamScoringPanel, type ExamScoringPanelProps } from "./ExamScoringPanel";

const rendered = vi.hoisted(() => ({
  widgets: [] as Array<{ name: string; props: Record<string, unknown> }>,
  tables: [] as Array<Record<string, unknown>>,
  fast: [] as Array<{ token: number; props: Record<string, unknown> }>,
  nextToken: 0
}));

vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  const widget = (name: string) => function Widget(props: Record<string, unknown>) {
    rendered.widgets.push({ name, props });
    if (name === "Button") return React.createElement("button", { disabled: Boolean(props.disabled), onClick: props.onClick as () => void }, props.children as ReactNode);
    if (name === "Alert") return React.createElement("div", { "data-alert": props.type }, props.message as ReactNode, props.description as ReactNode, props.action as ReactNode);
    if (name === "Modal" && !props.open) return null;
    return React.createElement("div", { "data-widget": name }, props.children as ReactNode);
  };
  return Object.fromEntries(["Alert", "Button", "Drawer", "Input", "Modal", "Popconfirm", "Progress", "Segmented", "Select", "Space", "Tooltip"].map((name) => [name, widget(name)]));
});
vi.mock("../../../../components/ResponsiveTable", () => ({ ResponsiveTable: (props: Record<string, unknown>) => { rendered.tables.push(props); return null; } }));
vi.mock("../../../../components/ScoringPaperMonitor", () => ({ ScoringPaperMonitor: () => null }));
vi.mock("../../../../components/OcrWorkerAlert", () => ({ OcrWorkerAlert: (props: Record<string, unknown>) => { rendered.widgets.push({ name: "OcrWorkerAlert", props }); return null; } }));
vi.mock("../../../../components/StatusTag", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return { StatusTag: (props: Record<string, unknown>) => React.createElement("span", { "data-tone": props.tone }, props.children as ReactNode) };
});
vi.mock("./FastConfirmDrawer", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return { FastConfirmDrawer: function FastConfirm(props: Record<string, unknown>) {
    const [token] = React.useState(() => ++rendered.nextToken);
    rendered.fast.push({ token, props });
    return React.createElement("span", { "data-fast-token": token }, String(props.examId));
  } };
});

function run(status = "completed") {
  const result: NonNullable<NonNullable<ExamScoringController["summary"]>["run"]> = { id: "run-1", tenant_id: "tenant", exam_id: "exam-A", idempotency_key: "fixture", started_by: "teacher", created_at: "", updated_at: "", status: status as NonNullable<NonNullable<ExamScoringController["summary"]>["run"]>["status"], total_count: 10, queued_count: 2, auto_confirmed_count: 3, human_confirmed_count: 1, review_count: 4, failed_count: 0 };
  return result;
}

function readiness(ready = true): NonNullable<ExamScoringController["readiness"]> {
  return { ready, exam_status: "grading", total_questions: 1, ready_segments: 10, total_segments: 10, automatic_candidates: 6, manual_review_candidates: 4, checks: [] };
}

function controller(overrides: Partial<ExamScoringController> = {}): ExamScoringController {
  return {
    pendingCommand: null, summary: null, readiness: readiness(), runDetail: null,
    loading: false, actioning: null, detailOpen: false, setDetailOpen: vi.fn(), resultType: "all", setResultType: vi.fn(), resultState: "all", setResultState: vi.fn(), resultKeyword: "", setResultKeyword: vi.fn(),
    image: null, imageLoading: "", plan: null, planOpen: false, setPlanOpen: vi.fn(),
    aiProgress: null, aiIssue: null, retryableAIBatches: [], blockingChecks: [], warningChecks: [], hasUnresolvedRun: false, filteredItems: [], resultMetrics: { total: 0, auto: 0, review: 0, failed: 0 },
    loadSummary: vi.fn(async () => undefined), showDetail: vi.fn(async () => undefined), prepareStart: vi.fn(async () => undefined), start: vi.fn(async () => undefined), retry: vi.fn(async () => undefined), cancel: vi.fn(async () => undefined), retryAI: vi.fn(async () => undefined), retryFailedAI: vi.fn(async () => undefined), showImage: vi.fn(async () => undefined), closeImage: vi.fn(),
    ...overrides
  };
}

function summary(status = "completed") {
  return { run: run(status), questions: [{ question_id: "first-question", question_no: "Q1", question_type: "short_answer", total: 10, queued: 2, confirmed: 4, review: 4, failed: 0 }] };
}

describe("batch8 scoring panel common original/current contract (mock UI/controller)", () => {
  let root: Root;
  let host: HTMLDivElement;
  let props: ExamScoringPanelProps;

  beforeEach(() => {
    setBusinessCommandScope("tenant", "teacher");
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    rendered.widgets.length = 0; rendered.tables.length = 0; rendered.fast.length = 0; rendered.nextToken = 0;
    host = document.createElement("div");
    root = createRoot(host);
    props = { initialExamId: "exam-A", canGrade: true, canWork: true, canManageTasks: true, currentQuestionId: "", scoring: controller(), onOpenGoldPapers: vi.fn(), onOpenCalibration: vi.fn(), tasks: [], currentUserId: "teacher", onTasksChanged: vi.fn(async () => undefined) };
  });
  afterEach(async () => { await act(async () => root.unmount()); });

  async function render(overrides: Partial<ExamScoringPanelProps> = {}) {
    props = { ...props, ...overrides };
    rendered.widgets.length = 0; rendered.tables.length = 0;
    await act(async () => root.render(<ExamScoringPanel {...props} />));
  }
  function button(label: string) {
    return rendered.widgets.find((entry) => entry.name === "Button" && entry.props.children === label)?.props;
  }
  function click(label: string) {
    const found = button(label);
    expect(found, label).toBeDefined();
    return (found!.onClick as () => unknown)();
  }

  it("continues a pending command even when a run is unresolved and readiness is blocked", async () => {
    await render({ scoring: controller({ pendingCommand: "command", hasUnresolvedRun: true, summary: summary("processing"), readiness: readiness(false) }) });
    expect(button("继续确认评分")).toBeDefined();
    expect(button("开始评分")).toBeUndefined();
    expect(button("重新评分")).toBeUndefined();
    expect(click("继续确认评分")).toBeUndefined();
    expect(props.scoring.start).toHaveBeenCalledWith();
  });

  it.each(["queued", "processing", "needs_review", "failed", "cancelling"])("does not offer a new start for unresolved %s", async (status) => {
    await render({ scoring: controller({ summary: summary(status), hasUnresolvedRun: true }) });
    expect(button("开始评分")).toBeUndefined();
    expect(button("重新评分")).toBeUndefined();
  });

  it.each([[null, "开始评分"], [summary(), "重新评分"]] as const)("uses the correct explicit new command label", async (nextSummary, label) => {
    await render({ scoring: controller({ summary: nextSummary }) });
    expect(click(label)).toBeUndefined();
    expect(props.scoring.prepareStart).toHaveBeenCalledWith();
  });

  it("disables a fresh start without readiness", async () => {
    await render({ scoring: controller({ readiness: null }) });
    expect(button("开始评分")?.disabled).toBe(true);
  });

  it("offers continue-cancel but no AI retry while cancelling", async () => {
    const failedBatch: ExamScoringController["retryableAIBatches"][number] = { id: "batch", tenant_id: "tenant", scoring_run_id: "run-1", idempotency_key: "fixture", status: "failed", segment_ids: ["segment"], total_count: 1, queued_count: 0, processing_count: 0, succeeded_count: 0, failed_count: 1, created_by: "teacher", created_at: "", updated_at: "" };
    await render({ scoring: controller({ summary: summary("cancelling"), hasUnresolvedRun: true, retryableAIBatches: [failedBatch], aiIssue: "未全部入队" }) });
    expect(button("继续取消")).toBeDefined();
    expect(button("重试失败的 AI 建议")).toBeUndefined();
    expect(button("重试 AI 入队")).toBeUndefined();
    const confirmation = rendered.widgets.find((entry) => entry.name === "Popconfirm")!.props;
    expect(confirmation.title).toBe("继续完成取消？");
    expect((confirmation.onConfirm as () => unknown)()).toBeUndefined();
    expect(props.scoring.cancel).toHaveBeenCalledWith();
  });

  it("preserves current-question priority and manager-only calibration", async () => {
    await render({ currentQuestionId: "current-question", scoring: controller({ summary: summary() }) });
    click("阅卷校准");
    expect(props.onOpenCalibration).toHaveBeenLastCalledWith("current-question");
    await render({ currentQuestionId: "" });
    click("阅卷校准");
    expect(props.onOpenCalibration).toHaveBeenLastCalledWith("first-question");
    await render({ canManageTasks: false });
    expect(button("标准卷")).toBeUndefined();
    expect(button("阅卷校准")).toBeUndefined();
  });

  it("limits active-run readiness issues to active_run_clear", async () => {
    const checks: ExamScoringController["blockingChecks"] = [
      { code: "active_run_clear", passed: false, severity: "blocker", label: "活动运行", message: "请先结束" },
      { code: "rubric_ready", passed: false, severity: "blocker", label: "细则缺失", message: "不应在此播报" }
    ];
    await render({ scoring: controller({ hasUnresolvedRun: true, blockingChecks: checks }) });
    const readiness = rendered.widgets.find((entry) => entry.name === "Alert" && entry.props.className === "grading-readiness-alert")!.props;
    expect(readiness.type).toBe("info");
    expect(readiness.message).toBe("已有评分任务，不能重复启动");
    expect(host.textContent).toContain("活动运行");
    expect(host.textContent).not.toContain("细则缺失");
  });

  it("retains FastConfirm state across unrelated renders and resets only on exam key change", async () => {
    await render();
    const first = rendered.fast[rendered.fast.length - 1]!;
    await render({ tasks: [{ assigned_to: "teacher", exam_id: "ignored-exam" }] as ReviewTask[], scoring: controller({ loading: true }) });
    expect(rendered.fast[rendered.fast.length - 1]?.token).toBe(first.token);
    await render({ initialExamId: "exam-B" });
    expect(rendered.fast[rendered.fast.length - 1]?.token).not.toBe(first.token);
    expect(rendered.fast[rendered.fast.length - 1]?.props.examId).toBe("exam-B");
  });

  it("derives FastConfirm's exam from the first own task, without granting scoring controls", async () => {
    await render({ initialExamId: "", canGrade: false, tasks: [{ assigned_to: "other", exam_id: "other" }, { assigned_to: "teacher", exam_id: "own-exam" }] as ReviewTask[] });
    expect(rendered.fast[rendered.fast.length - 1]?.props.examId).toBe("own-exam");
    expect(button("开始评分")).toBeUndefined();
    await (rendered.fast[rendered.fast.length - 1]!.props.onScoringChanged as () => Promise<void>)();
    expect(props.scoring.loadSummary).not.toHaveBeenCalled();
  });

  it("keeps the final fast-confirm receipt mounted after its own queue becomes empty", async () => {
    await render({ initialExamId: "", canGrade: false, tasks: [{ exam_id: "exam-A", assigned_to: "teacher" } as ReviewTask] });
    const token = host.querySelector("[data-fast-token]")?.getAttribute("data-fast-token");
    await render({ tasks: [] });
    expect(host.querySelector("[data-fast-token]")?.getAttribute("data-fast-token")).toBe(token);
    expect(host.querySelector("[data-fast-token]")?.textContent).toBe("exam-A");
    expect(rendered.fast[rendered.fast.length - 1]?.props.tasks).toEqual([]);
    expect(props.scoring.loadSummary).not.toHaveBeenCalled();
  });

  it.each(["different-user", "new-session"])("does not reuse an empty-queue receipt in a %s", async (change) => {
    await render({ initialExamId: "", canGrade: false, tasks: [{ exam_id: "exam-A", assigned_to: "teacher" } as ReviewTask] });
    await render({ tasks: [] });
    if (change === "new-session") setBusinessCommandScope("tenant", "teacher");
    await render(change === "different-user" ? { currentUserId: "other" } : {});
    expect(host.querySelector("[data-fast-token]")).toBeNull();
  });

  it("preserves result row identity, pagination, filter setters and callback return values", async () => {
    const item = { answer_segment_id: "segment-A", question_no: "Q1", question_type: "numeric", score: 0, max_score: 5, recognized_answer: "0" } as ScoringRunItem;
    await render({ scoring: controller({ filteredItems: [item], detailOpen: true }) });
    const table = rendered.tables.find((entry) => entry.className === "automation-result-table dense-data-table")!;
    expect(table.rowKey).toBe("answer_segment_id");
    expect(table.dataSource).toBe(props.scoring.filteredItems);
    expect(table.pagination).toMatchObject({ pageSize: 15, showSizeChanger: true });
    const drawer = rendered.widgets.find((entry) => entry.name === "Drawer")!.props;
    expect(drawer.destroyOnHidden).toBe(false);
    const input = rendered.widgets.find((entry) => entry.name === "Input")!.props;
    (input.onChange as (event: { target: { value: string } }) => void)({ target: { value: " 0 " } });
    expect(props.scoring.setResultKeyword).toHaveBeenCalledWith(" 0 ");
    const columns = table.columns as Array<{ title: string; render?: (value: unknown, row: ScoringRunItem) => ReactNode }>;
    await act(async () => root.render(<>{columns.find((column) => column.title === "操作")!.render!(undefined, item)}</>));
    expect(click("查看答题图")).toBeUndefined();
    expect(props.scoring.showImage).toHaveBeenCalledWith(item);
  });

  it.each([["completed", "success"], ["failed", "danger"], ["needs_review", "warning"], ["cancelled", "processing"]])("keeps the original run tone for %s", async (status, tone) => {
    await render({ scoring: controller({ summary: summary(status) }) });
    expect(host.querySelector(".grading-run-strip [data-tone]")?.getAttribute("data-tone")).toBe(tone);
  });
});
