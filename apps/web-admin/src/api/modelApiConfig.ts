import { ApiClientError, apiClient, getUserErrorMessage } from "./client";

export type ManagedAdapterType = "openai_compatible" | "dashscope_native";
export type ManagedAPIConfigStatus = "active" | "disabled";
export type ManagedAPIProbeStatus = "untested" | "success" | "failed" | "temporary_unavailable";

export interface ManagedModelAPIConfig {
  id: string;
  tenant_id: string;
  provider_key: string;
  display_name: string;
  adapter_type: ManagedAdapterType;
  base_url: string;
  model_name: string;
  model_version: string;
  region: string;
  credential_configured: boolean;
  credential_hint?: string;
  status: ManagedAPIConfigStatus;
  is_default: boolean;
  last_test_status: ManagedAPIProbeStatus;
  last_test_message?: string;
  last_test_latency_ms?: number;
  last_tested_at?: string;
  last_successful_tested_at?: string;
  last_probe_mode?: ManagedAPIProbeMode;
  last_text_status?: ManagedAPIProbeStatus;
  last_text_message?: string;
  last_text_tested_at?: string;
  last_text_usage?: ManagedAPIProbeUsage;
  last_text_diagnostic?: ManagedAPIProbeDiagnostic;
  last_capability_status?: ManagedAPIProbeStatus;
  last_capability_message?: string;
  last_capability_tested_at?: string;
  last_capability_probe_version?: string;
  last_capability_usage?: ManagedAPIProbeUsage;
  last_capability_diagnostic?: ManagedAPIProbeDiagnostic;
  config_source: "auto" | "manual" | "imported";
  provider_registry_version?: string;
  created_at: string;
  updated_at: string;
}

// capability 保留为旧客户端兼容别名；新结构化检测统一使用 structured。
export type ManagedAPIProbeMode = "quick" | "text" | "structured" | "capability";

export interface ManagedAPIProbeUsage {
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
}

export interface ManagedAPIProbeDiagnostic {
  finish_reason?: string;
  response_format?: string;
  content_length?: number;
  content_sha256?: string;
  content_preview?: string;
}

export type PanelAgentRole = "primary_a" | "primary_b" | "arbiter";

export interface DocumentModelSelection {
  revision: string;
  mode: "follow_chat" | "model";
  model_config_id?: string;
}

export interface AnswerSheetModelSelection {
  revision: string;
  mode: "unconfigured" | "model";
  model_config_id?: string;
}

export interface ModelApplications {
  document_selection: DocumentModelSelection;
  answer_sheet_selection?: AnswerSheetModelSelection;
  grading_selection?: AnswerSheetModelSelection;
  grading_model_ids: string[];
  grading_uses?: GradingModelUse[];
}

export interface GradingModelUse {
  model_config_id: string;
  education_stage: PanelEducationStage;
  subject_code: string;
  archetype_code: string;
  agent_role: PanelAgentRole;
}

export function getModelApplications(tenantId: string) {
  return apiClient.request<ModelApplications>(`/api/v1/platform/model-applications${tenantQuery(tenantId)}`);
}

export function saveDocumentModelSelection(tenantId: string, input: Omit<DocumentModelSelection, "revision"> & { expected_revision: string }) {
  return apiClient.request<{ selection: DocumentModelSelection }>("/api/v1/platform/document-model-selection", {
    method: "PUT", body: JSON.stringify({ tenant_id: tenantId, ...input })
  });
}

export function saveAnswerSheetModelSelection(tenantId: string, input: Omit<AnswerSheetModelSelection, "revision"> & { expected_revision: string }) {
  return apiClient.request<{ selection: AnswerSheetModelSelection }>("/api/v1/platform/answer-sheet-model-selection", {
    method: "PUT", body: JSON.stringify({ tenant_id: tenantId, ...input })
  });
}

