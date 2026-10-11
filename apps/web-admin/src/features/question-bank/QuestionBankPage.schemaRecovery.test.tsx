// @vitest-environment jsdom
import { act, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CreateQuestionBankItemRequest } from "@edugrade/sdk";
import type { SessionUser } from "../../auth/session";
import { ApiClientError } from "../../api/client";
import { QuestionBankPage } from "./QuestionBankPage";
import type { QuestionBankDialogs } from "./QuestionBankDialogs";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";

const mocks = vi.hoisted(() => ({
  schema: vi.fn(), updateSchema: vi.fn(), createItem: vi.fn(), confirm: vi.fn(), warning: vi.fn(), error: vi.fn(), success: vi.fn(),
  props: null as ComponentProps<typeof QuestionBankDialogs> | null,
}));
const banks = [
  { id: "bank", tenant_id: "tenant", school_id: "school", name: "数学题库", status: "active", created_by: "creator", revision: 7 },
  { id: "other-bank", tenant_id: "tenant", school_id: "school", name: "物理题库", status: "active", created_by: "creator", revision: 3 },
];
const emptySchema = { bank_id: "bank", version: 1, fields: [], taxonomies: [] };
const configuredSchema = { ...emptySchema,
  fields: [{ key: "curriculum", label: "所属教材", type: "string", required: true }],
  taxonomies: [{ id: "knowledge_points", label: "知识点", terms: [{ id: "algebra", label: "代数", active: true }] }],
};

