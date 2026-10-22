import { apiClient } from "./client";

export interface DeletedAnswerSheetSource {
  deleted: true;
  paper_id: string;
  deleted_template_ids: string[];
}

/** 服务端按考试、来源用途和正式引用检查删除边界；共享文件资产不会随来源登记删除。 */
export function deleteAnswerSheetSource(examId: string, paperId: string) {
  return apiClient.request<DeletedAnswerSheetSource>(`/api/v1/exams/${encodeURIComponent(examId)}/papers/${encodeURIComponent(paperId)}`, { method: "DELETE" });
}