export function saveGradingModelSelection(tenantId: string, input: Omit<AnswerSheetModelSelection, "revision"> & { expected_revision: string }) {
  return apiClient.request<{ selection: AnswerSheetModelSelection }>("/api/v1/platform/grading-model-selection", {
    method: "PUT", body: JSON.stringify({ tenant_id: tenantId, ...input })
  });
}

export function probeGradingModelSelection(tenantId: string) {
  return apiClient.request<{ capability: { visual_supported: boolean; model_version: string } }>("/api/v1/platform/grading-model-selection/probe", {
    method: "POST", body: JSON.stringify({ tenant_id: tenantId, request_id: `vision-probe-${crypto.randomUUID()}` })
  });
}
export type PanelEducationStage = "junior" | "senior";

export interface PanelModelRoleBinding {
  revision: string;
  id: string;
  tenant_id: string;
  education_stage: PanelEducationStage;
  subject_code: string;
  archetype_code: string;
  agent_role: PanelAgentRole;
  managed_model_api_config_id: string;
  prompt_version: string;
  strength_rank: number;
  status: "active" | "disabled";
  created_by: string;
  created_at: string;
  updated_at: string;
}

export type PanelModelRoleBindingInput = Pick<PanelModelRoleBinding,
  "education_stage" | "subject_code" | "archetype_code" | "agent_role" |
  "managed_model_api_config_id" | "prompt_version" | "strength_rank" | "status"> & { tenant_id: string; expected_revision: string };

export interface ManagedModelAPIConfigInput {
  tenant_id: string;
  provider_key: string;
  display_name: string;
  adapter_type: ManagedAdapterType;
  base_url: string;
  model_name: string;
  model_version: string;
  region: string;
  api_key: string;
  status: ManagedAPIConfigStatus;
  is_default: boolean;
}

export type ManagedModelAPIConfigUpdateInput = Omit<ManagedModelAPIConfigInput, "tenant_id" | "provider_key">;

export interface ManagedAPIProbeResult {
  ok: boolean;
  probe_mode: ManagedAPIProbeMode;
  generated_request: boolean;
  reused?: boolean;
  coalesced?: boolean;
  provider?: string;
  model?: string;
  status_code?: number;
  latency_ms: number;
  message: string;
  error_code?: string;
  credential_check: ManagedAPICheckResult;
  model_check: ManagedAPICheckResult;
  text_check: ManagedAPICheckResult;
  capability_check: ManagedAPICheckResult;
  usage: ManagedAPIProbeUsage;
  diagnostic: ManagedAPIProbeDiagnostic;
  connection_diagnostic: ManagedAPIConnectionDiagnostic;
}

export interface ManagedAPIConnectionDiagnostic {
  attempts?: number;
  dns_ms?: number;
  connect_ms?: number;
  tls_ms?: number;
  ttfb_ms?: number;
  failure_stage?: "dns" | "connect" | "tls" | "ttfb" | "response" | "body" | "request";
}

export interface ManagedAPICheckResult {
  ok: boolean;
  code?: string;
  message?: string;
}

export interface ProviderDefinition {
  key: string;
  display_name: string;
  base_url: string;
  adapter_type: ManagedAdapterType;
  region: string;
}

export interface AutoManagedModelAPIConfigInput {
  tenant_id: string;
  api_key?: string;
  credential_source_id?: string;
  model_name: string;
  provider?: string;
  base_url?: string;
}

export type ManagedModelDiscoveryInput = Omit<AutoManagedModelAPIConfigInput, "model_name"> & {
  model_name?: string;
};

function tenantQuery(tenantId: string) {
  return `?tenant_id=${encodeURIComponent(tenantId)}`;
}

export function listManagedModelAPIConfigs(tenantId: string) {
  return apiClient.request<{ configs: ManagedModelAPIConfig[] }>(`/api/v1/platform/model-api-configs${tenantQuery(tenantId)}`);
}

