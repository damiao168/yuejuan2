import { EduGradeApi } from "@edugrade/sdk";
import type {
  AnswerGroupSampleOutcome,
  PutAnswerGroupDecisionRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";
export type { AnswerGroupMemberContext } from "@edugrade/sdk";
export function getAnswerGroupMemberContext(groupId: string, segmentId: string, signal?: AbortSignal) { return answerGroupApi.getAnswerGroupMemberContext({path:{groupId,segmentId},signal}); }
export function getAnswerGroupMemberImage(groupId: string, segmentId: string, signal?: AbortSignal) { return apiClient.requestBlob(`/api/v1/answer-groups/${encodeURIComponent(groupId)}/samples/${encodeURIComponent(segmentId)}/segment-image`,{signal}); }

export type { AnswerGroup, AnswerGroupMetrics, AnswerGroupSampleOutcome, TeacherReferenceCase } from "@edugrade/sdk";

const answerGroupApi = new EduGradeApi(apiClient);

export function buildAnswerGroups(examId: string, questionId: string) {
  return answerGroupApi.buildAnswerGroups({ path: { examId, questionId }, body: {} });
}

export function listAnswerGroups(examId: string, questionId: string) {
  return answerGroupApi.listAnswerGroups({ path: { examId, questionId } });
}

export function getAnswerGroupMetrics(examId: string, questionId: string) {
  return answerGroupApi.getAnswerGroupMetrics({ path: { examId, questionId } });
}

export function getAnswerGroup(groupId: string) {
  return answerGroupApi.getAnswerGroup({ path: { groupId } });
}

export function reviewAnswerGroupSample(groupId: string, segmentId: string, outcome: AnswerGroupSampleOutcome, notes?: string) {
  return answerGroupApi.reviewAnswerGroupSample({ path: { groupId, segmentId }, body: { outcome, notes } });
}

export function saveAnswerGroupDecision(groupId: string, body: PutAnswerGroupDecisionRequest) {
  return answerGroupApi.putAnswerGroupDecision({ path: { groupId }, body });
}

export function confirmAnswerGroup(groupId: string, expectedRevision: number) {
  return answerGroupApi.confirmAnswerGroup({ path: { groupId }, body: { expected_revision: expectedRevision } });
}

export function rollbackAnswerGroup(groupId: string, rollbackReference: string, reason: string) {
  return answerGroupApi.rollbackAnswerGroup({ path: { groupId }, body: { rollback_reference: rollbackReference, reason } });
}
