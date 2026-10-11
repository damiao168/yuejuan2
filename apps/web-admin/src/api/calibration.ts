import { EduGradeApi } from "@edugrade/sdk";
import type {
  PutCalibrationPolicyRequest,
  SubmitCalibrationAttemptRequest
} from "@edugrade/sdk";
import { apiClient } from "./client";

const generatedApi = new EduGradeApi(apiClient);

export type {
  CalibrationAttempt,
  CalibrationMetrics,
  CalibrationPolicy,
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
