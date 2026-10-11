import { EduGradeApi } from "@edugrade/sdk";
import type {
  DecideQuestionAppealRequest,
  QuestionAppealReviewOpinionRequest,
  ResolveQuestionAppealRequest,
  StartQuestionAppealReviewRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";

const questionAppealApi = new EduGradeApi(apiClient);

export type {
  DecideQuestionAppealRequest,
  PublishedQuestionAppeal,
  PublishedQuestionAppealContext,
  QuestionAppealReviewOpinion,
  QuestionAppealReviewOpinionRequest,
  QuestionAppealReleaseVersion,
  StartQuestionAppealReviewRequest
} from "@edugrade/sdk";

export function listQuestionAppeals(examId: string, signal?: AbortSignal) {
  return questionAppealApi.listQuestionAppeals({ query: { exam_id: examId || undefined }, signal });
}

export function getQuestionAppealContext(id: string, signal?: AbortSignal) {
  return questionAppealApi.getQuestionAppealContext({ path: { id }, signal });
}

export function startQuestionAppealReview(id: string, body: StartQuestionAppealReviewRequest) {
  return questionAppealApi.startQuestionAppealReview({ path: { id }, body });
}

export function decideQuestionAppeal(id: string, body: DecideQuestionAppealRequest) {
  return questionAppealApi.decideQuestionAppeal({ path: { id }, body });
}

export function submitQuestionAppealReviewOpinion(id: string, body: QuestionAppealReviewOpinionRequest) {
  return questionAppealApi.submitQuestionAppealReviewOpinion({ path: { id }, body });
}

export function resolveQuestionAppeal(id: string, body: ResolveQuestionAppealRequest) {
  return questionAppealApi.resolveQuestionAppeal({ path: { id }, body });
}

export async function getQuestionAppealAnswerImage(id: string) {
  return apiClient.requestBlob(`/api/v1/question-appeals/${encodeURIComponent(id)}/answer-image`);
}
