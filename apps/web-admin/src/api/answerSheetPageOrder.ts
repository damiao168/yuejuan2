import { apiClient } from "./client";

export interface AnswerSheetPageOrderImage {
  page_id: string; width: number; height: number; media_type: "image/jpeg"; data_base64: string; sha256: string;
}
export interface AnswerSheetPageOrderResult {
  status: "ordered" | "uncertain";
  page_order: string[];
  reason_codes: string[];
  model_identity?: { provider_key?: string; model_name?: string; model_version?: string };
}

/** 页序只确认整套版面顺序，不代表这些页面属于同一名学生。 */
export function orderAnswerSheetPages(examId: string, pages: readonly AnswerSheetPageOrderImage[], signal?: AbortSignal) {
  return apiClient.request<AnswerSheetPageOrderResult>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-page-order`, {
    method: "POST", body: JSON.stringify({ pages }), signal
  });
}
