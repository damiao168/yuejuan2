import { apiClient } from "./client";
import type { CaptureBatch } from "./capture";

export interface CaptureCollectionEntry {
  student_id: string;
  student_no: string;
  student_name: string;
  class_name: string;
  status: "missing" | "processing" | "needs_attention" | "ready" | "absent";
  code: string;
  batch_id?: string;
  submission_id?: string;
  expected_pages: number;
  actual_pages: number;
  reason?: string;
}

export interface CaptureCollectionIssue {
  id: string;
  kind: "file" | "submission" | "batch" | "upload";
  code: string;
  name: string;
  batch_id: string;
  submission_id?: string;
  file_id?: string;
}

export interface CaptureCollectionSummary {
  exam_id: string;
  exam_status: string;
  revision: number;
  closed: boolean;
  completed_at?: string;
  completed_by?: string;
  pages_per_student: number;
  roster_count: number;
  required_count: number;
  ready_count: number;
  absent_count: number;
  pending_count: number;
  can_complete: boolean;
  blockers: string[];
  entries: CaptureCollectionEntry[];
  issues: CaptureCollectionIssue[];
}

export interface CompleteCaptureCollectionInput {
  revision: number;
  idempotency_key: string;
  reason?: string;
}

export function getCaptureCollectionSummary(examId: string) {
  return apiClient.request<{ summary: CaptureCollectionSummary }>(`/api/v1/exams/${encodeURIComponent(examId)}/capture-summary`);
}

export function completeCaptureCollection(examId: string, input: CompleteCaptureCollectionInput) {
  return apiClient.request<{ summary: CaptureCollectionSummary }>(`/api/v1/exams/${encodeURIComponent(examId)}/complete-collection`, {
    method: "POST", body: JSON.stringify(input),
  });
}

export function excludeCaptureFile(fileId: string, reason: string, replacementFileId?: string) {
  return apiClient.request<{ batch: CaptureBatch }>(`/api/v1/capture-files/${encodeURIComponent(fileId)}/exclude`, {
    method: "POST", body: JSON.stringify({ reason, replacement_file_id: replacementFileId }),
  });
}