export function createManagedModelAPIConfig(input: ManagedModelAPIConfigInput) {
  return apiClient.request<{ config: ManagedModelAPIConfig }>("/api/v1/platform/model-api-configs", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function resolveManagedModelAPIProvider(input: Pick<AutoManagedModelAPIConfigInput, "model_name" | "provider" | "base_url">) {
  return apiClient.request<{ provider: ProviderDefinition; model_name: string; registry_version: string }>("/api/v1/platform/model-api-configs/resolve", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function validateManagedModelAPIConfig(input: AutoManagedModelAPIConfigInput) {
  return apiClient.request<{ provider: ProviderDefinition; validation: ManagedAPIProbeResult }>("/api/v1/platform/model-api-configs/validate", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export async function listAvailableManagedModels(input: ManagedModelDiscoveryInput) {
  const response = await apiClient.request<{ provider: ProviderDefinition; models: string[] | null; latency_ms: number }>("/api/v1/platform/model-api-configs/models", {
    method: "POST",
    body: JSON.stringify(input)
  });
  // 旧服务把空切片编码成 null；先转为空数组，避免进入表单后读取 length 或 map 时崩溃。
  const models = response.models === null ? [] : response.models;
  if (!Array.isArray(models) || models.some((model) => typeof model !== "string")) {
    throw new ApiClientError(502, "provider_invalid_response", "模型列表格式异常");
  }
  return { ...response, models };
}

export function managedModelDiscoveryErrorMessage(error: unknown, reusingCredential: boolean) {
  if (reusingCredential && error instanceof ApiClientError && error.code === "invalid_request") {
    return "当前 API 服务未识别同 Key 模型请求，请更新 API 服务及数据库迁移后重试。";
  }
  return getUserErrorMessage(error, "获取模型列表失败");
}

export function autoCreateManagedModelAPIConfig(input: AutoManagedModelAPIConfigInput) {
  return apiClient.request<{ config: ManagedModelAPIConfig; validation: ManagedAPIProbeResult }>("/api/v1/platform/model-api-configs/auto", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function updateManagedModelAPIConfig(id: string, tenantId: string, input: ManagedModelAPIConfigUpdateInput) {
  return apiClient.request<{ config: ManagedModelAPIConfig }>(`/api/v1/platform/model-api-configs/${encodeURIComponent(id)}${tenantQuery(tenantId)}`, {
    method: "PATCH",
    body: JSON.stringify(input)
  });
}

export function probeManagedModelAPIConfig(id: string, tenantId: string, mode: ManagedAPIProbeMode = "quick", force = false) {
  const query = `${tenantQuery(tenantId)}&mode=${encodeURIComponent(mode)}${force ? "&force=true" : ""}`;
  return apiClient.request<{ result: ManagedAPIProbeResult; config: ManagedModelAPIConfig }>(`/api/v1/platform/model-api-configs/${encodeURIComponent(id)}/probe${query}`, {
    method: "POST"
  });
}

export function deleteManagedModelAPIConfig(id: string, tenantId: string) {
  return apiClient.request<void>(`/api/v1/platform/model-api-configs/${encodeURIComponent(id)}${tenantQuery(tenantId)}`, {
    method: "DELETE"
  });
}

export function listPanelModelRoleBindings(tenantId: string, stage: PanelEducationStage, subjectCode: string, archetypeCode: string) {
  const query = new URLSearchParams({ tenant_id: tenantId, education_stage: stage, subject_code: subjectCode, archetype_code: archetypeCode });
  return apiClient.request<{ bindings: PanelModelRoleBinding[] }>(`/api/v1/platform/panel-model-bindings?${query}`);
}

export function savePanelModelRoleBinding(input: PanelModelRoleBindingInput) {
  return apiClient.request<{ binding: PanelModelRoleBinding }>("/api/v1/platform/panel-model-bindings", {
    method: "PUT",
    body: JSON.stringify(input)
  });
}
