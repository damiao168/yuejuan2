import { apiClient } from "./client";
import { buildQueryString } from "./query";

export type AppealStatus = "submitted" | "under_review" | "need_more_info" | "accepted" | "rejected" | "score_adjusted" | "closed";

export interface AppealEvidence {
  raw_answer?: string;
  ocr_text?: string;
  ai_grades?: Record<string, unknown>[];
  human_grades?: Record<string, unknown>[];
  rubric?: Record<string, unknown>;
  final_grade?: Record<string, unknown>;
}

export interface ScoreAdjustment {
  id: string;
  tenant_id: string;
  appeal_id: string;
  exam_id: string;
  submission_id: string;
  submission_grade_id: string;
  final_grade_id: string;
  question_id: string;
  question_no: string;
  previous_score: number;
  adjusted_score: number;
  delta: number;
  reason: string;
  adjusted_by: string;
  created_at: string;
}

export interface Appeal {
  id: string;
  tenant_id: string;
  exam_id: string;
  exam_name?: string;
  subject?: string;
  submission_id: string;
  submission_grade_id: string;
  anonymous_code?: string;
  student_id: string;
  // 响应保留服务端扩展值；提交建议时仍使用下方请求类型约束已支持的操作。
  target_type: string;
  final_grade_id?: string;
  question_id?: string;
  question_no?: string;
  deduction_point_id?: string;
  reason: string;
  attachment?: Record<string, unknown>;
  status: string;
  result_reason?: string;
  assigned_to?: string;
  teacher_recommendation?: string;
  teacher_recommendation_reason?: string;
  teacher_recommended_score?: number;
  teacher_recommendation_by?: string;
  teacher_recommendation_at?: string;
  reviewed_by?: string;
  reviewed_at?: string;
  closed_by?: string;
  closed_at?: string;
  created_by: string;
  revision: number;
  created_at: string;
  updated_at: string;
  evidence?: AppealEvidence;
  adjustments?: ScoreAdjustment[];
}

export interface AppealListFilter {
  exam_id?: string;
  student_id?: string;
  status?: string;
  limit?: number;
  cursor?: string;
}

export interface ReviewAppealPayload {
  status: string;
  reason: string;
  assigned_to?: string;
  final_grade_id?: string;
  adjusted_score?: number;
  expected_revision: number;
}

export interface SubmitAppealRecommendationPayload {
  recommendation: "accept" | "reject" | "adjust_score" | "need_more_info";
  reason: string;
  recommended_score?: number;
  expected_revision: number;
}

export interface AppealStatistics {
  total: number;
  by_status: Record<string, number>;
  score_adjusted_count: number;
  average_handle_hours: number;
}

export async function listAppeals(filter: AppealListFilter = {}) {
  return apiClient.request<{ appeals: Appeal[]; next_cursor: string; has_more: boolean }>(`/api/v1/appeals${buildQueryString(filter)}`);
}

export async function getAppeal(id: string) {
  return apiClient.request<{ appeal: Appeal }>(`/api/v1/appeals/${encodeURIComponent(id)}`);
}

export async function assignAppeal(id: string, assignedTo: string, expectedRevision: number) {
  return apiClient.request<{ appeal: Appeal }>(`/api/v1/appeals/${encodeURIComponent(id)}/assign`, {
    method: "POST",
    body: JSON.stringify({ assigned_to: assignedTo, expected_revision: expectedRevision })
  });
}

export async function submitAppealRecommendation(id: string, payload: SubmitAppealRecommendationPayload) {
  return apiClient.request<{ appeal: Appeal }>(`/api/v1/appeals/${encodeURIComponent(id)}/recommendation`, {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function reviewAppeal(id: string, payload: ReviewAppealPayload) {
  return apiClient.request<{ appeal: Appeal; score_adjustment?: ScoreAdjustment }>(`/api/v1/appeals/${encodeURIComponent(id)}/review`, {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function closeAppeal(id: string, reason: string, expectedRevision: number) {
  return apiClient.request<{ appeal: Appeal }>(`/api/v1/appeals/${encodeURIComponent(id)}/close`, {
    method: "POST",
    body: JSON.stringify({ reason, expected_revision: expectedRevision })
  });
}

export async function getAppealStatistics(examId?: string) {
  const query = examId ? `?exam_id=${encodeURIComponent(examId)}` : "";
  return apiClient.request<{ statistics: AppealStatistics }>(`/api/v1/appeals/statistics${query}`);
}
