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
    version: asString(source.version),
    status: asString(source.status, "frozen"),
    max_score: asNumber(source.max_score, context.question.score),
    points: Array.isArray(source.points) ? source.points.map((point, index) => normalizeRubricPoint(point, index)) : [],
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
    options: Array.isArray(source.options) ? source.options.filter((option): option is string => typeof option === "string") : [],
    answer_key: source.answer_key as Question["answer_key"],
    solution: source.solution as Question["solution"],
    knowledge_points: source.knowledge_points ?? [],
    sort_order: 0,
    status: "ready",
    rubric: frozenRubricFromContext(context)
  };
}

export function requiresExplicitSecondOpinion(context: ReviewTaskContext): boolean {
  // 即使尚无 AI 建议，不可变策略仍然生效。
  return (context.question_snapshot.risk_tier === "R3" && context.question_snapshot.scoring_policy_snapshot.mode === "HUMAN_PRIMARY")
    || (context.ai_second_opinion?.presentation === "explicit_second_opinion" && context.ai_second_opinion.score_prefill_allowed === false);
}

export function secondOpinionMetadata(context: ReviewTaskContext, revealed: boolean): Record<string, unknown> | null {
  if (context.ai_failure) return null;
  if (!context.ai_second_opinion?.available) return null;
  if (requiresExplicitSecondOpinion(context) && !revealed) return null;
  return context.ai_second_opinion.metadata;
}

export function isChoiceReview(context: ReviewTaskContext): boolean {
  const type = context.question?.question_type;
  // 题型先于学科决定工具；旧任务缺少题型时才使用冻结题型原型兼容。
  return type ? ["single_choice", "multiple_choice", "true_false"].includes(type)
    : context.subject_tool_hints?.archetype_code === "selected_response";
}

const omrConfirmationReasons: Record<string, string> = {
  manual_only_profile: "当前填涂识别只提供参考结果，需要教师核对后确认分数。",
  template_difference_calibration_unapproved: "当前填涂识别尚未通过自动确认校准，需要教师核对。",
  template_difference_calibration_scope_mismatch: "当前校准不覆盖本题，需要教师核对。",
  template_difference_calibration_revoked: "当前填涂识别的校准已撤销，需要教师核对。",
  template_not_locked: "答题卡模板尚未锁定，当前识别结果需要教师核对。",
  template_not_found: "缺少自动确认所需的答题卡模板，请核对答题图。",
  template_profile_unverified: "答题卡识别配置尚未核验，需要教师核对。",
  template_reference_unverified: "答题卡参考页尚未核验，需要教师核对。",
  template_reference_mismatch: "答题卡参考页与当前识别配置不一致，需要教师核对。",
  template_content_hash_mismatch: "答题卡模板已变化，当前结果需要教师核对。",
  template_reference_region_missing: "缺少本题的空白参考区域，需要教师核对。",
  unsupported_omr_profile: "当前填涂识别配置不支持自动确认，需要教师核对。"
};

export function choiceReviewReason(context: ReviewTaskContext): string {
  // 来源只记录入队时的原因；核对当前候选和自动确认门禁，不能把历史来源继续解释成当前歧义。
  const taskReason = (context.task as ReviewTaskContext["task"] & { reason_code?: string })?.reason_code;
  if (taskReason === "omr_manual_only_profile" || taskReason === "manual_only") return omrConfirmationReasons.manual_only_profile;
  if (taskReason === "omr_set_quality_insufficient") return "多选题选项集合的识别依据不足，请对照答题图核对全部所选项。";
  if (taskReason === "omr_possible_erasure") return "检测到疑似擦改，请核对最终填涂。";
  if (taskReason === "omr_unreadable") return "填涂区域不清晰，请查看答题图。";
  if (taskReason === "omr_uncertain_mark") return "填涂较浅，暂不能确定所选项，请查看答题图。";
  if (taskReason === "omr_multiple_marks" || taskReason === "omr_multiple") return "检测到的填涂数量与本题要求不符，请核对答题图。";
  if (taskReason?.startsWith("omr_") && omrConfirmationReasons[taskReason.slice(4)]) return omrConfirmationReasons[taskReason.slice(4)];
  const current = context.ai_candidates?.find((candidate) => candidate.is_current && candidate.source === "omr");
  const decision = current?.decision || context.automation_result?.decision;
  if (decision === "ambiguous") return "填涂结果不明确，请对照答题图确认所选项。";
  if (decision === "multiple") return "检测到的填涂数量与本题要求不符，请核对答题图。";
  if (decision === "blank") return "未检测到有效填涂，请核对学生是否作答。";
  if (decision === "parse_failed") return "填涂结果解析失败，请核对答题图。";
  const evidence = current?.evidence ?? {};
  const reason = typeof evidence.auto_confirm_reason === "string" ? evidence.auto_confirm_reason : "";
  if (evidence.auto_confirm_eligible === false || reason === "manual_only_profile") {
    return omrConfirmationReasons[reason] ?? "当前识别结果尚不具备自动确认条件，需要教师核对。";
  }
  if (decision === "selected" || decision === "confirmed") {
    return "当前已识别出选项，请对照答题图与标准答案核对；识别置信度不等于最终得分。";
  }
  return "选择题待确认，请对照答题图核对选项与得分。";
}

export interface SubjectToolDescriptor {
  group: "writing" | "math_science" | "chemistry" | "humanities" | "geo_bio" | "general";
  label: string;
  capabilities: string[];
}

export function subjectToolDescriptor(context: ReviewTaskContext): SubjectToolDescriptor {
  if (isChoiceReview(context)) return { group: "general", label: "选项核对", capabilities: [] };
  const subject = context.subject_tool_hints.subject_code;
  const evidence = new Set(context.subject_tool_hints.allowed_evidence_types);
  if (subject === "chinese" || subject === "english") {
    return { group: "writing", label: "全文与分项评价", capabilities: ["连续阅读", "段落定位", "分项评分"] };
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
