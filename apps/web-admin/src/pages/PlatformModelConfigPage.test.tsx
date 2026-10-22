// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TableColumnsType } from "antd";
import { ApiClientError } from "../api/client";
import type { ManagedAPIProbeResult, ManagedModelAPIConfig, ManagedModelAPIConfigUpdateInput } from "../api/modelApiConfig";
import { PlatformModelConfigPage } from "./PlatformModelConfigPage";
import { modelConfigLocation, selectModelSchool } from "../features/platform-model-config/lib/modelConfigLocation";
import {
  applySavedModelConfig,
  capabilityVerified,
  capabilityDialogPresentation,
  hasModelFormChanges,
  isSameModelAPIOrigin,
  isTransientProbeError,
  modelSaveLabel,
  modelUsageMessage,
  textVerified,
  type ConfigFormValues
} from "../features/platform-model-config/lib/modelConfig";

const mocks = vi.hoisted(() => ({
  getChatAccess: vi.fn(), saveChatAccess: vi.fn(),
  list: vi.fn(), probe: vi.fn(), update: vi.fn(), remove: vi.fn(), schools: vi.fn(), discover: vi.fn(), create: vi.fn(), bindings: vi.fn(),
  success: vi.fn(), warning: vi.fn(), error: vi.fn(), info: vi.fn(), confirm: vi.fn(), invalidate: vi.fn(),
  saveDraft: vi.fn(), clearDraft: vi.fn(), restoreDraft: vi.fn(), watch: vi.fn(),
  applications: vi.fn(), saveDocument: vi.fn(), saveAnswerSheet: vi.fn(),
  detailInfo: vi.fn(), detailWarning: vi.fn(), detailError: vi.fn(),
  form: { setFieldsValue: vi.fn(), resetFields: vi.fn(), validateFields: vi.fn(), getFieldValue: vi.fn(), getFieldsValue: vi.fn() }
}));
const appContext = vi.hoisted(() => ({
  message: { success: mocks.success, warning: mocks.warning, error: mocks.error, info: mocks.info },
  modal: { confirm: mocks.confirm, info: mocks.detailInfo, warning: mocks.detailWarning, error: mocks.detailError }
}));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => ({ invalidateQueries: mocks.invalidate }) }));
vi.mock("../api/chatModelAccess", () => ({ getChatModelAccess: mocks.getChatAccess, saveChatModelAccess: mocks.saveChatAccess }));
vi.mock("../api/modelApiConfig", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/modelApiConfig")>(),
  listManagedModelAPIConfigs: mocks.list, probeManagedModelAPIConfig: mocks.probe,
  updateManagedModelAPIConfig: mocks.update, deleteManagedModelAPIConfig: mocks.remove,
  listAvailableManagedModels: mocks.discover, autoCreateManagedModelAPIConfig: mocks.create,
  getModelApplications: mocks.applications, saveDocumentModelSelection: mocks.saveDocument,
  saveAnswerSheetModelSelection: mocks.saveAnswerSheet
}));
vi.mock("../features/platform-model-config/lib/modelConfig", async (importOriginal) => ({
  ...await importOriginal<typeof import("../features/platform-model-config/lib/modelConfig")>(),
  loadAllSchoolTenants: mocks.schools
}));
vi.mock("../features/platform-model-config/hooks/useModelConfigDrafts", () => ({
  useModelConfigDrafts: () => ({ clearFormDraft: mocks.clearDraft, saveFormDraft: mocks.saveDraft, restoreFormDraft: mocks.restoreDraft })
}));
vi.mock("../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: vi.fn() }));
vi.mock("./PanelModelBindingsSection", () => ({ PanelModelBindingsSection: mocks.bindings }));
vi.mock("../components/model-governance/ManagedGovernanceWorkspace", () => ({ ManagedGovernanceWorkspace: () => null }));
vi.mock("../components/ResponsiveTable", () => ({
  ResponsiveTable: ({ columns, dataSource }: { columns: TableColumnsType<ManagedModelAPIConfig>; dataSource: ManagedModelAPIConfig[] }) =>
    <div>{dataSource.map((config, index) => <div key={config.id} data-config-id={config.id}>
      {columns.map((column) => "render" in column && column.render
        ? <div key={String(column.key)} data-column-key={String(column.key)}>{column.render(undefined, config, index) as ReactNode}</div> : null)}
    </div>)}</div>
}));
vi.mock("antd", async () => {
  const { useState } = await import("react");
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => appContext },
    Button: ({ children, onClick, disabled, loading, "aria-label": label }: {
      children?: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean; "aria-label"?: string
    }) => <button disabled={disabled || loading} onClick={onClick} aria-label={label}>{children}</button>,
    Dropdown: ({ children, menu, disabled }: { children: ReactNode; disabled?: boolean;
      menu: { items: { key: string; label: string; disabled?: boolean }[]; onClick: (item: { key: string }) => void } }) => {
      const [open, setOpen] = useState(false);
      return <div onClick={() => { if (!disabled) setOpen(true); }}>{children}{open ? <div role="menu">
        {menu.items.map((item) => <button role="menuitem" key={item.key} disabled={item.disabled}
          onClick={() => menu.onClick({ key: item.key })}>{item.label}</button>)}
      </div> : null}</div>;
    },
    Select: ({ id, value, options = [], disabled, onChange }: {
      id?: string; value?: string; options?: { value: string; label: string }[]; disabled?: boolean; onChange?: (value: string) => void
    }) => <select id={id} value={value ?? ""} disabled={disabled} onChange={(event) => onChange?.(event.target.value)}>
      {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>,
    Drawer: ({ open, children, extra }: { open: boolean; children?: ReactNode; extra?: ReactNode }) => open ? <aside>{extra}{children}</aside> : null,
    Alert: ({ message, description, action }: { message?: ReactNode; description?: ReactNode; action?: ReactNode }) => <section data-alert>{message}{description}{action}</section>, AutoComplete: () => null, Input: Object.assign(() => null, { Password: () => null }),
    Segmented: ({ options, onChange }: { options: { value: string; label: ReactNode }[]; onChange: (value: string) => void }) => <div>{options.map((option) => <button key={option.value} onClick={() => onChange(option.value)}>{option.label}</button>)}</div>, Space: passthrough, Tooltip: passthrough, Tag: passthrough, Empty: () => null,
    Form: Object.assign(passthrough, { useForm: () => [mocks.form], useWatch: mocks.watch,
      Item: ({ children, label, extra }: { children?: ReactNode; label?: ReactNode; extra?: ReactNode }) => <div>{label}{children}{extra}</div> })
  };
});

