// @vitest-environment jsdom
import { act, useRef, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";
import { QuestionBankPage } from "./QuestionBankPage";

const api = vi.hoisted(() => ({ version: vi.fn(), linkedItem:vi.fn(), linkedBank:vi.fn(), confirm: vi.fn(), success: vi.fn(), error: vi.fn(), form: { values: {} as Record<string, unknown>, setFieldsValue: vi.fn(), getFieldValue: vi.fn(), getFieldsValue: vi.fn() }, workspace: null as QuestionBankWorkspaceProps | null }));
const bank = { id: "bank", name: "学校题库", status: "active" };
const item = { id: "item", bank_id: "bank", item_code: "Q1", current_published_version_id: null };
const version = { id: "version", item_id: "item", revision: 1, workflow_status: "draft", stem: "原题干", default_score: 5, scoring: { answer: { standard_answer: "A" }, rubric: null, assets: [] } };
vi.mock("../../api/questionBank", () => ({ questionBankApi: {
  listQuestionBanks: async () => ({ banks: [bank], total: 1 }), listQuestionBankItems: async () => ({ items: [item], total: 1 }),
  listQuestionBankVersions: async () => ({ versions: [version], total: 1 }), getQuestionBankVersion: api.version,
  getQuestionBankItem:api.linkedItem, getQuestionBank:api.linkedBank, listQuestionBankMembers:async()=>({members:[]}),
  getQuestionBankMetadataSchema: async () => ({ schema: { fields: [], taxonomies: [] } }), listQuestionBankReviews: async () => ({ reviews: [] })
} }));
vi.mock("../../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: () => undefined }));
vi.mock("../../components/PageState", () => ({ ErrorState:({message,onRetry}:{message:string;onRetry:()=>void})=><aside role="alert">{message}<button onClick={onRetry}>重试</button></aside> }));
vi.mock("./QuestionBankDialogs", () => ({ QuestionBankDialogs: () => null }));
vi.mock("./QuestionBankWorkspace", () => ({ QuestionBankWorkspace: (props: QuestionBankWorkspaceProps) => {
  api.workspace = props;
  return <div><button onClick={() => props.selectItem(props.items.data!.items[0])}>选择题目</button><button onClick={() => { api.form.setFieldsValue({ stem: "本地未保存题干", default_score: 8 }); props.setContentDirty(true); props.setScoringDirty(true); props.onScoringDraftSnapshot?.({ fields: { answer: "本地答案", equivalent: '["本地等价答案"]', absolute: 0.1, relative: 0.01, solution: "本地解析", steps: '[{"step_no":1,"content":"本地第一步"}]', rubricEnabled: true, points: [{ description: "本地评分点", score: 8, required: true, evidence: '[{"type":"concept","target":"本地必要概念"}]' }], deductions: '[{"description":"本地扣分规则","score":-1}]', examples: '[{"student_answer":"本地样例作答","expected_score":4}]' }, assets: ["本地附件"] }); }}>编辑本地草稿</button><button onClick={props.loadLatest}>查看最新版本</button></div>;
} }));
vi.mock("antd", () => {
  const Input = Object.assign(() => null, { Search: () => null, TextArea: ({ value }: { value: string }) => <textarea value={value} readOnly /> });
  return { App: { useApp: () => ({ message: { success: api.success, error: api.error }, modal: { confirm: api.confirm } }) },
    Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>,
    Space: ({ children }: { children: ReactNode }) => <div>{children}</div>, Empty: () => null, Pagination: () => null, Select: () => null, Input,
    Form: { useForm: () => { const ref = useRef(api.form); return [ref.current]; } },
    Modal: ({ open, children, footer }: { open: boolean; children: ReactNode; footer: ReactNode }) => open ? <div role="dialog">{children}{footer}</div> : null };
});

describe("题库冲突恢复保留本地草稿", () => {
  let host: HTMLDivElement; let root: Root; let client: QueryClient;
  const user = { id: "actor", tenant: "tenant", roles: [], permissions: ["question_bank:read", "question_bank:edit"], organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] } } as unknown as SessionUser;
  beforeEach(() => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); api.form.values = {}; api.workspace=null; window.location.hash="";
    api.form.setFieldsValue.mockImplementation((values) => Object.assign(api.form.values, values));
    api.form.getFieldValue.mockImplementation((key) => api.form.values[key]); api.form.getFieldsValue.mockImplementation(() => api.form.values);
    api.version.mockResolvedValue({ version }); api.linkedItem.mockResolvedValue({item}); api.linkedBank.mockResolvedValue({bank}); host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  });
  afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); });
  async function flush() { await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); }); }
  async function click(text: string) { await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === text)!.click()); await flush(); }
  async function open() {
    await act(async () => root.render(<QueryClientProvider client={client}><QuestionBankPage user={user} /></QueryClientProvider>)); await flush();
    for (let tick = 0; tick < 20 && !api.workspace?.items.data?.items.length; tick++) await flush();
    await click("选择题目");
    for (let tick = 0; tick < 20 && !api.workspace?.editorVersion; tick++) await flush();
    await click("编辑本地草稿");
    api.version.mockResolvedValue({ version: { ...version, revision: 2, stem: "他人保存的新题干" } }); await click("查看最新版本");
  }
  it("导入回执深链接可打开当前列表页之外的确切题目和草稿版", async()=>{
    const linked={...item,id:"imported-item",bank_id:"outside-page-bank",item_code:"原卷精选题"};
    const draft={...version,id:"imported-draft",item_id:linked.id,revision:6};
    window.location.hash="/question-bank?bank=outside-page-bank&item=imported-item&version=imported-draft";
    api.linkedItem.mockResolvedValue({item:linked}); api.linkedBank.mockResolvedValue({bank:{...bank,id:linked.bank_id,name:"另一学校题库"}});
    api.version.mockImplementation(async({path})=>({version:path.versionId===draft.id ? draft : version}));
    await act(async()=>root.render(<QueryClientProvider client={client}><QuestionBankPage user={user}/></QueryClientProvider>));
    for(let tick=0;tick<20 && !api.workspace?.editorVersion;tick++) await flush();
    expect(api.workspace?.item?.id).toBe(linked.id); expect(api.workspace?.bank?.id).toBe(linked.bank_id);
    expect(api.workspace?.selected?.id).toBe(draft.id); expect(api.workspace?.editorVersion?.revision).toBe(6);
  });
  it("深链接错配的版本不进入另一道题的编辑器",async()=>{
    window.location.hash="/question-bank?bank=bank&item=item&version=wrong-draft";
    api.version.mockResolvedValue({version:{...version,id:"wrong-draft",item_id:"different-item"}});
    await act(async()=>root.render(<QueryClientProvider client={client}><QuestionBankPage user={user}/></QueryClientProvider>));
    for(let tick=0;tick<20 && !host.textContent?.includes("版本不属于当前题目");tick++) await flush();
    expect(host.textContent).toContain("版本不属于当前题目"); expect(api.workspace?.selected).toBeUndefined(); expect(api.workspace?.editorVersion).toBeNull();
  });
  it("预览与取消都保留本地内容，二次确认后才采用服务器修订并重建评分表单", async () => {
    await open(); expect(api.form.values.stem).toBe("本地未保存题干"); expect(api.workspace!.editorVersion!.revision).toBe(1);
    expect(host.querySelector('[aria-label="本地题干"]')?.textContent).toContain("本地未保存题干");
    expect(host.querySelector('[aria-label="最新题干"]')?.textContent).toContain("他人保存的新题干");
    await click("保留本地修改"); expect(api.form.values.stem).toBe("本地未保存题干");
    await click("查看最新版本"); await click("采用最新版本"); expect(api.form.values.stem).toBe("本地未保存题干"); expect(api.confirm).toHaveBeenCalledTimes(1);
    await act(async () => api.confirm.mock.calls[0][0].onOk());
    expect(api.form.values.stem).toBe("他人保存的新题干"); expect(api.workspace!.editorVersion!.revision).toBe(2); expect(api.workspace!.scoringResetToken).toBe(1);
    expect(api.workspace!.contentDirty).toBe(false); expect(api.workspace!.scoringDirty).toBe(false);
  });
  it("冲突比较两侧展示未保存答案、逐评分点条件与附件，突出实际差异", async () => {
    await open();
    api.version.mockResolvedValue({ version: { ...version, revision: 2, scoring: {
      answer: { standard_answer: "最新答案", equivalent_answers: ["最新等价答案"], tolerance: { absolute: 0.2, relative: 0.02 } },
      solution: { raw_text: "最新解析", steps: [{ step_no: 1, content: "最新第一步" }] },
      rubric: { points: [{ description: "最新评分点", score: 5, required: false, evidence_requirements: [{ type: "unit", target: "最新单位" }] }], deductions: [{ description: "最新扣分规则", score: -2 }], examples: [{ student_answer: "最新样例作答", expected_score: 3 }] },
      assets: [{ name: "最新附件.pdf", file_asset_id: "file", sha256: "hash" }]
    } } });
    await click("查看最新版本");
    for (const [label, left, right] of [
      ["标准答案", "本地答案", "最新答案"], ["等价答案", "本地等价答案", "最新等价答案"],
      ["解析", "本地解析", "最新解析"], ["分步解析", "本地第一步", "最新第一步"],
      ["评分点 1", "本地评分点", "最新评分点"], ["评分点 1 · 核对条件", "本地必要概念", "最新单位"],
      ["扣分规则", "本地扣分规则", "最新扣分规则"], ["样例答案", "本地样例作答", "最新样例作答"],
      ["评分附件", "本地附件", "最新附件.pdf"]
    ]) {
      expect(host.querySelector(`[aria-label="本地${label}"]`)?.textContent).toContain(left);
      expect(host.querySelector(`[aria-label="最新${label}"]`)?.textContent).toContain(right);
      expect(host.querySelector(`section[aria-label="${label} · 有差异"]`)?.getAttribute("data-changed")).toBe("true");
    }
    expect(host.querySelector('[aria-label="本地允许绝对误差"]')?.textContent).toContain("0.1");
    expect(host.querySelector('[aria-label="最新允许绝对误差"]')?.textContent).toContain("0.2");
    expect(host.querySelector('[aria-label="本地评分点 1"]')?.textContent).toContain("必须满足是");
    expect(host.querySelector('[aria-label="最新评分点 1"]')?.textContent).toContain("必须满足否");
    expect(api.form.values.stem).toBe("本地未保存题干");
  });
  it("下载的备份包含未保存答案、评分点和附件，下载不丢弃本地草稿", async () => {
    let backup!: Blob;
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: vi.fn((blob: Blob) => { backup = blob; return "blob:local-backup"; }) });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() }); vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    await open(); await click("下载本地修改备份");
    const text = await new Promise<string>((resolve) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.readAsText(backup); });
    expect(JSON.parse(text)).toMatchObject({ content: { stem: "本地未保存题干" }, scoring: { fields: { answer: "本地答案", points: [{ description: "本地评分点", score: 8 }] }, assets: ["本地附件"] } });
    expect(api.form.values.stem).toBe("本地未保存题干"); expect(api.workspace!.contentDirty).toBe(true);
  });
});
