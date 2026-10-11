import { apiClient } from "./client";

export interface StudentPrintCandidate {
  student_id: string;
  student_no: string;
  student_name: string;
  class_id: string;
  class_name: string;
  attendance_status: "expected" | "absent";
  has_active_sheet: boolean;
  active_sheet_serial?: string;
  active_sheet_status?: "issued" | "observed" | "conflict";
  active_print_batch_id?: string;
}

export interface StudentPrintBatchSummary {
  print_batch_id: string;
  operation: "initial" | "reprint";
  reason?: string;
  sheet_count: number;
  page_count: number;
  issued_count: number;
  observed_count: number;
  conflict_count: number;
  revoked_count: number;
  downloadable: boolean;
  issued_at: string;
}

export interface StudentPrintContext {
  exam_id: string;
  template_id: string;
  template_content_hash: string;
  candidates: StudentPrintCandidate[];
  batches: StudentPrintBatchSummary[];
}

export interface IssuedStudentBarcodes {
  print_batch_id: string;
  template_id: string;
  template_content_hash: string;
  kid: string;
  students: Array<{
    student_id: string;
    sheet_serial: string;
    pages: Array<{ page_no: number; value: string }>;
  }>;
  issued_at: string;
}

export async function getStudentPrintContext(templateId: string) {
  return apiClient.request<{ print_context: StudentPrintContext }>(
    `/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/print-context`
  );
}

export async function issueStudentPrintBatch(templateId: string, studentIds: string[], idempotencyKey: string) {
  return apiClient.request<{ barcodes: IssuedStudentBarcodes }>(
    `/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/student-barcodes`,
    {
      method: "POST",
      body: JSON.stringify({ student_ids: studentIds, idempotency_key: idempotencyKey })
    }
  );
}

export async function downloadStudentPrintPackage(printBatchId: string) {
  return apiClient.requestBlob(
    `/api/v1/answer-sheet-print-batches/${encodeURIComponent(printBatchId)}/package.pdf`
  );
}

export async function reprintStudentSheet(sheetSerial: string, reason: string, idempotencyKey: string) {
  return apiClient.request<{ barcodes: IssuedStudentBarcodes }>(
    `/api/v1/answer-sheet-print-sheets/${encodeURIComponent(sheetSerial)}/reprint`,
    { method: "POST", body: JSON.stringify({ reason, idempotency_key: idempotencyKey }) }
  );
}
