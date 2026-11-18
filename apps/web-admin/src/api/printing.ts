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
  qr_layout_required?: boolean;
  qr_layout?: StudentPrintLayout;
}

export interface StudentPrintQRPage {
  // 以原页左上角为原点，坐标和宽高均按整页归一化；矩形包含打印时的全部覆盖范围。
  page_no: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface StudentPrintLayout {
  source_sha256: string;
  // 只包含模板缺少专用二维码区的页；已有专用区的位置由后端读取，不能在此覆盖。
  pages: StudentPrintQRPage[];
}

export interface StudentPrintSource {
  file_asset_id: string;
  content_type: string;
  sha256: string;
}

export interface StudentPrintContext {
  exam_id: string;
  template_id: string;
  template_content_hash: string;
  candidates: StudentPrintCandidate[];
  batches: StudentPrintBatchSummary[];
  print_source?: StudentPrintSource;
  qr_layout_required?: boolean;
  qr_layout_page_nos?: number[];
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

export async function issueStudentPrintBatch(templateId: string, studentIds: string[], idempotencyKey: string, qrLayout?: StudentPrintLayout) {
  return apiClient.request<{ barcodes: IssuedStudentBarcodes }>(
    `/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/student-barcodes`,
    {
      method: "POST",
      body: JSON.stringify({ student_ids: studentIds, idempotency_key: idempotencyKey, ...(qrLayout ? { qr_layout: qrLayout } : {}) })
    }
  );
}

export async function downloadStudentPrintPackage(printBatchId: string) {
  return apiClient.requestBlob(
    `/api/v1/answer-sheet-print-batches/${encodeURIComponent(printBatchId)}/package.pdf`
  );
}

export function downloadStudentPrintSource(templateId: string) {
  // 打印管理员通过模板所属考试读取底图；不依赖通用文件管理权限。
  return apiClient.requestBlob(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/print-source`);
}

export async function reprintStudentSheet(sheetSerial: string, reason: string, idempotencyKey: string, qrLayout?: StudentPrintLayout) {
  return apiClient.request<{ barcodes: IssuedStudentBarcodes }>(
    `/api/v1/answer-sheet-print-sheets/${encodeURIComponent(sheetSerial)}/reprint`,
    { method: "POST", body: JSON.stringify({ reason, idempotency_key: idempotencyKey, ...(qrLayout ? { qr_layout: qrLayout } : {}) }) }
  );
}

export function confirmStudentPrintLayout(printBatchId: string, layout: StudentPrintLayout) {
  return apiClient.request<{ qr_layout: StudentPrintLayout }>(
    `/api/v1/answer-sheet-print-batches/${encodeURIComponent(printBatchId)}/confirm-layout`,
    { method: "POST", body: JSON.stringify(layout) }
  );
}
