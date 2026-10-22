import type { Exam } from "../../api/exams";
import type { PaperImportJob, Question, RubricEvidenceRequirement } from "../../api/papers";
import { questionTypeOptions } from "../../constants/examCatalog";
import { questionAnswerText } from "./hooks/useQuestionEditor";
import { formulaEvidenceEnabled, labelFrom } from "./paperPresentation";

export type QuestionFilter = "all" | "answer" | "rubric";
export type QuestionIssueTarget = "score" | "answer" | "rubric";
export type PaperDraftSection = "question" | "rubric" | "scoring";
const toleranceQuestionTypes = new Set(["numeric", "formula", "calculation"]);
export const fixedScoringQuestionTypes = new Set(["single_choice", "true_false", "multiple_choice", "fill_blank", "numeric"]);

// 数组和对象沿用编辑表单的 JSON 文本；这里只负责可读展示，完整可用性仍由服务端检查。
export function standardAnswerText(question: Question | null | undefined): string {
  return questionAnswerText(question?.answer_key?.standard_answer).trim();
}

function answerAvailabilityText(value: unknown): string {
  if (Array.isArray(value)) return value.map(answerAvailabilityText).join(",");
  return questionAnswerText(value);
}

// 判空沿用数组串联的旧语义：JSON 展示里的 [] 不能让空答案变成可发布答案。
export function hasStandardAnswer(question: Question | null | undefined) {
  return Boolean(answerAvailabilityText(question?.answer_key?.standard_answer).trim());
}

export function questionNeedsAnswer(question: Question) {
  return !hasStandardAnswer(question);
}

export function questionNeedsRubric(question: Question) {
  return !fixedScoringQuestionTypes.has(question.question_type) && question.rubric?.status !== "locked";
}

export function questionAttention(question: Question): "answer" | "rubric" | null {
  if (questionNeedsAnswer(question)) return "answer";
  if (questionNeedsRubric(question)) return "rubric";
  return null;
}

// 结构尚未采用时，未匹配且没有内容的预设题号只是占位，不计入待补答案和待锁定细则。
export function isUnmatchedPlaceholder(question: Question, job: PaperImportJob | undefined, structureConfirmed: boolean | undefined) {
  if (structureConfirmed || job?.status !== "review_required") return false;
  const recognized = job.questions.some((candidate) => candidate.question_no === question.question_no);
  return question.score_source === "preset" && !recognized && !question.stem?.trim() && !question.options?.length
    && !question.answer_key && !question.solution && !question.rubric;
}

export function questionSummary(questions: Question[], job: PaperImportJob | undefined, structureConfirmed: boolean | undefined) {
  const configured = questions.filter((question) => !isUnmatchedPlaceholder(question, job, structureConfirmed));
  return {
    pendingScoreCount: questions.filter((question) => question.score <= 0).length,
    unmatchedPlaceholderCount: questions.length - configured.length,
    needsAnswerCount: configured.filter(questionNeedsAnswer).length,
    needsRubricCount: configured.filter(questionNeedsRubric).length,
    totalScore: questions.reduce((total, question) => total + question.score, 0)
  };
}

export function filterPaperQuestions(questions: Question[], filter: QuestionFilter, search: string) {
  const query = search.trim().toLocaleLowerCase();
  return questions.filter((question) => {
    if (filter === "answer" && !questionNeedsAnswer(question)) return false;
    if (filter === "rubric" && !questionNeedsRubric(question)) return false;
    return !query || `${question.question_no} ${question.stem ?? ""}`.toLocaleLowerCase().includes(query);
  });
}

export function fixedScoringType(question: Question | null) {
  if (question && fixedScoringQuestionTypes.has(question.question_type)) return question.question_type;
  return "";
}

