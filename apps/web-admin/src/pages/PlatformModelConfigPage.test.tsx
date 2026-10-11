// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TableColumnsType } from "antd";
import { ApiClientError } from "../api/client";
import type { ManagedModelAPIConfig, ManagedModelAPIConfigUpdateInput } from "../api/modelApiConfig";
import { PlatformModelConfigPage } from "./PlatformModelConfigPage";
import { modelConfigLocation, selectModelSchool } from "../features/platform-model-config/lib/modelConfigLocation";
import {
  applySavedModelConfig,
  capabilityDialogPresentation,
  hasModelFormChanges,
  modelSaveLabel,
  modelUsageMessage,
  type ConfigFormValues
} from "../features/platform-model-config/lib/modelConfig";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), probe: vi.fn(), update: vi.fn(), remove: vi.fn(), schools: vi.fn(), discover: vi.fn(), create: vi.fn(), bindings: vi.fn(),
  success: vi.fn(), warning: vi.fn(), error: vi.fn(), confirm: vi.fn(), invalidate: vi.fn(),
  form: { setFieldsValue: vi.fn(), resetFields: vi.fn(), validateFields: vi.fn(), getFieldValue: vi.fn(), getFieldsValue: vi.fn() }
}));
const appContext = vi.hoisted(() => ({
  message: { success: mocks.success, warning: mocks.warning, error: mocks.error }, modal: { confirm: mocks.confirm }
}));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => ({ invalidateQueries: mocks.invalidate }) }));
vi.mock("../api/modelApiConfig", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/modelApiConfig")>(),
  listManagedModelAPIConfigs: mocks.list, probeManagedModelAPIConfig: mocks.probe,
  updateManagedModelAPIConfig: mocks.update, deleteManagedModelAPIConfig: mocks.remove,
  listAvailableManagedModels: mocks.discover, autoCreateManagedModelAPIConfig: mocks.create
}));
vi.mock("../features/platform-model-config/lib/modelConfig", async (importOriginal) => ({
  ...await importOriginal<typeof import("../features/platform-model-config/lib/modelConfig")>(),
  loadAllSchoolTenants: mocks.schools
}));
vi.mock("../features/platform-model-config/hooks/useModelConfigDrafts", () => ({
  useModelConfigDrafts: () => ({ clearFormDraft: vi.fn(), saveFormDraft: vi.fn(), restoreFormDraft: vi.fn() })
}));
vi.mock("../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: vi.fn() }));
vi.mock("./PanelModelBindingsSection", () => ({ PanelModelBindingsSection: mocks.bindings }));
vi.mock("../components/model-governance/ManagedGovernanceWorkspace", () => ({ ManagedGovernanceWorkspace: () => null }));
vi.mock("../components/ResponsiveTable", () => ({
  ResponsiveTable: ({ columns, dataSource }: { columns: TableColumnsType<ManagedModelAPIConfig>; dataSource: ManagedModelAPIConfig[] }) =>
    <div>{dataSource.map((config, index) => <div key={config.id} data-config-id={config.id}>
      {columns.map((column) => "render" in column && column.render
        ? <div key={String(column.key)}>{column.render(undefined, config, index) as ReactNode}</div> : null)}
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
    Form: Object.assign(passthrough, { useForm: () => [mocks.form], useWatch: () => undefined,
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

describe("模型配置显示和草稿边界", () => {
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
    expect(modelUsageMessage(current, "active", true)).toBe("model 已设为日常对话模型");
    expect(modelUsageMessage(current, "disabled", false)).toBe("model 已停用，日常对话已切回本地模型");
    expect(modelUsageMessage(current, "active", false)).toBe("日常对话已切回本地模型");
    expect(modelUsageMessage(config("model"), "disabled", false)).toBe("model 已停用");
    expect(modelUsageMessage(config("model"), "active", false)).toBe("model 已恢复");
    expect(modelSaveLabel(current)).toBe("验证并更新");
    expect(modelSaveLabel(config("model"))).toBe("检测连接并更新");
    expect(modelSaveLabel(null)).toBe("检测连接并保存");
  });

  it("暂时无法检测优先于失败提示，不把供应商暂不可用显示为密钥失效", () => {
    expect(capabilityDialogPresentation("model", true, true)).toEqual({ kind: "warning", title: "model：能力检测暂时无法完成" });
    expect(capabilityDialogPresentation("model", false, true)).toEqual({ kind: "error", title: "model：结构化输出检测未通过" });
    expect(capabilityDialogPresentation("model", false, false)).toEqual({ kind: "info", title: "model：能力检测详情" });
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
    mocks.bindings.mockImplementation(() => null);
    window.location.hash = "";
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    configs = [config("DeepSeek", "active", true), config("OpenAI"), config("百炼", "disabled"), config("智谱")];
    mocks.schools.mockResolvedValue([{ id: "school-a", name: "甲学校", code: "A", status: "active" }]);
    mocks.list.mockImplementation(async () => ({ configs }));
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
  });

  function button(text: string, scope: ParentNode = host) {
    const found = [...scope.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text);
    expect(found, text).toBeDefined();
    return found!;
  }
  function row(id: string) {
    return host.querySelector(`[data-config-id="${id}"]`)!;
  }
  async function render() {
    await act(async () => root.render(<PlatformModelConfigPage />));
  }

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
    expect(new URLSearchParams(window.location.hash.split("?")[1]).get("tenant_id")).toBe("school-b");
    expect(new URLSearchParams(window.location.hash.split("?")[1]).get("tab")).toBe("bindings");
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
    expect(new URLSearchParams(window.location.hash.split("?")[1]).get("tenant_id")).toBe("school-a");
    expect(new URLSearchParams(window.location.hash.split("?")[1]).get("tab")).toBe("bindings");
    expect(mocks.list.mock.calls).toHaveLength(listCalls);
    expect(mocks.schools.mock.calls).toHaveLength(schoolCalls);
    await act(async () => callbacks.onSavingChange(false));
    await act(async () => selectOther());
    await act(async () => mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1]![0].onOk());
    expect(new URLSearchParams(window.location.hash.split("?")[1]).get("tenant_id")).toBe("school-b");
  });

  it("从当前高中英语评分组合进入检查时保留范围，但不沿用旧题目的风险级别", async () => {
    window.location.hash = "/admin/platform/model-config?tenant_id=school-a&tab=bindings&risk=R2";
    await render();
    const callbacks = mocks.bindings.mock.calls[mocks.bindings.mock.calls.length - 1]![0] as { onOpenGovernance: (view: string, scope: { stage: string; subject: string; archetype: string }) => void };
    await act(async () => callbacks.onOpenGovernance("assurance", { stage: "senior", subject: "english", archetype: "essay" }));
    const params = new URLSearchParams(window.location.hash.split("?")[1]);
    expect(params.get("tenant_id")).toBe("school-a");
    expect(params.get("tab")).toBe("governance");
    expect(params.get("view")).toBe("assurance");
    expect(params.get("stage")).toBe("senior");
    expect(params.get("subject")).toBe("english");
    expect(params.get("archetype")).toBe("essay");
    expect(params.has("risk")).toBe(false);
  });

  it("失效学校深链保留空选择，不能自动改成名单中第一所学校", () => {
    const schools = [{ id: "school-a", name: "甲学校", code: "A", status: "active" }] as Parameters<typeof selectModelSchool>[0];
    expect(selectModelSchool(schools, "", "removed-school")).toBe("");
    expect(modelConfigLocation("#/admin/platform/model-config?tenant_id=school-b&extra=keep", "school-b", "bindings")).toBe("#/admin/platform/model-config?tenant_id=school-b&extra=keep&tab=bindings");
  });

  it("批量检测含已停用的全部厂商配置，限制并发并在单项失败后继续", async () => {
    // 挂起前三项，验证检测期间锁定上下文；释放后第四项仍执行，失败不会提前结束。
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
    expect(host.querySelector<HTMLSelectElement>("select")?.disabled).toBe(true);
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

  it("每行直接停用和恢复，默认模型停用后恢复为备用，菜单不再显示停用或删除", async () => {
    await render();
    expect(button("删除", row("DeepSeek")).disabled).toBe(true);
    await act(async () => button("停用", row("DeepSeek")).click());
    expect(mocks.update).toHaveBeenLastCalledWith("DeepSeek", "school-a", expect.objectContaining({ status: "disabled", is_default: false, api_key: "" }));
    expect(button("删除", row("DeepSeek")).disabled).toBe(false);
    await act(async () => button("恢复", row("DeepSeek")).click());
    expect(mocks.update).toHaveBeenLastCalledWith("DeepSeek", "school-a", expect.objectContaining({ status: "active", is_default: false }));
    expect(button("停用", row("DeepSeek"))).toBeDefined();
    await act(async () => row("DeepSeek").querySelector<HTMLButtonElement>('[aria-label="DeepSeek更多操作"]')?.click());
    const menu = row("DeepSeek").querySelector('[role="menu"]')!;
    expect(menu.textContent).toContain("完整能力检测");
    expect(menu.textContent).not.toMatch(/停用|恢复|删除/);
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
    configs[1]={...configs[1],last_capability_status:"success",last_capability_probe_version:"structured-json-v3"};
    configs[2]={...configs[2],last_test_status:"success",last_capability_status:"success",last_capability_probe_version:"structured-json-v3"};
    configs[3]={...configs[3],last_capability_status:"success",last_capability_probe_version:"structured-json-v2"};
    await render();
    expect(button(" 已启用 3 个")).toBeDefined();
    await act(async()=>button(" 访问检查通过 1 个").click());
    expect(host.querySelectorAll("[data-config-id]")).toHaveLength(1);expect(row("DeepSeek")).not.toBeNull();
    await act(async()=>button("结构化能力已验证 1 个").click());
    expect(host.querySelectorAll("[data-config-id]")).toHaveLength(1);expect(row("OpenAI")).not.toBeNull();
  });

  it("创建成功只确认访问，能力检测是原学校配置的独立授权动作", async () => {
    const values:ConfigFormValues={supplier:"deepseek",api_key:"valid-key",model_name:"new-model"};
    mocks.form.getFieldValue.mockImplementation((name:keyof ConfigFormValues)=>values[name]);mocks.form.getFieldsValue.mockReturnValue(values);mocks.form.validateFields.mockResolvedValue(values);
    const saved={...config("new-model"),last_test_status:"success" as const};mocks.create.mockResolvedValue({config:saved,validation:{latency_ms:12}});
    await render();await act(async()=>button("添加模型").click());await act(async()=>button("检测连接并保存").click());
    const receipt=[...host.querySelectorAll("[data-alert]")].find(item=>item.textContent?.includes("密钥及模型访问权限检查通过"))!;
    expect(receipt.textContent).toContain("密钥及模型访问权限检查通过");expect(receipt.textContent).toContain("尚未验证回复与结构化能力");expect(receipt.textContent).not.toContain("模型可用");expect(mocks.probe).not.toHaveBeenCalled();
    mocks.probe.mockResolvedValue({config:{...saved,last_capability_status:"success",last_capability_probe_version:"structured-json-v3"},result:{ok:true,generated_request:true,message:"能力已验证",usage:{total_tokens:5}}});
    await act(async()=>button("检测回复与结构化能力").click());
    expect(mocks.probe).toHaveBeenCalledWith("new-model","school-a","capability",true);
    expect(receipt.textContent).toContain("已通过当前结构化能力检测");
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
});
