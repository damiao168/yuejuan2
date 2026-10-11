// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { QuestionBank, QuestionBankImportPreview, QuestionBankVersion } from "@edugrade/sdk";
import type { Question } from "../../api/papers";
import type { SessionUser } from "../../auth/session";
import { ApiClientError } from "../../api/client";
import { HistoryQuestionImporter } from "./HistoryQuestionImporter";
import { QuestionBankWorkspace } from "./QuestionBankWorkspace";
import { BankQuestionPicker } from "./BankQuestionPicker";

const mocks = vi.hoisted(() => ({
  banks: vi.fn(), items: vi.fn(), versions: vi.fn(), item: vi.fn(), bank: vi.fn(), templates: vi.fn(),
  search: vi.fn(), preview: vi.fn(), confirm: vi.fn(), scoring: vi.fn(), materialize: vi.fn(), readiness: vi.fn(), exam: vi.fn(),
  success: vi.fn(), warning: vi.fn(), error: vi.fn(), upload: vi.fn(), identity: 0,
  forms: [] as { values: Record<string, unknown>; setFieldsValue: ReturnType<typeof vi.fn> }[],
  clickHandlers: new Map<string, (event?: unknown) => unknown>(),
  submit: {} as Record<string, unknown>,
}));
vi.mock("../../api/questionBank", () => ({ questionBankApi: {
  listQuestionBanks: mocks.banks, listQuestionBankItems: mocks.items, searchQuestionBankItems: mocks.search, listQuestionBankVersions: mocks.versions,
  getQuestionBankItem: mocks.item, getQuestionBank: mocks.bank, listQuestionBankRubricTemplates: mocks.templates,
  previewQuestionBankImports: mocks.preview, confirmQuestionBankImportBatch: mocks.confirm,
  updateQuestionBankScoring: mocks.scoring, materializeQuestionBankVersions: mocks.materialize,
} }));
vi.mock("../../api/configuration", () => ({ getExamReadiness: mocks.readiness }));
vi.mock("../../api/exams", () => ({ getExam: mocks.exam }));
vi.mock("../../api/files", () => ({ uploadFile: mocks.upload }));
// 只模拟控件边界，不模拟题库状态机；查询缓存仍使用真实 React Query。
vi.mock("antd", async () => {
  const { createContext, useContext, useRef } = await import("react");
  const checks = createContext({ values: [] as string[], change: (_values: string[]) => {} });
  function Button({ children, onClick, disabled, htmlType = "button", className }: Readonly<{ children?: ReactNode; onClick?: () => void; disabled?: boolean; htmlType?: "button" | "submit"; className?: string }>) {
    if (typeof children === "string" && onClick) mocks.clickHandlers.set(children, onClick);
    return <button type={htmlType} onClick={onClick} disabled={disabled} className={className}>{children}</button>;
  }
  function Input({ autoSize: _auto, ...props }: ComponentProps<"input"> & { autoSize?: unknown }) { return <input {...props} />; }
  Input.TextArea = ({ autoSize: _auto, ...props }: ComponentProps<"textarea"> & { autoSize?: unknown }) => <textarea {...props} />;
  Input.Search = ({ onSearch, placeholder }: Readonly<{ onSearch: (value: string) => void; placeholder?: string }>) => <input placeholder={placeholder} onKeyDown={(event) => { if (event.key === "Enter") onSearch(event.currentTarget.value); }} />;
  function Select({ value, options = [], onChange, ...props }: Readonly<{ value?: string; options?: { value: string; label: ReactNode; disabled?: boolean }[]; onChange?: (value: string) => void; "aria-label"?: string }>) {
    return <select aria-label={props["aria-label"]} value={value ?? ""} onChange={(event) => onChange?.(event.target.value)}><option value="" />{options.map((option) => <option key={option.value} value={option.value} disabled={option.disabled}>{option.label}</option>)}</select>;
  }
  function Checkbox({ value = "", children }: Readonly<{ value?: string; children?: ReactNode }>) {
    const group = useContext(checks);
    return <label><input type="checkbox" value={value} checked={group.values.includes(value)} onChange={(event) => group.change(event.target.checked ? [...group.values, value] : group.values.filter((entry) => entry !== value))} />{children}</label>;
  }
  Checkbox.Group = ({ value, onChange, children }: Readonly<{ value: string[]; onChange: (values: string[]) => void; children: ReactNode }>) => <checks.Provider value={{ values: value, change: onChange }}>{children}</checks.Provider>;
  function Form({ children, onFinish, onValuesChange, disabled, className }: Readonly<{ children: ReactNode; onFinish?: (values: Record<string, unknown>) => void; onValuesChange?: () => void; disabled?: boolean; className?: string }>) {
    return <form className={className} onSubmit={(event) => { event.preventDefault(); onFinish?.(mocks.submit); }} onChange={onValuesChange}><fieldset disabled={disabled}>{children}</fieldset></form>;
  }
  Form.Item = ({ children, label, name, hidden }: Readonly<{ children: ReactNode; label?: string; name?: string | (string | number)[]; hidden?: boolean }>) => <label hidden={hidden} data-field={String(name)}>{label}{children}</label>;
  Form.List = ({ children }: Readonly<{ children: (fields: never[], methods: { add: () => void; remove: () => void }) => ReactNode }>) => children([], { add: () => {}, remove: () => {} });
  Form.useForm = () => {
    const ref = useRef<(typeof mocks.forms)[number] | null>(null);
    if (!ref.current) {
      const values: Record<string, unknown> = {};
      ref.current = { values, setFieldsValue: vi.fn((next: Record<string, unknown>) => Object.assign(values, next)) };
      mocks.forms.push(ref.current);
    }
    return [ref.current];
  };
  Form.useWatch = (key: string, form: (typeof mocks.forms)[number]) => form.values[key];
  function List<T>({ dataSource, renderItem }: Readonly<{ dataSource: T[]; renderItem: (value: T) => ReactNode }>) { return <div>{dataSource.map((value, index) => <div key={index}>{renderItem(value)}</div>)}</div>; }
  List.Item = ({ children }: Readonly<{ children: ReactNode }>) => <div>{children}</div>;
  return {
    App: { useApp: () => ({ message: { success: mocks.success, warning: mocks.warning, error: mocks.error } }) },
    Button, Input, Select, Checkbox, Form, List, Segmented: Select,
    InputNumber: ({ value, onChange }: Readonly<{ value?: number; onChange?: (value: number) => void }>) => <input type="number" value={value ?? ""} onChange={(event) => onChange?.(Number(event.target.value))} />,
    Alert: ({ message, description, action }: Readonly<{ message: ReactNode; description?: ReactNode; action?: ReactNode }>) => <div role="alert">{message}{description}{action}</div>,
    Modal: ({ open, title, children, footer }: Readonly<{ open: boolean; title: string; children: ReactNode; footer: ReactNode }>) => open ? <div role="dialog" aria-label={title}>{children}{footer}</div> : null,
    Space: ({ children }: Readonly<{ children: ReactNode }>) => children,
    Tag: ({ children }: Readonly<{ children: ReactNode }>) => <span>{children}</span>,
    Empty: ({ description }: Readonly<{ description: string }>) => <p>{description}</p>,
    Spin: () => <span>loading</span>, Switch: () => <input type="checkbox" />,
    Pagination: ({ current, onChange }: Readonly<{ current: number; onChange: (page: number) => void }>) => <button type="button" onClick={() => onChange(current + 1)}>下一页版本</button>,
  };
});
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: Readonly<{ children: string }>) => <div data-markdown>{children}</div> }));
vi.mock("../../components/PageState", () => ({ ErrorState: ({ message, onRetry }: Readonly<{ message: string; onRetry: () => void }>) => <div role="alert">{message}<button type="button" onClick={onRetry}>重试</button></div> }));
vi.mock("./ContentFields", () => ({ ContentFields: () => <input aria-label="题干字段" />, gradeScopeLabel: (value: string) => value === "grade_8" ? "八年级" : value }));
vi.mock("./ScoringEditor", async () => {
  const { useState } = await import("react");
  return { ScoringEditor: ({ version, disabled, actorId, canManageFiles }: Readonly<{ version: QuestionBankVersion; disabled: boolean; actorId: string; canManageFiles: boolean }>) => {
    const [identity] = useState(() => ++mocks.identity);
    return <div data-scoring-instance={identity} data-version={version.id} data-actor={actorId} data-files={String(canManageFiles)}><button type="button" disabled={disabled}>编辑评分</button></div>;
  } };
});
vi.mock("../../components/ResponsiveTable", () => ({ ResponsiveTable: ({ dataSource, columns, pagination }: Readonly<{ dataSource: Record<string, unknown>[]; columns: { dataIndex?: string; render?: (value: unknown, row: Record<string, unknown>) => ReactNode }[]; pagination: { current: number; onChange: (page: number) => void } }>) => <div>{dataSource.map((row, rowIndex) => <div key={String(row.id ?? (row.item as { id?: string } | undefined)?.id ?? rowIndex)}>{columns.map((column, index) => <span key={index}>{column.render ? column.render(row[column.dataIndex ?? ""], row) : String(row[column.dataIndex ?? ""] ?? "")}</span>)}</div>)}<button type="button" onClick={() => pagination.onChange(pagination.current + 1)}>下一页题目</button></div> }));

