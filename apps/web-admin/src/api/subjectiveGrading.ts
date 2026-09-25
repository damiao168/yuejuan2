import { apiClient } from "./client";

export type SubjectiveGradingBatch = import("@edugrade/sdk").SubjectiveGradingBatch;

export interface SubjectiveGradingBatchEnqueueResult {
  requested_count: number;
  accepted_count: number;
  task_count: number;
  failed_count: number;
  partial_success: boolean;
  failures: Array<{
    segment_id: string;
    code: string;
  }>;
}

export async function createSubjectiveGradingBatch(idempotencyKey: string, segmentIds: string[], scoringRunId?: string) {
  return apiClient.request<{ batch: SubjectiveGradingBatch }>("/api/v1/subjective-grading-batches", {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ idempotency_key: idempotencyKey, segment_ids: segmentIds, scoring_run_id: scoringRunId })
  });
}

export async function enqueueSubjectiveGradingBatch(batchId: string) {
  return apiClient.request<{
    batch: SubjectiveGradingBatch;
    tasks: Array<{ id: string; status: string }>;
    enqueue_result: SubjectiveGradingBatchEnqueueResult;
  }>(`/api/v1/subjective-grading-batches/${encodeURIComponent(batchId)}/enqueue`, { method: "POST" });
}

export async function getSubjectiveGradingBatch(batchId: string, signal?: AbortSignal) {
  return apiClient.request<{ batch: SubjectiveGradingBatch }>(`/api/v1/subjective-grading-batches/${encodeURIComponent(batchId)}`, { signal });
}

export async function getScoringRunAIBatches(runId: string) {
  return apiClient.request<{ batches: SubjectiveGradingBatch[] }>(`/api/v1/scoring-runs/${encodeURIComponent(runId)}/ai-batches`);
}

export async function getFailedSubjectiveBatchSegments(batchId: string) {
  return apiClient.request<{ segment_ids: string[] }>(`/api/v1/subjective-grading-batches/${encodeURIComponent(batchId)}/failed-segments`);
}

export async function recoverSubjectiveBatchCommand(commandId: string) {
 return apiClient.request<import("@edugrade/sdk").SubjectiveBatchCommandRecovery>(`/api/v1/subjective-grading-batch-commands/${encodeURIComponent(commandId)}`);
}
export async function recoverSubjectiveEnqueueCommand(batchId: string) {
 return apiClient.request<import("@edugrade/sdk").SubjectiveEnqueueCommandRecovery>(`/api/v1/subjective-grading-batches/${encodeURIComponent(batchId)}/enqueue-command`);
}
