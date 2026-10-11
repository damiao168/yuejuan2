import { describe, expect, it } from "vitest";
import { normalizeAssessmentSubject } from "./assessmentSubjectScope";

describe("考试学科约束与后台规范化一致", () => {
  it.each([
    ["chinese", "chinese"], ["语文", "chinese"],
    ["mathematics", "mathematics"], ["math", "mathematics"], ["数学", "mathematics"],
    ["english", "english"], ["英语", "english"],
    ["physics", "physics"], ["物理", "physics"],
    ["chemistry", "chemistry"], ["化学", "chemistry"],
    ["biology", "biology"], ["生物", "biology"],
    ["history", "history"], ["历史", "history"],
    ["geography", "geography"], ["地理", "geography"],
    ["ethics_politics", "ethics_politics"], ["politics", "ethics_politics"], ["civics", "ethics_politics"],
    ["道德与法治", "ethics_politics"], ["思想政治", "ethics_politics"], ["政治", "ethics_politics"]
  ])("识别 %s，并接受两端空白和英文大小写", (input, expected) => {
    expect(normalizeAssessmentSubject(`  ${input.toUpperCase()}  `)).toBe(expected);
  });

  it.each(["综合", "理综", "science", "", " ", "toString", "constructor", "__proto__"])("%s 不冒充数学或其他已知学科", (input) => {
    expect(normalizeAssessmentSubject(input)).toBeUndefined();
  });
});
