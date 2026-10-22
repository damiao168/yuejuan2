import { apiClient } from "./client";
import type { LayoutRegion } from "./configuration";

export interface AnalysisBox { x: number; y: number; width: number; height: number }
export interface TemplateFillGradingGroup {
  group_id: string;
  question_labels: string[];
  bbox: AnalysisBox;
  reason_codes: string[];
}
export interface TemplateObjectiveItem {
  printed_question_label: string;
  // 题区与选项均为整页坐标；写入模板时才转换为本题内选项坐标。
  bbox: AnalysisBox;
  options: Array<{ label: string; bbox: AnalysisBox }>;
}
export interface TemplateAnalysisCandidate {
  page_no: number;
  candidate_id: string;
  printed_question_label: string | null;
  // 这里只接收身份栏类别；学生实际填写内容不能进入可复用模板。
  identity_label?: "姓名" | "学号" | "准考证号" | "考号" | "班级" | "学校" | "条码" | "二维码" | "身份信息" | null;
  role: "answer" | "objective_group" | "identity" | "instruction" | "other" | "uncertain";
  proposed_bbox: AnalysisBox | null;
  source_bbox?: AnalysisBox | null;
  association_status: "associated" | "unassociated" | "ambiguous";
  evidence_refs: string[];
  reason_codes: string[];
  semantic_check?: "supported" | "needs_review" | "not_evaluated";
  origin?: "model" | "local_detector";
  objective_items?: TemplateObjectiveItem[];
}
export interface TemplatePageAnalysis {
  template_id: string;
  template_revision: number;
  source_sha256: string;
  analysis: { request_id: string; schema_version: string; status: "review_required";
    coordinate_space: "source_display_normalized"; page_no: number; source_sha256: string;
    semantic_status: "completed" | "not_configured" | "failed" | "not_applicable";
    registration?: { version: string; status: "detected" | "not_found"; coordinate_space: "source_normalized";
      registration_marks: LayoutRegion[]; anchor_points?: Array<{ x: number; y: number }>;
      stable_quad?: Array<{ x: number; y: number }>; confidence?: number };
    fill_grading_groups?: TemplateFillGradingGroup[];
    reason_codes?: string[];
    semantic_error?: { code: string; retryable?: boolean; upstream_http_status?: number; upstream_error_code?: string } | null;
    model_identity?: { provider_key?: string; model_name?: string; model_version?: string };
    candidates: TemplateAnalysisCandidate[] };
}
export function analyzeAnswerSheetTemplatePage(templateId: string, input: {
  expected_revision: number; expected_source_sha256: string; page_no: number; width: number; height: number;
  media_type: "image/jpeg"; data_base64: string; sha256: string; writing_state: "unknown";
  candidates: Array<{ candidate_id: string; bbox: AnalysisBox; evidence_refs: string[] }>;
}, signal?: AbortSignal) {
  return apiClient.request<TemplatePageAnalysis>(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/analyze-page`, {
    method: "POST", body: JSON.stringify(input), signal
  });
}
