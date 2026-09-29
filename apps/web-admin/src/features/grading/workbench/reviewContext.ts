import type { ReviewTaskContext } from "../../../api/review";
import type { Question, Rubric, RubricPoint } from "../../../api/papers";

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function asString(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}

function asNumber(value: unknown, fallback = 0): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

export function normalizeRubricPoint(value: unknown, index: number): RubricPoint {
  const point = asRecord(value);
  return {
    id: asString(point.id, asString(point.code, `criterion-${index + 1}`)),
    description: asString(point.description, asString(point.label, `评分点 ${index + 1}`)),
    score: asNumber(point.score, asNumber(point.max_score)),
    required: point.required === true
  };
}

export function frozenRubricFromContext(context: ReviewTaskContext): Rubric {
  // 阅卷只能使用任务冻结的细则，不能回查当前可编辑题库并替换历史评分依据。
  const source = context.frozen_rubric;
  return {
    id: asString(source.id, `snapshot-${context.question_snapshot.id}`),
    question_id: asString(source.question_id, context.task.question_id),
    version: asString(source.version, String(context.question_snapshot.snapshot_version)),
    status: asString(source.status, "frozen"),
    max_score: asNumber(source.max_score, context.question.score),
    points: Array.isArray(source.points) ? source.points.map(normalizeRubricPoint) : [],
    deductions: Array.isArray(source.deductions) ? source.deductions : [],
    examples: Array.isArray(source.examples) ? source.examples : []
  };
}

export function questionFromContext(context: ReviewTaskContext): Question {
  const source = context.question;
  return {
    id: source.id,
    tenant_id: context.task.tenant_id,
    exam_id: source.exam_id,
    question_no: source.question_no,
    question_type: source.question_type,
    score: source.score,
    stem: source.stem,
    options: [],
    knowledge_points: source.knowledge_points ?? [],
    sort_order: 0,
    status: "ready",
    rubric: frozenRubricFromContext(context)
  };
}

export function requiresExplicitSecondOpinion(context: ReviewTaskContext): boolean {
  // The immutable policy applies even before an AI suggestion exists.
  return (context.question_snapshot.risk_tier === "R3" && context.question_snapshot.scoring_policy_snapshot.mode === "HUMAN_PRIMARY")
    || (context.ai_second_opinion?.presentation === "explicit_second_opinion" && context.ai_second_opinion.score_prefill_allowed === false);
}

export function secondOpinionMetadata(context: ReviewTaskContext, revealed: boolean): Record<string, unknown> | null {
  if (!context.ai_second_opinion?.available) return null;
  if (requiresExplicitSecondOpinion(context) && !revealed) return null;
  return context.ai_second_opinion.metadata;
}

export interface SubjectToolDescriptor {
  group: "writing" | "math_science" | "chemistry" | "humanities" | "geo_bio" | "general";
  label: string;
  capabilities: string[];
}

export function subjectToolDescriptor(context: ReviewTaskContext): SubjectToolDescriptor {
  const subject = context.subject_tool_hints.subject_code;
  const evidence = new Set(context.subject_tool_hints.allowed_evidence_types);
  if (subject === "chinese" || subject === "english") {
    return { group: "writing", label: "全文与分项评价", capabilities: ["连续阅读", "段落定位", "Trait 评分"] };
  }
  if (subject === "mathematics" || subject === "physics") {
    return { group: "math_science", label: "公式与步骤核对", capabilities: ["公式放大", "步骤评分", ...(evidence.has("unit_value") ? ["单位校验"] : [])] };
  }
  if (subject === "chemistry") {
    return { group: "chemistry", label: "化学表达核对", capabilities: ["反应式证据", "条件与状态", "步骤评分"] };
  }
  if (subject === "history" || subject === "ethics_politics") {
    return { group: "humanities", label: "材料证据核对", capabilities: ["材料高亮", "概念证据", "关系证据"] };
  }
  if (subject === "geography" || subject === "biology") {
    return { group: "geo_bio", label: "图表与概念核对", capabilities: ["图表证据", "概念证据", "关系证据"] };
  }
  return { group: "general", label: "通用评分工具", capabilities: ["答题产物", "评分细则", "证据"] };
}
