import { describe, expect, it } from "vitest";
import type { Question } from "../../api/papers";
import { groupedQuestionScores, uniformScorePreview } from "./groupedQuestionScores";

function question(id: string, type: string, score: number, locked = false): Question {
  return { id, exam_id: "exam", tenant_id: "tenant", question_no: id, question_type: type, score, options: [], knowledge_points: [], sort_order: 1, status: "draft",
    ...(locked ? { rubric: { id: `rubric-${id}`, question_id: id, version: "1", status: "locked", max_score: score, points: [], deductions: [], examples: [] } } : {}) };
}

describe("统一客观题分值的范围与预览", () => {
  it("按正式题型分组，同分预填，保留异分和解答题逐题值", () => {
    const questions = [question("1", "single_choice", 3), question("2", "single_choice", 3), question("3", "fill_blank", 2), question("4", "fill_blank", 4), question("5", "calculation", 10)];
    const groups = groupedQuestionScores(questions);
    expect(groups.map((group) => [group.type, group.uniformScore])).toEqual([["single_choice", 3], ["fill_blank", null]]);
    const preview = uniformScorePreview(groups[1], 3, questions)!;
    expect(preview.changed.map((item) => item.id)).toEqual(["3", "4"]);
    expect(preview).toMatchObject({ groupTotal: 6, examTotal: 22, difference: 0 });
    expect(questions.map((item) => item.score)).toEqual([3, 3, 2, 4, 10]);
  });

  it("锁定细则保留原分值，零值待填，非法分值不能预览保存", () => {
    const questions = [question("1", "fill_blank", 0), question("2", "fill_blank", 5, true)];
    const group = groupedQuestionScores(questions)[0];
    expect(group.uniformScore).toBeNull();
    expect(group.lockedCount).toBe(1);
    expect(uniformScorePreview(group, 2.5, questions)).toMatchObject({ groupTotal: 7.5, examTotal: 7.5, difference: 2.5 });
    for (const score of [null, 0, -1, NaN, Infinity]) expect(uniformScorePreview(group, score, questions)).toBeNull();
  });
});
