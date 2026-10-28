import { describe, expect, it } from "vitest";
import { answerNeedsRawDetails, recognizedAnswerText } from "./recognizedAnswerPresentation";

describe("识别答案可读展示", () => {
  it("按题型展示多选与逐空答案，保留零分值和判断题否定答案", () => {
    expect(recognizedAnswerText(["A", "C"], "multiple_choice")).toBe("A、C");
    expect(recognizedAnswerText({ answers: [0, "米"] }, "fill_blank")).toBe("各空答案：第 1 空：0\n\n第 2 空：米");
    expect(recognizedAnswerText(false, "true_false")).toBe("错误（×）");
  });
  it("未知字段保留原始详情提示，展示不改写输入对象", () => {
    const answer = { value: 0, custom_policy: { expression: "x=0" } };
    expect(recognizedAnswerText(answer, "numeric")).toBe("答案值：0");
    expect(answerNeedsRawDetails(answer)).toBe(true);
    expect(answer).toEqual({ value: 0, custom_policy: { expression: "x=0" } });
    expect(answerNeedsRawDetails({ blanks: [0, "米"] })).toBe(false);
  });
});
