import { apiClient } from "./client";
import type { SubjectiveGradingBatch, SubjectiveGradingBatchEnqueueResult } from "./subjectiveGrading";

export type ScoringTrialSampleRole = "representative" | "boundary" | "incomplete_evidence";
export type ScoringTrialStatus = "awaiting_enqueue" | "running" | "reviewing" | "stopped" | "expanded" | "invalidated";

export interface ScoringTrialResult {
	material_receipt?: import("./referenceMaterials").MaterialReceipt;
  // 保留未知状态供界面展示；只有服务端明确的终态才能参与试改完成判断。
  status: string;
  suggested_score?: number;
  grade_id?: string;
  evidence: unknown[];
  risk_flags: string[];
  latency_ms?: number;
  total_tokens?: number;
  error_code?: string;
}

export interface ScoringTrialSample {
  segment_id: string;
  human_score: number;
  max_score: number;
  roles: ScoringTrialSampleRole[];
  result?: ScoringTrialResult;
}

export interface ScoringTrial {
  id: string;
  run_id: string;
  exam_id: string;
  question_id: string;
  command_id: string;
  status: ScoringTrialStatus;
  revision: number;
  fingerprint: string;
  sample_batch_id: string;
  expansion_batch_id?: string;
  samples: ScoringTrialSample[];
  remaining_segment_ids: string[];
  decided_by?: string;
  decision_reason?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface CreateScoringAITrialInput {
  command_id: string;
  question_id: string;
  samples: Array<Pick<ScoringTrialSample, "segment_id" | "human_score" | "roles">>;
}

export interface ScoringTrialDecisionInput {
  decision: "stop" | "expand";
  reason: string;
  expected_revision: number;
}

export interface ScoringTrialEnqueueResponse {
  batch: SubjectiveGradingBatch;
  tasks: Array<{ id: string; status: string }>;
  enqueue_result: SubjectiveGradingBatchEnqueueResult;
}

function trialsPath(runId: string): string {
  return `/api/v1/scoring-runs/${encodeURIComponent(runId)}/ai-trials`;
}

export function getScoringRunAITrials(runId: string, signal?: AbortSignal) {
  return apiClient.request<{ trials: ScoringTrial[] }>(trialsPath(runId), { signal });
}

export function createScoringAITrial(runId: string, input: CreateScoringAITrialInput) {
  // 回执丢失后由调用方复用原命令和人工基准；HTTP 层与业务资源使用同一个命令 ID。
  return apiClient.request<{ trial: ScoringTrial }>(trialsPath(runId), {
    method: "POST",
    headers: { "Idempotency-Key": input.command_id },
    body: JSON.stringify(input)
  });
}

export function enqueueScoringAITrial(runId: string, trialId: string) {
  // 服务端按试改状态恢复其关联批次，客户端不传片段或批次白名单扩大执行范围。
  return apiClient.request<ScoringTrialEnqueueResponse>(`${trialsPath(runId)}/${encodeURIComponent(trialId)}/enqueue`, { method: "POST" });
}

export function decideScoringAITrial(runId: string, trialId: string, input: ScoringTrialDecisionInput) {
  // 决策绑定人工复核时的版本；扩批片段由服务端从该试改冻结范围中计算。
  return apiClient.request<{ trial: ScoringTrial }>(`${trialsPath(runId)}/${encodeURIComponent(trialId)}/decision`, {
    method: "POST",
    body: JSON.stringify(input)
  });
}
