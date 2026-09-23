import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import type { RubricPoint } from "../../../api/papers";
import type { AiGrade, ReviewDraft, ScoringRunItem } from "../../../api/review";
import type { StatusTone } from "../../../types";
import type {
  DraftFallbackSnapshot,
  ScoreDraft,
  TaskFilter,
  ViewerMode,
  WorkbenchContext
} from "./gradingWorkbench.types";

export const taskFilterOptions: { label: string; value: TaskFilter }[] = [
  { label: "全部状态", value: "all" },
  { label: "可处理", value: "active" },
  { label: "待分配", value: "pending" },
  { label: "已分配", value: "assigned" },
  { label: "处理中", value: "in_progress" },
  { label: "退回", value: "returned" },
  { label: "已提交", value: "submitted" }
];

export const sourceLabels: Record<string, string> = {
  ai_low_confidence: "系统评分把握不足",
  ocr_low_confidence: "手写内容识别不确定",
  subjective_default_review: "主观题需要教师确认",
  evidence_verification_failed: "未找到足够评分依据",
  double_mark_required: "需要第二位教师独立评分",
  score_anomaly: "评分结果与同类答案差异较大",
  manual_sample: "人工抽检",
  omr_ambiguous: "客观题识别存在歧义",
  rule_review_required: "规则评分需要确认",
  grading_failure: "自动评分失败"
};

export const sourceDescriptions: Record<string, string> = {
  ai_low_confidence: "系统无法可靠判断本题，请结合标准答案和采分点人工确认。",
  ocr_low_confidence: "识别结果可能与学生原始作答不一致，请优先核对答题图。",
  subjective_default_review: "本题按阅卷策略进入人工确认，请依据评分细则给分。",
  evidence_verification_failed: "系统建议缺少可核验的采分依据，请检查学生答案与评分细则。",
  double_mark_required: "本题需要独立完成第二次评分，避免受首次评分影响。",
  score_anomaly: "本题评分与相近答案差异较大，请复核最终得分。",
  manual_sample: "本题由抽样复核策略选中，用于检查自动评分质量。",
  omr_ambiguous: "涂卡结果无法唯一确定，请对照原始答题图确认选项。",
  rule_review_required: "规则未能自动确认结果，请人工判断答案是否满足得分条件。",
  grading_failure: "自动评分未能完成，请直接按评分细则人工处理。"
};

export const taskStatusLabels: Record<string, string> = {
  pending: "待分配",
  assigned: "已分配",
  in_progress: "处理中",
  returned: "退回",
  submitted: "已提交",
  completed: "已完成"
};

export const scoringRunStatusLabels: Record<string, string> = {
  queued: "等待处理",
  processing: "处理中",
  needs_review: "等待人工",
  failed: "处理失败",
  cancelling: "正在取消",
  cancelled: "已取消",
  completed: "已完成"
};

export const scoringItemStateLabels: Record<string, string> = {
  pending: "等待处理",
  processing: "处理中",
  review: "等待人工",
  confirmed: "已确认",
  failed: "失败",
  cancelling: "正在取消",
  cancelled: "已取消"
};

export const questionTypeLabels: Record<string, string> = {
  single_choice: "单选题",
  multiple_choice: "多选题",
  true_false: "判断题",
  fill_blank: "填空题",
  numeric: "数值题"
};

export const recognitionDecisionLabels: Record<string, string> = {
  selected: "识别成功",
  confirmed: "识别成功",
  blank: "未作答",
  multiple: "多选冲突",
  ambiguous: "结果不明确",
  parse_failed: "解析失败"
};

export const recognitionSourceLabels: Record<string, string> = {
  omr: "填涂识别",
  ocr: "文字识别",
  ocr_text: "文字识别",
  imported: "导入",
  imported_answer: "导入",
  manual: "人工录入",
  manual_entry: "人工录入"
};

export const riskFlagLabels: Record<string, string> = {
  low_confidence: "置信度低",
  ocr_low_confidence: "识别把握不足",
  ocr_text_empty_review_required: "未识别到作答内容",
  ambiguous_answer: "答案表述不明确",
  insufficient_evidence: "证据不足",
  possible_off_topic: "疑似离题",
  score_needs_review: "得分需人工复核",
  schema_repaired: "结果经系统修正",
  prompt_injection_suspected: "疑似异常输入",
  human_review_required: "需人工复核",
  needs_human_review: "需人工复核",
  format_mismatch: "格式不符",
  partial_match: "部分匹配"
};

