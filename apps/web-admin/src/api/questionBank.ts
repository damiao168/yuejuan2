import { EduGradeApi } from "@edugrade/sdk";
import { apiClient } from "./client";

export const questionBankApi = new EduGradeApi(apiClient);

export function getQuestionBankReferenceImage(versionId: string, itemId: string) {
  return apiClient.requestBlob(`/api/v1/question-bank/versions/${encodeURIComponent(versionId)}/reference-materials/${encodeURIComponent(itemId)}/image`);
}
