import type { MathCriterionDecision, MathSolutionStep } from "@edugrade/sdk";
import type { MathRubricScoreResponse, MathUnderstandingResponse } from "../../../api/mathUnderstanding";
import type { AiGrade } from "../../../api/review";
import { selectEffectiveMathArtifact } from "./mathEffectiveEvidence";

export type MathEvidencePhase = "loading" | "ready" | "pending" | "unavailable" | "conflict";
export interface MathWorkbenchEvidence {
  segmentId: string;
  phase: MathEvidencePhase;
  understanding: MathUnderstandingResponse | null;
  score: MathRubricScoreResponse | null;
  message: string;
}
export interface MathStepSelection {
  segmentId: string;
  binding: string;
  stepId: string;
  label: string;
  bbox: { x: number; y: number; width: number; height: number };
}

export function mathEvidenceBinding(response: MathUnderstandingResponse) {
  return `${response.artifact.id}:${response.artifact.version}:${response.artifact.correction_revision ?? 0}:${response.correction_revision ?? 0}`;
}

// Both reads are authoritative server projections. A raced pair cannot become
// a current suggestion, even if the total happens to be the same.
export function mathScoreMatchesUnderstanding(score: MathRubricScoreResponse, response: MathUnderstandingResponse) {
  return score.schema_version === "math-rubric-score-v1" && score.scope === "teacher_suggestion_only"
    && score.artifact_id === response.artifact.id && score.artifact_version === response.artifact.version
    && score.exam_question_snapshot_id === response.artifact.exam_question_snapshot_id
    && score.correction_revision === (response.correction_revision ?? 0)
    && score.verified_correction_revision === (response.artifact.correction_revision ?? 0);
}

export function mathSuggestionState(grade: AiGrade, evidence: MathWorkbenchEvidence, dirty = false): { current: boolean; label: string } {
  if (grade.answer_segment_id !== evidence.segmentId) return { current: false, label: "答题区不匹配" };
  if (!grade.math_artifact_id || !grade.math_scoring_version || grade.math_artifact_version === undefined || grade.math_correction_revision === undefined) {
    return { current: false, label: "缺少数学版本绑定" };
  }
  const response = evidence.understanding;
  // 建议绑定的是已吸收校正与当前未吸收校正之和；任一层变化都会让旧建议失效。
  if (response && (grade.math_artifact_id !== response.artifact.id || grade.math_artifact_version !== response.artifact.version
    || grade.math_correction_revision !== (response.artifact.correction_revision ?? 0) + (response.correction_revision ?? 0))) {
    return { current: false, label: "已过期 · 数学证据已更新" };
  }
  if (evidence.phase === "conflict") return { current: false, label: "已失效 · 图片或证据已变化" };
  if (dirty) return { current: false, label: "校正未保存" };
  if (evidence.phase !== "ready" || !response || !evidence.score) return { current: false, label: "尚未确认当前证据" };
  const score = evidence.score;
  if (!mathScoreMatchesUnderstanding(score, response) || grade.math_scoring_version !== score.schema_version || grade.rubric_version !== score.rubric_version) {
    return { current: false, label: "评分版本不匹配" };
  }
  if (grade.mock || grade.status !== "succeeded" || grade.delivery_mode !== "teacher_suggestion" || !grade.needs_human_review
    || !Number.isFinite(grade.suggested_score) || score.suggested_score === null || score.unresolved_score !== 0
    || grade.suggested_score !== score.suggested_score || grade.max_score !== score.max_score) {
    return { current: false, label: "不能作为当前数学建议" };
  }
  return { current: true, label: "当前 · 仍需教师确认" };
}

function validBBox(value: unknown): value is MathStepSelection["bbox"] {
  // 框坐标相对题块归一化，边界只容忍浮点运算误差，不能把页面像素坐标直接传入。
  if (!value || typeof value !== "object") return false;
  const { x, y, width, height } = value as MathStepSelection["bbox"];
  return [x, y, width, height].every(Number.isFinite) && x >= 0 && y >= 0 && width > 0 && height > 0
    && x + width <= 1.000001 && y + height <= 1.000001;
}

export function selectMathStep(response: MathUnderstandingResponse, stepId: string): MathStepSelection | null {
  const artifact = selectEffectiveMathArtifact(response);
  const step = artifact.solution_graph.steps.find((item) => item.id === stepId);
  if (!step) return null;
  let bbox = validBBox(step.bbox) ? step.bbox : null;
  if (!bbox) {
    const boxes = artifact.blocks.filter((block) => step.block_ids.includes(String(block.id)) && block.status !== "crossed_out")
      .map((block) => block.bbox).filter(validBBox);
    if (!boxes.length) return null;
    const x = Math.min(...boxes.map((box) => box.x)), y = Math.min(...boxes.map((box) => box.y));
    bbox = { x, y, width: Math.max(...boxes.map((box) => box.x + box.width)) - x, height: Math.max(...boxes.map((box) => box.y + box.height)) - y };
  }
  return { segmentId: response.artifact.answer_segment_id, binding: mathEvidenceBinding(response), stepId, label: step.normalized_text || stepId, bbox };
}

export function mathDecisionSteps(decision: MathCriterionDecision, score: MathRubricScoreResponse, response: MathUnderstandingResponse): MathSolutionStep[] {
  const artifact = selectEffectiveMathArtifact(response);
  const sourceIds = new Set(score.rubric_evidence.filter((item) => decision.evidence_ids.includes(item.id)).flatMap((item) => item.source_artifact_ids));
  const checks = artifact.verifications?.filter((item) => decision.verification_ids.includes(item.id)) ?? [];
  for (const check of checks) {
    if (check.step_id) sourceIds.add(check.step_id);
    if (check.formula_id) sourceIds.add(check.formula_id);
  }
  return artifact.solution_graph.steps.filter((step) => sourceIds.has(step.id) || step.block_ids.some((id) => sourceIds.has(id))
    || step.formula_ids?.some((id) => sourceIds.has(id)));
}