export const evidenceTypeLabels: Record<string, string> = {
  answer_text: "作答文本",
  answer_segment_answer: "作答内容",
  text_match: "文本匹配",
  rule: "规则命中",
  keyword: "关键词",
  mock: "演示数据"
};

export const reasonLabels: Record<string, string> = {
  rule_not_auto_confirmed: "规则未能自动确认",
  rule_review_required: "规则评分需人工确认",
  grading_failure: "评分处理失败",
  scoring_run_cancelled: "本次评分已取消",
  dead_letter: "多次失败",
  retryable_error: "处理失败，稍后会重试",
  terminal_error: "多次失败，已停止重试",
  omr_failed: "填涂识别失败",
  ocr_failed_unhandled: "文字识别失败",
  storage_timeout: "答题图像读取超时",
  source_lease_failed: "答题图像暂不可用",
  lease_expired: "任务超时已放回",
  segment_not_found: "答题图像缺失",
  image_quality_processing_failed: "图像质量检查失败"
};

export const commentPresets = ["答案完整，逻辑清晰", "关键步骤缺失", "结论正确但过程不充分", "请补充必要说明"];

export function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("接口请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function taskTone(status: string): StatusTone {
  if (status === "submitted" || status === "completed") return "success";
  if (status === "returned") return "warning";
  if (status === "pending") return "neutral";
  return "processing";
}

export function confidenceTone(value: number): StatusTone {
  if (value >= 0.85) return "success";
  if (value >= 0.65) return "warning";
  return "danger";
}

export function formatAnswer(value: unknown): string {
  if (value === null || value === undefined || value === "") return "-";
  if (Array.isArray(value)) return value.map(formatAnswer).filter((item) => item !== "-").join("、") || "-";
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    if ("answer" in record) return formatAnswer(record.answer);
    if ("answers" in record) return formatAnswer(record.answers);
    return Object.values(record).map(formatAnswer).filter((item) => item !== "-").join("、") || "-";
  }
  if (typeof value === "boolean") return value ? "正确" : "错误";
  return String(value);
}

export function resultState(item: ScoringRunItem): { label: string; tone: StatusTone } {
  if (item.state === "confirmed" && item.grade_source === "rule_confirmed") return { label: "自动确认", tone: "success" };
  if (item.state === "confirmed") return { label: "人工完成", tone: "success" };
  if (item.state === "review") return { label: "待人工复核", tone: "warning" };
  if (item.state === "failed") return { label: "处理失败", tone: "danger" };
  return { label: scoringItemStateLabels[item.state] ?? "处理中", tone: "processing" };
}

export function gradingConclusion(item: ScoringRunItem) {
  if (typeof item.score !== "number" || typeof item.max_score !== "number") return "尚未判分";
  if (item.score === item.max_score) return "匹配";
  if (item.score === 0) return "不匹配";
  return "部分得分";
}

export function latestGrade(grades: AiGrade[]) {
  return [...grades]
    .filter((grade) => grade.delivery_mode !== "shadow_only")
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0];
}

export function pointLabel(point: RubricPoint) {
  return `${point.description || "未命名采分点"} (${point.score} 分)`;
}

export function isInputTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false;
  return ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName) || target.isContentEditable;
}

export function createInitialDraft(ctx: WorkbenchContext | null): ScoreDraft {
  const ocrText = ctx?.ocrText || ctx?.ocrResults.map((item) => item.text).filter(Boolean).join("\n") || "";
  const selections: Record<string, number> = {};
  for (const point of ctx?.question?.rubric?.points ?? []) selections[point.id] = 0;
  return {
    score: null,
    comments: "",
    privateNote: "",
    studentFeedback: "",
    reason: "",
    disputeReason: "",
    rubricSelections: selections,
    answerText: ocrText
  };
}

export function createDraftSnapshot(draft: ScoreDraft, mode: ViewerMode, scale: number, rotation: number, offset: { x: number; y: number }, fit: boolean): DraftFallbackSnapshot {
  return { draft, viewer: { mode, scale, rotation, offset, fit } };
}

export interface ServerDraftSnapshot {
  snapshot: DraftFallbackSnapshot;
  revision: number;
  updatedAt: number;
}

