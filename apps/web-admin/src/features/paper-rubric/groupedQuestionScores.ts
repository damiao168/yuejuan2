import type { Question } from "../../api/papers";

export const uniformScoreTypes = [
  { type: "single_choice", label: "单选题" },
  { type: "multiple_choice", label: "多选题" },
  { type: "fill_blank", label: "填空题" }
] as const;

export interface QuestionScoreGroup {
  type: string;
  label: string;
  questions: Question[];
  editableQuestions: Question[];
  uniformScore: number | null;
  total: number;
  lockedCount: number;
}

export function groupedQuestionScores(questions: Question[]): QuestionScoreGroup[] {
  return uniformScoreTypes.flatMap(({ type, label }) => {
    const items = questions.filter((question) => question.question_type === type);
    if (!items.length) return [];
    // 填空题的历史锁定细则仍可保留答案编辑，但批量调分不能改变它的总分边界。
    const editable = items.filter((question) => question.rubric?.status !== "locked");
    const firstScore = editable[0]?.score;
    const uniformScore = firstScore !== undefined && firstScore > 0 && editable.every((question) => question.score === firstScore) ? firstScore : null;
    return [{ type, label, questions: items, editableQuestions: editable, uniformScore,
      total: items.reduce((sum, question) => sum + question.score, 0), lockedCount: items.length - editable.length }];
  });
}

export function scoreGroupSnapshot(questions: Question[]): string {
  return JSON.stringify(questions.map((question) => [question.id, question.question_type, question.score, question.rubric?.status])
    .sort((first, second) => String(first[0]).localeCompare(String(second[0]))));
}

export function uniformScorePreview(group: QuestionScoreGroup, score: number | null, questions: Question[]) {
  if (score === null || !Number.isFinite(score) || score <= 0) return null;
  const changed = group.editableQuestions.filter((question) => question.score !== score);
  const difference = changed.reduce((sum, question) => sum + score - question.score, 0);
  const round = (value: number) => Number(value.toFixed(4));
  return { changed, difference: round(difference), groupTotal: round(group.total + difference),
    examTotal: round(questions.reduce((sum, question) => sum + question.score, 0) + difference) };
}
