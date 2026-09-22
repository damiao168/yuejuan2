import { apiClient } from "./client";

export type ManagedAdapterType = "openai_compatible" | "dashscope_native";
export type ManagedAPIConfigStatus = "active" | "disabled";
export type ManagedAPIProbeStatus = "untested" | "success" | "failed";

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
  last_probe_mode?: ManagedAPIProbeMode;
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

export type ManagedAPIProbeMode = "quick" | "capability";

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
export type PanelEducationStage = "junior" | "senior";

export interface PanelModelRoleBinding {
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
  "managed_model_api_config_id" | "prompt_version" | "strength_rank" | "status"> & { tenant_id: string };

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
  capability_check: ManagedAPICheckResult;
  usage: ManagedAPIProbeUsage;
  diagnostic: ManagedAPIProbeDiagnostic;
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

export function listAvailableManagedModels(input: ManagedModelDiscoveryInput) {
  return apiClient.request<{ provider: ProviderDefinition; models: string[]; latency_ms: number }>("/api/v1/platform/model-api-configs/models", {
    method: "POST",
    body: JSON.stringify(input)
  });
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
