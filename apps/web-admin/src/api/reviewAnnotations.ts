import {
  EduGradeApi,
  type CreateReviewAnnotationRequest,
  type CreateReviewCommentTemplateRequest,
  type DeleteRevisionRequest,
  type UpdateReviewAnnotationRequest,
  type UpdateReviewCommentTemplateRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";

const generatedApi = new EduGradeApi(apiClient);
const applyReviewCommentTemplateRequest = generatedApi.useReviewCommentTemplate.bind(generatedApi);

export type {
  CreateReviewAnnotationRequest,
  CreateReviewCommentTemplateRequest,
  ReviewAnnotation,
  ReviewCommentTemplate,
  StudentReviewAnnotation,
  UpdateReviewAnnotationRequest,
  UpdateReviewCommentTemplateRequest
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

export async function updateReviewAnnotation(
  annotationId: string,
  body: UpdateReviewAnnotationRequest,
  signal?: AbortSignal
) {
  return generatedApi.updateReviewAnnotation({ path: { annotationId }, body, signal });
}

export async function deleteReviewAnnotation(annotationId: string, expectedRevision: number, signal?: AbortSignal) {
  const body: DeleteRevisionRequest = { expected_revision: expectedRevision };
  return generatedApi.deleteReviewAnnotation({ path: { annotationId }, body, signal });
}

export async function listReviewCommentTemplates(signal?: AbortSignal) {
  return generatedApi.listReviewCommentTemplates({ signal });
}

export async function createReviewCommentTemplate(body: CreateReviewCommentTemplateRequest, signal?: AbortSignal) {
  return generatedApi.createReviewCommentTemplate({ body, signal });
}

export async function updateReviewCommentTemplate(
  templateId: string,
  body: UpdateReviewCommentTemplateRequest,
  signal?: AbortSignal
) {
  return generatedApi.updateReviewCommentTemplate({ path: { templateId }, body, signal });
}

export async function deleteReviewCommentTemplate(templateId: string, expectedRevision: number, signal?: AbortSignal) {
  const body: DeleteRevisionRequest = { expected_revision: expectedRevision };
  return generatedApi.deleteReviewCommentTemplate({ path: { templateId }, body, signal });
}

export async function applyReviewCommentTemplate(shortcut: string, signal?: AbortSignal) {
  return applyReviewCommentTemplateRequest({ path: { shortcut }, signal });
}
