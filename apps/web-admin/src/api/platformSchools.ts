import { apiClient } from "./client";
import { buildQueryString } from "./query";
import type { ManagedUser } from "./users";

export type SchoolStatus = "active" | "disabled";
export type ModelStatus = "healthy" | "warning" | "unconfigured";
export type UsageWindow = "today" | "7d" | "30d" | "90d";

export interface PlatformSchoolSummary {
  tenant_id: string;
  school_id: string;
  name: string;
  code: string;
  status: SchoolStatus;
  created_at: string;
  last_activity_at?: string;
  administrator: {
    id?: string;
    display_name?: string;
    username?: string;
    admin_count: number;
    last_login_at?: string;
  };
  members: {
    accounts: number;
    active_accounts: number;
    administrators: number;
    teachers: number;
    graders: number;
    students: number;
    classes: number;
  };
  usage: UsageSummary;
  model_health: ModelHealth;
  exam_count: number;
  attention_reasons: string[];
}

export interface UsageSummary {
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  requests: number;
  arbitration_requests: number;
  estimated_cost_microusd: number;
  window_days: number;
}

export interface ModelHealth {
  status: ModelStatus;
  config_status?: string;
  config_id?: string;
  display_name?: string;
  provider_key?: string;
  model_name?: string;
  credential_hint?: string;
  connection_status?: string;
  connection_message?: string;
  capability_status?: string;
  capability_version?: string;
  capability_message?: string;
  latency_ms?: number;
  last_tested_at?: string;
  last_capability_tested_at?: string;
}

export interface PlatformSchoolListFilter {
  q?: string;
  status?: SchoolStatus;
  activity?: "today" | "7d" | "30d" | "inactive_30d" | "never";
  model_health?: ModelStatus;
  usage_window?: UsageWindow;
  sort?: "created_at" | "last_activity" | "token_usage" | "student_count";
  order?: "asc" | "desc";
  limit?: number;
  cursor?: string;
}

export interface PlatformSchoolListResponse {
  schools: PlatformSchoolSummary[];
  summary: { total: number; active: number; disabled: number };
  next_cursor: string;
  has_more: boolean;
}

export interface SchoolMembersResponse {
  members: ManagedUser[];
  summary: { total: number; active: number; disabled: number; admins: number; teachers: number; graders: number };
}

export interface UsageBreakdown { key: string; label: string; total_tokens: number; requests: number; share: number; estimated_cost_microusd: number }
export interface SchoolUsageResponse {
  summary: UsageSummary;
  trend: Array<{ date: string; total_tokens: number; input_tokens: number; output_tokens: number; requests: number }>;
  by_feature: UsageBreakdown[];
  by_model: UsageBreakdown[];
  start_date: string;
  end_date: string;
}

export interface SchoolModelHealthResponse {
  default_model: ModelHealth;
  roles: Array<{ agent_role: "primary_a" | "primary_b" | "arbiter"; model_name: string; provider_key: string; status: string }>;
}

export interface SchoolActivityResponse {
  activities: Array<{ id: string; event_type: string; severity: string; title: string; summary: string; actor_name?: string; happened_at: string }>;
  security: { active_admins: number; mfa_enabled: number; active_sessions: number; last_login_at?: string };
}

const base = "/api/v1/platform/schools";
const detailPath = (tenantId: string) => `${base}/${encodeURIComponent(tenantId)}`;

export function listPlatformSchools(filter: PlatformSchoolListFilter = {}) {
  return apiClient.request<PlatformSchoolListResponse>(`${base}${buildQueryString(filter)}`);
}
export function getPlatformSchool(tenantId: string) {
  return apiClient.request<{ school: PlatformSchoolSummary }>(detailPath(tenantId));
}
export function listPlatformSchoolMembers(tenantId: string, filter: { q?: string; role?: string; status?: string } = {}) {
  return apiClient.request<SchoolMembersResponse>(`${detailPath(tenantId)}/members${buildQueryString(filter)}`);
}
export function getPlatformSchoolUsage(tenantId: string, filter: { window?: UsageWindow; start_date?: string; end_date?: string } = {}) {
  return apiClient.request<SchoolUsageResponse>(`${detailPath(tenantId)}/usage${buildQueryString(filter)}`);
}
export function getPlatformSchoolModelHealth(tenantId: string) {
  return apiClient.request<SchoolModelHealthResponse>(`${detailPath(tenantId)}/model-health`);
}
export function getPlatformSchoolActivity(tenantId: string) {
  return apiClient.request<SchoolActivityResponse>(`${detailPath(tenantId)}/activity`);
}
