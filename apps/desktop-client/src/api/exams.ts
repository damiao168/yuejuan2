import type { Exam } from "../types";
import type { DesktopApiClient } from "./client";

export interface ExamListFilter {
  status?: string;
  school_id?: string;
}

/** 扫描站用于选择真实服务端批次的精简采集批次结构。
 * 原生客户端不会在本地自行生成批次 UUID。 */
export interface CaptureBatch {
  id: string;
  exam_id: string;
  name: string;
  source_type: string;
  // 服务端可能新增批次状态；客户端保留原值，具体操作仍由工作区按已知状态判断。
  status: string;
  file_count: number;
  page_count: number;
  created_at: string;
}

export async function listExams(client: DesktopApiClient, filter: ExamListFilter = {}) {
  const params = new URLSearchParams();
  if (filter.status) {
    params.set("status", filter.status);
  }
  if (filter.school_id) {
    params.set("school_id", filter.school_id);
  }
  const query = params.toString();
  const querySuffix = query ? `?${query}` : "";
  return client.request<{ exams: Exam[] }>(`/api/v1/exams${querySuffix}`);
}

export async function listCaptureBatches(client: DesktopApiClient, examId: string) {
  const encodedExamID = encodeURIComponent(examId);
  return client.request<{ batches: CaptureBatch[]; next_cursor?: string; has_more?: boolean }>(
    `/api/v1/exams/${encodedExamID}/capture-batches?limit=100`
  );
}

export interface CaptureBatchProcessingDetail {
  batch: CaptureBatch;
  pages: { id: string; status: string; source_index: number; submission_id?: string; submission_page_id?: string }[];
  files: { id: string; original_name: string; status: string; error_code?: string }[];
}
export function getCaptureBatchDetail(client: DesktopApiClient, batchId: string) {
  return client.request<CaptureBatchProcessingDetail>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}`);
}

export interface CaptureScanContext {
  batch: CaptureBatch;
  exam: { id: string; name: string; subject: string };
  template_status: "ready" | "missing" | "invalid";
  template_issue?: string;
  template?: {
    id: string; name: string; version_no: number; page_count: number;
    content_hash: string; binding_revision: number;
    layout: { pages: { page_no: number; width: number; height: number;
      registration_marks: ScanTemplateRegion[]; identity_regions: ScanTemplateRegion[]; question_regions: ScanTemplateRegion[] }[] };
  };
}
export interface ScanTemplateRegion { id: string; label?: string; x: number; y: number; width: number; height: number }

/** 只投影当前获授权采集批次的考试与锁定模板；扫描员无需获得考试配置权限。 */
export function getCaptureScanContext(client: DesktopApiClient, batchId: string) {
  return client.request<CaptureScanContext>(`/api/v1/capture-batches/${encodeURIComponent(batchId)}/scan-context`);
}
