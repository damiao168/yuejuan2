import { EduGradeApi } from "@edugrade/sdk";
import type {
  AIHumanDisagreement,
  ReviewTaskPage,
  AddGradingEvaluationObservationRequest,
  AddModelCalibrationEvidenceRequest,
  AIHumanDisagreementSeverity,
  AIHumanDisagreementStatus,
  ClassifyAIHumanDisagreementRequest,
  CreateGradingEvaluationRequest,
  CreateModelCalibrationRequest,
  EducationStage,
  ExamRiskTier,
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
} from "@edugrade/sdk";

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

export function getAIEligibilityDecision(runItemId: string, tenantID?: string) {
  if (tenantID) return apiClient.request<{ decision: import("@edugrade/sdk").AIEligibilityDecision }>(`/api/v1/ai-eligibility/decisions/${encodeURIComponent(runItemId)}?tenant_id=${encodeURIComponent(tenantID)}`);
  return api.getAIEligibilityDecision({ path: { runItemId } });
}

export interface AIEligibilityDecisionSummary {
  id: string; tenant_id: string; run_item_id: string;
  subject_code: string; education_stage: string; archetype_code: string; risk_tier: string;
  decision: string; external_ai_allowed: boolean; created_at: string;
}
export function listAIEligibilityDecisions(tenantID: string, query: { q?: string; cursor?: string } = {}) {
  const params = new URLSearchParams({ tenant_id: tenantID, limit: "30" });
  if (query.q) params.set("q", query.q);
  if (query.cursor) params.set("cursor", query.cursor);
  return apiClient.request<{ decisions: AIEligibilityDecisionSummary[]; next_cursor: string; has_more: boolean }>(`/api/v1/ai-eligibility/decisions?${params}`);
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

export async function listDisagreementReviewTasks(tenantID: string, disagreement: Pick<AIHumanDisagreement, "exam_id" | "question_id" | "submission_id" | "answer_segment_id">, cursor = "") {
  const params = new URLSearchParams({ tenant_id: tenantID, exam_id: disagreement.exam_id, question_id: disagreement.question_id, limit: "100" });
  if (cursor) params.set("cursor", cursor);
  // 沿用阅卷列表的账号权限，平台代管参数不会授予额外权限；客户端再核对同学校、同题和同答卷。
  const response = await apiClient.request<ReviewTaskPage>(`/api/v1/review-tasks?${params}`);
  if (response.tasks.some(task => task.tenant_id !== tenantID)) throw new Error("当前账号不能读取所选学校的阅卷任务，请由该校有阅卷权限的管理员处理");
  return { ...response, tasks: response.tasks.filter(task => task.exam_id === disagreement.exam_id && task.question_id === disagreement.question_id && task.submission_id === disagreement.submission_id && task.answer_segment_id === disagreement.answer_segment_id) };
}
