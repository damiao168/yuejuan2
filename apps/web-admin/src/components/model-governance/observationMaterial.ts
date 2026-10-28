import type { AddGradingEvaluationObservationRequest } from "../../api/scoringAssurance";

export function validateObservationRow(row: unknown, acceptedKeys: ReadonlySet<string>): AddGradingEvaluationObservationRequest {
  // 一行有多处错误时按原顺序报告首错；只校验并返回原对象，不登记键或写入观察。
  if (!row || typeof row !== "object") throw new Error("观察记录须为对象");
  const item = row as AddGradingEvaluationObservationRequest;
  if (!/^[a-z0-9][a-z0-9._-]{0,127}$/.test(item.response_key ?? "") || acceptedKeys.has(item.response_key) || !/^[a-f0-9]{64}$/.test(item.response_fingerprint ?? "")) throw new Error("答卷键须唯一，指纹须为真实64位SHA256");
  if (![item.reference_score, item.model_score, item.max_score].every((value) => typeof value === "number" && Number.isFinite(value)) || item.max_score <= 0 || item.reference_score < 0 || item.model_score < 0 || item.reference_score > item.max_score || item.model_score > item.max_score) throw new Error("请核对参考分、模型分和满分");
  if (![item.subject, item.archetype].every((value) => typeof value === "string" && /^[a-z0-9][a-z0-9._-]{0,127}$/.test(value))) throw new Error("学科和题型须填写报告中的规范标识");
  if (!["gold", "human_adjudicated"].includes(item.reference_kind) || !["high", "medium", "low", "unknown"].includes(item.ocr_quality) || !["short", "medium", "long", "unknown"].includes(item.answer_length) || !["low", "medium", "high", "unknown"].includes(item.rubric_complexity)) throw new Error("请核对真值来源与切片标签");
  const reviewerCount = item.reference_reviewer_count;
  if (typeof item.needs_human_review !== "boolean" || typeof item.reference_adjudicated !== "boolean" || typeof reviewerCount !== "number" || !Number.isInteger(reviewerCount) || reviewerCount < 1 || (item.reference_kind === "human_adjudicated" && (reviewerCount < 2 || !item.reference_adjudicated))) throw new Error("人工裁决真值须记录至少两位复核者及已裁决事实");
  if ([item.crop_iou, item.transcription_cer, item.rubric_criterion_agreement].some((value) => value !== undefined && (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 1))) throw new Error("可选质量比率须在0—1之间");
  return item;
}
