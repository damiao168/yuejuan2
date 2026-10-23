import { apiClient } from "./client";
import { buildQueryString } from "./query";

export interface CaptureBatch {
  id: string;
  tenant_id: string;
  exam_id: string;
  name: string;
  source_type: "web_upload" | "scanner_upload" | "folder_import" | "desktop_sync";
  status: "draft" | "uploading" | "matching" | "processing" | "needs_review" | "ready" | "completed" | "cancelled";
  revision: number;
  operator_id: string;
  scanner_device?: string;
  purpose?: string;
  file_count: number;
  page_count: number;
  submission_count: number;
  normal_count: number;
  review_count: number;
  failed_count: number;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface CaptureFile {
  id: string;
  capture_batch_id: string;
  file_asset_id: string;
  original_name: string;
  content_type: string;
  sha256: string;
  byte_size: number;
  page_count: number;
  status: string;
  error_code?: string;
  upload_group_id?: string;
  upload_group_file_count?: number;
  upload_group_file_index?: number;
  pages_per_student?: number;
  created_at: string;
}

export interface CapturePage {
  id: string;
  capture_batch_id: string;
  capture_file_id: string;
  source_index: number;
  submission_id?: string;
  submission_page_id?: string;
  assigned_page_no?: number;
  sequence_no: number;
  rotation_degrees: number;
  decoded_file_asset_id: string;
  status: string;
  revision: number;
  page_identity: Record<string, unknown>;
  match_candidates: Array<Record<string, unknown>>;
}

export interface CaptureBatchDetail {
  batch: CaptureBatch;
  files: CaptureFile[];
  pages: CapturePage[];
  processing_summaries: ProcessingSummary[];
}

export interface ImageQualityIssue {
  code: string;
  // 质量规则可新增级别；保留响应原值，不在传输层替换成既有级别。
  severity: string;
  metric?: string;
  observed?: unknown;
  threshold?: unknown;
  rule_id?: string;
  action: string;
  parameters?: Record<string, unknown>;
}

export interface ImageQualityRun {
  id: string;
  submission_id: string;
  submission_page_id: string;
  page_no: number;
  source_file_asset_id: string;
  normalized_file_asset_id?: string;
  processing_status: string;
  quality_status?: string;
  profile_name: string;
  profile_version: string;
  metric_schema_version: string;
  report_schema_version: string;
  quality_report: Record<string, unknown>;
  quality_issues: ImageQualityIssue[];
  normalization_transform: Record<string, unknown>;
  worker_service?: string;
  worker_instance_id?: string;
  attempt_no: number;
  duration_ms?: number;
  error_code?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface RegistrationRun {
  id: string;
  capture_page_id: string;
  submission_page_id: string;
  template_id: string;
  page_no: number;
  processing_status: string;
  match_status?: string;
  confidence?: number;
  method?: string;
  registered_file_asset_id?: string;
  error_code?: string;
}

export interface StudentCandidate {
  id: string; student_no: string; name: string; class_id: string; class_name: string;
}

export interface MatchingSubmission {
  id: string; student_id?: string; candidate_no?: string; identity_status: "unassigned" | "matched" | "unknown" | "conflict";
  identity_revision: number; identity_evidence: Record<string, unknown>; pages: CapturePage[];
}

export interface MatchingQueue {
  batch_id: string; exam_id: string; submissions: MatchingSubmission[]; candidates: StudentCandidate[];
}
export function findCaptureTargets(examId: string, submissionId: string, studentId: string) { return apiClient.request<{ targets: { batch_id: string; submission_id: string; page_id: string; candidate_no: string }[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/capture-targets${buildQueryString({submission_id:submissionId,student_id:studentId})}`); }
export interface ProcessingBlocker { page_id: string; page_no: number; registration_run_id?: string; stage: string; code: string; action: string; }
export interface ProcessingSummary { submission_id: string; total_pages: number; ready_pages: number; blocked_pages: number; pending_pages: number; can_complete: boolean; blockers: ProcessingBlocker[]; }
export interface NormalizedPoint { x: number; y: number; }
export interface RegistrationCorrectionContext { registration_run_id: string; capture_page_id: string; exam_id: string; page_revision: number; page_no: number; source_file_asset_id: string; template_file_asset_id: string; template_content_type: string; template_width: number; template_height: number; }
export interface RegistrationCorrection { id: string; capture_page_id: string; base_registration_run_id: string; applied_registration_run_id?: string; source_page_revision: number; template_id: string; template_content_hash: string; page_no: number; source_points: NormalizedPoint[]; template_points: NormalizedPoint[]; advanced_anchor_mode: boolean; status: "draft" | "queued" | "preview_ready" | "failed" | "expired" | "superseded" | "applied" | "undone"; revision: number; attempt_count: number; preview_registered_file_asset_id?: string; coverage?: number; reprojection_error?: number; validation_report: Record<string, unknown>; error_code?: string; }

export interface CreateCaptureBatchInput {
  name: string;
  source_type: CaptureBatch["source_type"];
  scanner_device?: string;
  idempotency_key: string;
  purpose?: "supplement";
  reason?: string;
}

export async function createCaptureBatch(examId: string, payload: CreateCaptureBatchInput) {
  return apiClient.request<{ batch: CaptureBatch }>(`/api/v1/exams/${encodeURIComponent(examId)}/capture-batches`, { method: "POST", headers: { "Idempotency-Key": payload.idempotency_key }, body: JSON.stringify(payload) });
}

export function recoverCaptureBatchCommand(examId: string, commandId: string) {
  return apiClient.request<{ command: { command_id: string; status: "not_accepted" | "succeeded"; batch?: CaptureBatch } }>(`/api/v1/exams/${encodeURIComponent(examId)}/capture-batches/commands/${encodeURIComponent(commandId)}`);
}

export async function listCaptureBatches(examId: string, filter: { limit?: number; cursor?: string } = {}) {
  return apiClient.request<{ batches: CaptureBatch[]; next_cursor: string; has_more: boolean }>(`/api/v1/exams/${encodeURIComponent(examId)}/capture-batches${buildQueryString(filter)}`);
}

export async function getCaptureBatch(batchId: string) {
  return apiClient.request<CaptureBatchDetail>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}`);
}

export function listPageQualityRuns(submissionPageId: string) {
  return apiClient.request<{ runs: ImageQualityRun[] }>(
    `/api/v1/submission-pages/${encodeURIComponent(submissionPageId)}/quality-runs`,
  );
}

export function runImageQualityCheck(submissionId: string) {
  return apiClient.request<{ runs: ImageQualityRun[] }>(
    `/api/v1/submissions/${encodeURIComponent(submissionId)}/run-quality-check`,
    { method: "POST" },
  );
}

export interface CaptureUploadGroupInput {
  upload_group_id: string;
  upload_group_file_count: number;
  // 文件位置从 1 开始；服务端按此顺序及文件内页序分组，不按请求完成时间排列。
  upload_group_file_index: number;
  pages_per_student: number;
}

export async function registerCaptureFile(batchId: string, fileAssetId: string, idempotencyKey: string, group?: CaptureUploadGroupInput) {
  return apiClient.request<{ file: CaptureFile }>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/files`, { method: "POST", body: JSON.stringify({ file_asset_id: fileAssetId, idempotency_key: idempotencyKey, ...group }) });
}

export async function processCaptureBatch(batchId: string) {
  return apiClient.request<{ batch: CaptureBatch }>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/process`, { method: "POST" });
}

export async function overrideCapturePageQuality(submissionPageId: string, reason: string) {
  return apiClient.request<{ page: { id: string; submission_id: string; quality_status: string }; registration_runs: RegistrationRun[] }>(
    `/api/v1/submission-pages/${encodeURIComponent(submissionPageId)}/quality-override`,
    { method: "POST", body: JSON.stringify({ reason }) },
  );
}

export async function completeCaptureBatch(batchId: string, reason: string) {
  return apiClient.request<{ batch: CaptureBatch }>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/complete`, { method: "POST", body: JSON.stringify({ reason }) });
}

export async function reopenCaptureBatch(batchId: string, reason: string) {
  return apiClient.request<{ batch: CaptureBatch }>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/reopen`, { method: "POST", body: JSON.stringify({ reason }) });
}

export async function cancelCaptureBatch(batchId: string, reason: string) {
  return apiClient.request<{ batch: CaptureBatch }>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/cancel`, { method: "POST", body: JSON.stringify({ reason }) });
}

export async function updateCapturePage(pageId: string, payload: { revision: number; rotation_degrees?: number; sequence_no?: number }) {
  return apiClient.request<{ page: CapturePage }>(`/api/v1/capture-pages/${encodeURIComponent(pageId)}`, { method: "PATCH", body: JSON.stringify(payload) });
}

export async function processSubmissionPages(submissionId: string) {
  return apiClient.request<{ runs: RegistrationRun[] }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/process-pages`, { method: "POST" });
}

