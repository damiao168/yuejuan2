import { apiClient } from "./client";
import { buildQueryString } from "./query";

export interface ExamTemplateSection {
  id: string;
  title: string;
  question_type: string;
  question_count: number;
  score_per_question: number;
  sort_order: number;
}

export interface ExamTemplateSubject {
  id: string;
  subject: string;
  total_score: number;
  duration_minutes: number;
  candidate_rule: "all_selected_classes" | "subject_selected_classes";
  sort_order: number;
  sections: ExamTemplateSection[];
}

export interface ExamTemplate {
  id: string;
  code: string;
  name: string;
  description: string;
  education_stage: "junior" | "senior";
  exam_type?: string;
  version: number;
  source: "system" | "regional" | "school";
  recommended: boolean;
  subjects: ExamTemplateSubject[];
}

export function listExamTemplates(filter: { educationStage?: "junior" | "senior" } = {}) {
  return apiClient.request<{ exam_templates: ExamTemplate[] }>(`/api/v1/exam-templates${buildQueryString({ education_stage: filter.educationStage })}`);
}
