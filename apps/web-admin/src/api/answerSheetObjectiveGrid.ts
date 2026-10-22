import { apiClient } from "./client";
import type { AnalysisBox } from "./answerSheetTemplateAnalysis";

export type ObjectiveOptionOrientation = "horizontal" | "vertical";
export interface ObjectiveGridGroup {
  group_id: string;
  bbox: AnalysisBox;
  options: Array<{ bbox: AnalysisBox }>;
  reason_codes: string[];
}
export interface ObjectiveGridAnalysis {
  template_id: string;
  template_revision: number;
  source_sha256: string;
  analysis: {
    request_id: string;
    schema_version: "answer-sheet-objective-grid-v1";
    status: "review_required";
    coordinate_space: "source_display_normalized";
    page_no: number;
    source_sha256: string;
    block_bbox: AnalysisBox;
    groups: ObjectiveGridGroup[];
    reason_codes: string[];
    versions: { algorithm: string };
  };
}
export interface ObjectiveGridInput {
  expected_revision: number;
  expected_source_sha256: string;
  page_no: number;
  width: number;
  height: number;
  media_type: "image/png" | "image/jpeg";
  data_base64: string;
  sha256: string;
  block_bbox: AnalysisBox;
  option_count: number;
  option_orientation: ObjectiveOptionOrientation;
}

/** 网格提取只生成几何建议，不绑定题号、保存草稿或调用多模态模型。 */
export function analyzeAnswerSheetObjectiveGrid(templateId: string, input: ObjectiveGridInput, signal?: AbortSignal) {
  return apiClient.request<ObjectiveGridAnalysis>(`/api/v1/answer-sheet-templates/${encodeURIComponent(templateId)}/analyze-objective-grid`, {
    method: "POST", body: JSON.stringify(input), signal
  });
}
