// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { QuestionBank, QuestionBankItem, QuestionBankMember, QuestionBankVersion } from "@edugrade/sdk";
import type { SessionUser } from "../../auth/session";
import { ApiClientError } from "../../api/client";
import { BankQuestionPicker } from "./BankQuestionPicker";
import { QuestionBankPage } from "./QuestionBankPage";
import { QuestionBankInspector } from "./QuestionBankPanels";
import type { QuestionBankDialogs } from "./QuestionBankDialogs";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";

const mocks = vi.hoisted(() => ({
  banks: vi.fn(), bank: vi.fn(), search: vi.fn(), schema: vi.fn(), updateSchema: vi.fn(),
  exam: vi.fn(), materialize: vi.fn(), success: vi.fn(), warning: vi.fn(), error: vi.fn(), confirm: vi.fn(),
  dialogs: null as ComponentProps<typeof QuestionBankDialogs> | null,
}));
vi.mock("../../api/questionBank", () => ({ questionBankApi: {
  listQuestionBanks: mocks.banks, getQuestionBank: mocks.bank, searchQuestionBankItems: mocks.search,
  listQuestionBankItems: async () => ({ items: [], total: 0 }),
  getQuestionBankMetadataSchema: mocks.schema, updateQuestionBankMetadataSchema: mocks.updateSchema,
  materializeQuestionBankVersions: mocks.materialize,
} }));
vi.mock("../../api/exams", () => ({ getExam: mocks.exam }));
vi.mock("../../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: () => undefined }));
vi.mock("./questionBankMembers", async importOriginal => ({
  ...await importOriginal<typeof import("./questionBankMembers")>(),
  listQuestionBankMembers: async () => ({ members: [] }),
}));
vi.mock("./QuestionBankWorkspace", () => ({ QuestionBankWorkspace: (props: QuestionBankWorkspaceProps) => <button onClick={props.openSchema}>编辑分类规则</button> }));
vi.mock("./QuestionBankDialogs", () => ({ QuestionBankDialogs: (props: ComponentProps<typeof QuestionBankDialogs>) => { mocks.dialogs = props; return null; } }));
vi.mock("./ContentFields", () => ({ ContentFields: () => null, gradeScopeLabel: (value: string) => value }));
vi.mock("./ScoringEditor", () => ({ ScoringEditor: () => null }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: Readonly<{ children: ReactNode }>) => children }));
vi.mock("../../components/PageState", () => ({ ErrorState: ({ message }: Readonly<{ message: string }>) => <p role="alert">{message}</p> }));
vi.mock("../../components/ResponsiveTable", () => ({ ResponsiveTable: ({ dataSource, columns }: Readonly<{
  dataSource: Record<string, unknown>[];
  columns: { render?: (value: unknown, row: Record<string, unknown>) => ReactNode }[];
}>) => <div>{dataSource.map((row, index) => <div key={index}>{columns.map((column, cell) => <span key={cell}>{column.render?.(undefined, row)}</span>)}</div>)}</div> }));

