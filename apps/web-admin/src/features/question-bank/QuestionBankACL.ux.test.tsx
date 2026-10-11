// @vitest-environment jsdom
import { act, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import { QuestionBankPage } from "./QuestionBankPage";
import type { QuestionBankDialogs } from "./QuestionBankDialogs";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";

const mocks = vi.hoisted(() => ({ update: vi.fn(), search: vi.fn(), members: vi.fn(), schema: vi.fn(), confirm: vi.fn(), error: vi.fn(), warning: vi.fn(), success: vi.fn(), props: null as ComponentProps<typeof QuestionBankDialogs> | null }));
const bank = { id: "bank", tenant_id: "tenant", school_id: "school", name: "学校题库", status: "active", created_by: "creator", revision: 7, metadata_schema_version: 1 };
const originalACL = { bank_id: "bank", revision: 7, bindings: [
  { user_id: "creator", preset: "Custom", actions: ["read", "manage"] }, { user_id: "actor", preset: "Manager", actions: ["manage"] },
  { user_id: "historic", preset: "Custom", actions: ["read", "review", "publish"] }, { user_id: "ordinary", preset: "Viewer", actions: ["read", "statistics"] }
], groups: [{ id: "custom-group", name: "历史联合复核组", preset: "Custom", actions: ["read", "review", "publish"], member_user_ids: ["historic"] }, { id: "ordinary-group", name: "普通出题组", preset: "Author", actions: ["read", "create", "edit"], member_user_ids: ["ordinary"] }] };

vi.mock("antd", async (importOriginal) => ({ ...await importOriginal<typeof import("antd")>(), App: { useApp: () => ({ message: { error: mocks.error, warning: mocks.warning, success: mocks.success }, modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: () => undefined }));
vi.mock("./QuestionBankWorkspace", () => ({ QuestionBankWorkspace: (props: QuestionBankWorkspaceProps) => <button onClick={props.openAcl}>访问权限</button> }));
vi.mock("./QuestionBankDialogs", async (importOriginal) => {
  const original = await importOriginal<typeof import("./QuestionBankDialogs")>();
  return { QuestionBankDialogs: (props: ComponentProps<typeof QuestionBankDialogs>) => { mocks.props = props; return <original.QuestionBankDialogs {...props} />; } };
});
vi.mock("./questionBankMembers", async (importOriginal) => ({ ...await importOriginal<typeof import("./questionBankMembers")>(), listQuestionBankMembers: mocks.members }));
vi.mock("../../api/questionBank", () => ({ questionBankApi: {
  listQuestionBanks: async () => ({ banks: [bank], total: 1 }), listQuestionBankItems: async () => ({ items: [], total: 0 }),
  getQuestionBankMetadataSchema: mocks.schema, getQuestionBankACL: async () => ({ acl: structuredClone(originalACL) }),
  updateQuestionBankACL: mocks.update, searchQuestionBankItems: mocks.search
} }));

// 真实 AntD 弹窗和下拉在全端并行验证时可能超过默认 5 秒；期限不替代身份和授权请求断言。
describe("历史组合权限保留与按名称组合检索", { timeout: 20_000 }, () => {
  let host: HTMLDivElement; let root: Root; let client: QueryClient;
  const user = { id: "actor", tenant: "tenant", roles: [], permissions: ["question_bank:manage", "question_bank:read"], organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] } } as unknown as SessionUser;
  beforeEach(() => {
    vi.clearAllMocks(); mocks.props = null; Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
    vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
    const computedStyle = window.getComputedStyle.bind(window); vi.spyOn(window, "getComputedStyle").mockImplementation(element => computedStyle(element));
    mocks.members.mockResolvedValue({ members: [{ user_id: "creator", display_name: "创建教师", actions: [] }, { user_id: "actor", display_name: "当前管理教师", actions: [] }, { user_id: "historic", display_name: "历史复核教师", actions: [] }, { user_id: "ordinary", display_name: "普通教师", actions: [] }] });
    mocks.schema.mockResolvedValue({ schema: { fields: [], version: 1, taxonomies: [{ id: "knowledge_points", label: "知识点", terms: [{ id: "stable-old", label: "旧知识点", active: false }, { id: "stable-new", label: "新知识点", active: true }] }] } });
    mocks.search.mockResolvedValue({ items: [], total: 0 }); mocks.update.mockResolvedValue({ acl: originalACL });
    window.location.hash = ""; host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  });
  afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
  async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
  async function click(text: string) { const button = [...document.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent?.replace(/\s/g, "") === text.replace(/\s/g, "")); expect(button).toBeDefined(); await act(async () => button!.click()); await flush(); }
  async function open() { await act(async () => root.render(<QueryClientProvider client={client}><QuestionBankPage user={user} /></QueryClientProvider>)); for (let i = 0; i < 20 && !mocks.props?.members?.data; i++) await flush(); }
  async function choose(selector: string, label: string) {
    const input = document.querySelector(selector)!; expect(input).not.toBeNull();
    await act(async () => input.closest(".ant-select")!.querySelector(".ant-select-selector")!.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    const option = [...document.querySelectorAll<HTMLElement>(".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option-content")].find(option => option.textContent === label); expect(option).toBeDefined();
    await act(async () => option!.click()); await flush();
  }

  it("shows custom facts as read-only and saves another teacher's preset with the original revision", async () => {
    await open(); await click("访问权限"); for (let i = 0; i < 20 && !mocks.props?.acl.data; i++) await flush();
    expect(document.body.textContent).toContain("历史组合权限只读保留"); expect(document.body.textContent).toContain("历史联合复核组"); expect(document.body.textContent).toContain("历史复核教师");
    expect(mocks.props!.aclForm.getFieldsValue(true)).toEqual({ bindings: [{ user_id: "ordinary", preset: "Viewer" }], groups: [{ id: "ordinary-group", name: "普通出题组", preset: "Author", members: ["ordinary"] }] });
    await choose("#bindings_0_preset", "审核题目"); await click("核对并更新授权");
    expect(mocks.confirm).toHaveBeenCalledTimes(1); await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ path: { bankId: "bank" }, body: { expected_revision: 7, bindings: [{ user_id: "ordinary", preset: "Reviewer" }], groups: [{ id: "ordinary-group", name: "普通出题组", preset: "Author", member_user_ids: ["ordinary"] }] }, headers: { "Idempotency-Key": expect.any(String) } }));
    expect(mocks.warning).not.toHaveBeenCalled();
  });

  it("rejects a preset row that would overwrite a read-only historical user", async () => {
    await open(); await click("访问权限"); for (let i = 0; i < 20 && !mocks.props?.acl.data; i++) await flush();
    await act(async () => mocks.props!.aclForm.setFieldsValue({ bindings: [{ user_id: "historic", preset: "Viewer" }], groups: [] }));
    await click("核对并更新授权"); expect(mocks.update).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("只读保留"));
  });

  it("distinguishes two teachers with the same name in selection and in individual and group confirmation", async () => {
    mocks.members.mockResolvedValue({ members: [
      { user_id: "ordinary", display_name: "王老师", username: "wang.math", actions: [] },
      { user_id: "another", display_name: "王老师", username: "wang.physics", actions: [] }
    ] });
    await open(); await click("访问权限"); for (let i = 0; i < 20 && !mocks.props?.acl.data; i++) await flush();
    await choose("#bindings_0_user_id", "王老师（账号：wang.physics）");
    await act(async () => mocks.props!.aclForm.setFieldsValue({ groups: [{ id: "ordinary-group", name: "普通出题组", preset: "Author", members: ["ordinary", "another"] }] }));
    await click("核对并更新授权");
    expect(mocks.confirm).toHaveBeenCalledOnce();
    const summaryHost = document.createElement("div"); const summaryRoot = createRoot(summaryHost);
    await act(async () => summaryRoot.render(mocks.confirm.mock.calls[0][0].content));
    expect(summaryHost.textContent).toContain("王老师（账号：wang.physics）：浏览题目");
    expect(summaryHost.textContent).toContain("王老师（账号：wang.math）、王老师（账号：wang.physics）");
    await act(async () => summaryRoot.unmount());
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ body: expect.objectContaining({ bindings: [{ user_id: "another", preset: "Viewer" }] }) }));
  });

  it("blocks ambiguous names when the directory has not returned disambiguating accounts", async () => {
    mocks.members.mockResolvedValue({ members: [
      { user_id: "ordinary", display_name: "王老师", actions: [] }, { user_id: "another", display_name: "王老师", actions: [] }
    ] });
    await open(); await click("访问权限"); for (let i = 0; i < 20 && !mocks.props?.acl.data; i++) await flush();
    expect(document.body.textContent).toContain("部分同名人员缺少可区分的账号");
    await click("核对并更新授权");
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.update).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("同名人员缺少可区分的账号"));
  });

  it("keeps historical permissions visible with a readable fallback when the member directory fails", async () => {
    mocks.members.mockRejectedValue(new Error("directory unavailable")); await open(); await click("访问权限"); for (let i = 0; i < 20 && !mocks.props?.acl.data; i++) await flush();
    expect(document.body.textContent).toContain("已有授权成员（姓名暂不可读取）"); expect(document.body.textContent).toContain("历史联合复核组");
    expect(document.querySelector('[aria-label="只读保留的用户授权"]')!.textContent).not.toContain("historic");
    await click("核对并更新授权"); expect(mocks.confirm).toHaveBeenCalledTimes(1);
  });

  it("submits the stable identifier after selecting a stopped knowledge point by name and a Chinese difficulty", async () => {
    mocks.search.mockResolvedValue({ items: [{ item: { id: "search-item", item_code: "Q1" }, version: { id: "search-version", stem: "检索结果题干", version_no: 1, workflow_status: "published", metadata: { difficulty_band: "hard" } }, statistics_available: false }], total: 1 });
    await open(); await click("组合检索"); await choose('[aria-label="检索知识点"]', "旧知识点（已停用，可查历史）"); await choose('[aria-label="检索难度"]', "较难");
    expect(mocks.search).toHaveBeenLastCalledWith(expect.objectContaining({ query: expect.objectContaining({ knowledge_point: "stable-old", difficulty_band: "hard" }) }));
    expect(document.body.textContent).not.toContain("知识点 ID");
    expect(document.body.textContent).toContain("检索结果题干"); expect(document.body.textContent).toContain("较难"); expect(document.body.textContent).not.toContain("hard");
  });

  it("does not reveal a stored identifier when the selected knowledge point is absent from the current directory", async () => {
    await open(); await click("组合检索"); await act(async () => mocks.props!.setSearchFilters(current => ({ ...current, knowledge_point: "unknown-internal-123" })));
    expect(document.body.textContent).toContain("原选知识点（当前目录已不包含，可清除）"); expect(document.body.textContent).not.toContain("unknown-internal-123");
  });

  it("explains a missing taxonomy and keeps other search conditions available", async () => {
    mocks.schema.mockResolvedValue({ schema: { fields: [], taxonomies: [], version: 1 } }); await open(); await click("组合检索");
    expect(document.body.textContent).toContain("当前题库尚未配置知识点目录，请联系题库负责人维护");
    expect(document.querySelector('[aria-label="检索知识点"]')!.closest(".ant-select")!.classList.contains("ant-select-disabled")).toBe(true);
    await choose('[aria-label="检索难度"]', "中等"); expect(mocks.search).toHaveBeenLastCalledWith(expect.objectContaining({ query: expect.objectContaining({ difficulty_band: "medium" }) }));
  });
});