/** Converts the authoritative API draft into the same shape as the in-memory fallback. */
export function serverDraftSnapshot(initial: ScoreDraft, draft: ReviewDraft | null, canViewOriginalImage: boolean): ServerDraftSnapshot {
  if (!draft) {
    return {
      snapshot: createDraftSnapshot(initial, "segment", 1, 0, { x: 0, y: 0 }, true),
      revision: 0,
      updatedAt: 0
    };
  }
  const viewer = draft.viewer_state;
  const savedMode = ["segment", "original", "ocr"].includes(String(viewer.mode)) ? viewer.mode as ViewerMode : "segment";
  const mode = savedMode === "original" && !canViewOriginalImage ? "segment" : savedMode;
  const scale = Number(viewer.scale ?? 1);
  const rotation = Number(viewer.rotation ?? 0);
  const savedOffset = viewer.offset as { x?: number; y?: number } | undefined;
  return {
    snapshot: createDraftSnapshot(
      {
        ...initial,
        score: draft.score ?? null,
        comments: draft.comments,
        privateNote: draft.private_note,
        studentFeedback: draft.student_feedback,
        rubricSelections: Object.fromEntries(draft.rubric_selections.map((item) => [item.point_id, item.score]))
      },
      mode,
      Number.isFinite(scale) ? scale : 1,
      Number.isFinite(rotation) ? rotation : 0,
      { x: Number(savedOffset?.x ?? 0), y: Number(savedOffset?.y ?? 0) },
      viewer.fit !== false
    ),
    revision: draft.revision,
    updatedAt: Date.parse(draft.updated_at) || 0
  };
}

export function fallbackSnapshot(value: unknown, initial: ScoreDraft): DraftFallbackSnapshot | null {
  if (!value || typeof value !== "object") return null;
  const root = value as { draft?: unknown; viewer?: unknown };
  if (!root.draft || typeof root.draft !== "object" || !root.viewer || typeof root.viewer !== "object") return null;
  const cachedDraft = root.draft as Partial<ScoreDraft>;
  const cachedViewer = root.viewer as { mode?: unknown; scale?: unknown; rotation?: unknown; offset?: unknown; fit?: unknown };
  const selections = cachedDraft.rubricSelections && typeof cachedDraft.rubricSelections === "object" && !Array.isArray(cachedDraft.rubricSelections)
    ? Object.fromEntries(Object.entries(cachedDraft.rubricSelections).flatMap(([key, value]) => typeof value === "number" && Number.isFinite(value) ? [[key, value]] : []))
    : initial.rubricSelections;
  const rawOffset = cachedViewer.offset && typeof cachedViewer.offset === "object" ? cachedViewer.offset as { x?: unknown; y?: unknown } : {};
  const score = typeof cachedDraft.score === "number" && Number.isFinite(cachedDraft.score) ? cachedDraft.score : null;
  const mode = cachedViewer.mode === "original" || cachedViewer.mode === "ocr" || cachedViewer.mode === "segment" ? cachedViewer.mode : "segment";
  const scale = typeof cachedViewer.scale === "number" && Number.isFinite(cachedViewer.scale) ? Math.min(4, Math.max(0.25, cachedViewer.scale)) : 1;
  const rotation = typeof cachedViewer.rotation === "number" && Number.isFinite(cachedViewer.rotation) ? cachedViewer.rotation : 0;
  const fit = cachedViewer.fit !== false;
  return {
    draft: {
      ...initial,
      score,
      comments: typeof cachedDraft.comments === "string" ? cachedDraft.comments : initial.comments,
      privateNote: typeof cachedDraft.privateNote === "string" ? cachedDraft.privateNote : initial.privateNote,
      studentFeedback: typeof cachedDraft.studentFeedback === "string" ? cachedDraft.studentFeedback : initial.studentFeedback,
      reason: typeof cachedDraft.reason === "string" ? cachedDraft.reason : initial.reason,
      disputeReason: typeof cachedDraft.disputeReason === "string" ? cachedDraft.disputeReason : initial.disputeReason,
      rubricSelections: selections,
      answerText: typeof cachedDraft.answerText === "string" ? cachedDraft.answerText : initial.answerText
    },
    viewer: {
      mode,
      scale,
      rotation,
      fit,
      offset: {
        x: typeof rawOffset.x === "number" && Number.isFinite(rawOffset.x) ? rawOffset.x : 0,
        y: typeof rawOffset.y === "number" && Number.isFinite(rawOffset.y) ? rawOffset.y : 0
      }
    }
  };
}