const bank: QuestionBank = { id: "bank", tenant_id: "tenant", school_id: "school", name: "授权题库", description: "", status: "active", revision: 7, created_by: "author", created_at: "2026-01-01", updated_at: "2026-01-01", metadata_schema_version: 3 };
const item = { id: "item", tenant_id: "tenant", bank_id: bank.id, item_code: "Q-1", subject_code: "mathematics" as const, grade_scope: "grade_8", current_published_version_id: "published", status: "active" as const, created_by: "author", created_at: "2026-01-01", kind: "question" as const, revision: 11 };
const version: QuestionBankVersion = { id: "version", tenant_id: "tenant", item_id: item.id, version_no: 1, schema_version: 3, revision: 4, workflow_status: "draft", source_version_id: null, author_id: "author", content_hash: "content", hash_scope: "draft_content_v1", question_type: "single_choice", assessment_archetype: "selected_response", stem: "冻结题干", options: ["甲", "乙"], default_score: 5, knowledge_points: [], metadata: { subject_code: "mathematics", education_stage: "junior", grade_scope: "grade_8", difficulty_band: "unclassified", cognitive_level: "unclassified", copyright: "owned", language: "zh-CN" }, created_at: "2026-01-01", updated_at: "2026-01-01", scoring: { answer: null, solution: null, rubric: null, template_version_id: null, assets: [], use_policy: "practice_only" }, bundle_hash: "bundle", answer_version_id: null, rubric_version_id: null, bundle_schema_version: 2 };
const user: SessionUser = { id: "reviewer", username: "reviewer", displayName: "审核员", name: "审核员", role: "school_admin", roles: ["school_admin"], tenant: "tenant", school: "学校", currentExam: "", permissions: ["question_bank:review", "question_bank:publish", "file:manage"], publicComputer: false, organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] } };
const questions: Question[] = [1, 2].map((number) => ({ id: `q${number}`, tenant_id: "tenant", exam_id: "exam", question_no: String(number), question_type: "single_choice", score: 5, options: [], knowledge_points: [], sort_order: number, status: "draft", stem: `原题${number}` }));
function preview(id: string): QuestionBankImportPreview {
  return { question_id: id, available: true, content: version, scoring: version.scoring, content_hash: "content", bundle_hash: "bundle", source: { exam_id: "exam", question_id: id, question_no: id.slice(1), readiness_snapshot_id: "readiness", configuration_hash: "configuration", snapshot_hash: "snapshot", assessment_snapshot_id: "assessment", assessment_snapshot_hash: "assessment-hash", assessment_snapshot_version: 2, profile_snapshot: {}, archetype_snapshot: {}, scoring_policy_snapshot: {} }, issues: [], duplicate_hints: ["exact_bundle", "similar_content", "same_content_different_scoring"].map((kind, index) => ({ kind: kind as "exact_bundle" | "similar_content" | "same_content_different_scoring", item_id: `existing-${index}`, item_code: `EX-${index}`, version_id: `existing-version-${index}`, version_no: 1, stem_summary: "重复提示", content_hash: "hash", bundle_hash: "bundle" })), suggested_item_code: `CODE-${id}` };
}
function query<T>(data: T) { return { data, isError: false, isLoading: false, error: null, refetch: vi.fn() }; }
type WorkspaceProps = ComponentProps<typeof QuestionBankWorkspace>;
function workspaceProps(): WorkspaceProps {
  return { bank, kind: "question", onKindChange: vi.fn(), canManage: true, openSchema: vi.fn(), openAcl: vi.fn(), items: query({ items: [item], total: 45, limit: 20, offset: 0 }), selectItem: vi.fn(), offset: 0, setOffset: vi.fn(), item, selected: version, versions: query({ versions: [version], total: 45, limit: 20, offset: 0 }), version: query({ version }), versionOffset: 0, setVersionId: vi.fn(), saving: false, loadLatest: vi.fn(), canEdit: true, derive: vi.fn(), canRetire: true, retireItem: vi.fn(), boundSchema: query({ schema: { bank_id: bank.id, version: 3, fields: [], taxonomies: [], created_by: "author", created_at: "2026-01-01" } }), editor: {} as WorkspaceProps["editor"], saveVersion: vi.fn(), contentDirty: false, setContentDirty: vi.fn(), scoringDirty: false, conflict: false, editorVersion: version, scoringSaved: vi.fn(), setScoringDirty: vi.fn(), user, comment: "", setComment: vi.fn(), transition: vi.fn(), reviews: query({ reviews: [] }) };
}