// 控件只提供原生事件边界，组件中的权限判断、effect 和查询状态仍真实执行。
vi.mock("antd", async () => {
  const { useRef } = await import("react");
  function Input({ value, onChange, maxLength, disabled, readOnly, ...props }: ComponentProps<"input">) {
    return <input aria-label={props["aria-label"]} value={value} onChange={onChange} maxLength={maxLength} disabled={disabled} readOnly={readOnly} />;
  }
  Input.Search = () => null;
  Input.TextArea = (props: ComponentProps<"textarea">) => <textarea {...props} />;
  function Form({ children, disabled }: Readonly<{ children: ReactNode; disabled?: boolean }>) { return <fieldset disabled={disabled}>{children}</fieldset>; }
  Form.Item = ({ children }: Readonly<{ children: ReactNode }>) => children;
  Form.useForm = () => {
    const form = useRef({ setFieldsValue: vi.fn(), getFieldsValue: vi.fn(() => ({})) });
    return [form.current];
  };
  return {
    App: { useApp: () => ({ message: { success: mocks.success, warning: mocks.warning, error: mocks.error }, modal: { confirm: mocks.confirm } }) },
    Input, Form, InputNumber: () => null, Pagination: () => null, Segmented: () => null,
    Button: ({ children, onClick, disabled }: Readonly<{ children: ReactNode; onClick?: () => void; disabled?: boolean }>) => <button onClick={onClick} disabled={disabled}>{children}</button>,
    Select: ({ value, options = [], onChange, ...props }: Readonly<{ value?: string; options?: { value: string; label: ReactNode }[]; onChange?: (value: string) => void; "aria-label"?: string }>) => <select aria-label={props["aria-label"]} value={value ?? ""} onChange={event => onChange?.(event.target.value)}><option value="" />{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
    Modal: ({ open, children, footer }: Readonly<{ open: boolean; children: ReactNode; footer: ReactNode }>) => open ? <div role="dialog">{children}{footer}</div> : null,
    Alert: ({ message, description, action }: Readonly<{ message: ReactNode; description?: ReactNode; action?: ReactNode }>) => <div role="alert">{message}{description}{action}</div>,
    Space: ({ children }: Readonly<{ children: ReactNode }>) => children,
    Tag: ({ children }: Readonly<{ children: ReactNode }>) => <span>{children}</span>,
    Empty: ({ description }: Readonly<{ description: string }>) => <p>{description}</p>,
  };
});

const bank: QuestionBank = { id: "bank", tenant_id: "tenant", school_id: "school", name: "授权题库", description: "", status: "active", revision: 7, created_by: "author", created_at: "2026-01-01", updated_at: "2026-01-01", metadata_schema_version: 3 };
const item: QuestionBankItem = { id: "item", tenant_id: "tenant", bank_id: bank.id, item_code: "Q-1", subject_code: "mathematics", grade_scope: "grade_8", current_published_version_id: "published", status: "active", created_by: "author", created_at: "2026-01-01", kind: "question", revision: 11 };
const version: QuestionBankVersion = { id: "version", tenant_id: "tenant", item_id: item.id, version_no: 1, schema_version: 3, revision: 4, workflow_status: "draft", source_version_id: null, author_id: "author", content_hash: "content", hash_scope: "draft_content_v1", question_type: "single_choice", assessment_archetype: "selected_response", stem: "冻结题干", options: [], default_score: 5, knowledge_points: [], metadata: { subject_code: "mathematics", education_stage: "junior", grade_scope: "grade_8", difficulty_band: "unclassified", cognitive_level: "unclassified", copyright: "owned", language: "zh-CN" }, created_at: "2026-01-01", updated_at: "2026-01-01", scoring: { answer: null, solution: null, rubric: null, template_version_id: null, assets: [], use_policy: "practice_only" }, bundle_hash: "bundle", answer_version_id: null, rubric_version_id: null, bundle_schema_version: 2 };
const user: SessionUser = { id: "reviewer", username: "reviewer", displayName: "审核员", name: "审核员", role: "teacher", roles: ["teacher"], tenant: "tenant", school: "学校", currentExam: "", permissions: ["question_bank:read", "question_bank:review", "question_bank:publish", "question_bank:manage"], publicComputer: false, organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] } };
const schema = { bank_id: bank.id, version: 3, fields: [], taxonomies: [], created_by: "author", created_at: "2026-01-01" };
function query<T>(data: T) { return { data, isError: false, isLoading: false, error: null, refetch: vi.fn() }; }
function directory(actions: string[], userId = user.id) { return query({ members: [{ user_id: userId, display_name: "审核员", actions }] }); }
type Mutable<T> = { -readonly [Key in keyof T]: T[Key] };
function inspectorProps(): Mutable<ComponentProps<typeof QuestionBankInspector>> {
  return {
    bank, item, selected: version, user, onBack: vi.fn(), kind: "question", onKindChange: vi.fn(),
    canManage: true, openSchema: vi.fn(), openAcl: vi.fn(), items: query({ items: [item], total: 1, limit: 20, offset: 0 }),
    selectItem: vi.fn(), offset: 0, setOffset: vi.fn(), versions: query({ versions: [version], total: 1, limit: 20, offset: 0 }),
    version: query({ version }), versionOffset: 0, setVersionId: vi.fn(), saving: false,
    loadLatest: vi.fn(), canEdit: true, derive: vi.fn(), canRetire: false, retireItem: vi.fn(),
    boundSchema: query({ schema }), editor: {} as QuestionBankWorkspaceProps["editor"], saveVersion: vi.fn(),
    contentDirty: false, setContentDirty: vi.fn(), scoringDirty: false, conflict: false,
    editorVersion: null, scoringSaved: vi.fn(), setScoringDirty: vi.fn(), comment: "", setComment: vi.fn(),
    transition: vi.fn(), reviews: query({ reviews: [] }),
  };
}

