import { EduGradeApi } from "@edugrade/sdk";
import type { RecordRegradeCandidateRequest } from "@edugrade/sdk";
import { apiClient } from "./client";
export type { RegradeReviewContext } from "@edugrade/sdk";
export function getRegradeReviewContext(id: string, signal?: AbortSignal) { return generatedApi.getRegradeItemReviewContext({path:{itemId:id},signal}); }
// SDK 的通用 JSON 传输不解析二进制；图片沿用同一鉴权客户端的 Blob 读取。
export function downloadRegradeReviewImage(id: string, signal?: AbortSignal) { return apiClient.requestBlob(`/api/v1/regrade-items/${encodeURIComponent(id)}/review-segment-image`, { signal }); }

const generatedApi = new EduGradeApi(apiClient);

export type { RecordRegradeCandidateRequest, RegradeGraderContext, RegradeWorkItem } from "@edugrade/sdk";

export function listMyRegradeItems() {
  return generatedApi.listMyRegradeItems();
}

export function claimRegradeItem(itemId: string) {
  return generatedApi.claimRegradeItem({ path: { itemId } });
}

export function getRegradeItemContext(itemId: string) {
  return generatedApi.getRegradeItemContext({ path: { itemId } });
}

export function recordRegradeCandidate(itemId: string, payload: RecordRegradeCandidateRequest) {
  return generatedApi.recordRegradeCandidate({ path: { itemId }, body: payload });
}

export function downloadRegradeSegmentImage(itemId: string) {
  return apiClient.requestBlob(`/api/v1/regrade-items/${encodeURIComponent(itemId)}/segment-image`);
}
