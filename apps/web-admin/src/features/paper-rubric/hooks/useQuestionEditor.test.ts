import { describe, expect, it } from "vitest";
import { questionAnswerText, questionAnswerValue } from "./useQuestionEditor";

describe("question answer editing", () => {
  it("preserves structured answers when changing unrelated question fields", () => {
    for (const answer of [["A", "C"], { blanks: ["x=1", "y=2"] }, 42]) {
      expect(questionAnswerValue(questionAnswerText(answer), answer)).toEqual(answer);
    }
  });

  it("parses edits to structured answers without flattening their shape", () => {
    expect(questionAnswerValue('["A","B"]', ["A", "C"])).toEqual(["A", "B"]);
    expect(questionAnswerValue('{"value":2}', { value: 1 })).toEqual({ value: 2 });
    expect(() => questionAnswerValue("A,B", ["A", "C"])).toThrow("有效 JSON");
    expect(() => questionAnswerValue('{"value":2}', ["A", "C"])).toThrow("有效 JSON");
  });

  it("continues accepting plain text answers", () => {
    expect(questionAnswerText(undefined)).toBe("");
    expect(questionAnswerValue("x=2", "x=1")).toBe("x=2");
    expect(questionAnswerValue("A", undefined)).toBe("A");
  });
});