describe("题库复杂度拆分的状态与权限边界", () => {
  let host: HTMLDivElement; let root: Root; let client: QueryClient;
  beforeEach(() => {
    vi.clearAllMocks(); mocks.dialogs = null; window.location.hash = "";
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.banks.mockResolvedValue({ banks: [bank], total: 1 }); mocks.bank.mockResolvedValue({ bank });
    mocks.schema.mockResolvedValue({ schema }); mocks.updateSchema.mockResolvedValue({ schema });
    mocks.search.mockResolvedValue({ items: [{ item, version: { ...version, id: "published", workflow_status: "published" } }], total: 1 });
    mocks.exam.mockResolvedValue({ exam: { revision: 17 } }); mocks.materialize.mockResolvedValue({});
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  });
  afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.restoreAllMocks(); });
  async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); }); }
  async function waitFor(ready: () => boolean) { for (let tick = 0; tick < 30 && !ready(); tick++) await flush(); expect(ready()).toBe(true); }
  async function waitForSchema(bankId: string) {
    await waitFor(() => client.getQueryState(["question-bank", user.tenant, user.id, "metadata-schema", bankId, "current"])?.status === "success");
  }
  async function render(element: ReactNode) { await act(async () => root.render(<QueryClientProvider client={client}>{element}</QueryClientProvider>)); await flush(); }
  function findButton(text: string) { return [...host.querySelectorAll<HTMLButtonElement>("button")].find(candidate => candidate.textContent === text); }
  function button(text: string) { const result = findButton(text); if (!result) throw new Error(`Missing button ${text}`); return result; }
  async function click(text: string) { await act(async () => button(text).click()); await flush(); }
  async function change(element: HTMLInputElement | HTMLSelectElement, value: string) {
    await act(async () => {
      Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), "value")!.set!.call(element, value);
      element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
    }); await flush();
  }
  async function picker(onCopied = vi.fn(async () => undefined)) {
    await render(<BankQuestionPicker examId="exam" scopeKey="tenant:reviewer" nextOrder={3} onCopied={onCopied} />);
    await click("从题库选题"); await change(host.querySelector<HTMLSelectElement>('[aria-label="选题题库"]')!, "bank");
    await waitFor(() => Boolean(findButton("加入选题"))); await click("加入选题");
  }

  it("已返回 schema 仍须等到所属题库读取完成才能启用表单", async () => {
    window.location.hash = "#/question-bank?bank=linked";
    let resolveBank!: (value: { bank: QuestionBank }) => void;
    mocks.bank.mockImplementation(() => new Promise(resolve => { resolveBank = resolve; }));
    mocks.schema.mockResolvedValue({ schema: { ...schema, bank_id: "linked" } });
    await render(<QuestionBankPage user={user} />);
    await waitForSchema("linked"); await click("编辑分类规则");
    expect(mocks.dialogs!.schemaReadable).toBe(false); expect(mocks.dialogs!.schemaFormReady).toBe(false);
    await act(async () => resolveBank({ bank: { ...bank, id: "linked" } }));
    await waitFor(() => Boolean(mocks.dialogs?.schemaFormReady));
    expect(mocks.dialogs!.schemaReadable).toBe(true);
    expect(client.getQueryCache().getAll().every(entry => entry.queryKey[1] === user.tenant && entry.queryKey[2] === user.id)).toBe(true);
  });

  it("成功响应缺少 schema 不能被当成已读取的空规则", async () => {
    mocks.schema.mockResolvedValue({}); await render(<QuestionBankPage user={user} />);
    await waitForSchema(bank.id); await click("编辑分类规则");
    expect(mocks.dialogs!.schemaReadable).toBe(false); expect(mocks.dialogs!.schemaFormReady).toBe(false);
    expect(mocks.dialogs!.schemaLoadFailed).toBe(false);
    await act(async () => mocks.dialogs!.saveSchema({ fields: [], taxonomies: [] }));
    expect(mocks.updateSchema).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it.each([408, 503, "network"])("%s 未知结果冻结选题并复用原修订号和请求键", async failure => {
    await picker();
    mocks.materialize.mockRejectedValueOnce(failure === "network" ? new Error("response lost") : new ApiClientError(Number(failure), "request_failed", "request failed"));
    await click("复制 1 道题到考试");
    expect(host.querySelector<HTMLInputElement>('input[maxlength="32"]')!.disabled).toBe(true);
    expect(button("移出清单").disabled).toBe(true);
    await click("继续确认原选题结果");
    expect(mocks.exam).toHaveBeenCalledOnce(); expect(mocks.materialize).toHaveBeenCalledTimes(2);
    expect(mocks.materialize.mock.calls[1]![0]).toEqual(mocks.materialize.mock.calls[0]![0]);
    expect(mocks.materialize.mock.calls[1]![0].body).toEqual({ expected_revision: 17, selections: [{ version_id: "published", question_no: "3", sort_order: 3 }] });
  });

  it.each([400, 409, 499])("%s 明确拒绝后允许更正并创建新的复制请求", async status => {
    await picker(); mocks.materialize.mockRejectedValueOnce(new ApiClientError(status, "rejected", "rejected"));
    await click("复制 1 道题到考试");
    const number = host.querySelector<HTMLInputElement>('input[maxlength="32"]')!;
    expect(number.disabled).toBe(false); expect(button("移出清单").disabled).toBe(false);
    await change(number, " 8 "); await click("复制 1 道题到考试");
    expect(mocks.exam).toHaveBeenCalledTimes(2);
    const [first, second] = mocks.materialize.mock.calls.map(call => call[0]);
    expect(second.headers["Idempotency-Key"]).not.toBe(first.headers["Idempotency-Key"]);
    expect(second.body.selections[0].question_no).toBe("8");
  });

  it("复制成功后的刷新失败保留成功事实并释放忙碌状态", async () => {
    const onCopied = vi.fn(async () => { throw new Error("refresh failed"); });
    await picker(onCopied); await click("复制 1 道题到考试");
    expect(mocks.success).toHaveBeenCalledOnce(); expect(mocks.warning).toHaveBeenCalledWith("题目已复制，页面刷新暂未完成；请刷新考试题目，不要再次复制。");
    expect(mocks.error).not.toHaveBeenCalled(); expect(host.querySelector('[role="dialog"]')).toBeNull();
    await click("从题库选题"); expect(host.textContent).toContain("选题清单 · 0 道题");
    expect(host.textContent).not.toContain("继续确认原选题结果");
    await click("加入选题"); await click("复制 1 道题到考试");
    expect(mocks.exam).toHaveBeenCalledTimes(2); expect(mocks.materialize).toHaveBeenCalledTimes(2);
  });

  it("离开后迟到的复制成功不再通知或触发旧页面刷新", async () => {
    const onCopied = vi.fn(async () => undefined); await picker(onCopied);
    let resolveCopy!: (value: object) => void;
    mocks.materialize.mockImplementationOnce(() => new Promise(resolve => { resolveCopy = resolve; }));
    await click("复制 1 道题到考试"); await render(<div>另一场考试</div>);
    await act(async () => resolveCopy({})); await flush();
    expect(onCopied).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.warning).not.toHaveBeenCalled(); expect(mocks.error).not.toHaveBeenCalled();
  });

  it.each([
    { actor: "reviewer", permission: true, actions: ["read", "review"], approve: true, back: true },
    { actor: "author", permission: true, actions: ["read", "review"], approve: false, back: true },
    { actor: "reviewer", permission: false, actions: ["read", "review"], approve: false, back: true },
    { actor: "reviewer", permission: true, actions: ["review"], approve: false, back: false },
    { actor: "reviewer", permission: true, actions: ["read"], approve: false, back: false },
  ])("审核动作保留独立审核和题库成员边界 $actor/$permission/$actions", async scenario => {
    const props = inspectorProps(); props.selected = { ...version, workflow_status: "reviewing" };
    props.user = { ...user, id: scenario.actor, permissions: scenario.permission ? user.permissions : [] };
    props.members = directory(scenario.actions, scenario.actor);
    await render(<QuestionBankInspector {...props} />);
    expect(Boolean(findButton("批准版本"))).toBe(scenario.approve); expect(Boolean(findButton("退回草稿"))).toBe(scenario.back);
    if (scenario.approve) { await click("批准版本"); expect(props.transition).toHaveBeenCalledWith("approve"); }
    if (scenario.back) { await click("退回草稿"); expect(props.transition).toHaveBeenCalledWith("return-to-draft"); }
  });

  it.each([
    { permission: true, actions: ["read", "publish"], allowed: true },
    { permission: false, actions: ["read", "publish"], allowed: false },
    { permission: true, actions: ["publish"], allowed: false },
    { permission: true, actions: ["read"], allowed: false },
  ])("发布同时要求全局权限与题库 read/publish $permission/$actions", async scenario => {
    const props = inspectorProps(); props.selected = { ...version, workflow_status: "approved" };
    props.user = { ...user, permissions: scenario.permission ? user.permissions : [] }; props.members = directory(scenario.actions);
    await render(<QuestionBankInspector {...props} />); expect(Boolean(findButton("发布版本"))).toBe(scenario.allowed);
    if (scenario.allowed) { await click("发布版本"); expect(props.transition).toHaveBeenCalledWith("publish"); }
  });

  it.each(["contentDirty", "scoringDirty", "conflict", "saving"] as const)("%s 时不能提交审核", async boundary => {
    const props = inspectorProps(); props.members = directory(["read", "review"]); props[boundary] = true;
    await render(<QuestionBankInspector {...props} />); expect(button("提交审核").disabled).toBe(true);
    await click("提交审核"); expect(props.transition).not.toHaveBeenCalled();
  });

  it.each(["loading", "error", "empty"])("成员目录 %s 时不能提交审核", async state => {
    const props = inspectorProps(); props.members = { ...query({ members: [] as QuestionBankMember[] }), isLoading: state === "loading", isError: state === "error" };
    await render(<QuestionBankInspector {...props} />); expect(button("提交审核").disabled).toBe(true);
  });

  it("未提供成员目录时保留原有审核展示兼容行为", async () => {
    const props = inspectorProps(); props.selected = { ...version, workflow_status: "reviewing" }; props.canEdit = false;
    await render(<QuestionBankInspector {...props} />);
    expect(findButton("批准版本")).toBeDefined(); expect(findButton("退回草稿")).toBeDefined();
    props.selected = { ...version, workflow_status: "approved" };
    await render(<QuestionBankInspector {...props} />); expect(findButton("发布版本")).toBeDefined();
  });
});
