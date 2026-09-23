import { EduGradeApi } from "@edugrade/sdk";
import type {
  CreateRegradeJobRequest,
  CreateRegradePreviewRequest,
  CreateRegradeScoreReleaseRequest,
  RegradeItem,
  CreateScoreReleaseRequest,
  RegradeJob,
  RegradePreview,
  RegradeSummary,
  ReviewRegradeItemRequest,
  ScoreRelease,
  ScoreReleaseGate
} from "@edugrade/sdk";
import { apiClient } from "./client";

const releaseApi = new EduGradeApi(apiClient);

export type {
  CreateRegradeJobRequest,
  CreateRegradePreviewRequest,
  CreateRegradeScoreReleaseRequest,
  CreateScoreReleaseRequest,
  RegradeJob,
  RegradePreview,
  RegradeItem,
  RegradeSummary,
  ReviewRegradeItemRequest,
  ScoreRelease,
  ScoreReleaseGate
};

export function listScoreReleases(examId: string, signal?: AbortSignal) {
  return releaseApi.listScoreReleases({ path: { examId }, signal });
}

export function getScoreReleaseGate(examId: string, signal?: AbortSignal) {
  return releaseApi.getScoreReleaseGate({ path: { examId }, signal });
}

export function createScoreRelease(examId: string, body: CreateScoreReleaseRequest) {
  return releaseApi.createScoreRelease({ path: { examId }, body });
}

export function publishScoreRelease(id: string) {
  return releaseApi.publishScoreRelease({ path: { id } });
}

export function revokeHighScorePaper(id: string) {
  return releaseApi.revokeHighScorePaper({ path: { id } });
}

export function previewRegrade(examId: string, questionId: string, body: CreateRegradePreviewRequest) {
  return releaseApi.previewRegrade({ path: { examId, questionId }, body });
}

export function createRegradeJob(examId: string, questionId: string, body: CreateRegradeJobRequest) {
  return releaseApi.createRegradeJob({ path: { examId, questionId }, body });
}

export function listRegradeJobs(examId: string, signal?: AbortSignal) {
  return releaseApi.listRegradeJobs({ query: { exam_id: examId }, signal });
}

export function getRegradeJob(jobId: string) {
  return releaseApi.getRegradeJob({ path: { jobId } });
}

export function reviewRegradeItem(itemId: string, body: ReviewRegradeItemRequest) {
  return releaseApi.reviewRegradeItem({ path: { itemId }, body });
}

export function approveRegradeJob(jobId: string) {
  return releaseApi.approveRegradeJob({ path: { jobId } });
}

export function startRegradeJob(jobId: string) {
  return releaseApi.startRegradeJob({ path: { jobId } });
}

export function pauseRegradeJob(jobId: string) {
  return releaseApi.pauseRegradeJob({ path: { jobId } });
}

export function resumeRegradeJob(jobId: string) {
  return releaseApi.resumeRegradeJob({ path: { jobId } });
}

export function finalizeRegradeJob(jobId: string) {
  return releaseApi.finalizeRegradeJob({ path: { jobId } });
}

export function createRegradeScoreRelease(jobId: string, body: CreateRegradeScoreReleaseRequest) {
  return releaseApi.createRegradeScoreRelease({ path: { jobId }, body });
}
