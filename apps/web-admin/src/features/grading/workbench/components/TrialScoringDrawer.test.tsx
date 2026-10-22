// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AssessmentSnapshot } from "../../../../api/assessment";
import type { ScoringRunItem } from "../../../../api/review";
import type { CreateScoringAITrialInput, ScoringTrial, ScoringTrialSample } from "../../../../api/scoringTrials";
import type { useScoringTrials } from "../hooks/useScoringTrials";
import { TrialScoringDrawer } from "./TrialScoringDrawer";

const mocks = vi.hoisted(() => ({ hook: vi.fn(), create: vi.fn(), decide: vi.fn(), resume: vi.fn(), refresh: vi.fn(), selectTrial: vi.fn(), setQuestionId: vi.fn() }));
vi.mock("../hooks/useScoringTrials", () => ({ useScoringTrials: mocks.hook }));
vi.mock("../../../../api/review", () => ({ downloadScoringResultImage: vi.fn() }));
vi.mock("../../../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: ReactNode }) => <div>{children}</div> }));
vi.mock("./FrozenQuestionReference", () => ({ referenceText: (value: unknown) => value === undefined ? "" : JSON.stringify(value) }));
vi.mock("antd", () => ({
  Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <div>{message}{description}</div>,
  Drawer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => <button type="button" disabled={disabled} onClick={onClick}>{children}</button>,
  InputNumber: (props: { "aria-label": string; value?: number; min?: number; max?: number; disabled?: boolean; onChange: (value: number | null) => void }) => <input type="number" aria-label={props["aria-label"]} value={props.value ?? ""} min={props.min} max={props.max} disabled={props.disabled} onChange={event => props.onChange(event.target.value === "" ? null : Number(event.target.value))} />
}));

const samples: ScoringTrialSample[] = Array.from({ length: 5 }, (_, index) => ({
  segment_id: `segment-${index + 1}`, human_score: 2, max_score: 5,
  roles: index === 0 ? ["representative", "boundary", "incomplete_evidence"] : ["representative"],
  result: { status: "succeeded", suggested_score: 3, evidence: ["本批步骤证据"], risk_flags: [], latency_ms: 120, total_tokens: 30 }
}));

function trial(overrides: Partial<ScoringTrial> = {}): ScoringTrial {
  return {
    id: "trial-1", run_id: "run-1", exam_id: "exam-1", question_id: "q1", command_id: "command-1",
    status: "reviewing", revision: 4, fingerprint: "frozen-v1", sample_batch_id: "batch-1", samples,
    remaining_segment_ids: ["remaining-1", "remaining-2"], created_by: "teacher", created_at: "", updated_at: "", ...overrides
  };
}

function item(index: number, overrides: Partial<ScoringRunItem> = {}): ScoringRunItem {
  return { answer_segment_id: `segment-${index}`, question_id: "q1", question_no: "1", question_type: "essay", state: "review", anonymous_code: `匿名-${index}`, ...overrides } as ScoringRunItem;
}

function controller(overrides: Partial<ReturnType<typeof useScoringTrials>> = {}): ReturnType<typeof useScoringTrials> {
  return {
    items: Array.from({ length: 11 }, (_, index) => item(index + 1)), trials: [], trial: null, trialId: "", selectTrial: mocks.selectTrial,
    questionId: "q1", setQuestionId: mocks.setQuestionId,
    snapshot: { id: "snapshot-1", source_snapshot: { stem: "冻结题干" }, rubric_snapshot: { max_score: 5 }, scoring_policy_snapshot: { mode: "AI_ASSIST" } } as unknown as AssessmentSnapshot,
    snapshotLoading: false, loading: false, busy: false, error: "", pending: null, refresh: mocks.refresh,
    create: mocks.create, decide: mocks.decide, resume: mocks.resume, ...overrides
  };
}

function show(overrides: Partial<ReturnType<typeof useScoringTrials>> = {}) {
  mocks.hook.mockReturnValue(controller(overrides));
  return render(<TrialScoringDrawer examId="exam-1" runId="run-1" scopeKey="tenant:teacher:session:exam-1" onClose={vi.fn()} />);
}

function choose(index: number) { fireEvent.click(screen.getByRole("checkbox", { name: `选择样本 1 · 匿名-${index}` })); }
function save() { fireEvent.click(screen.getByRole("button", { name: "保存人工基准并试改" })); }

beforeEach(() => { vi.resetAllMocks(); });
afterEach(cleanup);

describe("试改样本界面约束（mock hook/UI）", () => {
  it("只列出本题待复核样本，未选满五份或人工基准未填写时不创建", () => {
    show({ items: [
      ...Array.from({ length: 5 }, (_, index) => item(index + 1)),
      item(20, { question_id: "q2", question_no: "2" }), item(21, { state: "confirmed" })
    ] });
    expect(screen.getAllByRole("checkbox", { name: /选择样本/ })).toHaveLength(5);
    expect(screen.queryByRole("checkbox", { name: /匿名-20|匿名-21/ })).toBeNull();
    choose(1); choose(2); choose(3); choose(4); save();
    expect(screen.getByText("请选择 5–10 份不同的同题样本。")).toBeTruthy();
    choose(5); save();
    expect(screen.getByText("请为每份样本填写满分范围内的独立人工分数。")).toBeTruthy();
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it("十份样本后禁用其他选择，取消一份后可继续选择", () => {
    show();
    for (let index = 1; index <= 10; index++) choose(index);
    expect((screen.getByRole("checkbox", { name: "选择样本 1 · 匿名-11" }) as HTMLInputElement).disabled).toBe(true);
    choose(1);
    expect((screen.getByRole("checkbox", { name: "选择样本 1 · 匿名-11" }) as HTMLInputElement).disabled).toBe(false);
    choose(11);
    expect(screen.getByText(/已选 10 \/ 11 份/)).toBeTruthy();
  });

  it("保留人工基准独立输入，三类样本覆盖完整后只提交所选五份", () => {
    show();
    for (let index = 1; index <= 5; index++) {
      choose(index);
      fireEvent.change(screen.getByRole("spinbutton", { name: `人工基准 1 · 匿名-${index}` }), { target: { value: "2" } });
    }
    save();
    expect(screen.getByText("请标注样本类型，并覆盖代表答案、边界答案和证据不全三类。")).toBeTruthy();
    screen.getAllByRole("checkbox", { name: "代表答案" }).forEach(checkbox => fireEvent.click(checkbox));
    fireEvent.click(screen.getAllByRole("checkbox", { name: "边界答案" })[0]);
    fireEvent.click(screen.getAllByRole("checkbox", { name: "证据不全" })[0]);
    save();
    expect(mocks.create).toHaveBeenCalledExactlyOnceWith({ question_id: "q1", samples: samples.map(({ segment_id, human_score, roles }) => ({ segment_id, human_score, roles })) });
  });

  it("有待确认命令时锁定样本编辑，继续按钮发送原命令而不重新校验后来的选择", () => {
    const pending: CreateScoringAITrialInput = { command_id: "original-command", question_id: "q1", samples: samples.map(({ segment_id, human_score, roles }) => ({ segment_id, human_score, roles })) };
    show({ pending });
    expect((screen.getByRole("combobox", { name: "试改题目" }) as HTMLSelectElement).disabled).toBe(true);
    expect(screen.getAllByRole("checkbox", { name: /选择样本/ }).every(element => (element as HTMLInputElement).disabled)).toBe(true);
    expect(screen.getAllByRole("spinbutton").every(element => (element as HTMLInputElement).disabled)).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "继续确认原试改命令" }));
    expect(mocks.create).toHaveBeenCalledExactlyOnceWith(pending);
  });
});

