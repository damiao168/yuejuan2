import type { AiGrade } from "../../../api/review";
import { latestGrade } from "./gradingWorkbench.model";
import type { DraftFallbackSnapshot, DraftSaveStatus, WorkbenchContext } from "./gradingWorkbench.types";
import type { useMathWorkbenchEvidence } from "./hooks/useMathWorkbenchEvidence";
import { mathEvidenceBinding, mathSuggestionState, type MathStepSelection } from "./mathWorkbenchEvidence";
import { requiresExplicitSecondOpinion } from "./reviewContext";

type MathEvidenceState = ReturnType<typeof useMathWorkbenchEvidence>["state"];

export function mathRequestDisabledReason(canGrade: boolean, canEditDraft: boolean) {
  // 无评分权限时先解释身份限制，不能被当前任务只读的原因覆盖。
  if (!canGrade) return "当前身份无重新生成数学建议的权限；请按冻结细则人工评分，需要重新生成时请联系管理员。";
  if (!canEditDraft) return "当前任务只读；请先领取属于你的可编辑任务，再重新生成建议。";
  return undefined;
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

export function isImageWorkbenchGrade(grade: AiGrade | undefined): boolean {
  return grade?.raw_output?.schema_version === "grading-image-v3";
}

export function imageSuggestionState(grade: AiGrade, ctx: WorkbenchContext | null): { current: boolean; label: string } {
  if (!ctx || !isImageWorkbenchGrade(grade)) return { current: false, label: "缺少图像建议绑定" };
  const review = ctx.reviewContext;
  const metadata = record(review.ai_second_opinion?.metadata);
  const binding = record(metadata.input_binding);
  const raw = record(grade.raw_output);
  const observation = record(raw.observation);
  const panel = record(raw.panel_execution);
  if (Object.keys(panel).length > 0 && !Number.isFinite(panel.final_score)) return { current: false, label: "未形成汇总分，请人工评分" };
  const inputVersion = binding.input_version_id;
  const snapshot = review.question_snapshot.id;
  const members = Array.isArray(observation.members) ? observation.members.map(record) : [];
  // 图像协议以服务端核验的当前输入绑定为准，不借用旧版数学工件，也不能仅凭协议名放行历史建议。
  const bound = typeof inputVersion === "string" && inputVersion.length > 0
    && binding.input_mode === "image" && binding.input_protocol === "grading-image-v3"
    && typeof binding.subjective_run_id === "string" && binding.subjective_run_id.length > 0
    && typeof binding.crop_sha256 === "string" && /^[a-f0-9]{64}$/i.test(binding.crop_sha256)
    && metadata.id === grade.id && metadata.answer_version === inputVersion && grade.answer_version === inputVersion
    && binding.answer_segment_id === ctx.task.answer_segment_id && grade.answer_segment_id === ctx.task.answer_segment_id
    && binding.question_id === ctx.task.question_id && grade.question_id === ctx.task.question_id
    && binding.assessment_snapshot_id === snapshot && raw.assessment_snapshot_id === snapshot
    && raw.input_version_id === inputVersion && observation.input_version_id === inputVersion
    && observation.crop_sha256 === binding.crop_sha256
    && members.length === 1 && members[0].question_id === ctx.task.question_id
    && grade.rubric_version === review.frozen_rubric.version;
  if (!bound) return { current: false, label: "图像或评分版本已变化，请重新核对" };
  const valid = review.ai_second_opinion?.available === true && review.ai_second_opinion.score_prefill_allowed === true
    && !(review as typeof review & { ai_failure?: unknown }).ai_failure && !requiresExplicitSecondOpinion(review)
    && grade.status === "succeeded" && !grade.mock && grade.needs_human_review
    && ["teacher_review", "teacher_suggestion"].includes(grade.delivery_mode ?? "")
    && Number.isFinite(grade.suggested_score) && Number.isFinite(grade.max_score)
    && grade.max_score === review.question.score && grade.suggested_score >= 0 && grade.suggested_score <= grade.max_score;
  return valid ? { current: true, label: "当前建议 · 仍需教师确认" } : { current: false, label: "当前建议不可采纳，请人工评分" };
}

export function selectWorkbenchGrade(grades: AiGrade[], subjectCode: string, state: MathEvidenceState, dirty: boolean, ctx?: WorkbenchContext | null) {
  const image = ctx ? latestGrade(grades.filter((grade) => imageSuggestionState(grade, ctx).current)) : undefined;
  if (image) return image;
  if (subjectCode === "mathematics") {
    const current = latestGrade(grades.filter((grade) => mathSuggestionState(grade, state, dirty).current));
    if (current) return current;
  }
  return latestGrade(grades);
}

export function canAdoptWorkbenchGrade(grade: AiGrade | undefined, ctx: WorkbenchContext | null, requesting: boolean, subjectCode: string, state: MathEvidenceState, dirty: boolean): boolean {
  if (!grade || !ctx || ctx.reviewContext.ai_failure || grade.delivery_mode === "shadow_only" || requesting) return false;
  if (requiresExplicitSecondOpinion(ctx!.reviewContext)) return false;
  if (isImageWorkbenchGrade(grade)) return !dirty && imageSuggestionState(grade, ctx).current;
  return subjectCode !== "mathematics" || mathSuggestionState(grade, state, dirty).current;
}

export function visibleMathSelection(selection: MathStepSelection | null, state: MathEvidenceState, dirty: boolean): MathStepSelection | null {
  if (!selection || dirty || ["conflict", "loading", "unavailable"].includes(state.phase)) return null;
  if (!state.understanding || selection.binding !== mathEvidenceBinding(state.understanding)) return null;
  return selection;
}

interface ReviewDraftStateOptions {
  ctx: WorkbenchContext | null;
  currentUserId: string;
  canWork: boolean;
  hasSession: boolean;
  resolvingConflict: boolean;
  hasConflict: boolean;
  hydrated: boolean;
  saveStatus: DraftSaveStatus;
  actioning: string | null;
  snapshot: DraftFallbackSnapshot;
  lastSavedSnapshot: string;
}

export function reviewDraftState(options: Readonly<ReviewDraftStateOptions>) {
  const { ctx, snapshot } = options;
  const ownsSelectedTask = Boolean(ctx?.task.assigned_to && ctx.task.assigned_to === options.currentUserId);
  const canEditDraft = options.canWork && options.hasSession && ownsSelectedTask && !options.resolvingConflict
    && !options.hasConflict && Boolean(ctx && ["assigned", "in_progress", "returned"].includes(ctx.task.status));
  const unsyncedDraft = Boolean(options.hydrated && ownsSelectedTask && ctx?.task.status !== "submitted"
    && JSON.stringify(snapshot) !== options.lastSavedSnapshot);
  const canSubmit = canEditDraft && options.saveStatus !== "conflict" && !options.actioning && snapshot.draft.score !== null;
  return { ownsSelectedTask, canEditDraft, unsyncedDraft, canSubmit };
}
