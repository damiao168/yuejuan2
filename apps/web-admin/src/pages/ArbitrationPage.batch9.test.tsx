// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArbitrationTask, FinalGrade } from "../api/review";
import type { SessionUser } from "../auth/session";
import { ArbitrationPage } from "./ArbitrationPage";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), submit: vi.fn(), assign: vi.fn(), exams: vi.fn(), questions: vi.fn(), audit: vi.fn(),
  message: { error: vi.fn(), warning: vi.fn(), success: vi.fn() }, confirm: vi.fn(),
  controls: new Map<string, Record<string, unknown>>(), auditProps: null as Record<string, unknown> | null,
  query: { status: "", task: "" }, guard: vi.fn()
}));

vi.mock("../api/review", () => ({ listArbitrationTasks: mocks.list, getArbitrationTask: mocks.get, submitArbitration: mocks.submit, assignArbitrationTask: mocks.assign }));
vi.mock("../api/exams", () => ({ listExams: mocks.exams }));
vi.mock("../api/papers", () => ({ listQuestions: mocks.questions }));
vi.mock("../api/audit", () => ({ listAuditLogs: mocks.audit }));
vi.mock("../router/query", () => ({ hashQueryParam: (key: "status" | "task") => mocks.query[key] }));
vi.mock("../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: mocks.guard }));
vi.mock("../features/gold-papers", () => ({ GoldCoverageGaps: () => null }));
vi.mock("../features/answer-groups", () => ({ AnswerGroupingDrawer: () => null }));
vi.mock("./ArbitrationPanels", () => ({
  ArbitrationAiTab: () => null, ArbitrationContextTab: () => null, ArbitrationRubricTab: () => null, ArbitrationScoreComparison: () => null,
  ArbitrationAuditList: (props: Record<string, unknown>) => { mocks.auditProps = props; return null; }
}));
vi.mock("../components/PageState", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", { "data-state": "loading" }, label),
    ErrorState: ({ message }: { message: string }) => React.createElement("div", { "data-state": "error" }, message),
    EmptyState: ({ title }: { title: string }) => React.createElement("div", { "data-state": "empty" }, title)
  };
});
vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  function TextInput(props: Record<string, unknown>) {
    mocks.controls.set(String(props.placeholder), props);
    return React.createElement(props.rows ? "textarea" : "input", { "aria-label": String(props.placeholder), value: String(props.value ?? ""), disabled: Boolean(props.disabled), readOnly: true });
  }
  const Input = Object.assign(TextInput, { TextArea: TextInput });
  const Descriptions = Object.assign(({ children }: { children: ReactNode }) => React.createElement("div", null, children), { Item: ({ children }: { children: ReactNode }) => React.createElement("span", null, children) });
  return {
    App: { useApp: () => ({ message: mocks.message, modal: { confirm: mocks.confirm } }) },
    Alert: () => null, Input, InputNumber: TextInput, Descriptions, Tabs: () => null,
    Select: (props: Record<string, unknown>) => { const options = props.options as Array<{ value: string }>; mocks.controls.set(options[0].value === "mine" ? "scope" : "status", props); return null; },
    Button: (props: Record<string, unknown>) => { mocks.controls.set(String(props.children), props); return React.createElement("button", { disabled: Boolean(props.disabled), onClick: props.onClick as () => void }, props.children as ReactNode); },
    Space: ({ children }: { children: ReactNode }) => React.createElement("div", null, children),
    Tag: ({ children }: { children: ReactNode }) => React.createElement("span", null, children)
  };
});

function task(id: string, overrides: Partial<ArbitrationTask> = {}): ArbitrationTask {
  return { id, exam_id: "exam", question_id: `question-${id}`, question_no: id, anonymous_code: id, status: "assigned", assigned_to: "teacher", revision: 3, first_score: 3, second_score: 5, score_difference: 2, reason: `${id} 评分依据`, context: { raw_answer: `${id} 原文`, ocr_text: `${id} 识别`, question: { id: `question-${id}`, exam_id: "exam", question_no: id, question_type: "short_answer", score: 10 }, rubric: { id: "rubric", question_id: `question-${id}`, version: "frozen-v1", status: "approved", max_score: 10, points: [] } }, ...overrides } as ArbitrationTask;
}
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: Error) => void; const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; }); return { promise, resolve, reject }; }

