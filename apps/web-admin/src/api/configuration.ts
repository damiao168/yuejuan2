import { EduGradeApi } from "@edugrade/sdk";
import { apiClient } from "./client";

const generatedApi = new EduGradeApi(apiClient);

export interface LayoutRegion {
  id: string;
  question_id?: string;
  label?: string;
  x: number;
  y: number;
  width: number;
  height: number;
  option_regions?: OptionRegion[];
  suggestion_confidence?: number;
  suggestion_source?: "pdf_text_anchor" | "ocr_layout";
}

export interface OptionRegion {
  id: string;
  label: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface TemplatePage {
  page_no: number;
  width: number;
  height: number;
  registration_marks: LayoutRegion[];
  identity_regions: LayoutRegion[];
  question_regions: LayoutRegion[];
}

export interface TemplateOMRReference {
  source: "exam_paper";
  file_asset_id: string;
  hash_sha256: string;
  content_type: string;
}

export interface TemplateOMRProfile {
  mode: "manual_only" | "template_difference";
  version: "opencv-fill-v1" | "opencv-template-difference-bubble-v1";
  reference?: TemplateOMRReference;
}

export interface TemplateLayout {
  pages: TemplatePage[];
  omr_profile?: TemplateOMRProfile;
}

export interface AnswerSheetTemplate {
  id: string;
  tenant_id: string;
  exam_id: string;
  exam_paper_id: string;
  version_no: number;
  revision: number;
  name: string;
  status: "draft" | "locked" | "retired";
  page_count: number;
  layout: TemplateLayout;
  content_hash: string;
  created_by: string;
  locked_by?: string;
  locked_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ExamTemplateBinding {
  id: string;
  tenant_id: string;
  exam_id: string;
  template_id: string;
  template_content_hash: string;
  mode: "bound_auto" | "locked_with_guard";
  source: "automatic" | "manual";
  revision: number;
  bound_by: string;
  bound_at: string;
  updated_at: string;
}

export type OMRCalibrationStatus = import("@edugrade/sdk").OMRCalibrationSession["status"];
export type OMRCalibrationSummary = import("@edugrade/sdk").OMRCalibrationSummary;
export type OMRCalibrationSession = import("@edugrade/sdk").OMRCalibrationSession;
export type OMRCalibrationCase = import("@edugrade/sdk").OMRCalibrationCase;
export type OMRCalibrationDetail = import("@edugrade/sdk").OMRCalibrationDetail;

export interface TemplatePayload {
  exam_paper_id: string;
  name: string;
  page_count: number;
  layout: TemplateLayout;
}

export interface ReadinessCheck {
  code: string;
  label: string;
  passed: boolean;
  severity: "blocker" | "warning";
  message: string;
  section: string;
}

export interface ExamReadiness {
  ready: boolean;
  confirmed: boolean;
  configuration_hash: string;
  snapshot_id?: string;
  import_snapshot_available: boolean;
  checks: ReadinessCheck[];
  confirmed_at?: string;
  confirmed_by?: string;
}

export async function listAnswerSheetTemplates(examId: string) {
  return apiClient.request<{ templates: AnswerSheetTemplate[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-templates`);
}

export async function createAnswerSheetTemplate(examId: string, payload: TemplatePayload) {
  return apiClient.request<{ template: AnswerSheetTemplate }>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-templates`, {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function updateAnswerSheetTemplate(templateId: string, payload: TemplatePayload, expectedRevision: number) {
  return apiClient.request<{ template: AnswerSheetTemplate }>(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}`, {
    method: "PATCH",
    body: JSON.stringify({ name: payload.name, page_count: payload.page_count, layout: payload.layout, expected_revision: expectedRevision })
  });
}

export async function lockAnswerSheetTemplate(templateId: string) {
  return apiClient.request<{ template: AnswerSheetTemplate }>(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/lock`, { method: "POST" });
}

export async function cloneAnswerSheetTemplate(templateId: string) {
  return apiClient.request<{ template: AnswerSheetTemplate }>(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/clone`, { method: "POST" });
}

export async function getExamTemplateBinding(examId: string) {
  return apiClient.request<{ binding: ExamTemplateBinding | null }>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-template-binding`);
}

export async function bindExamTemplate(examId: string, templateId: string, expectedRevision: number, mode: "locked_with_guard" = "locked_with_guard") {
  return apiClient.request<{ binding: ExamTemplateBinding }>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-template-binding`, {
    method: "PUT",
    body: JSON.stringify({ template_id: templateId, mode, expected_revision: expectedRevision })
  });
}

export async function unbindExamTemplate(examId: string, expectedRevision: number, reason: string) {
  return apiClient.request<{ binding: ExamTemplateBinding }>(`/api/v1/exams/${encodeURIComponent(examId)}/answer-sheet-template-binding`, {
    method: "DELETE",
    body: JSON.stringify({ expected_revision: expectedRevision, reason })
  });
}

export async function listOMRCalibrations(templateId: string) {
  return apiClient.request<{ calibrations: OMRCalibrationSession[] }>(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/omr-calibrations`);
}

export async function createOMRCalibration(templateId: string) {
  return generatedApi.createOMRCalibration({ path: { id: templateId }, body: {} });
}

export async function getOMRCalibration(calibrationId: string) {
  return generatedApi.getOMRCalibration({ path: { id: calibrationId } });
}

export async function labelOMRCalibrationCase(calibrationId: string, caseId: string, expectedOptions: string[]) {
  return apiClient.request<{ calibration: OMRCalibrationDetail }>(`/api/v1/omr-calibrations/${encodeURIComponent(calibrationId)}/cases/${encodeURIComponent(caseId)}/label`, {
    method: "POST",
    body: JSON.stringify({ expected_options: expectedOptions })
  });
}

export async function approveOMRCalibration(calibrationId: string, approvalNote: string) {
  return apiClient.request<{ calibration: OMRCalibrationDetail }>(`/api/v1/omr-calibrations/${encodeURIComponent(calibrationId)}/approve`, {
    method: "POST",
    body: JSON.stringify({ approval_note: approvalNote })
  });
}

export async function revokeOMRCalibration(calibrationId: string, reason: string) {
  return apiClient.request<{ calibration: OMRCalibrationDetail }>(`/api/v1/omr-calibrations/${encodeURIComponent(calibrationId)}/revoke`, {
    method: "POST",
    body: JSON.stringify({ reason })
  });
}

export async function discardOMRCalibration(calibrationId: string, reason: string) {
  return apiClient.request<{ calibration: OMRCalibrationDetail }>(`/api/v1/omr-calibrations/${encodeURIComponent(calibrationId)}/discard`, {
    method: "POST",
    body: JSON.stringify({ reason })
  });
}

export async function downloadOMRCalibrationCaseImage(answerSegmentId: string) {
  return apiClient.requestBlob(`/api/v1/answer-segments/${encodeURIComponent(answerSegmentId)}/image`);
}

export async function getExamReadiness(examId: string) {
  return apiClient.request<{ readiness: ExamReadiness }>(`/api/v1/exams/${encodeURIComponent(examId)}/readiness`);
}

export async function confirmExamReadiness(examId: string) {
  return apiClient.request<{ readiness: ExamReadiness }>(`/api/v1/exams/${encodeURIComponent(examId)}/readiness/confirm`, { method: "POST" });
}

export async function startExamCollection(examId: string) {
  return apiClient.request<{ readiness: ExamReadiness; status: string }>(`/api/v1/exams/${encodeURIComponent(examId)}/start-collection`, { method: "POST" });
}