describe("question bank batch7 preserved component boundaries", () => {
  let host: HTMLDivElement;
  let root: Root;
  let client: QueryClient;
  beforeEach(() => {
    vi.clearAllMocks(); mocks.forms.length = 0; mocks.identity = 0; mocks.submit = {}; mocks.clickHandlers.clear();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    localStorage.clear();
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    mocks.banks.mockResolvedValue({ banks: [bank], total: 1, limit: 20, offset: 0 });
    mocks.items.mockResolvedValue({ items: [item], total: 45, limit: 20, offset: 0 }); mocks.search.mockResolvedValue({ items: [{ item, version: { ...version, id: "published", workflow_status: "published" }, statistics_available: false }], total: 45, limit: 20, offset: 0 });
    mocks.versions.mockResolvedValue({ versions: [{ ...version, id: "published", workflow_status: "published" }, version], total: 2 });
    mocks.item.mockResolvedValue({ item }); mocks.bank.mockResolvedValue({ bank }); mocks.templates.mockResolvedValue({ items: [], total: 0 });
    mocks.readiness.mockResolvedValue({ readiness: { snapshot_id: "readiness", import_snapshot_available: true } });
    mocks.preview.mockResolvedValue({ items: questions.map((question) => ({ question_id: question.id, preview: preview(question.id), status: "ready", retryable: false })) });
    mocks.confirm.mockImplementation(async ({ body }: { body: { items: { command_id: string; question_id: string }[] } }) => ({ items: body.items.map((entry) => ({ command_id: entry.command_id, question_id: entry.question_id, status: "succeeded", retryable: false })) }));
    mocks.exam.mockResolvedValue({ exam: { revision: 17 } }); mocks.materialize.mockResolvedValue({}); mocks.scoring.mockResolvedValue({ version });
  });
  afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.restoreAllMocks(); });
  async function render(element: ReactNode) { await act(async () => root.render(<QueryClientProvider client={client}>{element}</QueryClientProvider>)); await flush(); }
  async function flush() { await act(async () => { await new Promise((resolve) => setTimeout(resolve, 10)); }); }
  function button(text: string) { const found = [...host.querySelectorAll<HTMLButtonElement>("button")].find((entry) => entry.textContent === text); if (!found) throw new Error(`Missing button ${text}`); return found; }
  async function click(text: string) { await act(async () => button(text).click()); await flush(); }
  async function change(element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
    const prototype = Object.getPrototypeOf(element) as object;
    await act(async () => { Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(element, value); element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? "change" : "input", { bubbles: true })); }); await flush();
  }
  async function importer(onImported = vi.fn()) {
    await render(<HistoryQuestionImporter examId="exam" questions={questions} scopeKey="tenant:actor" onImported={onImported} />);
    await click("精选入题库");
  }
  async function selectQuestions() { for (const check of host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')) await act(async () => check.click()); }
  async function drafts() { await importer(); await selectQuestions(); await click("预览已确认题目"); }
  const storageKey = "question-bank-history-import:tenant:actor:exam";

  it("blocks preview without a frozen source", async () => {
    mocks.readiness.mockResolvedValueOnce({ readiness: { import_snapshot_available: false } });
    await importer(); await selectQuestions();
    expect(button("预览已确认题目").disabled).toBe(true); expect(host.textContent).toContain("没有可保存的已确认试卷内容"); expect(mocks.preview).not.toHaveBeenCalled();
  });
  it("partitions preview failures without discarding successful frozen facts", async () => {
    mocks.preview.mockResolvedValueOnce({ items: [{ question_id: "q1", preview: preview("q1") }, { question_id: "q2", error_code: "source_snapshot_unavailable" }] });
    await drafts(); expect(host.textContent).toContain("完全重复"); expect(host.textContent).toContain("相似题干"); expect(host.textContent).toContain("内容相同、评分不同"); expect(host.textContent).toContain("该题没有完整冻结来源"); expect(host.textContent).not.toContain("assessment-hash"); expect(host.textContent).toContain("查看原卷答案与评分标准"); expect(button("生成 1 个草稿").disabled).toBe(false);
  });
  it("persists exact commands before sending and clears only acknowledged success", async () => {
    await drafts(); mocks.confirm.mockImplementationOnce(async ({ body }) => { expect(JSON.parse(localStorage.getItem(storageKey)!)).toEqual(body); expect(body.items[0]).toMatchObject({ target_bank_id: "bank", expected_target_revision: 7, expected_target_schema_version: 3, source_snapshot_id: "readiness", assessment_snapshot_id: "assessment", scoring_source: "original_exam", mapping: { item_code: "CODE-q1", options: ["甲", "乙"], intended_use: "exam" } }); return { items: body.items.map((entry: { question_id: string; command_id: string }) => ({ ...entry, status: "succeeded", retryable: false })) }; });
    await click("生成 2 个草稿"); expect(localStorage.getItem(storageKey)).toBeNull(); expect(host.textContent).toContain("保存题库的结果"); expect(mocks.success).toHaveBeenCalledWith("2 道题已生成题库草稿");
  });
  it("keeps a synchronous pending identity for consecutive new-item confirmations", async () => {
    await drafts(); mocks.confirm.mockImplementation(() => new Promise(() => {}));
    const confirm = button("生成 2 个草稿");
    await act(async () => { confirm.click(); confirm.click(); });
    expect(mocks.confirm).toHaveBeenCalledTimes(2);
    expect(mocks.confirm.mock.calls[0]![0].body).toEqual(mocks.confirm.mock.calls[1]![0].body);
    expect(JSON.parse(localStorage.getItem(storageKey)!)).toEqual(mocks.confirm.mock.calls[0]![0].body);
  });
  it("uses the linked item's revision for a new version", async () => {
    await drafts(); const saveMode = [...host.querySelectorAll("select")].find((element) => [...element.options].some((option) => option.value === "new_version"))!;
    await change(saveMode, "new_version"); const linked = [...host.querySelectorAll("select")].find((element) => [...element.options].some((option) => option.value === "existing-0"))!;
    expect(button("生成 2 个草稿").disabled).toBe(true); await change(linked, "existing-0"); await click("生成 2 个草稿");
    expect(mocks.item).toHaveBeenCalledWith({ path: { itemId: "existing-0" } }); expect(mocks.confirm.mock.calls[0]![0].body.items[0]).toMatchObject({ dedup_decision: "new_version", existing_item_id: "existing-0", expected_target_revision: 11 }); expect(mocks.confirm.mock.calls[0]![0].body.items[0].mapping).not.toHaveProperty("item_code");
  });
  it("replays uncertain results with the same command IDs and payload", async () => {
    await drafts(); mocks.confirm.mockRejectedValueOnce(new Error("response lost")); await click("生成 2 个草稿"); const first = mocks.confirm.mock.calls[0]![0].body;
    expect(button("重新预览未完成题目").disabled).toBe(true); await click("重试未完成导入"); expect(mocks.confirm.mock.calls[1]![0].body).toEqual(first); expect(localStorage.getItem(storageKey)).toBeNull();
  });
  it("keeps only failed receipt command IDs for retry", async () => {
    await drafts(); mocks.confirm.mockImplementationOnce(async ({ body }) => ({ items: [{ ...body.items[0], status: "succeeded", retryable: false }, { ...body.items[1], status: "failed", retryable: true, error_code: "target_revision_conflict" }] }));
    await click("生成 2 个草稿"); const saved = JSON.parse(localStorage.getItem(storageKey)!); expect(saved.items).toHaveLength(1); expect(saved.items[0].question_id).toBe("q2"); expect(host.textContent).toContain("目标题库已更新，请重新预览（可重试）"); expect(button("重新预览未完成题目").disabled).toBe(false);
    await click("重试未完成导入"); expect(mocks.confirm.mock.calls[1]![0].body).toEqual(saved); expect(localStorage.getItem(storageKey)).toBeNull();
  });
  it("restores pending commands independently of current selected questions", async () => {
    const pending = { items: [{ command_id: "saved-command", question_id: "q2", target_bank_id: "bank", mapping: { options: ["persisted"] } }] }; localStorage.setItem(storageKey, JSON.stringify(pending)); await importer(); await click("重试未完成导入"); expect(mocks.confirm.mock.calls[0]![0].body).toEqual(pending); expect(mocks.preview).not.toHaveBeenCalled();
  });
  it("retains committed success when the parent refresh rejects", async () => {
    const refresh = vi.fn().mockRejectedValue(new Error("refresh failed")); await importer(refresh); await selectQuestions(); await click("预览已确认题目"); await click("生成 2 个草稿"); expect(refresh).toHaveBeenCalledOnce(); expect(mocks.warning).toHaveBeenCalledWith("草稿已保存，列表刷新失败，请手动刷新"); expect(localStorage.getItem(storageKey)).toBeNull();
  });
  it("does not send a new command batch when persistence throws", async () => {
    await drafts(); vi.spyOn(Storage.prototype, "setItem").mockImplementationOnce(() => { throw new Error("storage full"); }); await click("生成 2 个草稿"); expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.error).toHaveBeenCalled();
  });
  it("caps selection at fifty using the existing question identity", async () => {
    const many = Array.from({ length: 51 }, (_, index) => ({ ...questions[0]!, id: `many-${index}`, question_no: String(index) })); await render(<HistoryQuestionImporter examId="exam" questions={many} scopeKey="tenant:actor" onImported={vi.fn()} />); await click("精选入题库"); await selectQuestions(); await click("预览已确认题目"); expect(mocks.preview.mock.calls[0]![0].body.selections).toHaveLength(50);
  });
  it("keeps workspace paging, same-item mobile reopening, and option letters", async () => {
    const actions = { loadLatest: vi.fn(() => Promise.resolve()), derive: vi.fn(() => Promise.resolve()), retireItem: vi.fn(() => Promise.resolve()), transition: vi.fn((_decision: Parameters<WorkspaceProps["transition"]>[0]) => Promise.resolve()) };
    const props = { ...workspaceProps(), ...actions }; await render(<QuestionBankWorkspace {...props} />); expect(host.textContent).toContain("A. 甲"); expect(host.textContent).toContain("B. 乙"); await click("返回题目列表"); expect(host.querySelector(".is-list")).not.toBeNull(); await click("Q-1"); expect(host.querySelector(".is-detail")).not.toBeNull(); expect(props.selectItem).toHaveBeenCalledWith(item); await click("下一页题目"); expect(props.setOffset).toHaveBeenCalledWith(20); await click("下一页版本"); expect(props.setVersionId).toHaveBeenCalledWith("", 20);
    for (const label of ["查看最新版本", "创建新版本", "停止选用", "提交审核"]) {
      let returned: unknown;
      await act(async () => { returned = mocks.clickHandlers.get(label)!({ type: "synthetic-click" }); });
      expect(returned).toBeUndefined();
    }
    expect(actions.loadLatest).toHaveBeenLastCalledWith(); expect(actions.derive).toHaveBeenLastCalledWith(); expect(actions.retireItem).toHaveBeenLastCalledWith(); expect(actions.transition).toHaveBeenLastCalledWith("submit-review");
  });
  it("does not reopen mobile details on a same-item background update and remounts scoring for a different version", async () => {
    const props = workspaceProps(); await render(<QuestionBankWorkspace {...props} />); await click("返回题目列表");
    const identity = host.querySelector("[data-scoring-instance]")!.getAttribute("data-scoring-instance");
    await render(<QuestionBankWorkspace {...props} item={{ ...item, revision: 12 }} />); expect(host.querySelector(".is-list")).not.toBeNull();
    const next = { ...version, id: "second-version" };
    await render(<QuestionBankWorkspace {...props} selected={next} editorVersion={next} />);
    expect(host.querySelector("[data-scoring-instance]")!.getAttribute("data-scoring-instance")).not.toBe(identity);
  });
  it.each(["scoringDirty", "conflict", "saving"] as const)("locks content when %s is set", async (field) => {
    const props = { ...workspaceProps(), [field]: true }; await render(<QuestionBankWorkspace {...props} />); expect(host.querySelector("fieldset")?.disabled).toBe(true); expect(button("提交审核").disabled).toBe(true);
  });
  it("locks scoring while content is dirty and preserves file capability and editor identity", async () => {
    const props = workspaceProps(); await render(<QuestionBankWorkspace {...props} />); const identity = host.querySelector("[data-scoring-instance]")?.getAttribute("data-scoring-instance"); await render(<QuestionBankWorkspace {...props} contentDirty />); expect(button("编辑评分").disabled).toBe(true); expect(host.querySelector("[data-scoring-instance]")?.getAttribute("data-scoring-instance")).toBe(identity); expect(host.querySelector("[data-files=true]")?.getAttribute("data-actor")).toBe(user.id);
    await render(<QuestionBankWorkspace {...props} editorVersion={{ ...version, id: "other" }} />); expect(host.querySelector("[data-scoring-instance]")).toBeNull();
  });
  it("preserves author/reviewer/publisher visibility without broadening return-to-draft checks", async () => {
    const props = workspaceProps(); await render(<QuestionBankWorkspace {...props} selected={{ ...version, workflow_status: "reviewing", author_id: user.id }} />); expect(host.textContent).not.toContain("批准版本"); await click("退回草稿"); expect(props.transition).toHaveBeenCalledWith("return-to-draft"); await render(<QuestionBankWorkspace {...props} selected={{ ...version, workflow_status: "reviewing" }} />); await click("批准版本"); expect(props.transition).toHaveBeenCalledWith("approve"); await render(<QuestionBankWorkspace {...props} selected={{ ...version, workflow_status: "approved" }} />); await click("发布版本"); expect(props.transition).toHaveBeenCalledWith("publish");
    await render(<QuestionBankWorkspace {...props} user={{ ...user, permissions: [] }} selected={{ ...version, workflow_status: "reviewing" }} />); expect(host.textContent).not.toContain("批准版本"); expect(button("退回草稿").disabled).toBe(false);
  });
  it("locks archived or non-draft editors without removing published preview", async () => {
    const props = workspaceProps(); await render(<QuestionBankWorkspace {...props} bank={{ ...bank, status: "archived" }} />); expect(host.querySelector("fieldset")?.disabled).toBe(true); expect(button("创建新版本").disabled).toBe(true); await render(<QuestionBankWorkspace {...props} selected={{ ...version, workflow_status: "published" }} />); expect(host.querySelector("fieldset")?.disabled).toBe(true); expect(host.textContent).toContain("冻结题干"); expect(host.textContent).not.toContain("提交审核");
  });
  it("blocks review submission until an independent authorized reviewer is available", async () => {
    const props = workspaceProps();
    const members = query({ members: [{ user_id: version.author_id, display_name: "出题教师", actions: ["read", "review"] }] });
    await render(<QuestionBankWorkspace {...props} members={members} />);
    expect(button("提交审核").disabled).toBe(true);
    expect(host.textContent).toContain("授权独立审核教师");
    await render(<QuestionBankWorkspace {...props} members={{ ...members, data: { members: [{ user_id: user.id, display_name: "李老师", actions: ["read", "review"] }] } }} />);
    expect(button("提交审核").disabled).toBe(false);
    expect(host.textContent).toContain("请联系审核教师：李老师");
    await click("提交审核"); expect(props.transition).toHaveBeenCalledWith("submit-review");
  });
  it("keeps the draft editable when reviewer lookup fails and offers a local retry", async () => {
    const props = workspaceProps(); const members = { ...query({ members: [] }), isError: true, error: new Error("lookup unavailable") };
    await render(<QuestionBankWorkspace {...props} members={members} />);
    expect(button("提交审核").disabled).toBe(true);
    expect(button("保存草稿").disabled).toBe(false);
    await click("重试人员列表"); expect(members.refetch).toHaveBeenCalledOnce();
    expect(props.transition).not.toHaveBeenCalled();
  });
  it("limits review actions to actual bank members and provides the exact review link", async () => {
    const props = workspaceProps(); const selected = { ...version, workflow_status: "reviewing" as const };
    const members = query({ members: [{ user_id: "another-reviewer", display_name: "王老师", actions: ["read", "review"] }] });
    await render(<QuestionBankWorkspace {...props} selected={selected} members={members} />);
    expect(host.textContent).not.toContain("批准版本"); expect(host.textContent).not.toContain("退回草稿");
    const link = host.querySelector<HTMLInputElement>('input[aria-label="本题审核链接"]')!;
    expect(link.readOnly).toBe(true); expect(link.value).toContain("#/question-bank?bank=bank&item=item&version=version");
    const writeText = vi.fn().mockRejectedValueOnce(new Error("clipboard denied"));
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    await click("复制本题审核链接"); expect(writeText).toHaveBeenCalledWith(link.value);
    expect(mocks.warning).toHaveBeenCalledWith("暂不能自动复制，请选择下方审核链接后复制，并联系审核教师。");
  });
  it("preserves all review labels and retries both failed version queries", async () => {
    const props = workspaceProps(); props.reviews.data = { reviews: ["approve", "publish", "submit-review", "return-to-draft"].map((decision, index) => ({ id: `review-${index}`, version_id: version.id, reviewer_id: user.id, decision: decision as "approve" | "publish" | "submit-review" | "return-to-draft", comment: "", content_revision: index, bundle_hash: "bundle", created_at: "2026-01-01" })) }; await render(<QuestionBankWorkspace {...props} />); expect(host.querySelector("ol")?.textContent).toContain("批准"); expect(host.querySelector("ol")?.textContent).toContain("发布"); expect(host.querySelector("ol")?.textContent).toContain("提交审核"); expect(host.querySelector("ol")?.textContent).toContain("退回草稿");
    await render(<QuestionBankWorkspace {...props} versions={{ ...props.versions, isError: true, error: new Error("versions failed") }} />); await click("重试"); expect(props.versions.refetch).toHaveBeenCalledOnce(); expect(props.version.refetch).toHaveBeenCalledOnce();
  });
  async function picker() { await render(<BankQuestionPicker examId="exam" scopeKey="tenant:actor" nextOrder={3} onCopied={vi.fn()} />); await click("从题库选题"); await change(host.querySelector('select[aria-label="选题题库"]')!, "bank"); await click("预览与历史版本"); await click("加入选题"); await change([...host.querySelectorAll<HTMLInputElement>("input")].find((element) => element.maxLength === 32)!, " 8 "); }
  it("filters active banks and published versions, forwarding cancellation signals and scope keys", async () => {
    mocks.banks.mockResolvedValueOnce({ banks: [bank, { ...bank, id: "archived", status: "archived" }], total: 2 }); await picker(); expect([...host.querySelector('select[aria-label="选题题库"]')!.querySelectorAll("option")].map((entry) => entry.value)).not.toContain("archived"); expect([...host.querySelector('select[aria-label="选用发布版本"]')!.querySelectorAll("option")].map((entry) => entry.value)).not.toContain("version"); expect(host.textContent).toContain("A. 甲"); expect(mocks.search.mock.calls[0]![0].signal).toBeInstanceOf(AbortSignal); expect(client.getQueryCache().getAll().every((entry) => entry.queryKey[1] === "tenant:actor" && entry.queryKey[2] === "exam")).toBe(true);
  });
  it("reuses copy revision and idempotency key after uncertain failure", async () => {
    await picker(); mocks.materialize.mockRejectedValueOnce(new Error("response lost")); await click("复制 1 道题到考试"); await click("继续确认原选题结果"); expect(mocks.exam).toHaveBeenCalledOnce(); expect(mocks.materialize.mock.calls[0]![0]).toEqual(mocks.materialize.mock.calls[1]![0]); expect(mocks.materialize.mock.calls[1]![0].body).toEqual({ expected_revision: 17, selections: [{ version_id: "published", question_no: "8", sort_order: 3 }] });
  });
  it("starts a fresh copy command after a definite revision conflict", async () => {
    await picker(); mocks.materialize.mockRejectedValueOnce(new ApiClientError(409, "conflict", "conflict")); await click("复制 1 道题到考试"); await click("复制 1 道题到考试"); expect(mocks.exam).toHaveBeenCalledTimes(2); expect(mocks.materialize.mock.calls[0]![0].headers["Idempotency-Key"]).not.toBe(mocks.materialize.mock.calls[1]![0].headers["Idempotency-Key"]);
  });
  it("does not display a delayed old-bank response after selecting another bank", async () => {
    let resolveOld!: (value: unknown) => void; mocks.banks.mockResolvedValueOnce({ banks: [bank, { ...bank, id: "new", name: "新题库" }], total: 2 }); mocks.search.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; })).mockResolvedValueOnce({ items: [{ item: { ...item, id: "new-item", item_code: "NEW" }, version, statistics_available: false }], total: 1 });
    await render(<BankQuestionPicker examId="exam" scopeKey="tenant:actor" nextOrder={3} onCopied={vi.fn()} />); await click("从题库选题"); await change(host.querySelector('select[aria-label="选题题库"]')!, "bank"); const signal = mocks.search.mock.calls[0]![0].signal as AbortSignal; await change(host.querySelector('select[aria-label="选题题库"]')!, "new"); await act(async () => resolveOld({ items: [{ item: { ...item, item_code: "OLD-LATE" }, version, statistics_available: false }], total: 1 })); await flush(); expect(signal.aborted).toBe(true); expect(host.textContent).toContain("NEW"); expect(host.textContent).not.toContain("OLD-LATE");
  });
  it("copies a multi-question checklist once and validates exam numbering before writing", async () => {
    mocks.search.mockResolvedValue({ items: [
      { item, version: { ...version, id: "published", workflow_status: "published" }, statistics_available: false },
      { item: { ...item, id: "second", item_code: "Q-2" }, version: { ...version, id: "second-published", item_id: "second", workflow_status: "published" }, statistics_available: false },
    ], total: 2 });
    await render(<BankQuestionPicker examId="exam" scopeKey="tenant:actor" nextOrder={3} onCopied={vi.fn()} />);
    await click("从题库选题"); await change(host.querySelector('select[aria-label="选题题库"]')!, "bank");
    const first = [...host.querySelectorAll<HTMLButtonElement>("button")].find(entry => entry.textContent === "加入选题")!;
    await act(async () => { first.click(); first.click(); }); await flush();
    expect(host.querySelectorAll('input[maxlength="32"]')).toHaveLength(1);
    await click("加入选题"); expect(host.textContent).toContain("选题清单 · 2 道题 · 总分 10 分");
    const second = host.querySelector<HTMLInputElement>('input[aria-label="Q-2考试题号"]')!;
    await change(second, "3"); expect(button("复制 2 道题到考试").disabled).toBe(true); expect(mocks.materialize).not.toHaveBeenCalled();
    await change(second, "4");
    let resolve!: (value: unknown) => void; mocks.materialize.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    const copy = button("复制 2 道题到考试"); await act(async () => { copy.click(); copy.click(); }); await flush();
    expect(mocks.materialize).toHaveBeenCalledOnce();
    expect(mocks.materialize.mock.calls[0]![0].body).toEqual({ expected_revision: 17, selections: [
      { version_id: "published", question_no: "3", sort_order: 3 }, { version_id: "second-published", question_no: "4", sort_order: 4 },
    ] }); await act(async () => resolve({}));
  });
  it.each([403, 422])("allows checklist correction after a definite %s rejection", async status => {
    await picker(); mocks.materialize.mockRejectedValueOnce(new ApiClientError(status, "rejected", "rejected"));
    await click("复制 1 道题到考试"); expect(host.textContent).not.toContain("继续确认原选题结果");
    expect(host.querySelector<HTMLInputElement>('input[maxlength="32"]')!.disabled).toBe(false);
    await click("复制 1 道题到考试"); expect(mocks.exam).toHaveBeenCalledTimes(2);
  });
  it("does not write the old exam after leaving while its revision is loading", async () => {
    await picker(); let resolve!: (value: unknown) => void;
    mocks.exam.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    await click("复制 1 道题到考试"); await render(<div>另一场考试</div>);
    await act(async () => resolve({ exam: { revision: 20 } })); await flush();
    expect(mocks.materialize).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
  });
  async function scoring(answer: unknown = null, revision = 4) {
    const { ScoringEditor } = await vi.importActual<typeof import("./ScoringEditor")>("./ScoringEditor");
    await render(<ScoringEditor version={{ ...version, revision, scoring: { ...version.scoring, answer: { standard_answer: answer, equivalent_answers: [], tolerance: {} } } }} disabled={false} actorId="reviewer" canManageFiles={false} onSaved={vi.fn()} onDirty={vi.fn()} />);
  }
  it.each([[null, ""], [" A ", " A "], [false, "false"], [["A", "B"], '[\n  "A",\n  "B"\n]']] as const)("preserves answer editor representation for %j", async (answer, expected) => { await scoring(answer); expect(mocks.forms[0]!.values.answer).toBe(expected); expect(mocks.item).toHaveBeenCalledWith({ path: { itemId: "item" } }); expect(mocks.bank).toHaveBeenCalledWith({ path: { bankId: "bank" } }); expect(mocks.templates).toHaveBeenCalledWith({ query: { bank_id: "bank", limit: 100 } }); expect(host.querySelector('input[type="file"]')).toBeNull(); });
  it("reuses scoring command identity and refuses a non-array equivalent list", async () => {
    await scoring(); mocks.submit = { answer: "false", equivalent: "{}", solution: "", steps: "[]", rubricEnabled: false, points: [], deductions: "[]", examples: "[]", use_policy: "practice_only" }; await click("保存评分标准"); expect(mocks.scoring).not.toHaveBeenCalled(); mocks.submit.equivalent = "[]"; mocks.scoring.mockRejectedValueOnce(new Error("lost response")); await click("保存评分标准"); await click("保存评分标准"); expect(mocks.scoring.mock.calls[0]![0]).toEqual(mocks.scoring.mock.calls[1]![0]); expect(mocks.scoring.mock.calls[0]![0].body.scoring.answer.standard_answer).toBe(false);
  });
  it("does not reset same-revision scoring fields, but reloads a newer revision", async () => {
    await scoring("old"); const form = mocks.forms[0]!; form.values.answer = "unsaved"; await scoring("background", 4); expect(form.values.answer).toBe("unsaved"); await scoring("new", 5); expect(form.values.answer).toBe("new");
  });
  it("keeps a steps-only solution and unchanged text answers when saving", async () => {
    await scoring("false"); mocks.submit = { answer: "false", equivalent: "[]", solution: "", steps: '[{"step_no":1,"content":"先合并同类项"}]', rubricEnabled: false, points: [], deductions: "[]", examples: "[]", use_policy: "practice_only" };
    await click("保存评分标准");
    expect(mocks.scoring.mock.calls[0]![0].body.scoring).toMatchObject({ answer: { standard_answer: "false" }, solution: { raw_text: "", steps: [{ step_no: 1, content: "先合并同类项" }], source_refs: [] } });
  });
});

