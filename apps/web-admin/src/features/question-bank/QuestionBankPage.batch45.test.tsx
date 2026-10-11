// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { QuestionBankDialogs } from "./QuestionBankDialogs";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";
import { QuestionBankPage } from "./QuestionBankPage";

type Confirmation = { content: ReactNode; onOk: () => Promise<void> };
const mocks = vi.hoisted(() => ({
  submitReview: vi.fn(), approve: vi.fn(), returnToDraft: vi.fn(), publish: vi.fn(), updateACL: vi.fn(),
  confirm: vi.fn(), success: vi.fn(), error: vi.fn(), warning: vi.fn(),
  workspace: null as QuestionBankWorkspaceProps | null,
  dialogs: null as ComponentProps<typeof QuestionBankDialogs> | null,
}));
const bank = { id: "bank", name: "学校题库", status: "active", created_by: "creator", revision: 7 };
const item = { id: "item", bank_id: bank.id, item_code: "Q1" };
const version = {
  id: "version", item_id: item.id, revision: 3, schema_version: 1, workflow_status: "draft", bundle_hash: "bundle",
  stem: "原题干", default_score: 5, scoring: { answer: { standard_answer: "A" }, rubric: null, assets: [] },
};
const acl = { revision: 7, bindings: [{ user_id: "creator", preset: "Custom", actions: ["read"] }], groups: [] };
const duplicateACL = {
  bindings: [{ user_id: "ordinary", preset: "Viewer" as const }, { user_id: "ordinary", preset: "Viewer" as const }],
  groups: [{ name: "普通出题组", preset: "Author" as const, members: ["ordinary"] }],
};

