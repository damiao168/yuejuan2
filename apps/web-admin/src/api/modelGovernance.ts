import { apiClient } from "./client";

export type ProviderKind = "local" | "external";
export type ProviderStatus = "unverified" | "active" | "degraded" | "rate_limited" | "disabled";
export type DeploymentStatus = "unverified" | "shadow_only" | "disabled";
export type HealthState = "unverified" | "available" | "degraded" | "rate_limited" | "unavailable" | "disabled";
export type PolicyMode = "local_only" | "shadow_compare" | "cloud_suggestion" | "hybrid_escalation" | "dual_provider_review";

export interface DataPolicy {
  training_allowed: boolean;
  retention_mode: "no_store" | "contractual";
}

export interface ModelProvider {
  id: string;
  tenant_id: string;
  provider_key: string;
  display_name: string;
  provider_kind: ProviderKind;
  adapter_type: string;
  credential_reference_set: boolean;
  credential_scheme?: string;
  region: string;
  data_policy: DataPolicy;
  status: ProviderStatus;
  created_at: string;
  updated_at: string;
}

export interface ModelDeployment {
  id: string;
  tenant_id: string;
  provider_id: string;
  provider_key: string;
  deployment_key: string;
  model_name: string;
  model_version: string;
  region: string;
  capability_profile: string;
  modalities: Array<"text" | "image">;
  capability_policy?: Record<string, unknown>;
  pricing_policy?: Record<string, unknown>;
  status: DeploymentStatus;
  health_state: HealthState;
  created_at: string;
  updated_at: string;
}

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
  max_cost_micros_per_question: number;
  max_cost_micros_per_exam: number;
  fallback_mode: "manual_only" | "approved_deployment_only";
  status: string;
  version: number;
  updated_at: string;
}

export interface CreateProviderInput {
  provider_key: string;
  display_name: string;
  provider_kind: ProviderKind;
  adapter_type: string;
  credential_ref?: string;
  region: string;
  data_policy: DataPolicy;
  status?: ProviderStatus;
}

export interface CreateDeploymentInput {
  provider_id: string;
  deployment_key: string;
  model_name: string;
  model_version: string;
  region: string;
  capability_profile: string;
  modalities: Array<"text" | "image">;
  capability_policy: Record<string, unknown>;
  pricing_policy: Record<string, unknown>;
  status?: DeploymentStatus;
  health_state?: HealthState;
}

export interface UpdatePolicyInput {
  display_name: string;
  mode: PolicyMode;
  external_enabled: boolean;
  text_export_enabled: boolean;
  image_export_enabled: boolean;
  allowed_deployments: string[];
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
  deployment_id: string;
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
  evaluation_run_id: string;
  deployment_id: string;
  manual_review_rate: number;
  decision_reference: string;
  expires_at: string;
  reason: string;
}

export function listModelProviders() {
  return apiClient.request<{ providers: ModelProvider[] }>("/api/v1/model-providers");
}

export function createModelProvider(input: CreateProviderInput) {
  return apiClient.request<{ provider: ModelProvider }>("/api/v1/model-providers", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function listModelDeployments() {
  return apiClient.request<{ deployments: ModelDeployment[] }>("/api/v1/model-deployments");
}

export function createModelDeployment(input: CreateDeploymentInput) {
  return apiClient.request<{ deployment: ModelDeployment }>("/api/v1/model-deployments", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function getModelPolicy() {
  return apiClient.request<{ policy: TenantModelPolicy }>("/api/v1/model-policy");
}

export function updateModelPolicy(input: UpdatePolicyInput) {
  return apiClient.request<{ policy: TenantModelPolicy }>("/api/v1/model-policy", {
    method: "PUT",
    body: JSON.stringify(input)
  });
}

export function getCurrentRuntimePrompt() {
  return apiClient.request<{ prompt: RuntimePrompt }>("/api/v1/model-prompts/current");
}

export function listModelEvaluationRuns(filter: { limit?: number; cursor?: string } = {}) {
  const params = new URLSearchParams();
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.cursor) params.set("cursor", filter.cursor);
  const query = params.toString();
  return apiClient.request<{ evaluation_runs: EvaluationRun[]; next_cursor: string; has_more: boolean }>(`/api/v1/model-evaluation-runs${query ? `?${query}` : ""}`);
}

export function createModelEvaluationRun(input: CreateEvaluationRunInput) {
  return apiClient.request<{ evaluation_run: EvaluationRun }>("/api/v1/model-evaluation-runs", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function addModelEvaluationCandidate(runID: string, input: AddEvaluationCandidateInput) {
  return apiClient.request<{ candidate: EvaluationCandidate }>(`/api/v1/model-evaluation-runs/${runID}/candidates`, {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function completeModelEvaluationRun(runID: string, reason: string) {
  return apiClient.request<{ evaluation_run: EvaluationRun }>(`/api/v1/model-evaluation-runs/${runID}/complete`, {
    method: "POST",
    body: JSON.stringify({ reason })
  });
}

export function invalidateModelEvaluationRun(runID: string, reason: string) {
  return apiClient.request<{ evaluation_run: EvaluationRun }>(`/api/v1/model-evaluation-runs/${runID}/invalidate`, {
    method: "POST",
    body: JSON.stringify({ reason })
  });
}

export function listModelApprovals() {
  return apiClient.request<{ model_approvals: ModelApproval[] }>("/api/v1/model-approvals");
}

export function createModelApproval(input: CreateModelApprovalInput) {
  return apiClient.request<{ model_approval: ModelApproval }>("/api/v1/model-approvals", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function revokeModelApproval(approvalID: string, reason: string, expectedRevision: number) {
  return apiClient.request<{ model_approval: ModelApproval }>(`/api/v1/model-approvals/${approvalID}/revoke`, {
    method: "POST",
    body: JSON.stringify({ reason, expected_revision: expectedRevision })
  });
}
