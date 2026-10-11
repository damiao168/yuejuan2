import { EduGradeApi } from "@edugrade/sdk";
import { apiClient } from "./client";

const api = new EduGradeApi(apiClient);

export type { QualityDashboard } from "@edugrade/sdk";

export function getExamQualityDashboard(examId: string, signal?: AbortSignal) {
  return api.getExamQualityDashboard({ path: { examId }, signal });
}
