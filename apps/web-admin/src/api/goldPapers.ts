import { EduGradeApi } from "@edugrade/sdk";
import type {
  CreateGoldPaperVersionRequest,
  GoldPaperStatus,
  NominateGoldPaperRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";

export type { CreateGoldPaperVersionRequest, GoldCoverage, GoldPaper, GoldPaperStatus, NominateGoldPaperRequest } from "@edugrade/sdk";

const goldPaperApi = new EduGradeApi(apiClient);

export function nominateGoldPaper(examId: string, questionId: string, body: NominateGoldPaperRequest) {
  return goldPaperApi.nominateGoldPaper({ path: { examId, questionId }, body });
}

export function listGoldPapers(query: { exam_id?: string; question_id?: string; status?: GoldPaperStatus } = {}) {
  return goldPaperApi.listGoldPapers({ query });
}

export function getGoldCoverage(examId: string, questionId: string) {
  return goldPaperApi.getGoldCoverage({ path: { examId, questionId } });
}

export function createGoldPaperVersion(goldPaperId: string, body: CreateGoldPaperVersionRequest) {
  return goldPaperApi.createGoldPaperVersion({ path: { goldPaperId }, body });
}

export function approveGoldPaperVersion(goldPaperId: string, version: number) {
  return goldPaperApi.approveGoldPaperVersion({ path: { goldPaperId, version } });
}

export function retireGoldPaper(goldPaperId: string, reason: string) {
  return goldPaperApi.retireGoldPaper({ path: { goldPaperId }, body: { reason } });
}
