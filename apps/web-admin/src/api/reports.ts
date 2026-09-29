import { executeBusinessCommand } from "./businessCommand";
import { apiClient } from "./client";

export interface ReportEmptyState {
  empty: boolean;
  reason?: string;
}

export interface ReportMetric {
  // 不可计算与实际为零分别表示；展示前先看 available，并按 reason 解释缺失原因。
  available: boolean;
  value: number;
  numerator?: number;
  denominator?: number;
  reason?: string;
}

export interface ScoreDistributionBucket {
  label: string;
  min: number;
  max: number;
  count: number;
}

export interface ScoreStats {
  count: number;
  average: number;
  highest: number;
  lowest: number;
  median: number;
  stddev: number;
  pass_rate: number;
  excellent_rate: number;
  distribution: ScoreDistributionBucket[];
}

export interface ClassComparison {
  class_id: string;
  class_name: string;
  student_count: number;
  average: number;
  median: number;
  pass_rate: number;
  excellent_rate: number;
}

export interface OptionCount {
  option: string;
  count: number;
}

export interface ErrorClue {
  question_id?: string;
  question_no?: string;
  source: string;
  text: string;
  count?: number;
}

export interface KnowledgeMastery {
  knowledge_point: string;
  score: number;
  max_score: number;
  mastery_rate: number;
  question_count: number;
}

export interface QuestionWeakness {
  question_id: string;
  question_no: string;
  wrong_count: number;
  score_rate: number;
}

export interface QuestionAnalysis {
  question_id: string;
  question_no: string;
  question_type: string;
  max_score: number;
  average_score: number;
  score_rate: number;
  correct_rate: number;
  difficulty: number;
  discrimination: number;
  knowledge_points: string[];
  option_distribution?: OptionCount[];
  option_empty?: ReportEmptyState;
  frequent_errors?: ErrorClue[];
}

export interface OverviewReport {
  exam_id: string;
  empty?: ReportEmptyState;
  student_count: number;
  published_count: number;
  stats: ScoreStats;
  class_comparisons: ClassComparison[];
  question_score_rates: QuestionAnalysis[];
}

export interface ClassReport {
  class_id: string;
  class_name: string;
  student_count: number;
  stats: ScoreStats;
  frequent_wrong_questions: QuestionWeakness[];
  weak_knowledge_points: KnowledgeMastery[];
}

export interface GradingQualityReport {
  exam_id: string;
  total_segments: number;
  ai_grade_count: number;
  ai_accepted_count: number;
  ai_adoption_rate: ReportMetric;
  human_comparable_count: number;
  human_modified_count: number;
  human_modification_rate: ReportMetric;
  double_mark_session_count: number;
  average_double_mark_diff: ReportMetric;
  max_double_mark_diff: ReportMetric;
  arbitration_count: number;
  ocr_task_count: number;
  ocr_failed_count: number;
  ocr_failure_rate: ReportMetric;
  low_confidence_review_count: number;
}

export async function getReportOverview(examId: string) {
  return apiClient.request<{ overview: OverviewReport }>(`/api/v1/exams/${encodeURIComponent(examId)}/reports/overview`);
}

export async function listClassReports(examId: string) {
  return apiClient.request<{ classes: ClassReport[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/reports/classes`);
}

export async function listQuestionReports(examId: string) {
  return apiClient.request<{ questions: QuestionAnalysis[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/reports/questions`);
}

export async function getGradingQualityReport(examId: string) {
  return apiClient.request<{ grading_quality: GradingQualityReport }>(`/api/v1/exams/${encodeURIComponent(examId)}/reports/grading-quality`);
}

export async function exportLearningReport(examId: string) {
  return executeBusinessCommand("report.export", examId, null,
    commandId => apiClient.requestBlob(`/api/v1/exams/${encodeURIComponent(examId)}/reports/export`, { method: "POST", headers: { "Idempotency-Key": commandId } }),
    value => {
      // 命令恢复回执里的文件是 Base64；还原成与首次 Blob 下载相同的返回形状。
      const result = value as { Content: string; ContentType: string; Filename: string; Watermark: string };
      const bytes = Uint8Array.from(atob(result.Content), character => character.charCodeAt(0));
      return { blob: new Blob([bytes], { type: result.ContentType }), contentType: result.ContentType, filename: result.Filename, watermark: result.Watermark };
    });
}
