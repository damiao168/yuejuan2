import {
  EduGradeApi,
  type CreateReviewAnnotationRequest,
  type DeleteRevisionRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";

const generatedApi = new EduGradeApi(apiClient);

export type {
  CreateReviewAnnotationRequest,
  ReviewAnnotation,
  StudentReviewAnnotation
} from "@edugrade/sdk";

export async function listReviewAnnotations(taskId: string, signal?: AbortSignal) {
  return generatedApi.listReviewAnnotations({ path: { taskId }, signal });
}

export async function createReviewAnnotation(
  taskId: string,
  body: CreateReviewAnnotationRequest,
  signal?: AbortSignal
) {
  return generatedApi.createReviewAnnotation({ path: { taskId }, body, signal });
}

export async function deleteReviewAnnotation(annotationId: string, expectedRevision: number, signal?: AbortSignal) {
  const body: DeleteRevisionRequest = { expected_revision: expectedRevision };
  return generatedApi.deleteReviewAnnotation({ path: { annotationId }, body, signal });
}
