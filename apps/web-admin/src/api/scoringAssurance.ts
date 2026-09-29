import { EduGradeApi } from "@edugrade/sdk";
import type {
  AddGradingEvaluationObservationRequest,
  AddModelCalibrationEvidenceRequest,
  AIEligibilityDecision,
  AIEligibilityPolicy,
  AIHumanDisagreement,
  AIHumanDisagreementSeverity,
  AIHumanDisagreementStatus,
  AIHumanDisagreementTaxonomy,
  ClassifyAIHumanDisagreementRequest,
  CreateGradingEvaluationRequest,
  CreateModelCalibrationRequest,
  EducationStage,
  ExamRiskTier,
  GradingEvaluationResponseDifficulty,
  GradingEvaluationQualitySummary,
  GradingEvaluationRun,
  GradingEvaluationSliceMetric,
  ModelCalibration,
  ModelCalibrationEvidence,
  PutAIEligibilityPolicyRequest,
  RouteAIHumanDisagreementRequest,
  SubjectCode
} from "@edugrade/sdk";
import { ApiClient, apiClient } from "./client";

// 平台代管请求携带所选租户；空值交由服务端按当前登录范围处理，页面离开时需清除选择。
let selectedSchoolID = "";
export function setScoringAssuranceSchool(tenantID: string) {
  selectedSchoolID = tenantID;
}

const scopedClient = new class extends ApiClient {
  override request<T>(path: string, init: RequestInit = {}): Promise<T> {
    if (!selectedSchoolID) return apiClient.request<T>(path, init);
    const separator = path.includes("?") ? "&" : "?";
    return apiClient.request<T>(`${path}${separator}tenant_id=${encodeURIComponent(selectedSchoolID)}`, init);
  }
}();
const api = new EduGradeApi(scopedClient);

export type {
  AddGradingEvaluationObservationRequest,
  AddModelCalibrationEvidenceRequest,
  AIEligibilityDecision,
  AIEligibilityPolicy,
  AIHumanDisagreement,
  AIHumanDisagreementSeverity,
  AIHumanDisagreementStatus,
  AIHumanDisagreementTaxonomy,
  ClassifyAIHumanDisagreementRequest,
  CreateGradingEvaluationRequest,
  CreateModelCalibrationRequest,
  EducationStage,
  ExamRiskTier,
  GradingEvaluationResponseDifficulty,
  GradingEvaluationQualitySummary,
  GradingEvaluationRun,
  GradingEvaluationSliceMetric,
  ModelCalibration,
  ModelCalibrationEvidence,
  PutAIEligibilityPolicyRequest,
  RouteAIHumanDisagreementRequest,
  SubjectCode
};

export interface EligibilityAxis {
  subject_code: SubjectCode;
  education_stage: EducationStage;
  archetype_code: string;
  risk_tier: ExamRiskTier;
}

export function getAIEligibilityPolicy(axis: EligibilityAxis) {
  return api.getAIEligibilityPolicy({ query: axis });
}

export function putAIEligibilityPolicy(body: PutAIEligibilityPolicyRequest) {
  return api.putAIEligibilityPolicy({ body });
}

export function getAIEligibilityDecision(runItemId: string) {
  return api.getAIEligibilityDecision({ path: { runItemId } });
}

export function listGradingEvaluations() {
  return api.listGradingEvaluations({ query: { limit: 100 } });
}

export function createGradingEvaluation(body: CreateGradingEvaluationRequest) {
  return api.createGradingEvaluation({ body });
}

export function addGradingEvaluationObservation(runId: string, body: AddGradingEvaluationObservationRequest) {
  return api.addGradingEvaluationObservation({ path: { runId }, body });
}

export function completeGradingEvaluation(runId: string) {
  return api.completeGradingEvaluation({ path: { runId } });
}

export function invalidateGradingEvaluation(runId: string, reason: string) {
  return api.invalidateGradingEvaluation({ path: { runId }, body: { reason } });
}

export function listGradingEvaluationSliceMetrics(runId: string) {
  return api.listGradingEvaluationSliceMetrics({ path: { runId } });
}

export function listGradingEvaluationResponseDifficulty(runId: string) {
  return api.listGradingEvaluationResponseDifficulty({ path: { runId } });
}

export function getGradingEvaluationQualitySummary(runId: string) {
  return api.getGradingEvaluationQualitySummary({ path: { runId } });
}

export function listModelCalibrations() {
  return api.listModelCalibrations({ query: { limit: 100 } });
}

export function createModelCalibration(body: CreateModelCalibrationRequest) {
  return api.createModelCalibration({ body });
}

export function listModelCalibrationEvidence(calibrationId: string) {
  return api.listModelCalibrationEvidence({ path: { calibrationId } });
}

export function addModelCalibrationEvidence(calibrationId: string, body: AddModelCalibrationEvidenceRequest) {
  return api.addModelCalibrationEvidence({ path: { calibrationId }, body });
}

export function completeModelCalibration(calibrationId: string) {
  return api.completeModelCalibration({ path: { calibrationId } });
}

export function approveModelCalibration(calibrationId: string) {
  return api.approveModelCalibration({ path: { calibrationId } });
}

export function invalidateModelCalibration(calibrationId: string, reason: string) {
  return api.invalidateModelCalibration({ path: { calibrationId }, body: { reason } });
}

export function listAIHumanDisagreements(filter: {
  severity?: AIHumanDisagreementSeverity;
  status?: AIHumanDisagreementStatus;
  limit?: number;
} = {}) {
  return api.listAIHumanDisagreements({ query: { limit: 100, ...filter } });
}

export function classifyAIHumanDisagreement(id: string, body: ClassifyAIHumanDisagreementRequest) {
  return api.classifyAIHumanDisagreement({ path: { id }, body });
}

export function routeAIHumanDisagreement(id: string, body: RouteAIHumanDisagreementRequest) {
  return api.routeAIHumanDisagreement({ path: { id }, body });
}
