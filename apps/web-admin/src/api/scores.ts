import { executeBusinessCommand } from "./businessCommand";
import { apiClient } from "./client";
import { buildQueryString } from "./query";

export interface FinalGrade {
  id: string;
  tenant_id: string;
  exam_id: string;
  question_id: string;
  question_no: string;
  answer_segment_id: string;
  submission_id: string;
  anonymous_code: string;
  score: number;
  max_score: number;
  source: string;
  status: string;
  locked: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface SubmissionGrade {
  id: string;
  tenant_id: string;
  exam_id: string;
  submission_id: string;
  student_id?: string;
  anonymous_code: string;
  total_score: number;
  max_score: number;
  status: string;
  locked: boolean;
  confirmed_by?: string;
  confirmed_at?: string;
  published_by?: string;
  published_at?: string;
  revision: number;
  created_by: string;
  created_at: string;
  updated_at: string;
  items?: FinalGrade[];
}

export interface QualityIssue {
  code: string;
  message: string;
  blocking: boolean;
  count: number;
}

export interface QualityReport {
  passed: boolean;
  issues: QualityIssue[];
}

export type RosterStatus = "graded" | "submitted" | "absent" | "unmatched" | "missing_pages";

export interface RosterEntry {
  key: string;
  student_id?: string;
  student_no?: string;
  student_name?: string;
  class_id?: string;
  class_name?: string;
  submission_id?: string;
  candidate_no?: string;
  status: RosterStatus;
  resolution_code: string;
  expected_page_count: number;
  actual_page_count: number;
  total_score?: number;
  max_score?: number;
}

export interface RosterSummary {
  expected: number;
  received: number;
  graded: number;
  absent: number;
  unresolved: number;
  missing_pages: number;
  unidentified: number;
}

export interface RosterReport {
  entries: RosterEntry[];
  summary: RosterSummary;
}

export interface FinalizeResult {
  status: string;
  created_finals: number;
  submission_grades: SubmissionGrade[];
  quality: QualityReport;
  available_statuses: string[];
}

export interface PublishResult {
  status: string;
  submission_grades: SubmissionGrade[];
  quality: QualityReport;
  published_at: string;
}

export interface QualityCheckResult {
  stage: string;
  can_publish: boolean;
  quality: QualityReport;
}

export interface ScoreExport {
  blob: Blob;
  contentType: string;
  filename?: string;
  watermark?: string;
}

export async function finalizeExamGrades(examId: string) {
  return apiClient.request<FinalizeResult>(`/api/v1/exams/${encodeURIComponent(examId)}/finalize`, {
    method: "POST"
  });
}

export async function listExamGrades(
  examId: string,
  filter: { status?: string; q?: string; limit?: number; cursor?: string } = {}
) {
  return apiClient.request<{
    grades: SubmissionGrade[];
    total: number;
    filtered_total: number;
    all_locked: boolean;
    next_cursor: string;
    has_more: boolean;
  }>(`/api/v1/exams/${encodeURIComponent(examId)}/grades${buildQueryString(filter)}`);
}

export async function checkExamGradeQuality(examId: string, stage: "confirmation" | "publish" = "publish") {
  const query = stage === "publish" ? "?stage=publish" : "";
  return apiClient.request<QualityCheckResult>(`/api/v1/exams/${encodeURIComponent(examId)}/grades/quality${query}`);
}

export async function listExamRoster(examId: string) {
  return apiClient.request<{ roster: RosterReport }>(`/api/v1/exams/${encodeURIComponent(examId)}/roster`);
}

export async function confirmExamGrades(examId: string, reason: string) {
  return executeBusinessCommand("score.confirm", examId, { reason }, (commandId, original) => apiClient.request<{ grades: SubmissionGrade[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/confirm-grades`, {
    method: "POST",
    headers: { "Idempotency-Key": commandId },
    body: JSON.stringify(original)
  }), result => ({ grades: result as SubmissionGrade[] }));
}

export async function publishExamGrades(examId: string, reason: string) {
  return executeBusinessCommand("score.publish", examId, { reason }, (commandId, original) => apiClient.request<PublishResult>(`/api/v1/exams/${encodeURIComponent(examId)}/publish`, {
    method: "POST",
    headers: { "Idempotency-Key": commandId },
    body: JSON.stringify(original)
  }), result => result as PublishResult);
}

export async function exportExamGrades(examId: string, signal?: AbortSignal): Promise<ScoreExport> {
  return apiClient.requestBlob(`/api/v1/exams/${encodeURIComponent(examId)}/grades/export`, { signal });
}

/** 内部核验表包含尚未发布的成绩；正式交付使用发布快照导出。 */
export async function exportExamGradeReport(examId: string, signal?: AbortSignal): Promise<ScoreExport> {
  return apiClient.requestBlob(`/api/v1/exams/${encodeURIComponent(examId)}/grades/export/report`, { signal });
}

/** 每科均已发布时，按冻结班级生成教师 Excel；旧 CSV 接口仍保持原格式。 */
export async function exportPublishedExamGrades(examId: string, signal?: AbortSignal): Promise<ScoreExport> {
  return apiClient.requestBlob(`/api/v1/exams/${encodeURIComponent(examId)}/score-releases/export?format=xlsx`, { signal });
}
