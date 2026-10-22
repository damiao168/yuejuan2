import type { PaperImportDraftQuestion, PaperImportJob } from "../../api/papers";
import { importAnswerShapeError, resolveImportAnswerValue } from "./answerShape";
import { isReviewQuestionConfirmed, reviewConfirmationFields, reviewDraftIndex } from "./reviewConfirmation";
import { rubricPointIdentityError } from "../paper-rubric/rubricPointIdentity";

export function pointTotal(points: NonNullable<PaperImportDraftQuestion["rubric"]>["points"]) {
  return points.reduce((sum, point) => sum + (Number(point.score) || 0), 0);
}

export function reviewRubricHasScoreMismatch(draft: PaperImportDraftQuestion) {
  if (draft.question_type === "fill_blank") return false;
  return Boolean(draft.rubric
    && (Math.abs(draft.rubric.max_score - draft.score) > 0.0001
      || Math.abs(pointTotal(draft.rubric.points) - draft.score) > 0.0001));
}

export function reviewJobKey(job?: PaperImportJob) {
  return job ? `${job.exam_id}:${job.id}:${job.generation}:${job.source_revision ?? ""}` : null;
}

export function reviewJsonKeyMatches(key: string, index: number, draft: PaperImportDraftQuestion) {
  return key.startsWith(`${index}:`) || Boolean(draft.candidate_id && key.startsWith(`${draft.candidate_id}:`));
}

export function confirmedImportDrafts(drafts: PaperImportDraftQuestion[], originalDrafts: PaperImportDraftQuestion[], confirmedIndex?: number) {
  return drafts.map((draft, index) => {
    const original = originalDrafts.find((item) => item.candidate_id && item.candidate_id === draft.candidate_id) ?? originalDrafts[index];
    // 填空题按答案评分；不能把解析建议误存成已确认的主观题评分细则。
    const answerKeyOnly = draft.question_type === "fill_blank";
    const fields = new Set(draft.human_confirmed_fields ?? []);
    if (answerKeyOnly) fields.delete("rubric");
    if (index === confirmedIndex) for (const field of reviewConfirmationFields(draft)) fields.add(field);
    return {
      ...draft,
      answer_key: draft.answer_key ? {
        ...draft.answer_key,
        standard_answer: resolveImportAnswerValue(draft.answer_key.standard_answer, original?.answer_key?.standard_answer)
      } : undefined,
      rubric_candidate_id: answerKeyOnly ? undefined : draft.rubric_candidate_id,
      rubric: answerKeyOnly ? undefined : draft.rubric,
      human_confirmed_fields: [...fields]
    };
  });
}

export type ReviewValidationNotice = Readonly<{ level: "error" | "warning"; text: string }>;
export type ReviewSubmission = Readonly<{
  local: PaperImportDraftQuestion[];
  selected?: PaperImportDraftQuestion;
  texts: Record<string, string>;
  errors: Record<string, string>;
  payload: PaperImportDraftQuestion[];
  receiptBaseline: PaperImportDraftQuestion[];
  baseline: PaperImportDraftQuestion[];
  expectedUpdatedAt: string;
}>;
export type ReviewReceiptDecision = { kind: "accepted"; job: PaperImportJob } | { kind: "refresh" } | { kind: "ignored" };

export function reviewValidationNotice(
  local: PaperImportDraftQuestion[], authority: PaperImportDraftQuestion[], selected: PaperImportDraftQuestion | undefined,
  confirmedIndex: number | undefined, errors: Record<string, string>, apply: boolean
): ReviewValidationNotice | undefined {
  const checking = selected ? [selected] : local;
  // 本题确认只检查本题；整卷保存检查全卷，遇到首个错误就停止。
  if (selected && (!Number.isFinite(selected.score) || selected.score <= 0)) return { level: "error", text: `第${selected.question_no}题分值必须大于 0，不能确认` };
  const invalidRubric = checking.find(reviewRubricHasScoreMismatch);
  if (invalidRubric) return { level: "error", text: `第${invalidRubric.question_no}题评分细则分值不一致，不能确认` };
  const invalidIdentity = checking.find(draft => draft.question_type !== "fill_blank" && draft.rubric && rubricPointIdentityError(draft.rubric.points));
  if (invalidIdentity) return { level: "error", text: `第${invalidIdentity.question_no}题${rubricPointIdentityError(invalidIdentity.rubric!.points)}` };
  if (Object.keys(errors).some(key => !selected || reviewJsonKeyMatches(key, confirmedIndex!, selected))) return { level: "error", text: "高级评分数据包含无效 JSON，请先修正" };
  const invalidAnswer = checking.find(draft => importAnswerShapeError(draft.answer_key?.standard_answer,
    authority[reviewDraftIndex(authority, draft, local.indexOf(draft))]?.answer_key?.standard_answer));
  if (invalidAnswer) return { level: "error", text: `第${invalidAnswer.question_no}题标准答案格式不正确，不能保存` };
  if (!apply) return undefined;
  const unconfirmed = local.filter(draft => !isReviewQuestionConfirmed(draft));
  if (unconfirmed.length) return { level: "warning", text: `还有 ${unconfirmed.length} 道题未确认，请逐题核对并点击“确认并保存本题”后导入。` };
  return undefined;
}

export function remainingReviewJson(values: Record<string, string>, submitted: Record<string, string>, selected: PaperImportDraftQuestion | undefined, currentIndex: number) {
  return Object.fromEntries(Object.entries(values)
    .filter(([key, value]) => selected && !reviewJsonKeyMatches(key, currentIndex, selected) || value !== submitted[key]));
}

