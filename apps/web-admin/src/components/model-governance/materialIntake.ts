import type { AddGradingEvaluationObservationRequest, CreateGradingEvaluationRequest } from "../../api/scoringAssurance";
import { parseEvaluationRunMaterial, type EvaluationRunMaterial } from "./evaluationReportImport";
import { validateObservationRow } from "./observationMaterial";

export interface ScoringMaterial { evaluation: CreateGradingEvaluationRequest; observations: AddGradingEvaluationObservationRequest[] }
export type GovernanceMaterial = { kind: "candidate_summary"; material: EvaluationRunMaterial } | { kind: "scoring_records"; material: ScoringMaterial };
export interface MaterialDelivery<T> { tenantID: string; sequence: number; material: T }

export function parseGovernanceMaterial(text: string): GovernanceMaterial {
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("请提供候选汇总报告，或包含 evaluation 与 observations 的逐卷评分材料");
  const data = value as Record<string, unknown>;
  if (!data.evaluation) return { kind: "candidate_summary", material: parseEvaluationRunMaterial(text) };
  if (typeof data.evaluation !== "object" || Array.isArray(data.evaluation)) throw new Error("逐卷材料缺少报告元数据");
  const source = data.evaluation as Record<string, unknown>;
  const field = (key: string) => { const value = source[key]; if (typeof value !== "string" || !value.trim()) throw new Error(`报告缺少 ${key}`); return value.trim(); };
  const evaluation: CreateGradingEvaluationRequest = {
    key: field("key"), display_name: field("display_name"), model_reference: field("model_reference"), prompt_version: field("prompt_version"), rubric_version: field("rubric_version"), dataset_reference: field("dataset_reference"), dataset_sha256: field("dataset_sha256").toLowerCase()
  };
  if (![evaluation.key, evaluation.dataset_reference].every(value => /^[a-z0-9][a-z0-9._-]{0,127}$/.test(value)) || !/^[a-f0-9]{64}$/.test(evaluation.dataset_sha256)) throw new Error("报告标识或数据集哈希无效");
  if (!Array.isArray(data.observations) || data.observations.length < 1 || data.observations.length > 500) throw new Error("逐卷材料须包含1—500条脱敏观察");
  const keys = new Set<string>();
  const allowed: (keyof AddGradingEvaluationObservationRequest)[] = ["response_key", "response_fingerprint", "reference_kind", "subject", "archetype", "ocr_quality", "answer_length", "rubric_complexity", "reference_score", "model_score", "max_score", "page_match_correct", "crop_iou", "transcription_cer", "formula_exact", "rubric_criterion_agreement", "error_source", "needs_human_review", "reference_reviewer_count", "reference_adjudicated"];
  const observations = data.observations.map(row => {
    const checked = validateObservationRow(row, keys); keys.add(checked.response_key);
    return Object.fromEntries(allowed.filter(key => checked[key] !== undefined).map(key => [key, checked[key]])) as AddGradingEvaluationObservationRequest;
  });
  // 只白名单复制已验证的报告和脱敏观察；文件不能指定学校、冻结状态或批准结论。
  return { kind: "scoring_records", material: { evaluation, observations } };
}

export function scoringMaterialMatchesReport(material: ScoringMaterial, values: CreateGradingEvaluationRequest) {
  // 名称和新报告标识可调整，证据只能沿用到同一数据集及模型、提示词、细则版本。
  const fields = ["model_reference", "prompt_version", "rubric_version", "dataset_reference", "dataset_sha256"] as const;
  return fields.every(key => material.evaluation[key] === values[key]);
}
