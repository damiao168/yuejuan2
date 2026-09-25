import { describe, expect, it } from "vitest";
import { mergeClassSelection, readinessCheckRoute } from "./ExamPreparationPage";
import { preparationSteps } from "../features/exams/preparation/preparationSteps";

describe("exam preparation steps", () => {
  it("keeps the recommended order and actions in one shared configuration", () => {
    expect(preparationSteps.map(({ label, route, action }) => ({ label, route, action }))).toEqual([
      { label: "学生范围", route: "students", action: "设置范围" },
      { label: "考试资料", route: "paper", action: "上传资料" },
      { label: "小题与分值", route: "questions", action: "核对题目" },
      { label: "答题卡设置", route: "template", action: "设置答题卡" }
    ]);
  });
});

describe("readiness check routes", () => {
  it.each(["students", "paper", "questions", "template"])("routes %s checks to the existing exam workspace section", (section) => {
    expect(readinessCheckRoute("exam/一", section)).toBe(`/exams/exam%2F%E4%B8%80/${section}`);
  });

  it("keeps unknown backend sections inside the readiness page", () => {
    expect(readinessCheckRoute("exam-1", "unknown")).toBe("/exams/exam-1/settings");
  });
});

describe("class selection across grade groups", () => {
  it("keeps classes from other groups when one group changes", () => {
    expect(mergeClassSelection(["grade-one-a"], ["grade-two-a", "grade-two-b"], ["grade-two-b"])).toEqual(["grade-one-a", "grade-two-b"]);
    expect(mergeClassSelection(["grade-one-a", "grade-two-b"], ["grade-one-a"], [])).toEqual(["grade-two-b"]);
  });
});
