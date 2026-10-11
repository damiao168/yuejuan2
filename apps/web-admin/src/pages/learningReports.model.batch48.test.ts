import { describe, expect, it } from "vitest";
import type { ClassReport, QuestionAnalysis } from "../api/reports";
import { flattenErrors } from "./learningReports.model";

describe("学情线索排序和口径优先", () => {
  it("优先题目线索，按次数稳定排序并只保留前八项，不改输入", () => {
    const clues = Array.from({ length: 10 }, (_, index) => ({ source: "unknown", text: `clue-${index}`, count: index % 3 }));
    const questions = [{ question_id: "question", question_no: "1", score_rate: 0.5, frequent_errors: clues }] as QuestionAnalysis[];
    const before = structuredClone(questions);
    const classes = [{ class_id: "class", class_name: "class", frequent_wrong_questions: [{ question_id: "other", question_no: "2", score_rate: 0.2, wrong_count: 99 }] }] as ClassReport[];
    const result = flattenErrors(questions, classes);
    expect(result.map((row) => row.text)).toEqual(["clue-2", "clue-5", "clue-8", "clue-1", "clue-4", "clue-7", "clue-0", "clue-3"]);
    expect(result.every((row) => row.question_no === "1")).toBe(true);
    expect(questions).toEqual(before);
  });

  it("仅在没有题目线索时回退班级高频错题", () => {
    const classes = [{ class_id: "class", class_name: "班级", frequent_wrong_questions: [{ question_id: "other", question_no: "2", score_rate: 0.2, wrong_count: 99 }] }] as ClassReport[];
    expect(flattenErrors([], classes)).toMatchObject([{ question_no: "2", source: "班级", count: 99 }]);
    expect(flattenErrors([], [])).toEqual([]);
  });
});
