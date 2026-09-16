import { EduGradeApi } from "@edugrade/sdk";
import type {
  BackmarkBatch,
  CreateBackmarkBatchRequest,
  BackmarkGraderContext,
  BackmarkGraderItem,
  BackmarkPolicy,
  BackmarkPreview,
  BackmarkSummary,
  BackmarkSelector,
  BackmarkRegradeInput,
  RegradePreview,
  RegradeSummary,
  SubmitBackmarkItemRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";

const generatedApi = new EduGradeApi(apiClient);

export type { BackmarkBatch, BackmarkGraderContext, BackmarkGraderItem, BackmarkPolicy, BackmarkPreview, BackmarkSelector, BackmarkSummary, BackmarkRegradeInput };

export interface BackmarkPageOptions {
  limit?: number;
  cursor?: string;
}

export function previewBackmarkBatch(examId: string, questionId: string, selector: BackmarkSelector) {
  return generatedApi.previewBackmarkBatch({ path: { examId, questionId }, body: { selector } });
}

export function createBackmarkBatch(
  examId: string,
  questionId: string,
  payload: CreateBackmarkBatchRequest
) {
  return generatedApi.createBackmarkBatch({ path: { examId, questionId }, body: payload });
}

export function listBackmarkBatches(examId: string, questionId?: string, page: BackmarkPageOptions = {}) {
  return generatedApi.listBackmarkBatches({ query: { exam_id: examId, question_id: questionId, ...page } });
}

export function getBackmarkBatch(batchId: string, page: NonNullable<Parameters<typeof generatedApi.getBackmarkBatch>[0]["query"]> = {}) {
  return generatedApi.getBackmarkBatch({ path: { batchId }, query: page });
}

export function previewBackmarkBatchRegrade(batchId: string, payload: BackmarkRegradeInput): Promise<{ preview: RegradePreview }> {
  return generatedApi.previewBackmarkBatchRegrade({ path: { batchId }, body: payload });
}

export function createBackmarkBatchRegradeJob(batchId: string, payload: BackmarkRegradeInput): Promise<{ regrade: RegradeSummary }> {
  return generatedApi.createBackmarkBatchRegradeJob({ path: { batchId }, body: payload });
}

export function listMyBackmarkItems(page: BackmarkPageOptions = {}) {
  return generatedApi.listMyBackmarkItems({ query: page });
}

export function claimBackmarkItem(itemId: string) {
  return generatedApi.claimBackmarkItem({ path: { itemId } });
}

export function getBackmarkItemContext(itemId: string) {
  return generatedApi.getBackmarkItemContext({ path: { itemId } });
}

export function submitBackmarkItem(itemId: string, payload: SubmitBackmarkItemRequest) {
  return generatedApi.submitBackmarkItem({ path: { itemId }, body: payload });
}

export function downloadBackmarkSegmentImage(itemId: string) {
  return apiClient.requestBlob(`/api/v1/backmark-items/${encodeURIComponent(itemId)}/segment-image`);
}