vi.mock("antd", async (importOriginal) => ({ ...await importOriginal<typeof import("antd")>(),
  App: { useApp: () => ({ message: { warning: mocks.warning, error: mocks.error, success: mocks.success }, modal: { confirm: mocks.confirm } }) },
}));
vi.mock("../../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: () => undefined }));
vi.mock("./QuestionBankWorkspace", () => ({ QuestionBankWorkspace: (props: QuestionBankWorkspaceProps) => <button onClick={props.openSchema}>编辑分类规则</button> }));
vi.mock("./QuestionBankDialogs", async (importOriginal) => {
  const original = await importOriginal<typeof import("./QuestionBankDialogs")>();
  return { QuestionBankDialogs: (props: ComponentProps<typeof QuestionBankDialogs>) => { mocks.props = props; return <original.QuestionBankDialogs {...props} />; } };
});
vi.mock("./questionBankMembers", async (importOriginal) => ({ ...await importOriginal<typeof import("./questionBankMembers")>(), listQuestionBankMembers: async () => ({ members: [] }) }));
vi.mock("../../api/questionBank", () => ({ questionBankApi: {
  listQuestionBanks: async () => ({ banks, total: banks.length }), listQuestionBankItems: async () => ({ items: [], total: 0 }),
  getQuestionBankMetadataSchema: mocks.schema, updateQuestionBankMetadataSchema: mocks.updateSchema,
  createQuestionBankItem: mocks.createItem,
} }));

describe("题库分类规则读取与空规则保存", { timeout: 20_000 }, () => {
  let host: HTMLDivElement; let root: Root; let client: QueryClient;
  const user = { id: "actor", tenant: "tenant", roles: [], permissions: ["question_bank:read", "question_bank:manage", "question_bank:create"], organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] } } as unknown as SessionUser;
  beforeEach(() => {
    vi.clearAllMocks(); mocks.props = null; window.location.hash = "";
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
    vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
    const computedStyle = window.getComputedStyle.bind(window); vi.spyOn(window, "getComputedStyle").mockImplementation(element => computedStyle(element));
    mocks.schema.mockResolvedValue({ schema: structuredClone(configuredSchema) });
    mocks.updateSchema.mockResolvedValue({ schema: emptySchema });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  });
  afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
  async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
  async function waitFor(ready: () => boolean) { for (let tick = 0; tick < 30 && !ready(); tick++) await flush(); expect(ready()).toBe(true); }
  function button(text: string) { const result = [...document.querySelectorAll<HTMLButtonElement>("button")].find(candidate => candidate.textContent?.replace(/\s/g, "") === text.replace(/\s/g, "")); expect(result).toBeDefined(); return result!; }
  async function click(text: string) { await act(async () => button(text).click()); await flush(); }
  async function openSchema() {
    await act(async () => root.render(<QueryClientProvider client={client}><QuestionBankPage user={user} /></QueryClientProvider>));
    await waitFor(() => Boolean(mocks.props?.currentSchema.data || mocks.props?.currentSchema.isError));
    await click("编辑分类规则");
  }
  async function switchBank() {
    const select = document.querySelector('[aria-label="选择题库"]')!.closest(".ant-select")!;
    await act(async () => select.querySelector(".ant-select-selector")!.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    await flush();
    const option = [...document.querySelectorAll<HTMLElement>(".ant-select-item-option-content")].find(candidate => candidate.textContent === "物理题库");
    expect(option).toBeDefined(); await act(async () => option!.click()); await flush();
  }

  it("首次读取尚未完成时界面和函数都拒绝提交", async () => {
    let resolve!: (value: { schema: typeof configuredSchema }) => void;
    mocks.schema.mockImplementation(() => new Promise(done => { resolve = done; }));
    await act(async () => root.render(<QueryClientProvider client={client}><QuestionBankPage user={user} /></QueryClientProvider>));
    await waitFor(() => mocks.schema.mock.calls.length > 0); await click("编辑分类规则");
    expect(button("保存新的分类规则").disabled).toBe(true);
    await act(async () => mocks.props!.saveSchema({ fields: [], taxonomies: [] })); expect(mocks.updateSchema).not.toHaveBeenCalled();
    await act(async () => resolve({ schema: configuredSchema })); await waitFor(() => Boolean(mocks.props?.schemaFormReady));
    expect(button("保存新的分类规则").disabled).toBe(false);
  });

  it.each([new Error("network unavailable"), new ApiClientError(404, "question_bank_not_found", "not found")])("读取失败 %s 时持续提示、禁止界面和函数提交，成功重试后才启用", async failure => {
    mocks.schema.mockRejectedValue(failure); await openSchema();
    expect(document.body.textContent).toContain("当前分类规则读取失败，暂不能保存修改");
    expect(button("保存新的分类规则").disabled).toBe(true);
    await act(async () => mocks.props!.saveSchema({ fields: [], taxonomies: [] }));
    expect(mocks.updateSchema).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();

    // 重试尚未返回时仍保留失败说明，只有服务端成功返回的空规则才可作为无配置编辑。
    let resolve!: (value: { schema: typeof emptySchema }) => void;
    mocks.schema.mockImplementation(() => new Promise(done => { resolve = done; }));
    await click("重新读取分类规则");
    expect(document.body.textContent).toContain("当前分类规则读取失败"); expect(button("保存新的分类规则").disabled).toBe(true);
    await act(async () => resolve({ schema: emptySchema })); await waitFor(() => Boolean(mocks.props?.schemaFormReady));
    mocks.schema.mockResolvedValue({ schema: emptySchema });
    expect(document.body.textContent).not.toContain("当前分类规则读取失败"); expect(button("保存新的分类规则").disabled).toBe(false);
    await click("保存新的分类规则"); await waitFor(() => mocks.updateSchema.mock.calls.length === 1);
    expect(mocks.updateSchema).toHaveBeenCalledWith(expect.objectContaining({ path: { bankId: "bank" }, body: { expected_revision: 7, fields: [], taxonomies: [] } }));
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("成功读取已有配置后清空，须核对实际影响再提交", async () => {
    await openSchema(); await waitFor(() => Boolean(mocks.props?.schemaFormReady));
    await act(async () => mocks.props!.schemaForm.setFieldsValue({ fields: [], taxonomies: [] }));
    await click("保存新的分类规则"); await waitFor(() => mocks.confirm.mock.calls.length === 1);
    const confirmation = mocks.confirm.mock.calls[0][0];
    expect(confirmation.content).toContain("1 个补充字段和 1 个知识点或分类目录");
    expect(confirmation.content).toContain("新建题目"); expect(confirmation.content).toContain("组合检索"); expect(confirmation.content).toContain("已有题目版本继续保留原规则和记录");
    expect(mocks.updateSchema).not.toHaveBeenCalled();
    await act(async () => confirmation.onOk());
    expect(mocks.updateSchema).toHaveBeenCalledTimes(1);
  });

  it("已读取的配置后台重读失败时保留表单，但不能拿缓存继续保存", async () => {
    await openSchema(); await waitFor(() => Boolean(mocks.props?.schemaFormReady));
    mocks.schema.mockRejectedValue(new Error("refresh failed"));
    await act(async () => { await mocks.props!.currentSchema.refetch(); }); await waitFor(() => Boolean(mocks.props?.schemaLoadFailed));
    expect(mocks.props!.schemaForm.getFieldValue(["fields", 0, "label"])).toBe("所属教材");
    expect(button("保存新的分类规则").disabled).toBe(true);
    await act(async () => mocks.props!.saveSchema({ fields: [], taxonomies: [] }));
    expect(mocks.updateSchema).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("切库后清除前一库的表单，读取失败仍不能保存或确认前一库的清空", async () => {
    await openSchema(); await waitFor(() => Boolean(mocks.props?.schemaFormReady));
    await act(async () => mocks.props!.saveSchema({ fields: [], taxonomies: [] }));
    const confirmation = mocks.confirm.mock.calls[0][0];
    mocks.schema.mockImplementation(({ path }) => path.bankId === "other-bank" ? Promise.reject(new ApiClientError(404, "question_bank_not_found", "not found")) : Promise.resolve({ schema: configuredSchema }));
    await switchBank(); await waitFor(() => Boolean(mocks.props?.schemaLoadFailed));
    expect(mocks.props!.schemaForm.getFieldsValue(true)).toEqual({ fields: [], taxonomies: [] });
    expect(document.body.textContent).not.toContain("所属教材"); expect(button("保存新的分类规则").disabled).toBe(true);
    await act(async () => confirmation.onOk()); expect(mocks.updateSchema).not.toHaveBeenCalled();
    await act(async () => mocks.props!.saveSchema({ fields: [], taxonomies: [] })); expect(mocks.updateSchema).not.toHaveBeenCalled();
  });

  it("新建题目也须成功读取分类规则，避免漏填当前题库的必填字段", async () => {
    mocks.schema.mockRejectedValue(new Error("network unavailable")); await openSchema();
    await act(async () => mocks.props!.setSchemaOpen(false)); await click("新建题目");
    expect(document.body.textContent).toContain("当前分类规则读取失败，暂不能创建题目草稿"); expect(button("创建题目草稿").disabled).toBe(true);
    await act(async () => mocks.props!.createItem({ ...mocks.props!.defaults, metadata: mocks.props!.defaults.metadata } as CreateQuestionBankItemRequest));
    expect(mocks.createItem).not.toHaveBeenCalled(); expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("成功读取"));
  });

  it("切库后新建题目的表单重建，前一库的创建回调也不能提交", async () => {
    mocks.schema.mockImplementation(async ({ path }) => ({ schema: path.bankId === "other-bank" ? { ...emptySchema, bank_id: "other-bank" } : configuredSchema }));
    await openSchema(); await act(async () => mocks.props!.setSchemaOpen(false)); await click("新建题目");
    const previousCreate = mocks.props!.createItem;
    const code = document.querySelector<HTMLInputElement>("#item_code")!;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(code, "MATH-1"); code.dispatchEvent(new Event("input", { bubbles: true })); });
    expect(code.value).toBe("MATH-1");
    await switchBank(); await waitFor(() => mocks.props?.bankId === "other-bank" && mocks.props.schemaReadable);
    expect(document.querySelector<HTMLInputElement>("#item_code")!.value).toBe("");
    await act(async () => previousCreate({ ...mocks.props!.defaults } as CreateQuestionBankItemRequest)); expect(mocks.createItem).not.toHaveBeenCalled();
  });
});