vi.mock("../../api/questionBank", () => ({ questionBankApi: {
  listQuestionBanks: async () => ({ banks: [bank], total: 1 }),
  listQuestionBankItems: async () => ({ items: [item], total: 1 }),
  listQuestionBankVersions: async () => ({ versions: [version], total: 1 }),
  getQuestionBankVersion: async () => ({ version }),
  getQuestionBankMetadataSchema: async () => ({ schema: { fields: [], taxonomies: [] } }),
  listQuestionBankReviews: async () => ({ reviews: [] }),
  listQuestionBankMembers: async () => ({ members: [{ user_id: "ordinary", display_name: "同名教师", actions: [] }] }),
  getQuestionBankACL: async () => ({ acl }),
  submitQuestionBankReview: mocks.submitReview, approveQuestionBankVersion: mocks.approve,
  returnQuestionBankVersionToDraft: mocks.returnToDraft, publishQuestionBankVersion: mocks.publish,
  updateQuestionBankACL: mocks.updateACL,
} }));
vi.mock("../../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: () => undefined }));
vi.mock("./QuestionBankWorkspace", () => ({ QuestionBankWorkspace: (props: QuestionBankWorkspaceProps) => {
  mocks.workspace = props;
  return <><button onClick={() => props.selectItem(props.items.data!.items[0])}>选择题目</button>
    <button onClick={props.openAcl}>访问权限</button>
    {(["submit-review", "approve", "return-to-draft", "publish"] as const).map((decision) =>
      <button key={decision} onClick={() => props.transition(decision)}>{decision}</button>)}</>;
} }));
vi.mock("./QuestionBankDialogs", () => ({ QuestionBankDialogs: (props: ComponentProps<typeof QuestionBankDialogs>) => {
  mocks.dialogs = props;
  return props.aclOpen ? <button onClick={() => props.saveACL(duplicateACL)}>核对重复授权</button> : null;
} }));
vi.mock("antd", async () => {
  const { useRef } = await import("react");
  const app = { message: { success: mocks.success, error: mocks.error, warning: mocks.warning }, modal: { confirm: mocks.confirm } };
  const Input = Object.assign(() => null, { Search: () => null, TextArea: () => null });
  return {
    App: { useApp: () => app },
    Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>,
    Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
    Empty: () => null, Pagination: () => null, Select: () => null, Input, Modal: () => null,
    Form: { useForm: () => {
      const ref = useRef({ setFieldsValue: vi.fn(), getFieldValue: vi.fn(), getFieldsValue: vi.fn() });
      return [ref.current];
    } },
  };
});

describe("题库第45批入口行为", () => {
  let host: HTMLDivElement; let root: Root; let client: QueryClient;
  let confirmationHost: HTMLDivElement; let confirmationRoot: Root;
  const user = {
    id: "actor", tenant: "tenant", roles: [], permissions: ["question_bank:read", "question_bank:edit", "question_bank:manage"],
    organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] },
  } as unknown as SessionUser;
  beforeEach(() => {
    vi.clearAllMocks(); mocks.workspace = null; mocks.dialogs = null; window.location.hash = "";
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    for (const method of [mocks.submitReview, mocks.approve, mocks.returnToDraft, mocks.publish]) method.mockResolvedValue({ version });
    mocks.updateACL.mockResolvedValue({ acl });
    host = document.createElement("div"); confirmationHost = document.createElement("div");
    document.body.append(host, confirmationHost); root = createRoot(host); confirmationRoot = createRoot(confirmationHost);
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); confirmationRoot.unmount(); });
    client.clear(); host.remove(); confirmationHost.remove(); vi.restoreAllMocks();
  });
  async function flush() { await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); }); }
  async function waitFor(ready: () => boolean) { for (let tick = 0; tick < 20 && !ready(); tick++) await flush(); expect(ready()).toBe(true); }
  async function click(text: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((candidate) => candidate.textContent === text);
    expect(button).toBeDefined(); await act(async () => button!.click()); await flush();
  }
  async function open() {
    await act(async () => root.render(<QueryClientProvider client={client}><QuestionBankPage user={user} /></QueryClientProvider>));
    await waitFor(() => Boolean(mocks.workspace?.items.data?.items.length));
  }

  it.each([
    ["submit-review", "submitReview", "已提交审核，请通知本题库的审核教师在题库中处理。"],
    ["approve", "approve", "审核已通过，下一步由有发布权限的教师发布到题库。"],
    ["return-to-draft", "returnToDraft", "已退回草稿，可修改后重新提交审核。"],
    ["publish", "publish", "已发布到学校题库。请在考试的题目页选择此版本；已有考试不受影响。"],
  ] as const)("%s 成功后保留原操作与教师提示", async (decision, method, expectedMessage) => {
    await open(); await click("选择题目"); await waitFor(() => Boolean(mocks.workspace?.editorVersion));
    await click(decision); await waitFor(() => mocks.success.mock.calls.length > 0);
    expect(mocks[method]).toHaveBeenCalledWith(expect.objectContaining({
      path: { versionId: version.id }, body: { expected_revision: 3, bundle_hash: "bundle", comment: "" },
      headers: { "Idempotency-Key": expect.any(String) },
    }));
    expect(mocks.success).toHaveBeenLastCalledWith(expectedMessage);
  });

  it("重复授权摘要保留全部行，重复显示不生成编号，不确定失败后复用原请求身份", async () => {
    const uuid = vi.spyOn(crypto, "randomUUID");
    await open(); await click("访问权限"); await waitFor(() => Boolean(mocks.dialogs?.acl.data));
    await click("核对重复授权");
    const first = mocks.confirm.mock.calls[0]![0] as Confirmation;
    const createdCount = uuid.mock.calls.length;
    // 同一确认内容重复显示；请求失败后重新提交的是同一授权，显示行身份不能改变请求指纹。
    await act(async () => confirmationRoot.render(first.content));
    const firstLines = [...confirmationHost.querySelectorAll("li")].map((line) => line.textContent);
    expect(firstLines).toHaveLength(3); expect(firstLines[0]).toBe(firstLines[1]);
    expect(firstLines[0]).toContain("同名教师"); expect(firstLines[2]).toContain("普通出题组");
    await act(async () => confirmationRoot.render(first.content));
    expect(uuid.mock.calls).toHaveLength(createdCount);
    expect([...confirmationHost.querySelectorAll("li")].map((line) => line.textContent)).toEqual(firstLines);
    expect(mocks.updateACL).not.toHaveBeenCalled();

    mocks.updateACL.mockRejectedValueOnce(new Error("response lost"));
    await act(async () => first.onOk()); await flush();
    expect(mocks.error).toHaveBeenCalled(); expect(mocks.dialogs?.saving).toBe(false);
    await click("核对重复授权");
    const second = mocks.confirm.mock.calls[1]![0] as Confirmation;
    await act(async () => second.onOk()); await flush();
    expect(mocks.updateACL).toHaveBeenCalledTimes(2);
    expect(mocks.updateACL.mock.calls[1]![0]).toEqual(mocks.updateACL.mock.calls[0]![0]);
    expect(mocks.updateACL.mock.calls[1]![0]).toEqual({
      path: { bankId: bank.id },
      body: { expected_revision: 7, bindings: duplicateACL.bindings, groups: [{ name: "普通出题组", preset: "Author", member_user_ids: ["ordinary"] }] },
      headers: { "Idempotency-Key": expect.any(String) },
    });
    expect(mocks.success).toHaveBeenLastCalledWith("题库权限已更新");
  });
});
