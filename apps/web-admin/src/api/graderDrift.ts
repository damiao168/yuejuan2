import { EduGradeApi } from "@edugrade/sdk";
import { apiClient } from "./client";

const generatedApi = new EduGradeApi(apiClient);

export type { GradingQualityIncident } from "@edugrade/sdk";

export function listGradingQualityIncidents(examId: string, filter: { cursor?: string; question_id?: string } = {}) {
  return generatedApi.listGradingQualityIncidents({ query: { exam_id: examId, status: "open", limit: 100, ...filter } });
}

export function resolveGradingQualityIncident(id: string) {
  return generatedApi.resolveGradingQualityIncident({ path: { id } });
}

export function recomputeGraderDrift(examId: string, questionId: string) {
  return generatedApi.recomputeGraderDrift({ path: { examId, questionId } });
}
