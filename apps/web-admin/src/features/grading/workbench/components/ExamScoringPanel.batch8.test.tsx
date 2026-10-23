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
vi.mock("../../../../components/ScoringPaperMonitor", () => ({ ScoringPaperMonitor: (props: Record<string, unknown>) => { rendered.widgets.push({ name: "ScoringPaperMonitor", props }); return null; } }));
vi.mock("../../../../components/OcrWorkerAlert", () => ({ OcrWorkerAlert: (props: Record<string, unknown>) => { rendered.widgets.push({ name: "OcrWorkerAlert", props }); return null; } }));
vi.mock("./GradingAIPlanSettings", () => ({
  gradingAIModeLabels: { single: "单模型评分 · 非三智能体" },
  GradingAIPlanSettings: (props: Record<string, unknown>) => { rendered.widgets.push({ name: "GradingAIPlanSettings", props }); return null; },
  GradingAIPlanSummary: (props: Record<string, unknown>) => { rendered.widgets.push({ name: "GradingAIPlanSummary", props }); return null; }
}));
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
    pendingCommand: null, summary: null, readiness: readiness(), runDetail: null, detailError: null,
    loading: false, actioning: null, detailOpen: false, setDetailOpen: vi.fn(), resultType: "all", setResultType: vi.fn(), resultState: "all", setResultState: vi.fn(), resultKeyword: "", setResultKeyword: vi.fn(), resultReviewFilter: "all", setResultReviewFilter: vi.fn(),
    image: null, imageLoading: "", plan: null, planOpen: false, setPlanOpen: vi.fn(),
    aiProgress: null, aiBatches: [], aiIssue: null, retryableAIBatches: [], blockingChecks: [], warningChecks: [], hasUnresolvedRun: false, filteredItems: [], resultMetrics: { total: 0, auto: 0, review: 0, failed: 0 },
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

  it("大题方案阻塞时可从概览配置，保存刷新准备状态且关闭不打开空启动窗口", async () => {
    const blockingChecks: ExamScoringController["blockingChecks"] = [{ code: "grading_ai_plan_confirmation_required", passed: false, severity: "blocker", label: "大题方案未确认", message: "请学校管理员确认本场大题方案" }];
    await render({ scoring: controller({ readiness: readiness(false), blockingChecks }) });
    expect(button("开始评分")?.disabled).toBe(true);
    await act(async () => click("配置本场大题评分"));
    const modal = rendered.widgets.filter(entry => entry.name === "Modal" && entry.props.title === "配置本场大题 AI 评分").slice(-1)[0]!.props;
    await act(async () => (modal.onCancel as () => void)());
    expect(props.scoring.setPlanOpen).not.toHaveBeenCalled();
    await act(async () => click("配置本场大题评分"));
    const editor = rendered.widgets.filter(entry => entry.name === "GradingAIPlanSettings").slice(-1)[0]!.props;
    await act(async () => (editor.onSaved as () => void)());
    expect(props.scoring.loadSummary).toHaveBeenCalledOnce();
    expect(props.scoring.setPlanOpen).not.toHaveBeenCalled();
    await render({ showOverview: false, showSettings: true });
    const settingsEditor = rendered.widgets.find(entry => entry.name === "GradingAIPlanSettings")!.props;
    await act(async () => (settingsEditor.onSaved as () => void)());
    expect(props.scoring.loadSummary).toHaveBeenCalledTimes(2);
  });

  it("从已有启动窗口配置大题方案后仍返回该启动窗口", async () => {
    await render({ scoring: controller({ planOpen: true, plan: { examId: "exam-A", questions: [], counts: { rule: 0, ai: 1, human: 0 }, snapshotFailures: 0 } }) });
    const planSummary = rendered.widgets.find(entry => entry.name === "GradingAIPlanSummary")!.props;
    await act(async () => (planSummary.onConfigure as () => void)());
    expect(props.scoring.setPlanOpen).toHaveBeenLastCalledWith(false);
    const modal = rendered.widgets.filter(entry => entry.name === "Modal" && entry.props.title === "配置本场大题 AI 评分").slice(-1)[0]!.props;
    await act(async () => (modal.onCancel as () => void)());
    expect(props.scoring.setPlanOpen).toHaveBeenLastCalledWith(true);
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
    await render({ showOverview: false, showSettings: true, currentQuestionId: "current-question", scoring: controller({ summary: summary() }) });
    click("进入试评");
    expect(props.onOpenCalibration).toHaveBeenLastCalledWith("current-question");
    await render({ currentQuestionId: "" });
    click("进入试评");
    expect(props.onOpenCalibration).toHaveBeenLastCalledWith("first-question");
    await render({ canManageTasks: false });
    expect(button("管理标准卷")).toBeUndefined();
    expect(button("进入试评")).toBeUndefined();
  });

  it("only shows preparation blockers before a run starts", async () => {
    const checks: ExamScoringController["blockingChecks"] = [
      { code: "rubric_ready", passed: false, severity: "blocker", label: "细则缺失", message: "请完善评分细则" }
    ];
    await render({ scoring: controller({ blockingChecks: checks }) });
    expect(host.textContent).toContain("请完善评分细则");
    await render({ scoring: controller({ hasUnresolvedRun: true, summary: summary("needs_review"), blockingChecks: checks }) });
    expect(host.textContent).not.toContain("请完善评分细则");
    expect(button("重新处理失败项")).toBeUndefined();
  });

  it("keeps task assignment primary and question progress collapsed", async () => {
    const current = summary("needs_review");
    current.run = { ...current.run, total_count: 19, queued_count: 0, review_count: 16, auto_confirmed_count: 3, human_confirmed_count: 0 };
    current.questions = [{ ...current.questions[0]!, question_type: "single_choice", review: 11 }];
    const onOpenAssignments = vi.fn();
    const onOpenResults = vi.fn();
    await render({ scoring: controller({ summary: current, hasUnresolvedRun: true }), onOpenAssignments, onOpenResults });
    expect(host.querySelector("h1")?.textContent).toBe("阅卷概览");
    expect(host.textContent).toContain("共 19 道题 · 已确认 3 · 待处理 16");
    expect(host.querySelector('[aria-label="待确认事项"]')?.textContent).toContain("待确认16 道题");
    expect(host.textContent).not.toContain("选择题核对");
    expect(host.textContent).not.toContain("主观题确认");
    expect(host.textContent).not.toContain("建议分确认");
    expect(rendered.tables.filter(table => table.rowKey === "question_id")).toHaveLength(0);
    click("安排阅卷教师");
    expect(onOpenAssignments).toHaveBeenCalledOnce();
    click("查看答卷结果");
    expect(onOpenResults).toHaveBeenCalledWith("all");
    await act(async () => click("各题进度"));
    expect(rendered.tables.filter(table => table.rowKey === "question_id")).toHaveLength(1);
    expect(host.querySelector("details")?.open).toBe(false);
    expect(rendered.fast).toHaveLength(0);
  });

  it("管理者只在对应专页显示答卷结果和阅卷设置", async () => {
    await render({ showOverview: false, showFastConfirmEntry: false, scoring: controller({ summary: summary(), detailOpen: true }) });
    expect(host.querySelector(".grading-overview")).toBeNull();
    expect(host.querySelector('[aria-label="答卷结果"]')).toBeNull();
    expect(host.querySelector('[aria-label="阅卷设置"]')).toBeNull();
    expect(rendered.widgets.some(entry => entry.name === "Drawer" && entry.props.title === "答卷结果")).toBe(false);
    await render({ showResults: true });
    expect(host.querySelector('section[aria-label="答卷结果"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="阅卷设置"]')).toBeNull();
    expect(rendered.widgets.some(entry => entry.name === "Drawer" && entry.props.title === "答卷结果")).toBe(false);
    await render({ showResults: false, showSettings: true });
    expect(host.querySelector('[aria-label="答卷结果"]')).toBeNull();
    expect(host.querySelector('[aria-label="阅卷设置"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="阅卷设置"]')?.closest("details")).toBeNull();
    click("管理标准卷");
    expect(props.onOpenGoldPapers).toHaveBeenCalledOnce();
    click("进入试评");
    expect(props.onOpenCalibration).toHaveBeenCalledWith("first-question");
    await render({ canManageTasks: false });
    expect(host.querySelector('[aria-label="阅卷设置"]')).toBeNull();
  });

  it("retains FastConfirm state across unrelated renders and resets only on exam key change", async () => {
    await render({ tasks: [{ assigned_to: "teacher", exam_id: "exam-A", status: "assigned" }] as ReviewTask[] });
    const first = rendered.fast[rendered.fast.length - 1]!;
    await render({ tasks: [{ assigned_to: "teacher", exam_id: "exam-A", status: "assigned" }] as ReviewTask[], scoring: controller({ loading: true }) });
    expect(rendered.fast[rendered.fast.length - 1]?.token).toBe(first.token);
    await render({ initialExamId: "exam-B", tasks: [{ assigned_to: "teacher", exam_id: "exam-B", status: "assigned" }] as ReviewTask[] });
    expect(rendered.fast[rendered.fast.length - 1]?.token).not.toBe(first.token);
    expect(rendered.fast[rendered.fast.length - 1]?.props.examId).toBe("exam-B");
  });

  it("derives FastConfirm's exam from the first own task, without granting scoring controls", async () => {
    await render({ initialExamId: "", canGrade: false, tasks: [{ assigned_to: "other", exam_id: "other" }, { assigned_to: "teacher", exam_id: "own-exam", status: "assigned" }] as ReviewTask[] });
    expect(rendered.fast[rendered.fast.length - 1]?.props.examId).toBe("own-exam");
    expect(button("开始评分")).toBeUndefined();
    await (rendered.fast[rendered.fast.length - 1]!.props.onScoringChanged as () => Promise<void>)();
    expect(props.scoring.loadSummary).not.toHaveBeenCalled();
  });

  it("keeps the final fast-confirm receipt mounted after its own queue becomes empty", async () => {
    await render({ initialExamId: "", canGrade: false, tasks: [{ exam_id: "exam-A", assigned_to: "teacher", status: "assigned" } as ReviewTask] });
    const token = host.querySelector("[data-fast-token]")?.getAttribute("data-fast-token");
    await render({ tasks: [] });
    expect(host.querySelector("[data-fast-token]")?.getAttribute("data-fast-token")).toBe(token);
    expect(host.querySelector("[data-fast-token]")?.textContent).toBe("exam-A");
    expect(rendered.fast[rendered.fast.length - 1]?.props.tasks).toEqual([]);
    expect(props.scoring.loadSummary).not.toHaveBeenCalled();
  });

  it.each(["different-user", "new-session"])("does not reuse an empty-queue receipt in a %s", async (change) => {
    await render({ initialExamId: "", canGrade: false, tasks: [{ exam_id: "exam-A", assigned_to: "teacher", status: "assigned" } as ReviewTask] });
    await render({ tasks: [] });
    if (change === "new-session") setBusinessCommandScope("tenant", "teacher");
    await render(change === "different-user" ? { currentUserId: "other" } : {});
    expect(host.querySelector("[data-fast-token]")).toBeNull();
  });

  it("preserves result row identity, pagination, filter setters and callback return values", async () => {
    const item = { answer_segment_id: "segment-A", question_no: "Q1", question_type: "numeric", score: 0, max_score: 5, recognized_answer: "0" } as ScoringRunItem;
    await render({ showOverview: false, showResults: true, scoring: controller({ filteredItems: [item], runDetail: { items: [item] }, resultReviewFilter: "recognition", detailOpen: true }) });
    const table = rendered.tables.find((entry) => entry.className === "automation-result-table dense-data-table")!;
    expect(table.rowKey).toBe("answer_segment_id");
    expect(table.dataSource).toBe(props.scoring.filteredItems);
    expect(table.pagination).toMatchObject({ pageSize: 15, showSizeChanger: true });
    expect(host.querySelector('section[aria-label="答卷结果"]')).not.toBeNull();
    expect(rendered.widgets.some(entry => entry.name === "Drawer" && entry.props.title === "答卷结果")).toBe(false);
    const input = rendered.widgets.find((entry) => entry.name === "Input")!.props;
    (input.onChange as (event: { target: { value: string } }) => void)({ target: { value: " 0 " } });
    expect(props.scoring.setResultKeyword).toHaveBeenCalledWith(" 0 ");
    const typeFilter = rendered.widgets.find(entry => entry.name === "Segmented" && entry.props["aria-label"] === "按题型筛选")!.props;
    (typeFilter.onChange as (value: string) => void)("fill");
    expect(props.scoring.setResultType).toHaveBeenCalledWith("fill");
    const reviewFilter = rendered.widgets.find(entry => entry.name === "Select" && entry.props["aria-label"] === "按待办原因筛选")!.props;
    (reviewFilter.onChange as (value: string) => void)("recognition");
    expect(props.scoring.setResultReviewFilter).toHaveBeenCalledWith("recognition");
    const stateFilter = rendered.widgets.find(entry => entry.name === "Select" && entry.props["aria-label"] === "按处理状态筛选")!.props;
    (stateFilter.onChange as (value: string) => void)("review");
    expect(props.scoring.setResultState).toHaveBeenCalledWith("review");
    click("清除筛选");
    expect(props.scoring.setResultType).toHaveBeenLastCalledWith("all");
    expect(props.scoring.setResultState).toHaveBeenLastCalledWith("all");
    expect(props.scoring.setResultReviewFilter).toHaveBeenLastCalledWith("all");
    expect(props.scoring.setResultKeyword).toHaveBeenLastCalledWith("");
    const columns = table.columns as Array<{ title: string; render?: (value: unknown, row: ScoringRunItem) => ReactNode }>;
    await act(async () => root.render(<>{columns.find((column) => column.title === "操作")!.render!(undefined, item)}</>));
    expect(click("查看作答与评分")).toBeUndefined();
    expect(props.scoring.showImage).toHaveBeenCalledWith(item);
  });

  it("整卷影像和识别置信度由教师主动展开", async () => {
    await render({ showOverview: false, showResults: true, scoring: controller({ detailOpen: true }) });
    const columns = () => rendered.tables[rendered.tables.length - 1]!.columns as Array<{ title: string }>;
    expect(rendered.widgets.some(entry => entry.name === "ScoringPaperMonitor")).toBe(false);
    expect(columns().some(column => column.title === "识别置信度")).toBe(false);
    await act(async () => click("显示识别详情"));
    expect(columns().some(column => column.title === "识别置信度")).toBe(true);
    expect(rendered.widgets.some(entry => entry.name === "ScoringPaperMonitor")).toBe(false);
    await act(async () => click("查看整卷与评分标记"));
    const monitor = rendered.widgets.find(entry => entry.name === "ScoringPaperMonitor")!.props;
    expect((monitor.onRefresh as () => unknown)()).toBeUndefined();
    expect(props.scoring.showDetail).toHaveBeenCalledOnce();
  });

  it("保留普通阅卷者的结果抽屉与独立的单题图片抽屉", async () => {
    const item = { answer_segment_id: "segment-A", question_no: "Q1" } as ScoringRunItem;
    await render({ showOverview: false, canManageTasks: false, scoring: controller({ detailOpen: true, image: { url: "blob:answer", contentType: "image/png", filename: "answer.png", title: "第 Q1 题 · 作答详情", item } }) });
    const resultsDrawer = rendered.widgets.find(entry => entry.name === "Drawer" && entry.props.title === "答卷结果")!.props;
    expect(resultsDrawer.open).toBe(true);
    expect(resultsDrawer.destroyOnHidden).toBe(false);
    (resultsDrawer.onClose as () => void)();
    expect(props.scoring.setDetailOpen).toHaveBeenCalledWith(false);
    const imageDrawer = rendered.widgets.find(entry => entry.name === "Drawer" && entry.props.title === "第 Q1 题 · 作答详情")!.props;
    expect(imageDrawer.open).toBe(true);
    expect(imageDrawer.destroyOnHidden).toBe(true);
    (imageDrawer.onClose as () => void)();
    expect(props.scoring.closeImage).toHaveBeenCalledOnce();
  });

  it("keeps historical AI diagnostics out of the overview and only retries actual failures", async () => {
    const current = summary("failed");
    current.run.failed_count = 2;
    await render({ scoring: controller({ summary: current, hasUnresolvedRun: true, aiIssue: "内部历史诊断" }) });
    expect(host.querySelector("h1")?.textContent).toBe("阅卷概览");
    expect(host.querySelector('[role="status"]')?.textContent).toContain("2 道题处理失败");
    expect(host.textContent).not.toContain("内部历史诊断");
    click("重新处理失败项");
    expect(props.scoring.retry).toHaveBeenCalledOnce();
    await render({ showOverview: false });
    expect(host.querySelector(".grading-overview")).toBeNull();
  });

  it.each([["cancelled", "本次评分已取消"], ["cancelling", "正在取消本次评分"]])("shows the actual %s state", async (status, text) => {
    await render({ scoring: controller({ summary: summary(status), hasUnresolvedRun: true }) });
    expect(host.querySelector("h1")?.textContent).toBe("阅卷概览");
    expect(host.querySelector('[role="status"]')?.textContent).toBe(text);
  });
});
