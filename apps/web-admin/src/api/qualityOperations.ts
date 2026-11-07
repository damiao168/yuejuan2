import { apiClient } from "./client";
import { listBackmarkBatches as listGeneratedBackmarkBatches } from "./backmark";

export interface SeedPolicy {
  id: string;
  exam_id: string;
  question_id: string;
  rate: number;
  min_interval: number;
  max_interval: number;
  status: "active" | "paused";
  revision: number;
  needs_confirmation?: boolean;
  confirmation_reason?: "gold_set_changed" | "gold_set_missing";
}

export type { BackmarkBatch } from "./backmark";

export function getSeedPolicy(examId: string, questionId: string) {
  return apiClient.request<{ policy: SeedPolicy }>(`/api/v1/exams/${encodeURIComponent(examId)}/questions/${encodeURIComponent(questionId)}/seed-policy`);
}

export function putSeedPolicy(examId: string, questionId: string, body: Pick<SeedPolicy, "rate" | "min_interval" | "max_interval" | "status"> & { expected_revision: number }) {
  return apiClient.request<{ policy: SeedPolicy }>(`/api/v1/exams/${encodeURIComponent(examId)}/questions/${encodeURIComponent(questionId)}/seed-policy`, {
    method: "PUT",
    body: JSON.stringify(body)
  });
}

export function listBackmarkBatches(examId: string, cursor?: string) {
  return listGeneratedBackmarkBatches(examId, undefined, { limit: 50, cursor });
}
