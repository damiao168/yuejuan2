import type { PaperImportDraftQuestion } from "../../api/papers";

// 只有教师明确确认当前题时才补齐确认标记；保存草稿、整卷刷新和导入均不能替其他题作确认。
export function reviewConfirmationFields(draft: PaperImportDraftQuestion): string[] {
  const fields = ["question_no", "question_type", "score", "stem"];
  if (draft.options?.length) fields.push("options");
  if (draft.answer_key) fields.push("answer");
  if (draft.solution) fields.push("solution");
  if (draft.rubric && draft.question_type !== "fill_blank") fields.push("rubric");
  return fields;
}

export function isReviewQuestionConfirmed(draft: PaperImportDraftQuestion): boolean {
  return reviewConfirmationFields(draft).every((field) => draft.human_confirmed_fields?.includes(field));
}

export function reviewDraftIndex(drafts: PaperImportDraftQuestion[], draft: PaperImportDraftQuestion, fallbackIndex: number): number {
  if (draft.candidate_id) {
    const matches = drafts.flatMap((item, index) => item.candidate_id === draft.candidate_id ? [index] : []);
    return matches.length === 1 ? matches[0] : -1;
  }
  // 旧任务没有 candidate_id 时只能对应原位置；不能把有编号的新候选当成旧草稿。
  return drafts[fallbackIndex] && !drafts[fallbackIndex].candidate_id ? fallbackIndex : -1;
}

export function sameReviewValue(left: unknown, right: unknown): boolean {
  const ordered = (_key: string, value: unknown) => value && typeof value === "object" && !Array.isArray(value)
    ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b))) : value;
  return JSON.stringify(left, ordered) === JSON.stringify(right, ordered);
}

export function olderReviewTimestamp(incoming?: string, saved?: string): boolean {
  if (!incoming || !saved) return false;
  const left = Date.parse(incoming); const right = Date.parse(saved);
  if (!Number.isFinite(left) || !Number.isFinite(right)) return false;
  if (left !== right) return left < right;
  // 数据库时间可能精确到微秒；Date.parse 截到毫秒，不能把同一毫秒内的旧轮询当成新回执。
  const fraction = (value: string) => (/\.(\d+)(?:Z|[+-]\d{2}:\d{2})$/.exec(value)?.[1] ?? "").padEnd(9, "0").slice(0, 9);
  return fraction(incoming) < fraction(saved);
}

export function mergeDirtyReviewDrafts(current: PaperImportDraftQuestion[], baseline: PaperImportDraftQuestion[], incoming: PaperImportDraftQuestion[]): PaperImportDraftQuestion[] {
  return incoming.map((saved, index) => {
    const local = current[reviewDraftIndex(current, saved, index)];
    const previous = baseline[reviewDraftIndex(baseline, saved, index)];
    // 回读可更新未改动题和本轮已提交题；其他本地草稿以及请求期间的新修改必须保留。
    return local && previous && !sameReviewValue(local, previous) ? local : saved;
  });
}

export function remapReviewJsonFields(fields: Record<string, string>, previous: PaperImportDraftQuestion[], next: PaperImportDraftQuestion[]): Record<string, string> {
  return Object.fromEntries(Object.entries(fields).map(([key, value]) => {
    const match = /^(\d+):(.*)$/.exec(key);
    if (!match) return [key, value];
    const oldIndex = Number(match[1]);
    const draft = previous[oldIndex];
    const newIndex = draft ? reviewDraftIndex(next, draft, oldIndex) : -1;
    return [newIndex < 0 ? key : `${newIndex}:${match[2]}`, value];
  }));
}
