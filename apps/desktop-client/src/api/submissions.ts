import type { AnswerSegment, OcrTask, SubmissionPage, SubmissionQualityResult } from "../types";
import type { DesktopApiClient } from "./client";

export interface AddSubmissionPagePayload {
  file_asset_id: string;
  page_no: number;
}

export async function addSubmissionPage(client: DesktopApiClient, submissionId: string, payload: AddSubmissionPagePayload) {
  return client.request<{ page: SubmissionPage }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/pages`, {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function runSubmissionQualityCheck(client: DesktopApiClient, submissionId: string) {
  return client.request<{ result: SubmissionQualityResult }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/quality-check`, {
    method: "POST"
  });
}

export interface SubmissionImageQuality {
  id: string;
  exam_id: string;
  quality_status: string;
  quality_issues: { code: string; message: string }[];
  expected_page_count: number;
  actual_page_count: number;
}

/** 页数检查的 valid 不代表图像质量通过；图像结论只从答卷的服务端聚合状态读取。 */
export function getSubmissionImageQuality(client: DesktopApiClient, submissionId: string) {
  return client.request<{ submission: SubmissionImageQuality }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}`);
}

export async function listSubmissionPages(client: DesktopApiClient, submissionId: string) {
  return client.request<{ pages: SubmissionPage[] }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/pages`);
}

export async function listAnswerSegments(client: DesktopApiClient, submissionId: string) {
  return client.request<{ segments: AnswerSegment[] }>(`/api/v1/submissions/${encodeURIComponent(submissionId)}/answer-segments`);
}

export async function listOcrTasks(client: DesktopApiClient, submissionId: string) {
  return client.request<{ tasks: OcrTask[]; next_cursor: string; has_more: boolean }>(
    `/api/v1/submissions/${encodeURIComponent(submissionId)}/ocr-tasks?limit=20`
  );
}

export async function getOcrTask(client: DesktopApiClient, taskId: string) {
  return client.request<{ task: OcrTask }>(`/api/v1/ocr-tasks/${encodeURIComponent(taskId)}`);
}
