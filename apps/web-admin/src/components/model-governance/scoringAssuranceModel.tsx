import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { type AddGradingEvaluationObservationRequest, type AIHumanDisagreementTaxonomy, type CreateGradingEvaluationRequest, type CreateModelCalibrationRequest, type EligibilityAxis, type PutAIEligibilityPolicyRequest } from "../../api/scoringAssurance";
import { type EligibilitySelection } from "./eligibilityScope";


import { type MaterialDelivery, type ScoringMaterial } from "./materialIntake";


export const taxonomyOptions: Array<{ value: AIHumanDisagreementTaxonomy; label: string }> = [
  ["ai_scoring_error", "AI 评分错误"], ["human_scoring_error", "人工评分错误"], ["ocr_error", "文字识别错误"],
  ["parser_error", "解析错误"], ["rubric_ambiguity", "评分细则歧义"], ["reference_answer_issue", "参考答案问题"],
  ["question_issue", "题目问题"], ["insufficient_evidence", "证据不足"], ["acceptable_variation", "可接受差异"]
].map(([value, label]) => ({ value: value as AIHumanDisagreementTaxonomy, label }));

export const defaultPolicy = (axis: EligibilityAxis): PutAIEligibilityPolicyRequest => ({
  ...axis,
  min_ocr_quality: 0.9,
  min_parser_quality: 0.9,
  min_eval_n: 30,
  max_severe_error_rate: 0.02,
  allowed_modes: axis.risk_tier === "R3" && axis.archetype_code === "extended_response" ? ["AI_ASSIST", "HUMAN_PRIMARY"] : ["RULE_AUTO", "AI_ASSIST"],
  status: "active",
  expected_version: 0
});

export function asErrorMessage(error: unknown) {
  return getUserErrorMessage(error, "请求失败，请稍后重试");
}

export function isNotFound(error: unknown) {
  return error instanceof ApiClientError && error.status === 404;
}

export type PolicyForm = PutAIEligibilityPolicyRequest;
export type EvaluationForm = CreateGradingEvaluationRequest;
export type ObservationForm = AddGradingEvaluationObservationRequest;
export type CalibrationForm = CreateModelCalibrationRequest;
export type AssuranceDraft = "evaluation" | "observation" | "calibration" | "classification" | "route";

export type ScoringAssuranceProps = Readonly<{
  tenantID: string;
  initialAxis?: EligibilitySelection;
  canReadEligibility: boolean;
  canManageEligibility: boolean;
  canManageEvaluations: boolean;
  canReadDisagreements: boolean;
  canManageDisagreements: boolean;
  onDirtyChange?: (dirty: boolean) => void;
  onSavingChange?: (saving: boolean) => void;
  materialDelivery?: MaterialDelivery<ScoringMaterial>;
  onMaterialReceived?: () => void;
}>;
