import type { ManagedAdapterType, ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { listTenants, type Tenant } from "../../../api/org";

export type SupplierPreset = "aliyun" | "deepseek" | "openai" | "zhipu" | "moonshot" | "anthropic" | "gemini" | "custom";

export interface ConfigFormValues {
  supplier: SupplierPreset;
  provider_key?: string;
  display_name?: string;
  adapter_type?: ManagedAdapterType;
  base_url?: string;
  api_key: string;
  model_name: string;
  model_version?: string;
  region?: string;
  enabled?: boolean;
  is_default?: boolean;
}

export interface ModelConfigDraft {
  values: ConfigFormValues;
  expiresAt: number;
}

export const MODEL_CONFIG_DRAFT_TTL_MS = 2 * 60 * 1000;

export const supplierOptions = [
  { value: "aliyun", label: "阿里云百炼" },
  { value: "deepseek", label: "DeepSeek" },
  { value: "openai", label: "OpenAI" },
  { value: "zhipu", label: "智谱 AI" },
  { value: "moonshot", label: "Moonshot / Kimi" },
  { value: "anthropic", label: "Anthropic" },
  { value: "gemini", label: "Google Gemini" },
  { value: "custom", label: "其他兼容接口" }
];

export const supplierDefaults: Record<SupplierPreset, Partial<ConfigFormValues>> = {
  aliyun: { provider_key: "aliyun", display_name: "阿里云百炼", adapter_type: "openai_compatible", region: "cn" },
  deepseek: { provider_key: "deepseek", display_name: "DeepSeek", adapter_type: "openai_compatible", region: "global" },
  openai: { provider_key: "openai", display_name: "OpenAI", adapter_type: "openai_compatible", region: "global" },
  zhipu: { provider_key: "zhipu", display_name: "智谱 AI", adapter_type: "openai_compatible", region: "cn" },
  moonshot: { provider_key: "moonshot", display_name: "Moonshot / Kimi", adapter_type: "openai_compatible", region: "cn" },
  anthropic: { provider_key: "anthropic", display_name: "Anthropic", adapter_type: "openai_compatible", region: "global" },
  gemini: { provider_key: "gemini", display_name: "Google Gemini", adapter_type: "openai_compatible", region: "global" },
  custom: { provider_key: "custom", display_name: "自定义供应商", adapter_type: "openai_compatible", region: "global" }
};

export function formatDateTime(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false
  }).format(date);
}

export function presetForConfig(config: ManagedModelAPIConfig): SupplierPreset {
  if (config.provider_key === "aliyun") return "aliyun";
  if (config.provider_key === "deepseek") return "deepseek";
  if (config.provider_key === "openai") return "openai";
  if (config.provider_key === "zhipu") return "zhipu";
  if (config.provider_key === "moonshot") return "moonshot";
  if (config.provider_key === "anthropic") return "anthropic";
  if (config.provider_key === "gemini") return "gemini";
  return "custom";
}

export function modelConfigOptionLabel(config: ManagedModelAPIConfig) {
  return `${config.display_name} · ${config.model_name} · 密钥 ${config.credential_hint || "已加密"} · ${config.id.slice(0, 8)}`;
}

export function modelConfigDraftKey(tenantID: string, editingID?: string, credentialSourceID?: string) {
  if (editingID) return `${tenantID}:edit:${editingID}`;
  if (credentialSourceID) return `${tenantID}:reuse:${credentialSourceID}`;
  return `${tenantID}:create`;
}

// 能力通过还须匹配当前探测协议，旧版探测成功不能代替当前结构化输出验证。
export function capabilityVerified(config: ManagedModelAPIConfig) {
  return config.last_capability_status === "success" && config.last_capability_probe_version === "structured-json-v3";
}

export function isTransientProbeError(code?: string) {
  return code === "provider_timeout" || code === "provider_unavailable";
}

export function applySavedModelConfig(current: ManagedModelAPIConfig[], saved: ManagedModelAPIConfig) {
  // 默认模型由服务端确认；同一学校只能保留一个日常对话默认项。
  return current.map((item) => {
    if (item.id === saved.id) return saved;
    if (saved.is_default) return { ...item, is_default: false };
    return item;
  });
}

export function modelUsageMessage(config: ManagedModelAPIConfig, status: "active" | "disabled", isDefault: boolean) {
  if (isDefault) return `${config.display_name} 已设为日常对话模型`;
  if (status === "disabled") {
    const fallback = config.is_default ? "，日常对话已切回本地模型" : "";
    return `${config.display_name} 已停用${fallback}`;
  }
  if (config.is_default) return "日常对话已切回本地模型";
  return `${config.display_name} 已恢复`;
}

export function modelSaveLabel(editing: ManagedModelAPIConfig | null) {
  if (editing?.is_default) return "验证并更新";
  if (editing) return "检测连接并更新";
  return "检测连接并保存";
}

export function hasModelFormChanges(values: ConfigFormValues, editing: ManagedModelAPIConfig | null, credentialSource: ManagedModelAPIConfig | null) {
  const apiKey = String(values.api_key ?? "").trim();
  const baseURL = String(values.base_url ?? "").trim();
  const modelName = String(values.model_name ?? "").trim();
  if (editing) return apiKey !== "" || baseURL !== editing.base_url || modelName !== editing.model_name;
  // 沿用密钥的新模型不编辑供应商和地址，只判断用户是否填写了模型名。
  if (credentialSource) return modelName !== "";
  return Boolean(values.supplier || apiKey || baseURL || modelName);
}

export function capabilityDialogPresentation(displayName: string, temporarilyUnavailable: boolean, failed: boolean): { kind: "warning" | "error" | "info"; title: string } {
  if (temporarilyUnavailable) return { kind: "warning", title: `${displayName}：能力检测暂时无法完成` };
  if (failed) return { kind: "error", title: `${displayName}：结构化输出检测未通过` };
  return { kind: "info", title: `${displayName}：能力检测详情` };
}

export function connectionStatusLabel(config: ManagedModelAPIConfig) {
  if (config.last_test_status === "success") return "正常";
  if (config.last_test_status === "temporary_unavailable") return "暂时无法验证";
  if (config.last_test_status === "failed") return "异常";
  return "未测试";
}

export function probeFormatLabel(value?: string) {
  if (value === "json_object") return "JSON Object 模式";
  if (value === "json_schema") return "严格 JSON Schema";
  if (value === "prompt_only") return "提示词约束";
  return "—";
}

export async function loadAllSchoolTenants(): Promise<Tenant[]> {
  const schools: Tenant[] = [];
  let cursor = "";
  for (let page = 0; page < 20; page += 1) {
    const response = await listTenants({ limit: 100, cursor: cursor || undefined });
    schools.push(...response.tenants.filter((tenant) => tenant.code !== "platform"));
    if (!response.has_more || !response.next_cursor) break;
    cursor = response.next_cursor;
  }
  return schools;
}
