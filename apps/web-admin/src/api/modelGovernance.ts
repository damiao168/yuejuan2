import { apiClient } from "./client";

export type PolicyMode = "local_only" | "shadow_compare" | "cloud_suggestion" | "hybrid_escalation" | "dual_provider_review";

export interface TenantModelPolicy {
  id: string;
  tenant_id: string;
  policy_key: string;
  display_name: string;
  mode: PolicyMode;
  external_enabled: boolean;
  text_export_enabled: boolean;
  image_export_enabled: boolean;
  allowed_deployments: string[];
  allowed_model_config_ids: string[];
  max_cost_micros_per_question: number;
  max_cost_micros_per_exam: number;
  fallback_mode: "manual_only" | "approved_deployment_only";
  status: string;
  version: number;
  updated_at: string;
}

export interface UpdatePolicyInput {
  display_name: string;
  mode: PolicyMode;
  external_enabled: boolean;
  text_export_enabled: boolean;
  image_export_enabled: boolean;
  allowed_model_config_ids: string[];
  max_cost_micros_per_question: number;
  max_cost_micros_per_exam: number;
  fallback_mode: "manual_only" | "approved_deployment_only";
  expected_version: number;
  reason: string;
}

export interface RuntimePromptComponent {
  key: string;
  filename: string;
  sha256: string;
  content: string;
}

export interface RuntimePrompt {
  prompt_version: string;
  bundle_sha256: string;
  components: RuntimePromptComponent[];
  activation_mode: "deployment_manifest";
  mutable_at_runtime: false;
}

export type EvaluationEvidenceClass = "protocol_fixture" | "authorized_frozen_set";
export type EvaluationRunStatus = "draft" | "completed" | "invalidated";

export interface EvaluationMetrics {
  teacher_acceptance_rate: number;
  serious_error_rate: number;
  evidence_validity_rate: number;
  stability_rate: number;
  average_cost_micros: number;
}

export interface EvaluationCandidate {
  id: string;
  tenant_id?: string;
  run_id: string;
  deployment_id: string;
  model_config_id?: string;
  model_name: string;
  provider_key: string;
  deployment_key: string;
  model_version: string;
  prompt_version: string;
  rubric_version: string;
  evaluated_samples: number;
  teacher_reviewed_samples: number;
  teacher_accepted_samples: number;
  serious_error_samples: number;
  evidence_valid_samples: number;
  repeat_comparisons: number;
  stable_repeat_samples: number;
  p95_latency_ms: number;
  total_cost_micros: number;
  metrics: EvaluationMetrics;
  created_at: string;
}

export interface EvaluationRun {
  id: string;
  tenant_id?: string;
  run_key: string;
  display_name: string;
  dataset_reference: string;
  dataset_sha256: string;
  authorization_reference?: string;
  evidence_class: EvaluationEvidenceClass;
  subject: string;
  grade: string;
  question_type: string;
  modality: "text" | "image";
  sample_count: number;
  repeat_count: number;
  status: EvaluationRunStatus;
  candidates: EvaluationCandidate[];
  completed_at?: string;
  invalidated_at?: string;
  created_at: string;
}

export interface CreateEvaluationRunInput {
  tenant_id: string;
  run_key: string;
  display_name: string;
  dataset_reference: string;
  dataset_sha256: string;
  authorization_reference?: string;
  evidence_class: EvaluationEvidenceClass;
  subject: string;
  grade: string;
  question_type: string;
  modality: "text" | "image";
  sample_count: number;
  repeat_count: number;
  reason: string;
}

export interface AddEvaluationCandidateInput {
  model_config_id: string;
  prompt_version: string;
  rubric_version: string;
  evaluated_samples: number;
  teacher_reviewed_samples: number;
  teacher_accepted_samples: number;
  serious_error_samples: number;
  evidence_valid_samples: number;
  repeat_comparisons: number;
  stable_repeat_samples: number;
  p95_latency_ms: number;
  total_cost_micros: number;
  reason: string;
}

