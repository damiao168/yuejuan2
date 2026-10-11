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
}

export type { BackmarkBatch } from "./backmark";

export function getSeedPolicy(examId: string, questionId: string) {
  return apiClient.request<{ policy: SeedPolicy }>(`/api/v1/exams/${encodeURIComponent(examId)}/questions/${encodeURIComponent(questionId)}/seed-policy`);
}

export function putSeedPolicy(examId: string, questionId: string, body: Omit<SeedPolicy, "id" | "exam_id" | "question_id" | "revision"> & { expected_revision: number }) {
  return apiClient.request<{ policy: SeedPolicy }>(`/api/v1/exams/${encodeURIComponent(examId)}/questions/${encodeURIComponent(questionId)}/seed-policy`, {
    method: "PUT",
    body: JSON.stringify(body)
  });
}

export function listBackmarkBatches(examId: string, cursor?: string) {
  return listGeneratedBackmarkBatches(examId, undefined, { limit: 50, cursor });
}
