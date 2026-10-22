import { apiClient } from "./client";

export type ReferenceRole = "question_diagram" | "reference_solution";
export interface ReferenceBox { x: number; y: number; width: number; height: number }
export interface ReferenceSource { page_ref: string; name: string; kind: string; page_no: number; sha256: string; width: number; height: number }
export interface ReferenceItem { id: string; crop_ref: string; role: ReferenceRole; ordinal: number; required: true; sha256: string; width: number; height: number; size_bytes: number; pixel_bbox: ReferenceBox; source: ReferenceSource }
export interface ReferenceBinding { manifest_id: string; mode: "text_only" | "reference_images"; revision: number; content_hash: string; image_count: number }
export interface ReferenceMaterials { manifest_id?: string; mode: "text_only" | "reference_images" | "legacy_unrecorded"; revision: number; content_hash?: string; authorization_revision?: number; use_state?: "active" | "withdrawn"; frozen: boolean; items: ReferenceItem[] }
export interface ReferenceCropInput { command_id: string; page_ref: string; page_sha256: string; page_width: number; page_height: number; pixel_bbox: ReferenceBox }
export interface ReferenceSaveInput { command_id: string; expected_revision: number; mode: "text_only" | "reference_images"; items: Array<Pick<ReferenceItem, "crop_ref" | "role" | "ordinal">> }
export interface ReferenceWithdrawInput { command_id: string; expected_authorization_revision: number; reason: string }
export interface ReferenceCommand { command_id: string; status: string; operation?: string; target_id?: string; result?: ReferenceItem | ReferenceMaterials }
export interface MaterialReceipt { stage: string; manifest_hash: string; image_count: number; permit_consumed: boolean; recorded_at: string }
const examPath = (exam: string) => `/api/v1/exams/${encodeURIComponent(exam)}`;
const questionPath = (exam: string, question: string) => `${examPath(exam)}/questions/${encodeURIComponent(question)}`;
export const getReferenceSources = (exam: string) => apiClient.request<{ sources: ReferenceSource[] }>(`${examPath(exam)}/reference-material-sources`);
export const getReferenceMaterials = (exam: string, question: string) => apiClient.request<{ materials: ReferenceMaterials }>(`${questionPath(exam, question)}/reference-materials`);
export const getReferenceSourceImage = (exam: string, ref: string) => apiClient.requestBlob(`${examPath(exam)}/reference-material-sources/${encodeURIComponent(ref)}/image`);
export const getReferenceCropImage = (exam: string, question: string, crop: string) => apiClient.requestBlob(`${questionPath(exam, question)}/reference-material-crops/${encodeURIComponent(crop)}/image`);
export const createReferenceCrop = (exam: string, question: string, input: ReferenceCropInput) => apiClient.request<{ crop: ReferenceItem }>(`${questionPath(exam, question)}/reference-material-crops`, { method: "POST", headers: { "Idempotency-Key": input.command_id }, body: JSON.stringify(input) });
export const saveReferenceMaterials = (exam: string, question: string, input: ReferenceSaveInput) => apiClient.request<{ materials: ReferenceMaterials }>(`${questionPath(exam, question)}/reference-materials`, { method: "PUT", headers: { "Idempotency-Key": input.command_id }, body: JSON.stringify(input) });
export const withdrawReferenceMaterials = (exam: string, question: string, manifest: string, input: ReferenceWithdrawInput) => apiClient.request<{ materials: ReferenceMaterials }>(`${questionPath(exam, question)}/reference-materials/${encodeURIComponent(manifest)}/withdraw`, { method: "POST", headers: { "Idempotency-Key": input.command_id }, body: JSON.stringify(input) });
export const recoverReferenceCommand = (exam: string, command: string) => apiClient.request<{ command: ReferenceCommand }>(`${examPath(exam)}/reference-material-commands/${encodeURIComponent(command)}`);
export const resumeReferenceCommand = (exam: string, question: string, command: string) => apiClient.request<{ command: ReferenceCommand }>(`${questionPath(exam, question)}/reference-material-commands/${encodeURIComponent(command)}/resume`, { method: "POST" });
export const getTaskReferenceMaterials = (task: string) => apiClient.request<{ materials: ReferenceMaterials }>(`/api/v1/review-tasks/${encodeURIComponent(task)}/reference-materials`);
export const getTaskReferenceImage = (task: string, item: string) => apiClient.requestBlob(`/api/v1/review-tasks/${encodeURIComponent(task)}/reference-materials/${encodeURIComponent(item)}/image`);
export const getRunReferenceMaterials = (run: string, question: string) => apiClient.request<{ materials: ReferenceMaterials }>(`/api/v1/scoring-runs/${encodeURIComponent(run)}/questions/${encodeURIComponent(question)}/reference-materials`);
export const getRunReferenceImage = (run: string, question: string, item: string) => apiClient.requestBlob(`/api/v1/scoring-runs/${encodeURIComponent(run)}/questions/${encodeURIComponent(question)}/reference-materials/${encodeURIComponent(item)}/image`);
export function materialReceiptLabel(receipt?: MaterialReceipt): string {
  if (!receipt) return "历史未记录图片发送情况";
  if (receipt.stage === "provider_response_received") return `已收到本次模型响应；请求包含 ${receipt.image_count} 张参考图`;
  if (receipt.stage === "provider_request_attempted") return "已发起图像请求，等待回执";
  if (receipt.stage === "failed_before_send") return "未发起图像请求";
  if (receipt.permit_consumed || receipt.stage === "unknown_after_attempt") return "可能已发起图像请求，发送结果待核实";
  return "材料已核验，尚未发起";
}