export interface ModelApproval {
  id: string;
  tenant_id?: string;
  evaluation_run_id: string;
  evaluation_candidate_id: string;
  deployment_id: string;
  model_config_id?: string;
  model_name: string;
  provider_key: string;
  deployment_key: string;
  model_version: string;
  prompt_version: string;
  rubric_version: string;
  dataset_reference: string;
  dataset_sha256: string;
  authorization_reference: string;
  subject: string;
  grade: string;
  question_type: string;
  modality: "text" | "image";
  manual_review_rate: number;
  decision_reference: string;
  expires_at: string;
  revoked_at?: string;
  revision: number;
  created_at: string;
}

export interface CreateModelApprovalInput {
  tenant_id: string;
  evaluation_run_id: string;
  model_config_id: string;
  manual_review_rate: number;
  decision_reference: string;
  expires_at: string;
  reason: string;
}

function tenantURL(path: string, tenantID: string) {
  const separator = path.includes("?") ? "&" : "?";
  return `${path}${separator}tenant_id=${encodeURIComponent(tenantID)}`;
}

export function getModelPolicy(tenantID: string) {
  return apiClient.request<{ policy: TenantModelPolicy }>(tenantURL("/api/v1/model-policy", tenantID));
}

export function updateModelPolicy(tenantID: string, input: UpdatePolicyInput) {
  return apiClient.request<{ policy: TenantModelPolicy }>(tenantURL("/api/v1/model-policy", tenantID), {
    method: "PUT",
    body: JSON.stringify(input)
  });
}

export function getCurrentRuntimePrompt() {
  return apiClient.request<{ prompt: RuntimePrompt }>("/api/v1/model-prompts/current");
}

export function listModelEvaluationRuns(tenantID: string, filter: { limit?: number; cursor?: string } = {}) {
  const params = new URLSearchParams();
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.cursor) params.set("cursor", filter.cursor);
  const query = params.toString();
  return apiClient.request<{ evaluation_runs: EvaluationRun[]; next_cursor: string; has_more: boolean }>(tenantURL(`/api/v1/model-evaluation-runs${query ? `?${query}` : ""}`, tenantID));
}

export function createModelEvaluationRun(input: CreateEvaluationRunInput) {
  return apiClient.request<{ evaluation_run: EvaluationRun }>("/api/v1/model-evaluation-runs", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function addModelEvaluationCandidate(tenantID: string, runID: string, input: AddEvaluationCandidateInput) {
  return apiClient.request<{ candidate: EvaluationCandidate }>(tenantURL(`/api/v1/model-evaluation-runs/${runID}/candidates`, tenantID), {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function completeModelEvaluationRun(tenantID: string, runID: string, reason: string) {
  return apiClient.request<{ evaluation_run: EvaluationRun }>(tenantURL(`/api/v1/model-evaluation-runs/${runID}/complete`, tenantID), {
    method: "POST",
    body: JSON.stringify({ reason })
  });
}

export function invalidateModelEvaluationRun(tenantID: string, runID: string, reason: string) {
  return apiClient.request<{ evaluation_run: EvaluationRun }>(tenantURL(`/api/v1/model-evaluation-runs/${runID}/invalidate`, tenantID), {
    method: "POST",
    body: JSON.stringify({ reason })
  });
}

export function listModelApprovals(tenantID: string) {
  return apiClient.request<{ model_approvals: ModelApproval[] }>(tenantURL("/api/v1/model-approvals", tenantID));
}

export function createModelApproval(input: CreateModelApprovalInput) {
  return apiClient.request<{ model_approval: ModelApproval }>("/api/v1/model-approvals", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function revokeModelApproval(tenantID: string, approvalID: string, reason: string, expectedRevision: number) {
  return apiClient.request<{ model_approval: ModelApproval }>(tenantURL(`/api/v1/model-approvals/${approvalID}/revoke`, tenantID), {
    method: "POST",
    body: JSON.stringify({ reason, expected_revision: expectedRevision })
  });
}
