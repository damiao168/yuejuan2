import { describe, expect, it } from "vitest";
import type { Exam } from "../../api/exams";
import type { PaperImportJob, Question } from "../../api/papers";
import {
  evidenceTypePatch, filterPaperQuestions, hasStandardAnswer, isUnmatchedPlaceholder, otherPaperDraftsDirty,
  questionEditingAccess, questionEditorDescription, questionIndexLabel, questionNeedsAnswer,
  questionSummary, rubricProgressLabel, standardAnswerText
} from "./paperQuestionPresentation";

function question(overrides: Partial<Question> = {}): Question {
  return {
    id: "question-1", tenant_id: "synthetic-school", exam_id: "exam-1", question_no: "1", question_type: "short_answer",
    score: 5, stem: "synthetic stem", options: [], knowledge_points: [], sort_order: 1, status: "draft", ...overrides
  };
}

function answered(answer: unknown): Question {
  return question({ answer_key: { id: "answer-1", question_id: "question-1", answer_version: "1", standard_answer: answer, equivalent_answers: [], tolerance: {} } });
}

describe("paper question presentation boundaries", () => {
  it("uses the editor's structured answer representation without losing zero or false", () => {
    const structured = answered({ blanks: ["a", "b"] });
    expect(standardAnswerText(structured)).toBe(JSON.stringify({ blanks: ["a", "b"] }, null, 2));
    expect(standardAnswerText(structured)).not.toContain("[object Object]");
    expect(standardAnswerText(answered(["a", "b"]))).toBe(JSON.stringify(["a", "b"], null, 2));
    expect(questionNeedsAnswer(answered(0))).toBe(false);
    expect(questionNeedsAnswer(answered(false))).toBe(false);
    expect(questionNeedsAnswer(answered("  "))).toBe(true);
    expect(questionNeedsAnswer(answered(null))).toBe(true);
    expect(questionNeedsAnswer(question())).toBe(true);
  });

  it("preserves the previous array answer emptiness boundary independently of JSON display", () => {
    const answers = [[[], false], [[null], false], [[[]], false], [[" "], false], [["", ""], true], [[null, null], true], [[["A"]], true], [[{ blanks: [] }], true]] satisfies [unknown, boolean][];
    for (const [answer, expected] of answers) expect(hasStandardAnswer(answered(answer))).toBe(expected);
    expect(standardAnswerText(answered([]))).toBe("[]");
    expect(questionNeedsAnswer(answered([]))).toBe(true);
  });

  it("counts unmatched empty presets as placeholders until the current structure is confirmed", () => {
    const placeholder = question({ id: "placeholder", question_no: "2", stem: "", score_source: "preset", score: 0 });
    const recognized = question({ score_source: "preset" });
    const job = { status: "review_required", questions: [{ question_no: "1" }] } as PaperImportJob;
    expect(isUnmatchedPlaceholder(placeholder, job, false)).toBe(true);
    expect(isUnmatchedPlaceholder(recognized, job, false)).toBe(false);
    expect(isUnmatchedPlaceholder({ ...placeholder, answer_key: answered(0).answer_key }, job, false)).toBe(false);
    expect(questionSummary([recognized, placeholder], job, false)).toMatchObject({ pendingScoreCount: 1, unmatchedPlaceholderCount: 1, needsAnswerCount: 1, needsRubricCount: 1, totalScore: 5 });
    expect(questionSummary([recognized, placeholder], job, true)).toMatchObject({ unmatchedPlaceholderCount: 0, needsAnswerCount: 2, needsRubricCount: 2 });
  });

  it("keeps answer and rubric filters independent when both need attention", () => {
    const both = question();
    const fixed = { ...answered("A"), id: "fixed", question_type: "single_choice", stem: "Other stem" };
    expect(filterPaperQuestions([both, fixed], "answer", " SYNTHETIC ")).toEqual([both]);
    expect(filterPaperQuestions([both, fixed], "rubric", "")).toEqual([both]);
    expect(questionIndexLabel(both, false)).toContain("待补答案、待确认细则");
    expect(questionIndexLabel(both, true)).toContain("预设占位，等待结构核对");
  });

  it("allows fill-blank answer editing despite an old locked rubric but preserves the subjective lock", () => {
    const locked = question({ rubric: { id: "rubric-1", question_id: "question-1", version: "1", status: "locked", max_score: 5, points: [], deductions: [], examples: [] } });
    const exam = { subject: "math" } as Exam;
    expect(questionEditingAccess(true, undefined, locked, exam)).toMatchObject({ selectedLocked: true, questionDisabled: true });
    expect(questionEditingAccess(true, "fill_blank", locked, exam)).toMatchObject({ selectedLocked: false, questionDisabled: false, editorUsesFixedScoring: true });
    expect(questionEditingAccess(false, "fill_blank", locked, exam).questionDisabled).toBe(true);
    expect(questionEditingAccess(true, "numeric", locked, exam)).toMatchObject({ showTolerance: true, objectiveRuleType: "", showFormulaEvidence: false });
  });

  it.each([
    ["question", true, false, false, false], ["question", false, true, false, true],
    ["rubric", false, true, false, false], ["rubric", true, false, false, true],
    ["scoring", false, false, true, false], ["scoring", false, true, false, true]
  ] satisfies ["question" | "rubric" | "scoring", boolean, boolean, boolean, boolean][])("protects other drafts while saving %s", (section, form, rubric, scoring, expected) => {
    expect(otherPaperDraftsDirty(section, form, rubric, scoring)).toBe(expected);
  });

  it("keeps lock and unsaved notices ahead of the question type and score", () => {
    expect(questionEditorDescription(question(), true, true)).toBe("评分细则已确认并锁定");
    expect(questionEditorDescription(question(), false, true)).toBe("有未保存的修改");
    expect(questionEditorDescription(null, false, false)).toBe("填写题目与答案后保存");
    expect(questionEditorDescription(question({ score: 0 }), false, false)).toContain("分值待确认");
  });

  it("clears grouped evidence fields when choosing a simple evidence type", () => {
    const existing = { type: "all_of" as const, minimum: 2, target: "old", children: [{ type: "unit" as const, target: "m" }] };
    expect(evidenceTypePatch("final_result", existing)).toEqual({ type: "final_result", target: undefined, minimum: undefined, children: undefined });
    expect(evidenceTypePatch("unit", existing)).toEqual({ type: "unit", target: "old", minimum: undefined, children: undefined });
    expect(evidenceTypePatch("domain", { type: "final_result" })).toMatchObject({ target: "" });
  });

  it("distinguishes saved drafts from empty editor defaults and confirmed scoring", () => {
    const saved = { ...answered("synthetic answer"), rubric: { id: "rubric-1", question_id: "question-1", version: "1", status: "draft", max_score: 5,
      points: [{ id: "p1", description: "synthetic scoring point", score: 5, required: true }], deductions: [], examples: [] } };
    expect(rubricProgressLabel(null)).toBe("尚未配置细则");
    expect(rubricProgressLabel(question())).toBe("尚未配置细则");
    expect(rubricProgressLabel({ ...saved, rubric: { ...saved.rubric, points: [] } })).toBe("尚未配置细则");
    expect(rubricProgressLabel(saved)).toBe("草稿已保存，待确认");
    expect(rubricProgressLabel(saved, true)).toBe("有未保存修改");
    expect(questionIndexLabel(saved, false)).toContain("草稿已保存，待确认");
    const locked = { ...saved, rubric: { ...saved.rubric, status: "locked" } };
    expect(rubricProgressLabel(locked)).toBe("已确认并锁定");
    expect(questionIndexLabel(locked, false)).toContain("细则已确认");
    expect(questionIndexLabel(locked, false)).not.toContain("待确认");
  });

  it.each([["pending_review", "已提交审批，待确认"], ["approved", "审批已通过，待确认"]])("preserves the historical %s review state", (status, label) => {
    const existing = question({ rubric: { id: "rubric-1", question_id: "question-1", version: "1", status, max_score: 5,
      points: [{ id: "p1", description: "synthetic scoring point", score: 5, required: true }], deductions: [], examples: [] } });
    expect(rubricProgressLabel(existing)).toBe(label);
    expect(questionIndexLabel(existing, false)).toContain(label);
  });
});