function config(id: string, status: "active" | "disabled" = "active", isDefault = false): ManagedModelAPIConfig {
  return {
    id, tenant_id: "school-a", provider_key: id, display_name: id, adapter_type: "openai_compatible",
    base_url: "https://example.test/v1", model_name: `${id}-model`, model_version: "v1", region: "global",
    credential_configured: true, status, is_default: isDefault, last_test_status: "untested",
    config_source: "auto", created_at: "", updated_at: ""
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

type ProbeResponse = { config: ManagedModelAPIConfig; result: Partial<ManagedAPIProbeResult> };

describe("模型配置显示和草稿边界", () => {
  it.each([
    ["https://provider.example/", true],
    ["https://provider.example/v2/", true],
    ["https://PROVIDER.example:443/v2", true],
    ["https://provider.example:8443/v1", false],
    ["https://other.example/v1", false],
    ["https://provider.example.attacker.test/v1", false],
    ["https://sub.provider.example/v1", false],
    ["http://provider.example/v1", false],
    ["https://user@provider.example/v1", false],
    ["https://provider.example/v1?token=value", false],
    ["https://provider.example/v1#fragment", false],
    ["not a URL", false],
  ])("密钥复用按 HTTPS 域名和端口判断，目标 %s 的结果为 %s", (target, allowed) => {
    expect(isSameModelAPIOrigin("https://provider.example/v1", String(target))).toBe(allowed);
  });
  it("保存默认模型时清除其他默认项，保存备用模型不改动其他记录", () => {
    const previous = config("previous", "active", true);
    const target = config("target");
    const saved = { ...target, is_default: true };
    expect(applySavedModelConfig([previous, target], saved)).toEqual([{ ...previous, is_default: false }, saved]);
    const unchanged = applySavedModelConfig([previous, target], target);
    expect(unchanged[0]).toBe(previous);
    expect(unchanged[1]).toBe(target);
  });

  it("保留默认项停用、恢复和设为默认时的文案优先级", () => {
    const current = config("model", "active", true);
    expect(modelUsageMessage(current, "active", true)).toBe("model 已设为默认配置");
    expect(modelUsageMessage(current, "disabled", false)).toBe("model 已停用，已取消默认配置");
    expect(modelUsageMessage(current, "active", false)).toBe("已取消默认配置，对话开放范围不变");
    expect(modelUsageMessage(config("model"), "disabled", false)).toBe("model 已停用");
    expect(modelUsageMessage(config("model"), "active", false)).toBe("model 已恢复");
    expect(modelSaveLabel(current)).toBe("验证并更新");
    expect(modelSaveLabel(config("model"))).toBe("检测连接并更新");
    expect(modelSaveLabel(null)).toBe("检测连接并保存");
  });

  it("暂时无法检测优先于失败提示，不把供应商暂不可用显示为密钥失效", () => {
    expect(capabilityDialogPresentation("model", true, true)).toEqual({ kind: "warning", title: "model：结构化检测暂时无法完成" });
    expect(capabilityDialogPresentation("model", false, true)).toEqual({ kind: "error", title: "model：结构化输出检测未通过" });
    expect(capabilityDialogPresentation("model", false, false)).toEqual({ kind: "info", title: "model：结构化检测详情" });
  });

  it("聊天只认独立文本结果，旧结构化成功与访问检查不能冒充普通回复验证", () => {
    const accessOnly = { ...config("model"), last_test_status: "success" as const, last_probe_mode: "quick" as const };
    expect(textVerified(accessOnly)).toBe(false);
    expect(textVerified({ ...accessOnly, last_capability_status: "success", last_capability_probe_version: "structured-json-v3" })).toBe(false);
    const textOnly = { ...accessOnly, last_text_status: "success" as const, last_capability_status: "failed" as const };
    expect(textVerified(textOnly)).toBe(true);
    expect(capabilityVerified(textOnly)).toBe(false);
    expect(capabilityVerified({ ...textOnly, last_capability_status: "success", last_capability_probe_version: "structured-json-v3" })).toBe(false);
    expect(capabilityVerified({ ...textOnly, last_capability_status: "success", last_capability_probe_version: "structured-json-v4" })).toBe(true);
  });

  it("供应商错误区分暂时故障和确定的参数错误", () => {
    for (const status of [undefined, 0, 408, 425, 429, 500, 503]) expect(isTransientProbeError("provider_error", status)).toBe(true);
    for (const status of [400, 401, 403, 404, 422]) expect(isTransientProbeError("provider_error", status)).toBe(false);
    expect(isTransientProbeError("schema_mismatch", 200)).toBe(false);
  });

  it("编辑比较模型与地址，沿用密钥只比较新模型输入，新建允许仅选择供应商", () => {
    const existing = config("model");
    const values: ConfigFormValues = { supplier: "custom", api_key: " ", base_url: existing.base_url, model_name: ` ${existing.model_name} ` };
    expect(hasModelFormChanges(values, existing, null)).toBe(false);
    expect(hasModelFormChanges({ ...values, base_url: "https://other.test" }, existing, null)).toBe(true);
    expect(hasModelFormChanges({ ...values, api_key: "new-key" }, existing, null)).toBe(true);
    expect(hasModelFormChanges({ ...values, model_name: " " }, null, existing)).toBe(false);
    expect(hasModelFormChanges(values, null, existing)).toBe(true);
    expect(hasModelFormChanges({ ...values, api_key: "", base_url: "", model_name: "" }, null, null)).toBe(true);
  });
});

describe("学校模型库操作", () => {
  let host: HTMLDivElement;
  let root: Root;
  let configs: ManagedModelAPIConfig[];

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.watch.mockReset();
    vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
    mocks.bindings.mockImplementation(() => null);
    window.history.replaceState(null, "", "/admin/platform/model-config");
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    configs = [config("DeepSeek", "active", true), config("OpenAI"), config("百炼", "disabled"), config("智谱")];
    mocks.getChatAccess.mockResolvedValue({ access: { revision: "absent", model_ids: ["DeepSeek"] } });
    mocks.saveChatAccess.mockImplementation(async (_tenant: string, _access: unknown, ids: string[]) => ({ access: { revision: "saved-chat", model_ids: ids } }));
    mocks.schools.mockResolvedValue([{ id: "school-a", name: "甲学校", code: "A", status: "active" }]);
    mocks.list.mockImplementation(async () => ({ configs }));
    mocks.applications.mockResolvedValue({ document_selection: { revision: "absent", mode: "follow_chat" }, answer_sheet_selection: { revision: "absent", mode: "unconfigured" }, grading_model_ids: [] });
    mocks.saveDocument.mockImplementation(async (_tenant: string, input: { mode: string; model_config_id?: string }) => ({ selection: { revision: "saved-document", mode: input.mode, model_config_id: input.model_config_id } }));
    mocks.saveAnswerSheet.mockImplementation(async (_tenant: string, input: { mode: string; model_config_id?: string }) => ({ selection: { revision: "saved-answer-sheet", mode: input.mode, model_config_id: input.model_config_id } }));
    mocks.update.mockImplementation(async (id: string, _tenantID: string, input: ManagedModelAPIConfigUpdateInput) => {
      const updated = { ...configs.find((item) => item.id === id)!, ...input };
      configs = configs.map((item) => item.id === id ? updated : item);
      return { config: updated };
    });
    mocks.remove.mockResolvedValue(undefined);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  function button(text: string, scope: ParentNode = host) {
    const found = [...scope.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text || item.getAttribute("aria-label")?.endsWith(`：${text}`));
    expect(found, text).toBeDefined();
    return found!;
  }
  function row(id: string) {
    return host.querySelector(`[data-config-id="${id}"]`)!;
  }
  function column(id: string, key: string) {
    return row(id).querySelector(`[data-column-key="${key}"]`)!;
  }
  function conversationChoice(id: string) {
    const found = column(id, "assignment").querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(found).not.toBeNull();
    return found!;
  }
  async function render() {
    await act(async () => root.render(<PlatformModelConfigPage />));
  }

  it("应用用途始终显示四个入口，阅卷使用结构化门槛并在抽屉带入当前模型", async () => {
    configs[1] = { ...configs[1], last_text_status: "success" };
    configs[3] = { ...configs[3], last_capability_status: "success", last_capability_probe_version: "structured-json-v4" };
    mocks.applications.mockResolvedValue({ document_selection: { revision: "absent", mode: "follow_chat" }, grading_model_ids: ["智谱"] });
    await render();
    expect(button("阅卷配置", row("OpenAI")).disabled).toBe(true);
    expect(button("试卷解析", row("OpenAI")).disabled).toBe(true);
    expect(button("阅卷配置", row("智谱")).textContent).toBe("阅卷·已配置");
    await act(async () => button("阅卷配置", row("智谱")).click());
    expect(mocks.bindings).toHaveBeenCalledWith(expect.objectContaining({ tenantID: "school-a", suggestedModelID: "智谱" }), undefined);
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("独立解析选择使用读取修订保存，不修改日常对话或评分角色", async () => {
    configs[1] = { ...configs[1], last_capability_status: "success", last_capability_probe_version: "structured-json-v4" };
    await render();
    await act(async () => button("试卷解析", row("OpenAI")).click());
    await act(async () => button("保存解析用途").click());
    expect(mocks.saveDocument).toHaveBeenCalledWith("school-a", { mode: "model", model_config_id: "OpenAI", expected_revision: "absent" });
    expect(button("试卷解析", row("OpenAI")).textContent).toBe("解析 ✓");
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(mocks.update).not.toHaveBeenCalled();
    expect(mocks.bindings).not.toHaveBeenCalled();
  });

  it("管理员直接分配已启用且有密钥的自定义模型，无需先做合成图片或结构化检测", async () => {
    configs[1] = { ...configs[1], provider_key: "custom", model_name: "custom-image-reader" };
    configs[3] = { ...configs[3], credential_configured: false };
    await render();
    expect(button("答题卡识别", row("百炼")).disabled).toBe(true);
    expect(button("答题卡识别", row("智谱")).disabled).toBe(true);
    expect(button("试卷解析", row("OpenAI")).disabled).toBe(true);
    expect(button("答题卡识别", row("OpenAI")).disabled).toBe(false);
    await act(async () => button("答题卡识别", row("OpenAI")).click());
    expect(host.querySelector("aside")?.textContent).toContain("直接分析答题卡图像");
    await act(async () => button("保存答题卡用途").click());
    expect(mocks.saveAnswerSheet).toHaveBeenCalledWith("school-a", { mode: "model", model_config_id: "OpenAI", expected_revision: "absent" });
    expect(button("答题卡识别", row("OpenAI")).textContent).toBe("答题卡 ✓");
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(mocks.saveDocument).not.toHaveBeenCalled();
    expect(mocks.update).not.toHaveBeenCalled();
    expect(mocks.bindings).not.toHaveBeenCalled();
    expect(mocks.probe).not.toHaveBeenCalled();
  });

  it("已选答题卡模型停用后仍可打开用途并取消", async () => {
    configs[1] = { ...configs[1], status: "disabled" };
    mocks.applications.mockResolvedValue({ document_selection: { revision: "document-r1", mode: "follow_chat" },
      answer_sheet_selection: { revision: "vision-r3", mode: "model", model_config_id: "OpenAI" }, grading_model_ids: [] });
    await render();
    expect(button("答题卡识别", row("OpenAI")).disabled).toBe(false);
    await act(async () => button("答题卡识别", row("OpenAI")).click());
    expect(button("保存答题卡用途").disabled).toBe(true);
    const modes = host.querySelectorAll<HTMLInputElement>('input[name="answer-sheet-selection-mode"]');
    await act(async () => modes[1].click());
    expect(button("保存答题卡用途").disabled).toBe(false);
    await act(async () => button("保存答题卡用途").click());
    expect(mocks.saveAnswerSheet).toHaveBeenCalledWith("school-a", { mode: "unconfigured", model_config_id: undefined, expected_revision: "vision-r3" });
    expect(button("答题卡识别", row("OpenAI")).textContent).toBe("答题卡识别");
    expect(mocks.saveDocument).not.toHaveBeenCalled();
  });

  it("答题卡用途 CAS 冲突保留输入，显式刷新修订后可重试", async () => {
    mocks.saveAnswerSheet.mockRejectedValueOnce(new ApiClientError(409, "answer_sheet_model_selection_changed", "设置已更新"));
    await render();
    await act(async () => button("答题卡识别", row("OpenAI")).click());
    await act(async () => button("保存答题卡用途").click());
    expect(host.querySelector("aside")).not.toBeNull();
    expect(button("保存答题卡用途").disabled).toBe(true);
    mocks.applications.mockResolvedValue({ document_selection: { revision: "absent", mode: "follow_chat" },
      answer_sheet_selection: { revision: "vision-r4", mode: "unconfigured" }, grading_model_ids: [] });
    await act(async () => button("刷新设置").click());
    await act(async () => button("保存答题卡用途").click());
    expect(mocks.saveAnswerSheet).toHaveBeenLastCalledWith("school-a", { mode: "model", model_config_id: "OpenAI", expected_revision: "vision-r4" });
  });

  it("旧 API 缺少答题卡用途时只禁用该用途，仍可设置独立解析", async () => {
    configs[1] = { ...configs[1], last_capability_status: "success", last_capability_probe_version: "structured-json-v4" };
    mocks.applications.mockResolvedValue({ document_selection: { revision: "absent", mode: "follow_chat" }, grading_model_ids: [] });
    await render();
    expect(button("答题卡识别", row("OpenAI")).disabled).toBe(true);
    expect(button("试卷解析", row("OpenAI")).disabled).toBe(false);
    expect(mocks.saveAnswerSheet).not.toHaveBeenCalled();
  });

  it("切换学校后忽略旧学校晚到的用途设置，不把同 ID 模型标记成旧校用途", async () => {
    const pending = deferred<{ document_selection: { revision: string; mode: "model"; model_config_id: string }; grading_model_ids: string[] }>();
    mocks.schools.mockResolvedValue([{ id: "school-a", name: "甲学校", code: "A", status: "active" }, { id: "school-b", name: "乙学校", code: "B", status: "active" }]);
    configs[1] = { ...configs[1], last_capability_status: "success", last_capability_probe_version: "structured-json-v4" };
    mocks.applications.mockImplementation((tenant: string) => tenant === "school-a" ? pending.promise : Promise.resolve({ document_selection: { revision: "absent", mode: "follow_chat" }, grading_model_ids: [] }));
    await render();
    await act(async () => { const select = host.querySelector<HTMLSelectElement>("select")!; select.value = "school-b"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    await act(async () => pending.resolve({ document_selection: { revision: "old-school", mode: "model", model_config_id: "OpenAI" }, grading_model_ids: ["OpenAI"] }));
    expect(button("阅卷配置", row("OpenAI")).textContent).toBe("阅卷配置");
    expect(button("试卷解析", row("OpenAI")).textContent).toBe("试卷解析");
  });

  it("解析保存冲突保留抽屉输入，显式刷新后才能使用新修订重试", async () => {
    configs[1] = { ...configs[1], last_capability_status: "success", last_capability_probe_version: "structured-json-v4" };
    mocks.saveDocument.mockRejectedValueOnce(new ApiClientError(409, "document_model_selection_changed", "设置已更新"));
    await render();
    await act(async () => button("试卷解析", row("OpenAI")).click());
    await act(async () => button("保存解析用途").click());
    expect(button("保存解析用途").disabled).toBe(true);
    mocks.applications.mockResolvedValue({ document_selection: { revision: "new-revision", mode: "follow_chat" }, grading_model_ids: [] });
    await act(async () => button("刷新设置").click());
    await act(async () => button("保存解析用途").click());
    expect(mocks.saveDocument).toHaveBeenLastCalledWith("school-a", { mode: "model", model_config_id: "OpenAI", expected_revision: "new-revision" });
  });

  it("三种检测各自在对应列发出请求，列表只有七列且无更多操作菜单", async () => {
    configs[1] = { ...configs[1], last_test_status: "temporary_unavailable", last_test_message: "模型服务连接超时，请稍后重试", last_successful_tested_at: "2026-10-15T06:04:00Z", last_text_status: "success", last_capability_status: "failed" };
    mocks.probe.mockImplementation(async (id: string) => ({ config: configs.find((item) => item.id === id), result: { ok: true, message: "检测完成" } }));
    await render();
    expect(column("OpenAI", "connection").textContent).toContain("连接超时");
    expect(column("OpenAI", "text").textContent).toContain("通过");
    expect(column("OpenAI", "structured").textContent).toContain("未通过");
    expect(row("OpenAI").textContent).not.toContain("暂时无法验证");
    expect([...row("OpenAI").querySelectorAll("[data-column-key]")].map((item) => item.getAttribute("data-column-key"))).toEqual(["model-name", "provider", "connection", "text", "structured", "assignment", "actions"]);
    expect(column("OpenAI", "actions").querySelector('[aria-label$="：检测连接"]')).toBeNull();
    expect(row("OpenAI").querySelector('[aria-label="OpenAI更多操作"]')).toBeNull();
    expect([...column("OpenAI", "actions").querySelectorAll("button")].map((item) => item.textContent)).toEqual(["编辑", "停用", "删除"]);
    for (const [key, label, mode, force] of [
      ["connection", "检测连接", "quick", false], ["text", "检测普通回复", "text", false], ["structured", "检测结构化输出", "structured", true],
    ] as const) {
      await act(async () => button(label, column("OpenAI", key)).click());
      expect(mocks.probe).toHaveBeenLastCalledWith("OpenAI", "school-a", mode, force);
    }
    expect(mocks.probe).toHaveBeenCalledTimes(3);
  });

  it.each([
    ["connection", "检测连接"], ["text", "检测普通回复"], ["structured", "检测结构化输出"],
  ])("%s 检测只在该列显示进行状态，其他检测列保留各自结果", async (key, label) => {
    const pending = deferred<ProbeResponse>();
    mocks.probe.mockReturnValue(pending.promise);
    await render();
    await act(async () => button(label, column("OpenAI", key)).click());
    for (const candidate of ["connection", "text", "structured"]) {
      expect(column("OpenAI", candidate).textContent?.includes("检测中")).toBe(candidate === key);
    }
    expect(button("检测普通回复", column("OpenAI", "text")).disabled).toBe(true);
    expect(button("检测结构化输出", column("智谱", "structured")).disabled).toBe(false);
    await act(async () => pending.resolve({ config: configs[1], result: { ok: true, message: "检测完成" } }));
  });

  it("普通回复详情与结构化详情分别读取各自结果", async () => {
    configs[1] = { ...configs[1], last_text_status: "success", last_text_message: "普通文本返回成功", last_text_tested_at: "2026-10-15T06:04:00Z", last_capability_status: "failed", last_capability_message: "结构化字段不匹配" };
    await render();
    await act(async () => button("查看普通回复详情", column("OpenAI", "text")).click());
    expect(mocks.detailInfo).toHaveBeenCalledOnce();
    const textDetail = renderToStaticMarkup(mocks.detailInfo.mock.calls[0][0].content);
    expect(textDetail).toContain("普通文本返回成功");
    expect(textDetail).not.toContain("结构化字段不匹配");
    await act(async () => button("查看结构化详情", column("OpenAI", "structured")).click());
    expect(mocks.detailError).toHaveBeenCalledOnce();
    expect(renderToStaticMarkup(mocks.detailError.mock.calls[0][0].content)).toContain("结构化字段不匹配");
    expect([...row("OpenAI").querySelectorAll("button")].some((item) => item.textContent === "详情")).toBe(false);
  });

  it("点击连接状态可查看历史成功时间和零耗时，不发起新的检测", async () => {
    configs[1] = { ...configs[1], last_test_status: "temporary_unavailable", last_test_message: "连接超时", last_tested_at: "2026-10-15T06:05:00Z", last_successful_tested_at: "2026-10-15T06:04:00Z", last_test_latency_ms: 0 };
    await render();
    await act(async () => button("查看连接详情", column("OpenAI", "connection")).click());
    const detail = renderToStaticMarkup(mocks.detailWarning.mock.calls[0][0].content);
    expect(detail).toContain("上次连接成功");
    expect(detail).toContain("10/15");
    expect(detail).toContain("0ms");
    expect(detail).toContain(configs[1].base_url);
    expect(mocks.probe).not.toHaveBeenCalled();
  });

  it("只有同供应商同模型的多份配置才显示尾号，普通列表不重复展示密钥", async () => {
    configs[0] = { ...configs[0], provider_key: "custom", model_name: "shared-model", credential_hint: "•••• AAAA" };
    configs[1] = { ...configs[1], provider_key: "custom", model_name: "shared-model", credential_hint: "•••• BBBB" };
    configs[2] = { ...configs[2], provider_key: "other-provider", model_name: "shared-model", credential_hint: "•••• CCCC" };
    configs[3] = { ...configs[3], provider_key: "custom", model_name: "unique-model", credential_hint: "•••• DDDD" };
    await render();
    expect(column("DeepSeek", "model-name").textContent).toContain("AAAA");
    expect(column("OpenAI", "model-name").textContent).toContain("BBBB");
    expect(column("百炼", "model-name").textContent).not.toContain("CCCC");
    expect(column("智谱", "model-name").textContent).not.toContain("DDDD");
    expect(host.querySelector('[data-column-key="credential"]')).toBeNull();
  });

  it("表格上方清除默认配置不会取消开放对话，更新失败保留原选择", async () => {
    await render();
    mocks.update.mockRejectedValueOnce(new Error("切换失败"));
    await act(async () => button("清除默认").click());
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(button("清除默认").disabled).toBe(false);
    await act(async () => button("清除默认").click());
    expect(mocks.update).toHaveBeenLastCalledWith("DeepSeek", "school-a", expect.objectContaining({ status: "active", is_default: false }));
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(host.textContent).toContain("默认配置：未指定");
    expect(host.textContent).toContain("对话已开放 1 个");
    expect([...host.querySelectorAll("button")].some((item) => item.textContent === "清除默认")).toBe(false);
  });

  it("编辑抽屉复用已保存密钥前暂存原编辑，创建请求只提交密钥来源编号", async () => {
    configs[1] = { ...configs[1], credential_hint: "•••• BBBB" };
    const draft: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[1].base_url, model_name: "unsaved-model" };
    mocks.form.getFieldsValue.mockReturnValue(draft);
    await render();
    await act(async () => row("OpenAI").querySelector<HTMLButtonElement>('[aria-label="编辑OpenAI"]')!.click());
    await act(async () => button("使用此密钥添加模型", host.querySelector("aside")!).click());
    expect(mocks.saveDraft).toHaveBeenLastCalledWith("school-a:edit:OpenAI", draft);
    expect(mocks.restoreDraft).toHaveBeenLastCalledWith("school-a:reuse:OpenAI", expect.objectContaining({ api_key: "", model_name: "" }));
    expect(host.querySelector("aside")?.textContent).toContain("沿用已保存的密钥");
    mocks.form.validateFields.mockResolvedValue({ ...draft, model_name: "new-model" });
    mocks.create.mockResolvedValue({ config: config("new-model"), validation: { latency_ms: 12 } });
    await act(async () => button("检测连接并保存", host.querySelector("aside")!).click());
    expect(mocks.create).toHaveBeenLastCalledWith({ tenant_id: "school-a", credential_source_id: "OpenAI", model_name: "new-model", provider: "custom", base_url: configs[1].base_url });
  });

  it("选择另一所学校并切换评分页签后，重新进入仍恢复该学校", async () => {
    const schools = [{ id: "school-a", name: "甲学校", code: "A", status: "active" }, { id: "school-b", name: "乙学校", code: "B", status: "active" }];
    mocks.schools.mockResolvedValue(schools);
    await render();
    await act(async () => {
      const select = host.querySelector<HTMLSelectElement>("#platform-model-school")!;
      select.value = "school-b";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const tab = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.includes("三智能体配置"))!;
    expect(tab).toBeDefined();
    await act(async () => tab.click());
    expect(new URLSearchParams(window.location.search).get("tenant_id")).toBe("school-b");
    expect(new URLSearchParams(window.location.search).get("tab")).toBe("bindings");
    await act(async () => root.unmount());
    root = createRoot(host);
    await render();
    expect(host.querySelector<HTMLSelectElement>("#platform-model-school")?.value).toBe("school-b");
  });

  it("角色保存开始后拦截排队切换、刷新以及先前打开的放弃确认", async () => {
    mocks.schools.mockResolvedValue([{ id: "school-a", name: "甲学校", code: "A", status: "active" }, { id: "school-b", name: "乙学校", code: "B", status: "active" }]);
    await render();
    await act(async () => button("三智能体配置").click());
    const callbacks = mocks.bindings.mock.calls[mocks.bindings.mock.calls.length - 1]![0] as { onDirtyChange: (dirty: boolean) => void; onSavingChange: (busy: boolean) => void };
    await act(async () => callbacks.onDirtyChange(true));
    const select = host.querySelector<HTMLSelectElement>("#platform-model-school")!;
    const selectOther = () => { select.value = "school-b"; select.dispatchEvent(new Event("change", { bubbles: true })); };
    await act(async () => selectOther());
    const queuedConfirmation = mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1]![0].onOk;
    const listCalls = mocks.list.mock.calls.length;
    const schoolCalls = mocks.schools.mock.calls.length;
    // 保存通知与旧回调放在同一轮事件，不能依赖 disabled 重绘或弹窗打开时间判断忙状态。
    await act(async () => {
      callbacks.onSavingChange(true);
      queuedConfirmation();
      selectOther();
      button("学校模型").click();
      button("刷新").click();
    });
    expect(new URLSearchParams(window.location.search).get("tenant_id")).toBe("school-a");
    expect(new URLSearchParams(window.location.search).get("tab")).toBe("bindings");
    expect(mocks.list.mock.calls).toHaveLength(listCalls);
    expect(mocks.schools.mock.calls).toHaveLength(schoolCalls);
    await act(async () => callbacks.onSavingChange(false));
    await act(async () => selectOther());
    await act(async () => mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1]![0].onOk());
    expect(new URLSearchParams(window.location.search).get("tenant_id")).toBe("school-b");
  });

  it("从当前高中英语评分组合进入检查时保留范围，但不沿用旧题目的风险级别", async () => {
    window.history.replaceState({ preserved: true }, "", "/admin/platform/model-config?tenant_id=school-a&tab=bindings&risk=R2");
    await render();
    const callbacks = mocks.bindings.mock.calls[mocks.bindings.mock.calls.length - 1]![0] as { onOpenGovernance: (view: string, scope: { stage: string; subject: string; archetype: string }) => void };
    await act(async () => callbacks.onOpenGovernance("assurance", { stage: "senior", subject: "english", archetype: "essay" }));
    const params = new URLSearchParams(window.location.search);
    expect(params.get("tenant_id")).toBe("school-a");
    expect(params.get("tab")).toBe("governance");
    expect(params.get("view")).toBe("assurance");
    expect(params.get("stage")).toBe("senior");
    expect(params.get("subject")).toBe("english");
    expect(params.get("archetype")).toBe("essay");
    expect(params.has("risk")).toBe(false);
    expect(window.location.pathname).toBe("/admin/platform/model-config");
    expect(window.location.hash).toBe("");
    expect(window.history.state).toEqual({ preserved: true });
  });

  it("失效学校深链保留空选择，不能自动改成名单中第一所学校", () => {
    const schools = [{ id: "school-a", name: "甲学校", code: "A", status: "active" }] as Parameters<typeof selectModelSchool>[0];
    expect(selectModelSchool(schools, "", "removed-school")).toBe("");
  });

  it.each(["/admin/platform/model-config", "#/admin/platform/model-config"])("学校恢复地址兼容 %s，保留额外参数并移除 #", (path) => {
    expect(modelConfigLocation(`${path}?tenant_id=school-a&extra=keep`, "school-b", "bindings")).toBe("/admin/platform/model-config?tenant_id=school-b&extra=keep&tab=bindings");
    expect(modelConfigLocation(`${path}?tenant_id=school-a&extra=keep`, "", "models")).toBe("/admin/platform/model-config?extra=keep&tab=models");
  });

  it("旧学校深链仍可打开，切换页签后写回干净地址", async () => {
    window.history.replaceState({ preserved: true }, "", "/#/admin/platform/model-config?tenant_id=school-a&tab=bindings&extra=keep");
    await render();
    expect(host.querySelector<HTMLSelectElement>("#platform-model-school")?.value).toBe("school-a");
    await act(async () => button("学校模型").click());
    expect(window.location.pathname).toBe("/admin/platform/model-config");
    expect(window.location.hash).toBe("");
    expect(new URLSearchParams(window.location.search).get("extra")).toBe("keep");
    expect(window.history.state).toEqual({ preserved: true });
  });

  it("单项检测只锁定该模型的冲突操作，其他模型和页面入口仍可操作", async () => {
    const pending = deferred<ProbeResponse>();
    mocks.probe.mockReturnValue(pending.promise);
    const values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[1].base_url, model_name: configs[1].model_name };
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    await render();
    const probeA = button("检测连接", row("OpenAI"));
    await act(async () => { probeA.click(); probeA.click(); });
    expect(mocks.probe).toHaveBeenCalledTimes(1);
    expect(button("停用", row("OpenAI")).disabled).toBe(true);
    expect(button("删除", row("OpenAI")).disabled).toBe(true);
    expect(row("OpenAI").querySelector<HTMLButtonElement>('[aria-label="编辑OpenAI"]')?.disabled).toBe(false);
    expect(button("检测连接", row("智谱")).disabled).toBe(false);
    expect(button("停用", row("智谱")).disabled).toBe(false);
    expect(button("删除", row("智谱")).disabled).toBe(false);
    expect(button("添加模型").disabled).toBe(false);
    expect(button("刷新").disabled).toBe(false);
    expect(host.querySelector<HTMLSelectElement>("#platform-model-school")?.disabled).toBe(false);
    await act(async () => button(" 已启用 3 个").click());
    expect(row("百炼")).toBeNull();
    await act(async () => button("显示全部").click());
    await act(async () => button("三智能体配置").click());
    expect(new URLSearchParams(window.location.search).get("tab")).toBe("bindings");
    await act(async () => button("学校模型").click());
    await act(async () => button("停用", row("智谱")).click());
    expect(mocks.update).toHaveBeenLastCalledWith("智谱", "school-a", expect.objectContaining({ status: "disabled" }));
    expect(button("启用", row("智谱")).disabled).toBe(false);

    await act(async () => row("OpenAI").querySelector<HTMLButtonElement>('[aria-label="编辑OpenAI"]')!.click());
    expect(button("检测连接并更新", host.querySelector("aside")!).disabled).toBe(true);
    await act(async () => button("关闭", host.querySelector("aside")!).click());
    await act(async () => row("智谱").querySelector<HTMLButtonElement>('[aria-label="编辑智谱"]')!.click());
    expect(button("检测连接并更新", host.querySelector("aside")!).disabled).toBe(false);
    await act(async () => pending.resolve({ config: { ...configs[1], last_test_status: "success" }, result: { ok: true, message: "访问检查通过" } }));
    expect(host.querySelector("aside")).not.toBeNull();
    expect(button("停用", row("OpenAI")).disabled).toBe(false);
  });

  it("同一学校开放多个对话模型时串行保存，保留此前开放项", async () => {
    const pending = deferred<{ access: { revision: string; model_ids: string[] } }>();
    configs[1] = { ...configs[1], last_text_status: "success" };
    configs[3] = { ...configs[3], last_text_status: "success" };
    mocks.saveChatAccess.mockImplementationOnce(() => pending.promise);
    await render();
    const chooseA = conversationChoice("OpenAI");
    const chooseB = conversationChoice("智谱");
    // 两次点击放在同一事件轮，保护同步互斥；只靠下一次重绘禁用按钮会同时发出两次切换。
    await act(async () => { chooseA.click(); chooseB.click(); });
    expect(mocks.saveChatAccess).toHaveBeenCalledTimes(1);
    expect(conversationChoice("智谱").disabled).toBe(true);
    expect(button("停用", row("智谱")).disabled).toBe(false);
    expect(button("检测连接", row("智谱")).disabled).toBe(false);
    await act(async () => pending.resolve({ access: { revision: "saved-chat", model_ids: ["DeepSeek", "OpenAI"] } }));
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(conversationChoice("OpenAI").checked).toBe(true);
    expect(host.textContent).toContain("对话已开放 2 个");
    expect(conversationChoice("智谱").disabled).toBe(false);
  });

  it("开放保存冲突后可重新读取，页面刷新也同步更新开放范围", async () => {
    configs[1] = { ...configs[1], last_text_status: "success" };
    await render();
    mocks.saveChatAccess.mockRejectedValueOnce(new Error("conflict"));
    await act(async () => conversationChoice("OpenAI").click());
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(conversationChoice("OpenAI").checked).toBe(false);
    mocks.getChatAccess.mockResolvedValueOnce({ access: { revision: "changed", model_ids: ["OpenAI"] } });
    await act(async () => button("重新读取开放设置").click());
    expect(conversationChoice("OpenAI").checked).toBe(true);
    expect(conversationChoice("DeepSeek").checked).toBe(false);
    mocks.getChatAccess.mockResolvedValueOnce({ access: { revision: "new", model_ids: ["DeepSeek", "OpenAI"] } });
    await act(async () => button("刷新").click());
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(conversationChoice("OpenAI").checked).toBe(true);
    expect(host.textContent).toContain("对话已开放 2 个");
  });

  it("编辑期间刷新到新的启停和日常对话状态，保存连接字段时保留最新状态", async () => {
    const values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[0].base_url, model_name: "edited-model" };
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    await render();
    await act(async () => row("DeepSeek").querySelector<HTMLButtonElement>('[aria-label="编辑DeepSeek"]')!.click());
    configs[0] = { ...configs[0], status: "disabled", is_default: false };
    configs[1] = { ...configs[1], is_default: true, last_text_status: "success" };
    await act(async () => button("刷新").click());
    expect(host.querySelector("aside")).not.toBeNull();
    const saveButton = [...host.querySelectorAll<HTMLButtonElement>("aside button")].find((item) => item.textContent?.endsWith("更新"));
    expect(saveButton).toBeDefined();
    await act(async () => saveButton!.click());
    expect(mocks.update).toHaveBeenLastCalledWith("DeepSeek", "school-a", expect.objectContaining({ status: "disabled", is_default: false, model_name: "edited-model" }));
    // 开放范围与默认项独立；停用后仍可取消开放，原有开放事实不会由刷新改写。
    expect(conversationChoice("DeepSeek").disabled).toBe(false);
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(conversationChoice("OpenAI").checked).toBe(false);
  });

  it("切换学校后丢弃旧学校的检测回填和提示，即使两校模型 ID 相同", async () => {
    const pending = deferred<ProbeResponse>();
    const original = configs[1];
    const otherSchool = { ...original, tenant_id: "school-b", model_name: "school-b-model", last_test_message: "乙校原记录" };
    mocks.schools.mockResolvedValue([{ id: "school-a", name: "甲学校", code: "A", status: "active" }, { id: "school-b", name: "乙学校", code: "B", status: "active" }]);
    mocks.list.mockImplementation(async (tenantID: string) => ({ configs: tenantID === "school-b" ? [otherSchool] : configs }));
    mocks.probe.mockReturnValue(pending.promise);
    await render();
    await act(async () => button("检测连接", row("OpenAI")).click());
    await act(async () => {
      const select = host.querySelector<HTMLSelectElement>("#platform-model-school")!;
      select.value = "school-b";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(row("OpenAI").textContent).toContain("school-b-model");
    await act(async () => pending.resolve({ config: { ...original, model_name: "old-school-result", last_test_status: "failed" }, result: { ok: false, message: "旧学校检测失败" } }));
    expect(row("OpenAI").textContent).toContain("school-b-model");
    expect(row("OpenAI").textContent).not.toContain("old-school-result");
    expect(mocks.error).not.toHaveBeenCalled();
    expect(mocks.warning).not.toHaveBeenCalled();
  });

  it("检测后返回的旧刷新列表不能覆盖刚收到的检测状态", async () => {
    const refresh = deferred<{ configs: ManagedModelAPIConfig[] }>();
    const pending = deferred<ProbeResponse>();
    const stale = configs.map((item) => ({ ...item }));
    mocks.probe.mockReturnValue(pending.promise);
    await render();
    mocks.list.mockReturnValueOnce(refresh.promise);
    await act(async () => button("检测连接", row("OpenAI")).click());
    await act(async () => button("刷新").click());
    await act(async () => pending.resolve({ config: { ...configs[1], last_test_status: "success", last_test_message: "最新连接成功" }, result: { ok: true, message: "最新连接成功" } }));
    await act(async () => refresh.resolve({ configs: stale }));
    expect(row("OpenAI").textContent).toContain("连接正常");
  });

  it("刷新已读到更新后的连接配置时，稍后返回的旧版本检测不能覆盖它", async () => {
    const pending = deferred<ProbeResponse>();
    const original = { ...configs[1], updated_at: "2026-10-15T01:00:00Z" };
    configs[1] = original;
    mocks.probe.mockReturnValue(pending.promise);
    await render();
    await act(async () => button("检测连接", row("OpenAI")).click());
    const changed = { ...original, model_name: "changed-connection-model", updated_at: "2026-10-15T01:01:00Z" };
    // 后续回读仍应返回服务端的新版本，不能让默认夹具退回刷新前的配置。
    configs = configs.map((item) => item.id === changed.id ? changed : item);
    mocks.list.mockResolvedValueOnce({ configs });
    await act(async () => button("刷新").click());
    expect(row("OpenAI").textContent).toContain("changed-connection-model");
    await act(async () => pending.resolve({ config: { ...original, last_test_status: "success", updated_at: "2026-10-15T01:00:30Z" }, result: { ok: true, message: "旧配置连接成功" } }));
    expect(row("OpenAI").textContent).toContain("changed-connection-model");
    expect(row("OpenAI").textContent).not.toContain("连接正常");
  });

  it("结构化检测失败只更新行内状态，保留正在编辑的其他模型和焦点", async () => {
    const pending = deferred<ProbeResponse>();
    mocks.probe.mockReturnValue(pending.promise);
    await render();
    await act(async () => button("检测结构化输出", column("OpenAI", "structured")).click());
    await act(async () => row("智谱").querySelector<HTMLButtonElement>('[aria-label="编辑智谱"]')!.click());
    const editorButton = button("关闭", host.querySelector("aside")!);
    editorButton.focus();
    await act(async () => pending.resolve({ config: { ...configs[1], last_capability_status: "failed", last_capability_message: "输出格式不匹配" }, result: { ok: false, message: "输出格式不匹配", error_code: "schema_mismatch" } }));
    expect(document.activeElement).toBe(editorButton);
    expect(host.querySelector("aside")).not.toBeNull();
    expect(column("OpenAI", "structured").textContent).toContain("未通过");
    expect(mocks.detailInfo).not.toHaveBeenCalled();
    expect(mocks.detailWarning).not.toHaveBeenCalled();
    expect(mocks.detailError).not.toHaveBeenCalled();
    await act(async () => button("查看结构化详情", row("OpenAI")).click());
    expect(mocks.detailError).toHaveBeenCalledOnce();
  });

  it("离开页面后完成的检测不再弹出结果通知", async () => {
    const pending = deferred<ProbeResponse>();
    mocks.probe.mockReturnValue(pending.promise);
    await render();
    await act(async () => button("检测连接", row("OpenAI")).click());
    await act(async () => root.unmount());
    root = createRoot(host);
    await act(async () => pending.resolve({ config: { ...configs[1], last_test_status: "success" }, result: { ok: true, message: "已卸载页面的检测结果" } }));
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.warning).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("批量检测含已停用的全部厂商配置，限制并发并在单项失败后继续", async () => {
    // 挂起前三项验证共享并发上限；检测期间允许操作页面，单项失败后第四项仍执行。
    const releases: (() => void)[] = [];
    mocks.probe.mockImplementation((id: string) => new Promise((resolve, reject) => {
      releases.push(() => {
        if (id === "OpenAI") reject(new ApiClientError(503, "provider_unavailable", "暂不可用"));
        else resolve({ config: { ...configs.find((item) => item.id === id), last_test_status: id === "百炼" ? "failed" : "success" },
          result: { ok: id !== "百炼", error_code: id === "百炼" ? "invalid_api_key" : undefined } });
      });
    }));
    await render();
    await act(async () => button("检测全部连接").click());
    expect(mocks.probe).toHaveBeenCalledTimes(3);
    expect(button("检测中 0/4").disabled).toBe(true);
    expect(host.querySelector<HTMLSelectElement>("select")?.disabled).toBe(false);
    expect(button("停用", row("OpenAI")).disabled).toBe(true);
    await act(async () => releases.slice(0, 3).forEach((release) => release()));
    expect(mocks.probe).toHaveBeenCalledTimes(4);
    expect(button("检测中 3/4").disabled).toBe(true);
    await act(async () => releases[3]());
    expect(mocks.probe.mock.calls).toEqual(configs.map((item) => [item.id, "school-a", "quick"]));
    expect(mocks.warning).toHaveBeenCalledWith("全部连接检测完成：2 个正常，1 个暂时无法验证，1 个失败");
    expect(mocks.list).toHaveBeenCalledTimes(2);
    expect(button("检测全部连接").disabled).toBe(false);
    expect(host.querySelector<HTMLSelectElement>("select")?.disabled).toBe(false);
  });

  it("批量检测按返回结果分别汇总正常、暂不可用和失败，不额外刷新配置", async () => {
    const results: Record<string, { ok: boolean; error_code?: string }> = {
      OpenAI: { ok: false, error_code: "provider_unavailable" },
      "百炼": { ok: false, error_code: "invalid_api_key" }
    };
    mocks.probe.mockImplementation(async (id: string) => ({
      config: configs.find((item) => item.id === id),
      result: results[id] ?? { ok: true }
    }));
    await render();
    await act(async () => button("检测全部连接").click());
    expect(mocks.probe.mock.calls).toEqual(configs.map((item) => [item.id, "school-a", "quick"]));
    expect(mocks.warning).toHaveBeenCalledWith("全部连接检测完成：2 个正常，1 个暂时无法验证，1 个失败");
    expect(mocks.list).toHaveBeenCalledTimes(1);
    expect(button("检测全部连接").disabled).toBe(false);
    expect(host.querySelector<HTMLSelectElement>("select")?.disabled).toBe(false);
  });

  it("批量检测停止后续任务时只等已发出的三项结束，排队模型不发送请求", async () => {
    const pending = configs.map(() => deferred<ProbeResponse>());
    mocks.probe.mockImplementation((id: string) => pending[configs.findIndex((item) => item.id === id)].promise);
    await render();
    await act(async () => button("检测全部连接").click());
    expect(mocks.probe).toHaveBeenCalledTimes(3);
    await act(async () => button("停止后续检测").click());
    await act(async () => pending.slice(0, 3).forEach((item, index) => item.resolve({ config: configs[index], result: { ok: true, message: "访问检查通过" } })));
    expect(mocks.probe).toHaveBeenCalledTimes(3);
    expect(mocks.probe.mock.calls.every(([id]) => id !== "智谱")).toBe(true);
    expect(button("检测全部连接").disabled).toBe(false);
    expect(button("检测连接", row("智谱")).disabled).toBe(false);
  });

  it("启停配置不改写独立的对话开放范围", async () => {
    await render();
    expect(button("删除", row("DeepSeek")).disabled).toBe(true);
    await act(async () => button("停用", row("DeepSeek")).click());
    expect(mocks.update).toHaveBeenLastCalledWith("DeepSeek", "school-a", expect.objectContaining({ status: "disabled", is_default: false, api_key: "" }));
    expect(button("删除", row("DeepSeek")).disabled).toBe(false);
    await act(async () => button("启用", row("DeepSeek")).click());
    expect(mocks.update).toHaveBeenLastCalledWith("DeepSeek", "school-a", expect.objectContaining({ status: "active", is_default: false }));
    expect(button("停用", row("DeepSeek"))).toBeDefined();
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(row("DeepSeek").querySelector('[role="menu"]')).toBeNull();
  });

  it("状态更新失败保留原按钮，删除需确认且失败保留配置", async () => {
    await render();
    mocks.update.mockRejectedValueOnce(new Error("更新失败"));
    await act(async () => button("停用", row("OpenAI")).click());
    expect(button("停用", row("OpenAI")).disabled).toBe(false);
    await act(async () => button("删除", row("OpenAI")).click());
    expect(mocks.remove).not.toHaveBeenCalled();
    mocks.remove.mockRejectedValueOnce(new Error("模型仍被引用"));
    await act(async () => mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0].onOk());
    expect(row("OpenAI")).not.toBeNull();
    await act(async () => button("删除", row("OpenAI")).click());
    await act(async () => mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0].onOk());
    expect(mocks.remove).toHaveBeenLastCalledWith("OpenAI", "school-a");
    expect(row("OpenAI")).toBeNull();
  });

  it("无配置时禁用批量检测", async () => {
    configs = [];
    await render();
    expect(button("检测全部连接").disabled).toBe(true);
    expect(mocks.probe).not.toHaveBeenCalled();
  });

  it("已启用、访问检查和当前能力协议分别统计，摘要点击过滤真实配置", async () => {
    configs[0]={...configs[0],last_test_status:"success"};
    configs[1]={...configs[1],last_capability_status:"success",last_capability_probe_version:"structured-json-v4",last_text_status:"success"};
    configs[2]={...configs[2],last_test_status:"success",last_capability_status:"success",last_capability_probe_version:"structured-json-v4"};
    configs[3]={...configs[3],last_capability_status:"success",last_capability_probe_version:"structured-json-v2"};
    await render();
    expect(button(" 已启用 3 个")).toBeDefined();
    await act(async()=>button(" 访问检查通过 1 个").click());
    expect(host.querySelectorAll("[data-config-id]")).toHaveLength(1);expect(row("DeepSeek")).not.toBeNull();
    await act(async()=>button("结构化能力已验证 1 个").click());
    expect(host.querySelectorAll("[data-config-id]")).toHaveLength(1);expect(row("OpenAI")).not.toBeNull();
    await act(async()=>button("普通回复已验证 1 个").click());
    expect(host.querySelectorAll("[data-config-id]")).toHaveLength(1);expect(row("OpenAI")).not.toBeNull();
  });

  it("创建成功只确认访问，默认提供普通回复检测，结构化另行主动检测", async () => {
    const values:ConfigFormValues={supplier:"deepseek",api_key:"valid-key",model_name:"new-model"};
    mocks.form.getFieldValue.mockImplementation((name:keyof ConfigFormValues)=>values[name]);mocks.form.getFieldsValue.mockReturnValue(values);mocks.form.validateFields.mockResolvedValue(values);
    const saved={...config("new-model"),last_test_status:"success" as const};mocks.create.mockResolvedValue({config:saved,validation:{latency_ms:12}});
    await render();await act(async()=>button("添加模型").click());await act(async()=>button("检测连接并保存").click());
    const receipt=[...host.querySelectorAll("[data-alert]")].find(item=>item.textContent?.includes("密钥及模型访问权限检查通过"))!;
    expect(receipt.textContent).toContain("密钥及模型访问权限检查通过");expect(receipt.textContent).toContain("尚未验证普通回复与结构化能力");expect(receipt.textContent).not.toContain("模型可用");expect(mocks.probe).not.toHaveBeenCalled();
    const textReady = { ...saved, last_text_status: "success" as const };
    mocks.probe.mockResolvedValue({config:textReady,result:{ok:true,generated_request:true,message:"普通回复已验证",usage:{total_tokens:5}}});
    await act(async()=>button("检测普通回复", receipt).click());
    expect(mocks.probe).toHaveBeenCalledWith("new-model","school-a","text",false);
    expect(receipt.textContent).toContain("可开放给学校对话");
    mocks.probe.mockResolvedValue({config:{...textReady,last_capability_status:"success",last_capability_probe_version:"structured-json-v4"},result:{ok:true,generated_request:true,message:"结构化已验证",usage:{total_tokens:5}}});
    await act(async()=>button("检测结构化输出", receipt).click());
    expect(mocks.probe).toHaveBeenCalledWith("new-model","school-a","structured",true);
    expect(receipt.textContent).toContain("已通过当前结构化输出检测");
  });

  it("普通回复成功且结构化失败的模型仍可设为日常对话，只有连接成功的模型不能设置", async () => {
    configs[1] = { ...configs[1], last_test_status: "success", last_text_status: "success", last_capability_status: "failed" };
    configs[3] = { ...configs[3], last_test_status: "success", last_probe_mode: "quick" };
    await render();
    expect(column("OpenAI", "text").textContent).toContain("通过");
    expect(column("OpenAI", "structured").textContent).toContain("未通过");
    const useForChat = conversationChoice("OpenAI");
    expect(useForChat.disabled).toBe(false);
    await act(async () => useForChat.click());
    expect(mocks.saveChatAccess).toHaveBeenLastCalledWith("school-a", expect.objectContaining({ revision: "absent" }), ["DeepSeek", "OpenAI"]);
    expect(conversationChoice("DeepSeek").checked).toBe(true);
    expect(conversationChoice("智谱").disabled).toBe(true);
  });

  it.each(["provider_rate_limited", "provider_error"])("普通回复检测遇到 %s 时提示暂时无法验证并允许重试", async (errorCode) => {
    configs[1] = { ...configs[1], last_text_status: "success" };
    mocks.probe.mockResolvedValue({ config: { ...configs[1], last_test_status: "temporary_unavailable" }, result: { ok: false, error_code: errorCode, message: "供应商暂不可用" } });
    await render();
    await act(async () => button("检测普通回复", row("OpenAI")).click());
    expect(mocks.probe).toHaveBeenCalledWith("OpenAI", "school-a", "text", false);
    expect(mocks.warning).toHaveBeenLastCalledWith("OpenAI：供应商暂不可用（本次暂时无法验证，不代表模型已失效）");
    expect(mocks.error).not.toHaveBeenCalled();
    expect(column("OpenAI", "text").textContent).toContain("通过");
    expect(button("检测普通回复", row("OpenAI")).disabled).toBe(false);
  });

  it.each([400, 422])("普通回复检测的供应商 HTTP %s 参数错误显示失败，不能误报暂不可用", async (statusCode) => {
    mocks.probe.mockResolvedValue({ config: configs[1], result: { ok: false, error_code: "provider_error", status_code: statusCode, message: "供应商拒绝请求参数" } });
    await render();
    await act(async () => button("检测普通回复", row("OpenAI")).click());
    expect(mocks.error).toHaveBeenLastCalledWith("OpenAI：供应商拒绝请求参数");
    expect(mocks.warning).not.toHaveBeenCalled();
  });

  it("浏览器网络断开时普通回复检测显示暂时无法验证，保留历史成功证据", async () => {
    configs[1] = { ...configs[1], last_text_status: "success" };
    mocks.probe.mockRejectedValue(new TypeError("Failed to fetch"));
    await render();
    await act(async () => button("检测普通回复", row("OpenAI")).click());
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("本次暂时无法验证，不代表模型已失效"));
    expect(mocks.error).not.toHaveBeenCalled();
    expect(column("OpenAI", "text").textContent).toContain("通过");
  });

  it("关闭编辑并打开另一个模型后，旧的模型发现结果不覆盖新侧栏或结束新请求的加载状态", async () => {
    const previous = deferred<{ provider: { display_name: string }; models: string[] }>();
    const current = deferred<{ provider: { display_name: string }; models: string[] }>();
    let values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[1].base_url, model_name: configs[1].model_name };
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockImplementation(() => values);
    mocks.form.validateFields.mockImplementation(async () => values);
    mocks.discover.mockReturnValueOnce(previous.promise).mockReturnValueOnce(current.promise);
    await render();
    await act(async () => row("OpenAI").querySelector<HTMLButtonElement>('[aria-label="编辑OpenAI"]')!.click());
    await act(async () => button("获取可用模型", host.querySelector("aside")!).click());
    await act(async () => button("关闭", host.querySelector("aside")!).click());
    values = { ...values, model_name: configs[3].model_name };
    await act(async () => row("智谱").querySelector<HTMLButtonElement>('[aria-label="编辑智谱"]')!.click());
    expect(button("获取可用模型", host.querySelector("aside")!).disabled).toBe(false);
    await act(async () => button("获取可用模型", host.querySelector("aside")!).click());
    expect(mocks.discover).toHaveBeenCalledTimes(2);
    await act(async () => previous.resolve({ provider: { display_name: "旧编辑模型" }, models: ["old-model"] }));
    expect(mocks.success).not.toHaveBeenCalled();
    expect(button("获取可用模型", host.querySelector("aside")!).disabled).toBe(true);
    await act(async () => current.resolve({ provider: { display_name: "当前编辑模型" }, models: ["current-model"] }));
    expect(mocks.success).toHaveBeenCalledOnce();
    expect(mocks.success).toHaveBeenLastCalledWith("已从当前编辑模型获取 1 个模型，请选择");
    expect(button("已找到 1 个，点击选择", host.querySelector("aside")!).disabled).toBe(false);
  });

  it("停用配置说明复用限制并禁用模型发现，仍可手动编辑保存且不自动启用", async () => {
    const values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[2].base_url, model_name: "edited-model" };
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    await render();
    await act(async () => row("百炼").querySelector<HTMLButtonElement>('[aria-label="编辑百炼"]')!.click());
    const drawer = host.querySelector("aside")!;
    expect(drawer.textContent).toContain("此配置已停用");
    expect(drawer.textContent).toContain("模型列表中启用后重试");
    const discover = button("获取可用模型", drawer);
    expect(discover.disabled).toBe(true);
    await act(async () => discover.click());
    expect(mocks.discover).not.toHaveBeenCalled();
    expect(button("检测连接并更新", drawer).disabled).toBe(false);
    await act(async () => button("检测连接并更新", drawer).click());
    expect(mocks.update).toHaveBeenLastCalledWith("百炼", "school-a", expect.objectContaining({ status: "disabled", model_name: "edited-model", api_key: "" }));
  });

  it("停用配置输入新密钥后可获取列表，清空密钥恢复禁用且不发送来源引用", async () => {
    const values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[2].base_url, model_name: "" };
    mocks.watch.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockImplementation(() => values);
    mocks.form.validateFields.mockResolvedValue(values);
    mocks.discover.mockResolvedValue({ provider: { display_name: "其他兼容接口" }, models: ["listed-model"] });
    await render();
    await act(async () => row("百炼").querySelector<HTMLButtonElement>('[aria-label="编辑百炼"]')!.click());
    expect(button("获取可用模型").disabled).toBe(true);
    values.api_key = "replacement-key-at-least-16";
    await render();
    expect(button("获取可用模型").disabled).toBe(false);
    await act(async () => button("获取可用模型").click());
    expect(mocks.discover).toHaveBeenCalledWith({ tenant_id: "school-a", provider: "custom", base_url: values.base_url, api_key: values.api_key });
    values.api_key = " ";
    await render();
    expect(button("获取可用模型").disabled).toBe(true);
    expect(mocks.discover).toHaveBeenCalledOnce();
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("模型发现期间刷新到停用状态会禁用入口并丢弃晚到列表，重新启用后恢复入口", async () => {
    const pending = deferred<{ provider: { display_name: string }; models: string[] }>();
    const values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[1].base_url, model_name: "" };
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    mocks.discover.mockReturnValue(pending.promise);
    await render();
    await act(async () => row("OpenAI").querySelector<HTMLButtonElement>('[aria-label="编辑OpenAI"]')!.click());
    await act(async () => button("获取可用模型").click());
    configs[1] = { ...configs[1], status: "disabled" };
    await act(async () => button("刷新").click());
    expect(button("获取可用模型").disabled).toBe(true);
    await act(async () => pending.resolve({ provider: { display_name: "旧供应商" }, models: ["old-model"] }));
    expect(mocks.success).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain("已找到 1 个");
    configs[1] = { ...configs[1], status: "active" };
    await act(async () => button("刷新").click());
    expect(button("获取可用模型").disabled).toBe(false);
  });

  it("页面仍显示启用而后台已停用时，模型发现明确提示状态冲突并保留表单", async () => {
    const values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: configs[1].base_url, model_name: "" };
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    mocks.discover.mockRejectedValue(new ApiClientError(409, "managed_model_credential_source_disabled", "disabled"));
    await render();
    await act(async () => row("OpenAI").querySelector<HTMLButtonElement>('[aria-label="编辑OpenAI"]')!.click());
    await act(async () => button("获取可用模型").click());
    expect(mocks.error).toHaveBeenLastCalledWith("密钥来源配置已停用，请先启用该配置，再使用已保存的密钥获取模型列表或添加模型。");
    expect(host.querySelector("aside")).not.toBeNull();
    expect(mocks.form.resetFields).not.toHaveBeenCalled();
  });

  it("获取到空模型列表时保留添加表单，可继续手工输入和保存", async () => {
    const values: ConfigFormValues = { supplier: "deepseek", api_key: "valid-key-for-test", model_name: "manual-model" };
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    mocks.discover.mockResolvedValue({ provider: { display_name: "DeepSeek" }, models: [] });
    await render();
    await act(async () => button("添加模型").click());
    await act(async () => button("获取可用模型").click());
    expect(mocks.warning).toHaveBeenLastCalledWith("DeepSeek没有返回可用模型");
    expect(host.querySelector("aside")).not.toBeNull();
    expect(button("检测连接并保存").disabled).toBe(false);
    expect(mocks.form.resetFields).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("只改同域 API 路径时获取模型和保存都沿用旧密钥，不要求重新填写", async () => {
    const values: ConfigFormValues = { supplier: "custom", api_key: "", base_url: "https://example.test/", model_name: configs[1].model_name };
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    mocks.discover.mockResolvedValue({ provider: { display_name: "其他兼容接口" }, models: [values.model_name] });
    await render();
    await act(async () => row("OpenAI").querySelector<HTMLButtonElement>('[aria-label="编辑OpenAI"]')!.click());
    expect(host.querySelector("aside")?.textContent).toContain("同域名、同端口修改路径无需重填");
    await act(async () => button("获取可用模型").click());
    expect(mocks.form.validateFields).toHaveBeenCalledWith(["supplier", "base_url"]);
    expect(mocks.discover).toHaveBeenCalledWith({ tenant_id: "school-a", credential_source_id: "OpenAI", provider: "custom", base_url: "https://example.test/" });
    await act(async () => button("检测连接并更新").click());
    expect(mocks.update).toHaveBeenLastCalledWith("OpenAI", "school-a", expect.objectContaining({ base_url: "https://example.test/", api_key: "" }));
  });

  it("新建表单保留获取模型和保存入口，暂不可用时警告且保留填写界面", async () => {
    const values: ConfigFormValues = { supplier: "deepseek", api_key: "valid-key-for-test", model_name: "model" };
    mocks.form.getFieldValue.mockImplementation((name: keyof ConfigFormValues) => values[name]);
    mocks.form.getFieldsValue.mockReturnValue(values);
    mocks.form.validateFields.mockResolvedValue(values);
    mocks.discover.mockRejectedValue(new ApiClientError(503, "provider_unavailable", "暂不可用"));
    mocks.create.mockRejectedValue(new ApiClientError(503, "provider_unavailable", "暂不可用"));
    await render();
    await act(async () => button("添加模型").click());
    expect(host.querySelector("aside")?.textContent).toContain("供应商");
    expect(host.querySelector("aside")?.textContent).toContain("API Key");
    await act(async () => button("获取可用模型").click());
    expect(mocks.warning).toHaveBeenLastCalledWith(expect.stringContaining("（本次暂时无法验证，不代表 API Key 无效）"));
    expect(mocks.discover).toHaveBeenLastCalledWith({ tenant_id: "school-a", api_key: values.api_key, provider: "deepseek", base_url: undefined });
    await act(async () => button("检测连接并保存").click());
    expect(mocks.warning).toHaveBeenLastCalledWith(expect.stringContaining("（本次暂时无法验证，已保留当前填写内容）"));
    expect(button("检测连接并保存").disabled).toBe(false);
    expect(host.querySelector("aside")).not.toBeNull();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("单模型返回暂不可用时保留警告文案，解除操作锁后仍可检测", async () => {
    mocks.probe.mockResolvedValue({ config: configs[1], result: { ok: false, error_code: "provider_unavailable", message: "暂不可用" } });
    await render();
    await act(async () => button("检测连接", row("OpenAI")).click());
    expect(mocks.warning).toHaveBeenLastCalledWith("OpenAI：暂不可用（本次暂时无法验证，不代表模型已失效）");
    expect(mocks.error).not.toHaveBeenCalled();
    expect(button("检测连接", row("OpenAI")).disabled).toBe(false);
    expect(host.querySelector<HTMLSelectElement>("select")?.disabled).toBe(false);
  });

  it.each([undefined, 400, 503])("保存失败按内层供应商 HTTP %s 判断临时性，不使用网关的 422 状态", async (statusCode) => {
    const values: ConfigFormValues = { supplier: "deepseek", api_key: "valid-key-for-test", model_name: "model" };
    mocks.form.validateFields.mockResolvedValue(values);
    mocks.create.mockRejectedValue(new ApiClientError(422, "managed_model_validation_failed", "验证失败", {
      validation: { error_code: "provider_error", status_code: statusCode } as ManagedAPIProbeResult
    }));
    await render();
    await act(async () => button("添加模型").click());
    await act(async () => button("检测连接并保存").click());
    if (!statusCode || statusCode === 503) {
      expect(mocks.warning).toHaveBeenLastCalledWith(expect.stringContaining("本次暂时无法验证"));
      expect(mocks.error).not.toHaveBeenCalled();
    } else {
      expect(mocks.error).toHaveBeenCalledOnce();
      expect(mocks.warning).not.toHaveBeenCalled();
    }
    expect(host.querySelector("aside")).not.toBeNull();
  });
});