export async function getMatchingQueue(batchId: string) {
  return apiClient.request<MatchingQueue>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/matching-queue`);
}

export async function confirmStudentMatch(submissionId: string, studentId: string, revision: number, reason = "人工核对答卷信息") {
  return apiClient.request<{ submission: MatchingSubmission }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/student-match/confirm`, { method: "POST", body: JSON.stringify({ student_id: studentId, revision, reason }) });
}

export async function markStudentUnknown(submissionId: string, revision: number, reason: string) {
  return apiClient.request<{ submission: MatchingSubmission }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/student-match/unknown`, { method: "POST", body: JSON.stringify({ revision, reason }) });
}

export async function confirmPageMatch(pageId: string, pageNo: number, revision: number, reason = "人工核对页码") {
  return apiClient.request<{ page: CapturePage }>(`/api/v1/capture-pages/${encodeURIComponent(pageId)}/page-match/confirm`, { method: "POST", body: JSON.stringify({ page_no: pageNo, revision, reason }) });
}

export async function deleteCapturePage(pageId: string, revision: number, reason: string) { return apiClient.request<{ page: CapturePage }>(`/api/v1/capture-pages/${encodeURIComponent(pageId)}/delete`, { method: "POST", body: JSON.stringify({ revision, reason }) }); }
export async function restoreCapturePage(pageId: string, revision: number, reason: string) { return apiClient.request<{ page: CapturePage }>(`/api/v1/capture-pages/${encodeURIComponent(pageId)}/restore`, { method: "POST", body: JSON.stringify({ revision, reason }) }); }
export async function splitCaptureSubmission(batchId: string, submissionId: string, pageIds: string[], reason: string) { return apiClient.request<MatchingQueue>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/submissions/split`, { method: "POST", body: JSON.stringify({ submission_id: submissionId, page_ids: pageIds, reason }) }); }
export async function mergeCaptureSubmissions(batchId: string, targetSubmissionId: string, sourceSubmissionId: string, reason: string) { return apiClient.request<MatchingQueue>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/submissions/merge`, { method: "POST", body: JSON.stringify({ target_submission_id: targetSubmissionId, source_submission_id: sourceSubmissionId, reason }) }); }
export async function getProcessingSummary(submissionId: string) { return apiClient.request<ProcessingSummary>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/processing-summary`); }
export async function confirmRegistration(runId: string, reason: string) { return apiClient.request<{ run: RegistrationRun }>(`/api/v1/page-registration-runs/${encodeURIComponent(runId)}/confirm`, { method: "POST", body: JSON.stringify({ reason }) }); }
export async function retryRegistration(runId: string) { return apiClient.request<{ run: RegistrationRun }>(`/api/v1/page-registration-runs/${encodeURIComponent(runId)}/retry`, { method: "POST" }); }
export async function getRegistrationCorrectionContext(runId: string) { return apiClient.request<{ context: RegistrationCorrectionContext }>(`/api/v1/page-registration-runs/${encodeURIComponent(runId)}/correction-context`); }
export async function createRegistrationCorrection(runId: string, payload: { page_revision: number; source_points: NormalizedPoint[]; template_points: NormalizedPoint[]; advanced_anchor_mode: boolean }) { return apiClient.request<{ correction: RegistrationCorrection }>(`/api/v1/page-registration-runs/${encodeURIComponent(runId)}/corrections`, { method: "POST", body: JSON.stringify(payload) }); }
export async function getRegistrationCorrection(id: string) { return apiClient.request<{ correction: RegistrationCorrection }>(`/api/v1/page-registration-corrections/${encodeURIComponent(id)}`); }
export async function previewRegistrationCorrection(id: string, revision: number) { return apiClient.request<{ correction: RegistrationCorrection }>(`/api/v1/page-registration-corrections/${encodeURIComponent(id)}/preview`, { method: "POST", body: JSON.stringify({ revision }) }); }
export async function applyRegistrationCorrection(id: string, revision: number, reason: string) { return apiClient.request<{ correction: RegistrationCorrection }>(`/api/v1/page-registration-corrections/${encodeURIComponent(id)}/apply`, { method: "POST", body: JSON.stringify({ revision, reason }) }); }
export async function undoRegistrationCorrection(id: string, revision: number, reason: string) { return apiClient.request<{ correction: RegistrationCorrection }>(`/api/v1/page-registration-corrections/${encodeURIComponent(id)}/undo`, { method: "POST", body: JSON.stringify({ revision, reason }) }); }
