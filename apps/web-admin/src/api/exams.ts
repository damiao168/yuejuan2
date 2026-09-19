import { apiClient } from "./client";
import { buildQueryString } from "./query";

export type ExamStatus = "draft" | "configured" | "ready" | "collecting" | "grading" | "reviewing" | "finalized" | "published" | "archived";

export type GradingMode = "auto_objective_only" | "ai_assisted" | "human_review_required" | "double_mark" | "blind_double_mark";

export interface Exam {
  id: string;
  tenant_id: string;
  school_id: string;
  exam_session_id?: string;
  exam_session_name?: string;
  exam_session_grade_id?: string;
  name: string;
  subject: string;
  exam_type: string;
  total_score: number;
  status: ExamStatus | string;
  grading_mode: GradingMode | string;
  appeal_enabled: boolean;
  publish_policy: string;
  created_by: string;
  class_ids: string[];
  revision: number;
  created_at?: string;
}

export interface ExamListFilter {
  status?: string;
  school_id?: string;
  limit?: number;
  cursor?: string;
}

export interface ExamPayload {
  school_id: string;
  name: string;
  subject: string;
  exam_type: string;
  total_score: number;
  grading_mode: string;
  appeal_enabled: boolean;
  publish_policy: string;
  class_ids: string[];
}

export interface ExamSessionPayload {
  school_id: string;
  grade_id: string;
  template_id?: string;
  name: string;
  exam_type: string;
  grading_mode: string;
  appeal_enabled: boolean;
  publish_policy: string;
  class_ids: string[];
  subjects: Array<{
    subject: string;
    total_score: number;
    duration_minutes: number;
    candidate_rule: string;
    class_ids: string[];
    sections: Array<{ title: string; question_type: string; question_count: number; score_per_question: number }>;
  }>;
}

export interface ExamSession {
  id: string;
  school_id: string;
  grade_id: string;
  template_id?: string;
  template_version?: number;
  name: string;
  exam_type: string;
  status: string;
  exams: Exam[];
}

export interface CandidateRefreshResult {
  before_count: number;
  after_count: number;
  added_count: number;
  removed_count: number;
}

export async function listExams(filter: ExamListFilter = {}) {
  return apiClient.request<{ exams: Exam[]; next_cursor?: string; has_more?: boolean }>(`/api/v1/exams${buildQueryString(filter)}`);
}

export async function getExam(id: string) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}`);
}

export async function createExam(payload: ExamPayload) {
  return apiClient.request<{ exam: Exam }>("/api/v1/exams", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function createExamSession(payload: ExamSessionPayload, commandId: string) {
  return apiClient.request<{ exam_session: ExamSession }>("/api/v1/exam-sessions", {
    method: "POST",
    headers: { "Idempotency-Key": commandId },
    body: JSON.stringify({ ...payload, command_id: commandId })
  });
}

export interface ExamSessionCommandResult {
  command_id: string;
  status: "not_accepted" | "processing" | "succeeded" | "rejected" | "unknown";
  http_status?: number;
  error_code?: string;
  exam_session?: ExamSession;
}

export async function recoverExamSessionCommand(commandId: string) {
  return apiClient.request<{ command: ExamSessionCommandResult }>(`/api/v1/exam-sessions/commands/${encodeURIComponent(commandId)}`);
}

export async function updateExam(id: string, payload: Partial<ExamPayload> & { expected_revision: number }) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify(payload)
  });
}

export async function updateExamStatus(id: string, status: string, expectedRevision: number) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}/status`, {
    method: "POST",
    body: JSON.stringify({ status, expected_revision: expectedRevision })
  });
}

export async function refreshExamCandidates(id: string) {
  return apiClient.request<{ candidate_refresh: CandidateRefreshResult }>(`/api/v1/exams/${encodeURIComponent(id)}/candidates/refresh`, {
    method: "POST"
  });
}

export async function archiveExam(id: string, expectedRevision: number) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}/archive`, {
    method: "POST",
    body: JSON.stringify({ expected_revision: expectedRevision })
  });
}
