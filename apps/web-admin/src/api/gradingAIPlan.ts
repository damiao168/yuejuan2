import { apiClient } from "./client";

export interface GradingAIModel {
  model_config_id: string;
  display_name: string;
  provider_key: string;
  model_name: string;
  model_version: string;
  config_revision: string;
  binding_revision: string;
  vision_probe_status?: "passed" | "failed" | "not_checked" | "unavailable";
  vision_probe_reason?: string;
}
export interface GradingAIRole { model_config_id: string; strength_rank: number; }
export interface GradingAIDecision {
  primary_allowed_diff_points: number | null;
  teacher_allowed_diff_points: number | null;
  aggregation_version: "dual-conditional-arbiter-v1";
  rounding: "ceil_final";
}
export interface GradingAIPlan {
  version: number;
  revision: string;
  fallback_model_config_id: string;
  roles: { primary_a?: GradingAIRole; primary_b?: GradingAIRole; arbiter?: GradingAIRole };
  decision_config: GradingAIDecision;
  effective_mode: "single" | "dual" | "dual_conditional_arbiter" | "unconfigured";
  reason_codes: string[];
  resolved_roles: { primary_a?: GradingAIModel; primary_b?: GradingAIModel; arbiter?: GradingAIModel };
  single_model?: GradingAIModel;
  independent_model_count: number;
  confirmed_by?: string;
  confirmed_at?: string;
}
export interface GradingAIPlanResponse { plan: GradingAIPlan; available_models: GradingAIModel[]; can_configure?: boolean; }
export interface SaveGradingAIPlan {
  expected_revision: string;
  fallback_model_config_id: string;
  roles: { primary_a: GradingAIRole; primary_b: GradingAIRole; arbiter: GradingAIRole };
  decision_config: GradingAIDecision;
}
export function getGradingAIPlan(examId: string) {
  return apiClient.request<GradingAIPlanResponse>(`/api/v1/exams/${encodeURIComponent(examId)}/grading-ai-plan`);
}
export function saveGradingAIPlan(examId: string, payload: SaveGradingAIPlan) {
  return apiClient.request<GradingAIPlanResponse>(`/api/v1/exams/${encodeURIComponent(examId)}/grading-ai-plan`, { method: "PUT", body: JSON.stringify(payload) });
}
export function probeGradingAIModel(examId: string, modelConfigId: string) {
  return apiClient.request<{ model_config_id: string; vision_probe_status: string; vision_probe_reason: string; cache_hit: boolean }>(`/api/v1/exams/${encodeURIComponent(examId)}/grading-ai-plan/probe`, {
    method: "POST", body: JSON.stringify({ model_config_id: modelConfigId, request_id: crypto.randomUUID() })
  });
}
