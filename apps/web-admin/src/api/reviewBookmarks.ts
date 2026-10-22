import { apiClient } from "./client";

export interface PersonalReviewBookmark {
  id: string | null;
  task_id: string;
  marked: boolean;
  reason: string;
  revision: number;
  marked_at?: string;
  can_edit_reason: boolean;
}
export interface PersonalReviewBookmarkItem extends PersonalReviewBookmark {
  exam_id: string; exam_name: string; question_id: string; question_no: string;
  subject: string; anonymous_code: string; state: string;
}
export interface PersonalReviewBookmarkFilter { exam_id?: string; question_id?: string; subject?: string; state?: string; q?: string }
export interface PersonalReviewBookmarkList {
  items: PersonalReviewBookmarkItem[]; total_count: number; next_cursor?: string;
  exams: Array<{ id: string; name: string }>;
  questions: Array<{ id: string; question_no: string }>;
}
export interface PersonalReviewBookmarkAck { bookmark_id: string; revision: number; marked: boolean }
export interface PersonalReviewBookmarkContext {
  bookmark: PersonalReviewBookmark;
  task: { id: string; exam_id: string; exam_name: string; question_id: string; question_no: string; subject: string; anonymous_code: string; state: string; max_score: number };
  question: { stem: string; options: string[]; rubric: { max_score: number; points: Array<{ id: string; description: string; score: number }> } };
  own_submission?: { score: number; max_score: number; rubric_selections: Array<{ point_id: string; score: number }>; submitted_at: string };
  answer: { text: string; text_binding: "unverified" | "unavailable"; image_url: string; evidence_version: string; image_available: boolean; material_change: "unchanged" | "changed" | "unknown" };
  readonly: true;
}
const base = "/api/v1/me/review-bookmarks";
// 与后端 strings.TrimSpace 的 Unicode 空白范围一致；JS trim 还会移除 BOM，却不移除 NEL。
// 输入框保留原文，计数只使用服务端换行和首尾空白规范化后的文本。
export function normalizeReviewBookmarkReason(text: string) {
  return text.replace(/\r\n?/g, "\n").replace(/^[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g, "");
}
export const reviewBookmarkCodePointLength = (text: string) => Array.from(normalizeReviewBookmarkReason(text)).length;
export function getPersonalReviewBookmark(taskId: string, signal?: AbortSignal) {
  return apiClient.request<{ bookmark: PersonalReviewBookmark }>(`${base}/by-task/${encodeURIComponent(taskId)}`, { signal, cache: "no-store" });
}
export function listPersonalReviewBookmarks(filter: PersonalReviewBookmarkFilter, cursor = "", signal?: AbortSignal) {
  const search = new URLSearchParams({ limit: "50" });
  for (const [key, value] of Object.entries({ ...filter, cursor })) if (value) search.set(key, value);
  return apiClient.request<PersonalReviewBookmarkList>(`${base}?${search}`, { signal, cache: "no-store" });
}
// 命令键由功能内存中的意图提供；不经过持久化业务命令或通用请求载荷缓存。
export function putPersonalReviewBookmark(taskId: string, reason: string, revision: number, commandId: string, signal?: AbortSignal) {
  return apiClient.request<PersonalReviewBookmarkAck>(`${base}/by-task/${encodeURIComponent(taskId)}`, { method: "PUT", headers: { "Idempotency-Key": commandId }, body: JSON.stringify({ reason, expected_revision: revision }), signal });
}
export function deletePersonalReviewBookmark(id: string, revision: number, commandId: string, signal?: AbortSignal) {
  return apiClient.request<PersonalReviewBookmarkAck>(`${base}/${encodeURIComponent(id)}`, { method: "DELETE", headers: { "Idempotency-Key": commandId }, body: JSON.stringify({ expected_revision: revision }), signal });
}
export function getPersonalReviewBookmarkCleanup(signal?: AbortSignal) {
  return apiClient.request<{ items: Array<{ bookmark_id: string; revision: number }> }>(`${base}/cleanup`, { signal, cache: "no-store" });
}
export function getPersonalReviewBookmarkContext(id: string, signal?: AbortSignal) {
  return apiClient.request<PersonalReviewBookmarkContext>(`${base}/${encodeURIComponent(id)}/context`, { signal, cache: "no-store" });
}
export function getPersonalReviewBookmarkImage(id: string, version: string, signal?: AbortSignal) {
  return apiClient.requestBlob(`${base}/${encodeURIComponent(id)}/image?${new URLSearchParams({ version })}`, { signal, cache: "no-store" });
}