describe("batch9 arbitration common old/current workflow (mock UI/API)", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.resetAllMocks(); mocks.controls.clear(); mocks.auditProps = null; mocks.query.status = ""; mocks.query.task = "";
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.list.mockResolvedValue({ arbitration_tasks: [task("A"), task("B")], has_more: false });
    mocks.get.mockImplementation(async (id: string) => ({ arbitration_task: task(id) }));
    mocks.exams.mockResolvedValue({ exams: [{ id: "exam", name: "模拟考试" }] });
    mocks.questions.mockResolvedValue({ questions: [{ id: "question-A", score: 10 }, { id: "question-B", score: 10 }] });
    mocks.audit.mockResolvedValue({ audit_logs: [] });
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(overrides: Partial<Parameters<typeof ArbitrationPage>[0]> = {}) {
    await act(async () => root.render(<ArbitrationPage canAssign canWork canReadAudit={false} canReadExams currentUser={{ id: "teacher" } as SessionUser} {...overrides} />));
  }
  function control(label: string) { const value = mocks.controls.get(label); expect(value, label).toBeDefined(); return value!; }
  function change(label: string, value: unknown) { return (control(label).onChange as (next: unknown) => unknown)(value); }
  function click(label: string) { return (control(label).onClick as () => unknown)(); }
  function select(id: string) { const button = [...host.querySelectorAll<HTMLButtonElement>(".arbitration-task-item")].find((node) => node.querySelector(".arbitration-task-meta > span:first-child")?.textContent?.startsWith(`${id} ·`)); expect(button).toBeDefined(); button!.click(); }

  it("ignores A's late detail after B becomes current", async () => {
    const pending = deferred<{ arbitration_task: ArbitrationTask }>();
    mocks.get.mockImplementation((id: string) => id === "A" ? pending.promise : Promise.resolve({ arbitration_task: task(id, { final_score: 5 }) }));
    await render(); await act(async () => select("B"));
    expect(host.querySelector(".arbitration-context-row")?.textContent).toContain("B");
    await act(async () => pending.resolve({ arbitration_task: task("A", { final_score: 1 }) }));
    expect(control("最终分").value).toBe(5); expect(control("仲裁说明").value).toBe("B 评分依据");
  });

  it("reads standards with the task and ignores late audit after switching tasks", async () => {
    await render(); await act(async () => select("B"));
    expect(mocks.questions).not.toHaveBeenCalled();
    expect(control("最终分").max).toBe(10);
    const audit = deferred<{ audit_logs: Array<{ id: string; created_at: string }> }>();
    mocks.audit.mockImplementation(({ target_id }: { target_id: string }) => target_id === "B" ? audit.promise : Promise.resolve({ audit_logs: [{ id: "audit-A", created_at: "2026-10-04" }] }));
    await render({ canReadAudit: true });
    await act(async () => select("A"));
    await act(async () => audit.resolve({ audit_logs: [{ id: "audit-B", created_at: "2026-10-05" }] }));
    expect(mocks.auditProps?.logs).toEqual([{ id: "audit-A", created_at: "2026-10-04" }]);
  });

  it.each([task("deep", { exam_id: "other-exam" }), task("deep", { assigned_to: "other" })])("rejects an out-of-scope deep link before selecting it", async (outside) => {
    mocks.query.task = "deep"; mocks.get.mockResolvedValue({ arbitration_task: outside });
    await render({ initialExamId: "exam", personalScope: true });
    expect(mocks.get).toHaveBeenCalledWith("deep"); expect(host.querySelector(".arbitration-task-item")).toBeNull();
    expect(host.querySelector('.arbitration-queue-panel [data-state="error"]')).not.toBeNull();
  });

  it("includes an allowed deep-linked own task missing from the first page", async () => {
    mocks.query.task = "deep"; mocks.list.mockResolvedValue({ arbitration_tasks: [task("A")], has_more: false });
    await render({ initialExamId: "exam", personalScope: true });
    expect(mocks.list).toHaveBeenCalledWith(expect.objectContaining({ assigned_to: "teacher", exam_id: "exam", limit: 50 }));
    expect(host.querySelector(".arbitration-context-row")?.textContent).toContain("deep");
    expect(host.querySelectorAll(".arbitration-task-item")).toHaveLength(2);
  });

  it("merges pagination by task id while retaining existing button identity", async () => {
    mocks.list.mockResolvedValueOnce({ arbitration_tasks: [task("A"), task("B")], has_more: true, next_cursor: "cursor-1" });
    mocks.list.mockResolvedValueOnce({ arbitration_tasks: [task("B", { first_score: 7 }), task("C")], has_more: false });
    await render(); const before = host.querySelectorAll(".arbitration-task-item")[1];
    await act(async () => { expect(click("加载更多仲裁任务")).toBeUndefined(); });
    expect(mocks.list).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: "cursor-1", assigned_to: "teacher" }));
    expect(host.querySelectorAll(".arbitration-task-item")).toHaveLength(3);
    expect(host.querySelectorAll(".arbitration-task-item")[1]).toBe(before); expect(before.textContent).toContain("A 7 / B 5");
  });

  it("does not apply a late mine response after the all-scope response", async () => {
    const pending = deferred<{ arbitration_tasks: ArbitrationTask[]; has_more: boolean }>();
    mocks.list.mockReturnValueOnce(pending.promise).mockResolvedValueOnce({ arbitration_tasks: [task("B")], has_more: false });
    await render(); await act(async () => { change("scope", "all"); });
    expect(mocks.list).toHaveBeenLastCalledWith(expect.objectContaining({ assigned_to: undefined }));
    await act(async () => pending.resolve({ arbitration_tasks: [task("A")], has_more: false }));
    expect(host.querySelector(".arbitration-context-row")?.textContent).toContain("B");
    expect(host.querySelectorAll(".arbitration-task-item")).toHaveLength(1);
  });

  it("requires an explicit score and submits zero with the current revision and trimmed text", async () => {
    await render(); await act(async () => { expect(click("提交仲裁")).toBeUndefined(); });
    expect(mocks.submit).not.toHaveBeenCalled(); expect(mocks.message.error).toHaveBeenCalledWith("请输入仲裁最终分");
    await act(async () => { change("最终分", 0); change("仲裁说明", { target: { value: "  明确零分  " } }); change("学生可见反馈", { target: { value: "  反馈  " } }); });
    mocks.submit.mockResolvedValue({ arbitration_task: task("A", { status: "submitted", final_score: 0 }), final_grade: { score: 0, max_score: 10, source: "arbitration", locked: true } as FinalGrade });
    await act(async () => { expect(click("提交仲裁")).toBeUndefined(); });
    expect(mocks.submit).toHaveBeenCalledWith("A", { final_score: 0, reason: "明确零分", student_feedback: "反馈", expected_revision: 3 });
    expect(host.querySelector(".final-grade-result")?.textContent).toContain("0 / 10");
  });

  it("keeps controls mounted and retains the edited draft on failed submission", async () => {
    await render(); const input = host.querySelector<HTMLInputElement>('[aria-label="最终分"]')!; input.focus();
    await act(async () => { change("最终分", 6); });
    expect(host.querySelector('[aria-label="最终分"]')).toBe(input); expect(document.activeElement).toBe(input);
    const pending = deferred<never>(); mocks.submit.mockReturnValue(pending.promise);
    await act(async () => { click("提交仲裁"); });
    expect(control("最终分").disabled).toBe(true); expect(control("刷新").disabled).toBe(true);
    expect([...host.querySelectorAll<HTMLButtonElement>(".arbitration-task-item")].every((button) => button.disabled)).toBe(true);
    await act(async () => pending.reject(new Error("network failed")));
    expect(control("最终分").value).toBe(6); expect(control("仲裁说明").value).toBe("A 评分依据"); expect(control("最终分").disabled).toBe(false);
  });

  it("does not discard the draft or select B before the confirmation callback", async () => {
    await render(); await act(async () => { change("最终分", 6); });
    await act(async () => { select("B"); });
    expect(control("最终分").value).toBe(6); expect(mocks.get).not.toHaveBeenCalledWith("B");
    const options = mocks.confirm.mock.calls[0][0] as { onOk: () => unknown };
    await act(async () => { expect(options.onOk()).toBeUndefined(); });
    expect(control("仲裁说明").value).toBe("B 评分依据"); expect(control("最终分").value).toBeNull();
  });

  it("keeps work, audit and exam permissions at their original boundaries", async () => {
    await render({ canAssign: false, canWork: false, canReadAudit: false, canReadExams: false, personalScope: true });
    expect(control("提交仲裁").disabled).toBe(true); expect(mocks.controls.has("分配给我")).toBe(false);
    expect(mocks.questions).not.toHaveBeenCalled(); expect(mocks.audit).not.toHaveBeenCalled(); expect(mocks.exams).not.toHaveBeenCalled();
    expect(host.textContent).toContain("本场考试"); expect(control("最终分").disabled).toBe(false);
  });

  it("普通仲裁教师无需考试管理权限即可使用冻结满分提交", async () => {
    await render({ canAssign: false, canWork: true, canReadExams: false, personalScope: true });
    expect(control("最终分").max).toBe(10); expect(control("提交仲裁").disabled).toBe(false);
    expect(mocks.questions).not.toHaveBeenCalled(); expect(mocks.exams).not.toHaveBeenCalled();
    await act(async () => change("最终分", 7));
    mocks.submit.mockResolvedValue({ arbitration_task: task("A", { status: "submitted", final_score: 7 }), final_grade: { score: 7, max_score: 10, source: "arbitration", locked: true } });
    await act(async () => click("提交仲裁"));
    expect(mocks.submit).toHaveBeenCalledWith("A", expect.objectContaining({ final_score: 7, expected_revision: 3 }));
  });

  it("冻结标准缺失时明确阻断提交，不用考试管理接口替代", async () => {
    const original = task("A");
    mocks.get.mockResolvedValue({ arbitration_task: { ...original, context: { ...original.context, rubric: { ...original.context.rubric!, status: "missing" } } } });
    await render({ canReadExams: false, personalScope: true });
    expect(control("提交仲裁").disabled).toBe(true);
    await act(async () => { change("最终分", 7); click("提交仲裁"); });
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.message.error).toHaveBeenCalledWith("本题冻结评分材料不完整，请联系本场考试负责人补齐后重试");
    expect(mocks.questions).not.toHaveBeenCalled();
  });
});
