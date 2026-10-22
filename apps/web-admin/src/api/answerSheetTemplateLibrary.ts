import { apiClient } from "./client";
import type { AnswerSheetTemplate, TemplateLayout } from "./configuration";
import type { PaperVersion } from "./papers";

export interface AnswerSheetTemplateLibrary {
  id: string;
  tenant_id: string;
  school_id: string;
  name: string;
  version_no: number;
  page_count: number;
  layout: Omit<TemplateLayout, "pages"> & { pages: Array<Omit<TemplateLayout["pages"][number], "question_regions"> & {
    question_regions: Array<Omit<TemplateLayout["pages"][number]["question_regions"][number], "question_id"> & { question_no: string; question_type?: string }>;
  }> };
  source_sha256: string;
  content_hash: string;
  reviewed_by: string;
  reviewed_at: string;
}

export interface TemplateLibraryInstance {
  template: AnswerSheetTemplate;
  paper: PaperVersion;
  reused: boolean;
}

export interface TemplateLibraryResolution {
  status: "matched" | "unknown" | "ambiguous";
  candidates: AnswerSheetTemplateLibrary[];
  instance?: TemplateLibraryInstance;
}

export function listAnswerSheetTemplateLibrary(examId: string, signal?: AbortSignal) {
  return apiClient.request<{ libraries: AnswerSheetTemplateLibrary[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-template-library`, { signal });
}

export function publishAnswerSheetTemplate(templateId: string, payload: {
  name: string;
  expected_revision: number;
  confirmed_blank_reference: boolean;
  confirmed_reusable_layout: boolean;
}, idempotencyKey: string) {
  return apiClient.request<{ library: AnswerSheetTemplateLibrary }>(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/library`, {
    method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: JSON.stringify(payload)
  });
}

export function instantiateAnswerSheetTemplate(examId: string, libraryId: string) {
  return apiClient.request<TemplateLibraryInstance>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-template-library/${encodeURIComponent(libraryId)}/instantiate`, {
    method: "POST", body: "{}"
  });
}

export function resolveAnswerSheetTemplate(examId: string, paperId: string) {
  return apiClient.request<TemplateLibraryResolution>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-template-library/resolve`, {
    method: "POST", body: JSON.stringify({ exam_paper_id: paperId })
  });
}
