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
    expect(() => questionAnswerValue("A,B", ["A", "C"])).toThrow("标准答案暂不可保存");
    expect(() => questionAnswerValue('{"value":2}', ["A", "C"])).toThrow("标准答案暂不可保存");
  });

  it("continues accepting plain text answers", () => {
    expect(questionAnswerText(undefined)).toBe("");
    expect(questionAnswerValue("x=2", "x=1")).toBe("x=2");
    expect(questionAnswerValue("A", undefined)).toBe("A");
  });

  it("将原始值转成文本，结构化答案仍使用 JSON", () => {
    expect(questionAnswerText('"x=2"\ny=3')).toBe('"x=2"\ny=3');
    expect(questionAnswerText(42)).toBe("42");
    expect(questionAnswerText(true)).toBe("true");
    expect(questionAnswerText(false)).toBe("false");
    expect(questionAnswerText(42n)).toBe("42");
    expect(questionAnswerText(Symbol("answer"))).toBe("Symbol(answer)");
    const answerFunction = () => "A";
    expect(questionAnswerText(answerFunction)).toBe(String(answerFunction));
    expect(questionAnswerText(null)).toBe("");
    expect(questionAnswerText(["A", "C"])).toBe('[\n  "A",\n  "C"\n]');
    expect(questionAnswerText({ value: 2 })).toBe('{\n  "value": 2\n}');
  });
});