export function questionEditingAccess(canManage: boolean, watchedType: string | undefined, question: Question | null, exam: Exam | undefined) {
  const effectiveType = watchedType ?? question?.question_type;
  const isFillBlank = effectiveType === "fill_blank";
  // 填空题使用固定判分规则；历史主观细则的锁定状态不能禁用填空题的答案编辑。
  const selectedLocked = !isFillBlank && question?.rubric?.status === "locked";
  return {
    isFillBlank, selectedLocked, questionDisabled: !canManage || selectedLocked,
    showTolerance: toleranceQuestionTypes.has(watchedType ?? ""),
    showFormulaEvidence: formulaEvidenceEnabled(exam?.subject, question?.question_type),
    objectiveRuleType: fixedScoringType(question),
    editorUsesFixedScoring: fixedScoringQuestionTypes.has(effectiveType ?? "")
  };
}

export function otherPaperDraftsDirty(section: PaperDraftSection, formDirty: boolean, rubricDirty: boolean, scoringDirty: boolean) {
  if (section === "question") return rubricDirty || scoringDirty;
  if (section === "rubric") return formDirty || scoringDirty;
  return formDirty || rubricDirty;
}

export function questionScoreLabel(score: number) {
  return score > 0 ? `${score} 分` : "分值待确认";
}

export function questionStateLabel(question: Question, placeholder: boolean) {
  if (placeholder) return "预设占位，等待结构核对";
  const pending: string[] = [];
  if (questionNeedsAnswer(question)) pending.push("待补答案");
  if (questionNeedsRubric(question)) pending.push(question.rubric?.points.length ? rubricProgressLabel(question) : "待确认细则");
  if (pending.length) return pending.join("、");
  return fixedScoringQuestionTypes.has(question.question_type) ? "答案已填" : "细则已确认";
}

// 保存过的有效采分点才算草稿；表单默认生成的空内容不能显示成已经保存。
export function rubricProgressLabel(question: Question | null, unsaved = false) {
  if (question?.rubric?.status === "locked") return "已确认并锁定";
  if (unsaved) return "有未保存修改";
  if (!question?.rubric?.points.length) return "尚未配置细则";
  if (question.rubric.status === "pending_review") return "已提交审批，待确认";
  if (question.rubric.status === "approved") return "审批已通过，待确认";
  return "草稿已保存，待确认";
}

export function questionIndexLabel(question: Question, placeholder: boolean) {
  const typeLabel = labelFrom(questionTypeOptions, question.question_type);
  return `第 ${question.question_no} 题 · ${typeLabel} · ${questionScoreLabel(question.score)} · ${questionStateLabel(question, placeholder)}`;
}

export function questionEditorDescription(question: Question | null, locked: boolean, unsaved: boolean) {
  if (locked) return "评分细则已确认并锁定";
  if (unsaved) return "有未保存的修改";
  if (!question) return "填写题目与答案后保存";
  return `${labelFrom(questionTypeOptions, question.question_type)} · ${questionScoreLabel(question.score)}`;
}

export function rubricSaveLabel(status: string) {
  if (status === "locked") return "确认并锁定细则";
  if (status === "pending_review") return "提交审批";
  return "保存草稿";
}

export function questionEditorToggleLabel(open: boolean, questionCount: number) {
  if (open) return "收起逐题校对";
  return questionCount ? "逐题校对" : "手动补充题目";
}

export function fixedScoringDescription(type: string) {
  if (type === "multiple_choice") return "少选且无错选、有错选的得分以下方设置为准；空白得 0 分。";
  if (type === "fill_blank") return "填空题还需核对每空答案、等价写法和分值。";
  return "空白或识别不确定的答卷应转人工确认。";
}

export function evidenceTargetPlaceholder(type: RubricEvidenceRequirement["type"]) {
  if (type === "concept") return "例如：配方法";
  if (type === "unit") return "例如：m/s";
  return "例如：x ≥ 0";
}

export function evidenceHasTarget(type: RubricEvidenceRequirement["type"]) {
  return type === "concept" || type === "unit" || type === "domain";
}

export function evidenceTypePatch(type: RubricEvidenceRequirement["type"], requirement: RubricEvidenceRequirement) {
  return { type, target: evidenceHasTarget(type) ? requirement.target ?? "" : undefined, minimum: undefined, children: undefined };
}