describe("试改复核与决策界面（mock hook/UI）", () => {
  it.each(["queued", "processing", "future_status", undefined])("任一样本为 %s 时不可扩批，负责人仍可说明后停止", status => {
    const current = trial({ samples: samples.map((sample, index) => index === 4 ? { ...sample, result: status === undefined ? undefined : { status, evidence: [], risk_flags: [] } } : sample) });
    show({ trial: current, trials: [current], trialId: current.id });
    const expand = screen.getByRole("button", { name: "确认并扩批本题" }) as HTMLButtonElement;
    const stop = screen.getByRole("button", { name: "停止试改" }) as HTMLButtonElement;
    expect(stop.disabled).toBe(true);
    fireEvent.change(screen.getByRole("textbox", { name: "负责人核对说明" }), { target: { value: "  第五份等待核对，停止继续调用  " } });
    expect(expand.disabled).toBe(true);
    expect(stop.disabled).toBe(false);
    fireEvent.click(stop);
    expect(mocks.decide).toHaveBeenCalledExactlyOnceWith(current, "stop", "第五份等待核对，停止继续调用");
  });

  it.each(["succeeded", "failed", "conflict"])("逐样本终态包含 %s 且填写说明才允许扩批本题", status => {
    const current = trial({ samples: samples.map((sample, index) => index === 4 ? { ...sample, result: { status, evidence: [], risk_flags: [] } } : sample) });
    show({ trial: current, trials: [current], trialId: current.id });
    const expand = screen.getByRole("button", { name: "确认并扩批本题" }) as HTMLButtonElement;
    expect(expand.disabled).toBe(true);
    fireEvent.change(screen.getByRole("textbox", { name: "负责人核对说明" }), { target: { value: "  已核对本批证据及差异  " } });
    expect(expand.disabled).toBe(false);
    fireEvent.click(expand);
    expect(mocks.decide).toHaveBeenCalledExactlyOnceWith(current, "expand", "已核对本批证据及差异");
    expect(screen.getByText(/拟扩批：本题剩余 2 份/)).toBeTruthy();
  });

  it.each([
    { status: "running" as const, remaining: ["remaining"] },
    { status: "awaiting_enqueue" as const, remaining: ["remaining"] },
    { status: "reviewing" as const, remaining: [] }
  ])("状态或剩余范围不满足条件时禁止扩批：$status/$remaining", ({ status, remaining }) => {
    const current = trial({ status, remaining_segment_ids: remaining });
    show({ trial: current, trials: [current], trialId: current.id });
    fireEvent.change(screen.getByRole("textbox", { name: "负责人核对说明" }), { target: { value: "已核对" } });
    expect((screen.getByRole("button", { name: "确认并扩批本题" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it.each(["stopped", "invalidated", "expanded"] as const)("%s 试改不再提供新的停止或扩批决定", status => {
    const current = trial({ status });
    show({ trial: current, trials: [current], trialId: current.id });
    expect(screen.queryByRole("button", { name: "停止试改" })).toBeNull();
    expect(screen.queryByRole("button", { name: "确认并扩批本题" })).toBeNull();
    if (status === "expanded") {
      fireEvent.click(screen.getByRole("button", { name: "继续安排已批准扩批" }));
      expect(mocks.resume).toHaveBeenCalledExactlyOnceWith(current);
    }
  });

  it("只展示本次试改逐样本建议，全场正式分数不能填补缺失结果", () => {
    const current = trial({ samples: samples.map((sample, index) => index === 4 ? { ...sample, result: undefined } : sample) });
    show({ trial: current, trials: [current], trialId: current.id, items: samples.map((_, index) => item(index + 1, { score: 5 })) });
    const articles = screen.getAllByRole("article");
    expect(articles).toHaveLength(5);
    expect(within(articles[0]).getByText(/人工基准 2 \/ 5 · AI 建议 3 · 分差 \+1/)).toBeTruthy();
    expect(within(articles[4]).getByText(/人工基准 2 \/ 5 · AI 建议 未知 · 分差 未知/)).toBeTruthy();
    expect(within(articles[4]).getByText("尚无本次建议证据，请人工核对。")).toBeTruthy();
    expect(screen.getAllByText("[\"本批步骤证据\"]")).toHaveLength(4);
  });
});
