import { EduGradeApi } from "@edugrade/sdk";
import type {
  PutCalibrationPolicyRequest,
  SubmitCalibrationAttemptRequest,
  ApproveCalibrationSchoolTemplateRequest,
  WithdrawCalibrationSchoolTemplateRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";

const generatedApi = new EduGradeApi(apiClient);

export type {
  CalibrationAttempt,
  CalibrationMetrics,
  CalibrationPolicy,
  CalibrationSchoolTemplate,
  CalibrationSchoolTemplateDirectory,
  CalibrationSession,
  GraderQualification,
  PutCalibrationPolicyRequest,
  SubmitCalibrationAttemptRequest
} from "@edugrade/sdk";

export async function getCalibrationPolicy(examId: string, questionId: string, signal?: AbortSignal) {
  return generatedApi.getCalibrationPolicy({ path: { examId, questionId }, signal });
}

export async function putCalibrationPolicy(examId: string, questionId: string, body: PutCalibrationPolicyRequest) {
  return generatedApi.putCalibrationPolicy({ path: { examId, questionId }, body });
}

export async function createCalibrationSession(examId: string, questionId: string) {
  return generatedApi.createCalibrationSession({ path: { examId, questionId }, body: {} });
}

export async function submitCalibrationAttempt(sessionId: string, body: SubmitCalibrationAttemptRequest) {
  return generatedApi.submitCalibrationAttempt({ path: { calibrationSessionId: sessionId }, body });
}

export async function getGraderQualification(examId: string, questionId: string, graderId?: string) {
  return generatedApi.getGraderQualification({
    path: { examId, questionId },
    query: graderId ? { grader_id: graderId } : undefined
  });
}

export async function listCalibrationSchoolTemplates(examId: string, questionId: string, signal?: AbortSignal) {
  return generatedApi.listCalibrationSchoolTemplates({ path: { examId, questionId }, signal });
}

export async function approveCalibrationSchoolTemplate(examId: string, questionId: string, body: ApproveCalibrationSchoolTemplateRequest) {
  return generatedApi.approveCalibrationSchoolTemplate({ path: { examId, questionId }, body });
}

export async function withdrawCalibrationSchoolTemplate(examId: string, questionId: string, templateId: string, body: WithdrawCalibrationSchoolTemplateRequest) {
  return generatedApi.withdrawCalibrationSchoolTemplate({ path: { examId, questionId, templateId }, body });
}
